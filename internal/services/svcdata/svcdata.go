// Package svcdata is cached access to sources.
//
//	key = sha256(source, connection, credential owner, params)
//
// A shared connection is fetched once for everybody; a connection with
// personal credentials once per user.
//
// Results live in memory for the source's TTL (typed datasets, no decoding
// needed); Force skips that. The persisted cache row only records the last
// outcome for inspection.
package svcdata

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"dashboard/internal/crypto"
	"dashboard/internal/db"
	"dashboard/internal/enums"
	"dashboard/internal/model"
	"dashboard/internal/repos/content"
	data "dashboard/internal/repos/data"
	"dashboard/internal/sources"
)

// Freshness selects whether a cached result within its TTL is acceptable.
type Freshness int

const (
	Cached Freshness = iota
	Force
)

// ErrMissingCredential means personal credentials are required but the
// user has none yet.
var ErrMissingCredential = errors.New("svcdata: missing personal credential")

// Result is one source fetch's outcome. Data is the source's own typed
// dataset (e.g. *sources.KimaiDataset), or nil on failure.
type Result struct {
	Data      any
	FetchedAt time.Time
	OkAt      time.Time
	Error     string
}

// Ok reports whether the fetch succeeded.
func (r Result) Ok() bool { return r.Error == "" && r.Data != nil }

// CredentialOwner is the cache partition: nil for shared data, the user for
// personal credentials.
func CredentialOwner(conn *model.Connection, userID *int64) *int64 {
	if conn == nil || conn.CredentialMode != enums.CredentialPersonal {
		return nil
	}
	return userID
}

func cacheKey(sourceKey string, connID *int64, owner *int64, params map[string]any) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	sorted := make(map[string]any, len(params))
	for _, k := range keys {
		sorted[k] = params[k]
	}
	raw, _ := json.Marshal([]any{sourceKey, connID, owner, sorted})
	sum := sha256.Sum256(raw)
	return fmt.Sprintf("%x", sum)
}

func buildCtx(q db.Queryer, conn *model.Connection, userID *int64, params map[string]any) (sources.Ctx, error) {
	if conn == nil {
		return sources.Ctx{Params: params}, nil
	}

	var secret string
	if conn.CredentialMode == enums.CredentialPersonal {
		owner := int64(0)
		if userID != nil {
			owner = *userID
		}
		cred, err := content.Credential(q, conn.ID, owner)
		if err != nil {
			return sources.Ctx{}, err
		}
		if cred == nil {
			return sources.Ctx{}, ErrMissingCredential
		}
		s, err := crypto.Decrypt(cred.SecretEnc, crypto.PurposeCredential)
		if err != nil {
			return sources.Ctx{}, err
		}
		secret = s
	} else if len(conn.SecretEnc) > 0 {
		s, err := crypto.Decrypt(conn.SecretEnc, crypto.PurposeCredential)
		if err != nil {
			return sources.Ctx{}, err
		}
		secret = s
	}

	return sources.Ctx{
		URL: conn.URL, Secret: secret, VerifyTLS: conn.VerifyTLS, Options: conn.Options, Params: params,
	}, nil
}

// connVersion changes whenever what a fetch depends on changes (URL,
// secret, options), so edited connections never see older results.
func connVersion(conn *model.Connection) string {
	if conn == nil {
		return ""
	}
	raw, _ := json.Marshal([]any{conn.URL, conn.SecretEnc, conn.Options, conn.VerifyTLS})
	sum := sha256.Sum256(raw)
	return fmt.Sprintf(":%x", sum[:8])
}

// errorTTL caps how long a failed fetch is served before retrying.
const errorTTL = time.Minute

type memEntry struct {
	result  Result
	connID  int64 // 0 without a connection
	expires time.Time
}

var (
	memMu sync.Mutex
	mem   = map[string]memEntry{}
)

func remembered(key string, now time.Time) (Result, bool) {
	memMu.Lock()
	defer memMu.Unlock()

	e, ok := mem[key]
	if !ok || now.After(e.expires) {
		delete(mem, key)
		return Result{}, false
	}
	return e.result, true
}

