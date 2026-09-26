// Package hooks receives events that services push (PG Back Web
// webhooks) and hands out the URL they must call.
//
//	POST /hooks/{connection id}/{HMAC(master key, id)}  body {"event", "name"}
//	     → hook_events row → pgbackweb.data (svcdata passes the events)
package hooks

import (
	"database/sql"
	"errors"
	"strconv"
	"strings"
	"time"

	"andon/internal/crypto"
	"andon/internal/db"
	"andon/internal/enums"
	"andon/internal/model"
	"andon/internal/repos/content"
	data "andon/internal/repos/data"
	"andon/internal/services/svcdata"
)

const (
	subjectMax = 120
	eventMax   = 60
	keepDays   = 60
	pathPrefix = "/hooks/"
)

// ErrRejected hides whether the connection or the signature was wrong.
var ErrRejected = errors.New("hooks: rejected")

// pushServices accept webhooks.
var pushServices = map[enums.ServiceType]bool{enums.ServicePGBackWeb: true}

// Accepts reports whether a service is fed by webhooks.
func Accepts(service enums.ServiceType) bool { return pushServices[service] }

func signature(connID int64) (string, error) {
	return crypto.Sign("hook:"+strconv.FormatInt(connID, 10), crypto.PurposeHook)
}

// URL is the webhook address for a connection, e.g.
// https://dash.example/hooks/7/q3…
func URL(baseURL string, connID int64) (string, error) {
	sig, err := signature(connID)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(baseURL, "/") + pathPrefix + strconv.FormatInt(connID, 10) + "/" + sig, nil
}

// Receive stores one event after checking the URL's signature.
func Receive(d *sql.DB, connID int64, sig, event, subject string) error {
	want, err := signature(connID)
	if err != nil || !crypto.Same(want, sig) {
		return ErrRejected
	}
	event, subject = clip(strings.TrimSpace(event), eventMax), clip(strings.TrimSpace(subject), subjectMax)
	if event == "" {
		return ErrRejected
	}

	err = db.WithTx(d, func(tx *sql.Tx) error {
		conn, err := content.Connection(tx, connID)
		if err != nil {
			return err
		}
		if conn == nil || !Accepts(enums.ServiceType(conn.Service)) {
			return ErrRejected
		}
		now := time.Now().UTC()
		if err := data.PruneHookEvents(tx, now.AddDate(0, 0, -keepDays)); err != nil {
			return err
		}
		return data.AddHookEvent(tx, &model.HookEvent{ConnectionID: connID, Event: event, Subject: subject, At: now})
	})
	if err != nil {
		return err
	}
	svcdata.Forget(connID)
	return nil
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}