func remember(key string, connID int64, result Result, ttl time.Duration) {
	if !result.Ok() {
		ttl = min(ttl, errorTTL)
	}
	memMu.Lock()
	defer memMu.Unlock()

	now := time.Now()
	for k, e := range mem {
		if now.After(e.expires) {
			delete(mem, k)
		}
	}
	mem[key] = memEntry{result: result, connID: connID, expires: now.Add(ttl)}
}

// Forget drops every cached result of a connection, e.g. after its URL
// or credentials changed.
func Forget(connID int64) {
	memMu.Lock()
	defer memMu.Unlock()

	for k, e := range mem {
		if e.connID == connID {
			delete(mem, k)
		}
	}
}

// Get fetches source sourceKey (never raises for a service error — it
// comes back as Result.Error) and persists the outcome to the cache table.
func Get(ctx context.Context, d *sql.DB, sourceKey string, params map[string]any, conn *model.Connection, userID *int64, fresh Freshness) (Result, error) {
	source, err := sources.Get(sourceKey)
	if err != nil {
		return Result{}, err
	}

	owner := CredentialOwner(conn, userID)
	var connID *int64
	if conn != nil {
		connID = &conn.ID
	}
	key := cacheKey(sourceKey, connID, owner, params) + connVersion(conn)
	if fresh == Cached {
		if result, ok := remembered(key, time.Now()); ok {
			return result, nil
		}
	}

	var sctx sources.Ctx
	err = db.WithTx(d, func(tx *sql.Tx) error {
		sctx, err = buildCtx(tx, conn, userID, params)
		return err
	})
	if err != nil {
		if errors.Is(err, ErrMissingCredential) {
			return Result{}, err
		}
		return Result{}, err
	}

	now := time.Now().UTC()
	if push, ok := source.(sources.PushSource); ok && conn != nil {
		if sctx.Events, err = pushedEvents(d, conn.ID, now.Add(-push.PushWindow())); err != nil {
			return Result{}, err
		}
	}
	out, fetchErr := source.Fetch(ctx, sctx)
	result := Result{FetchedAt: now}
	if fetchErr != nil {
		result.Error = fetchErr.Error()
	} else {
		result.Data, result.OkAt = out, now
	}

	memConn := int64(0)
	if conn != nil {
		memConn = conn.ID
	}
	remember(key, memConn, result, source.TTL())
	_ = persistCache(d, key, sourceKey, result) // best-effort; a cache write failure must not fail the fetch

	return result, nil
}

func pushedEvents(d *sql.DB, connID int64, since time.Time) ([]sources.Pushed, error) {
	events, err := data.HookEvents(d, connID, since)
	if err != nil {
		return nil, err
	}
	out := make([]sources.Pushed, 0, len(events))
	for _, e := range events {
		out = append(out, sources.Pushed{Event: e.Event, Subject: e.Subject, At: e.At})
	}
	return out, nil
}

func persistCache(d *sql.DB, key, sourceKey string, result Result) error {
	entry := &model.CacheEntry{Key: key, Source: sourceKey, FetchedAt: result.FetchedAt, Error: result.Error}
	if result.Ok() {
		okAt := result.OkAt
		entry.OkAt = &okAt
		// Best-effort JSON snapshot for future typed-decode/inspection use;
		// not read back yet (see package doc).
		raw, err := json.Marshal(result.Data)
		if err == nil {
			var asMap map[string]any
			if json.Unmarshal(raw, &asMap) == nil {
				entry.Data = asMap
			}
		}
	}
	return db.WithTx(d, func(tx *sql.Tx) error {
		return data.PutCache(tx, entry)
	})
}

// Prune deletes cache entries older than the retention window.
const CacheRetention = 7 * 24 * time.Hour

func Prune(d *sql.DB) error {
	return db.WithTx(d, func(tx *sql.Tx) error {
		return data.PruneCache(tx, time.Now().UTC().Add(-CacheRetention))
	})
}

// Secret returns the credential a user would fetch conn with, for the few
// calls that act instead of read (e.g. switching a light).
func Secret(d *sql.DB, conn *model.Connection, userID int64) (string, error) {
	var sctx sources.Ctx
	err := db.WithTx(d, func(tx *sql.Tx) error {
		var err error
		sctx, err = buildCtx(tx, conn, &userID, nil)
		return err
	})
	return sctx.Secret, err
}
