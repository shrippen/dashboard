// Package connections manages connections to services and their
// credentials.
//
//	shared credentials   one token, stored on the connection, same data for all
//	personal credentials every user stores his own token, data per user
//
// Tokens are write-only: they are encrypted and never shown again.
package connections

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"andon/internal/crypto"
	"andon/internal/db"
	"andon/internal/enums"
	"andon/internal/model"
	"andon/internal/repos/content"
	"andon/internal/repos/misc"
	"andon/internal/services/access"
	"andon/internal/services/audit"
	"andon/internal/services/svcdata"
	"andon/internal/services/util"
)

const sharedLocationKey = "dawarich_shared"

// ErrLocationPersonal means Dawarich location data may not be shared while
// location sharing is disabled instance-wide.
var ErrLocationPersonal = errors.New("connections: location data must stay personal")

// ErrNotFound means the connection does not exist.
var ErrNotFound = util.ErrNotFound

// TLS selects certificate verification for a connection.
type TLS string

const (
	TLSVerify TLS = "verify"
	TLSSkip   TLS = "skip"
)

// View is a connection as shown to one principal.
type View struct {
	ID        int64
	Key       string
	Name      string
	Service   enums.ServiceType
	URL       string
	Mode      enums.CredentialMode
	HasSecret bool
	HasMine   bool
	VerifyTLS bool
	Options   map[string]any
	SpaceID   int64
	Right     enums.Right

	SecretAt      time.Time // shared token, or the caller's own; zero = unknown
	SecretExpires string
	DailyBudget   int
	Health        Health
}

func rightOf(q db.Queryer, who *access.Principal, conn *model.Connection) (enums.Right, error) {
	space, err := access.SpaceOf(q, who, conn.SpaceID)
	if err != nil {
		return enums.RightNone, err
	}
	return access.Right(who, enums.ResourceConnection, conn.ID, space, nil), nil
}

func viewOf(q db.Queryer, who *access.Principal, conn *model.Connection, granted enums.Right) (View, error) {
	cred, err := content.Credential(q, conn.ID, who.UserID)
	if err != nil {
		return View{}, err
	}
	health, err := healthOf(q, conn.ID, time.Now().UTC())
	if err != nil {
		return View{}, err
	}
	secretAt := conn.SecretAt
	if conn.CredentialMode == enums.CredentialPersonal {
		secretAt = time.Time{}
		if cred != nil {
			secretAt = cred.SecretAt
		}
	}
	return View{
		ID: conn.ID, Key: conn.Key, Name: conn.Name, Service: enums.ServiceType(conn.Service), URL: conn.URL,
		Mode: conn.CredentialMode, HasSecret: len(conn.SecretEnc) > 0, HasMine: cred != nil,
		VerifyTLS: conn.VerifyTLS, Options: conn.Options, SpaceID: conn.SpaceID, Right: granted,
		SecretAt: secretAt, SecretExpires: conn.SecretExpires, DailyBudget: conn.DailyBudget, Health: health,
	}, nil
}

// Listing returns the connections visible to who with at least `minimum`
// right (default USE).
func Listing(d *sql.DB, who *access.Principal, minimum enums.Right) ([]View, error) {
	if minimum == enums.RightNone {
		minimum = enums.RightUse
	}
	var out []View
	err := db.WithTx(d, func(tx *sql.Tx) error {
		spaceIDs := make([]int64, 0, len(who.Spaces))
		for id := range who.Spaces {
			spaceIDs = append(spaceIDs, id)
		}
		found, err := content.Connections(tx, spaceIDs)
		if err != nil {
			return err
		}
		for _, id := range access.GrantedResourceIDs(who, enums.ResourceConnection) {
			c, err := content.Connection(tx, id)
			if err != nil {
				return err
			}
			if c == nil {
				continue
			}
			if _, inOwnSpace := who.Spaces[c.SpaceID]; !inOwnSpace {
				found = append(found, c)
			}
		}

		for _, conn := range found {
			granted, err := rightOf(tx, who, conn)
			if err != nil {
				return err
			}
			if granted < minimum {
				continue
			}
			v, err := viewOf(tx, who, conn, granted)
			if err != nil {
				return err
			}
			out = append(out, v)
		}
		return nil
	})
	return out, err
}

// Get returns one connection's view. Requires at least USE.
func Get(d *sql.DB, who *access.Principal, connID int64) (View, error) {
	var out View
	err := db.WithTx(d, func(tx *sql.Tx) error {
		conn, err := content.Connection(tx, connID)
		if err != nil {
			return err
		}
		if conn == nil {
			return ErrNotFound
		}
		granted, err := rightOf(tx, who, conn)
		if err != nil {
			return err
		}
		if err := access.Need(granted, enums.RightUse); err != nil {
			return err
		}
		out, err = viewOf(tx, who, conn, granted)
		return err
	})
	return out, err
}

func checkLocationSharing(q db.Queryer, service enums.ServiceType, mode enums.CredentialMode, space *access.SpaceRef) error {
	if service != enums.ServiceDawarich || mode == enums.CredentialPersonal {
		return nil
	}
	if space != nil && space.Kind == enums.SpacePersonal {
		return nil
	}
	setting, err := misc.Setting(q, sharedLocationKey)
	if err != nil {
		return err
	}
	if allowed, _ := setting["allowed"].(bool); !allowed {
		return ErrLocationPersonal
	}
	return nil
}

// Create adds a new connection.
func Create(d *sql.DB, who *access.Principal, spaceID int64, service enums.ServiceType, name, url string,
	mode enums.CredentialMode, secret string, tls TLS, options map[string]any) (int64, error) {
	var id int64
	err := db.WithTx(d, func(tx *sql.Tx) error {
		space, err := access.SpaceOf(tx, who, spaceID)
		if err != nil {
			return err
		}
		need := enums.RightEdit
		if space != nil && space.Kind == enums.SpaceTeam {
			need = enums.RightManage
		}
		if err := access.Need(access.SpaceRight(who, space), need); err != nil {
			return err
		}
		if err := checkLocationSharing(tx, service, mode, space); err != nil {
			return err
		}

		existing, err := content.Connections(tx, []int64{spaceID})
		if err != nil {
			return err
		}
		taken := map[string]bool{}
		for _, c := range existing {
			taken[c.Key] = true
		}

		label := strings.TrimSpace(name)
		if label == "" {
			label = string(service)
		}
		var secretEnc []byte
		if secret != "" {
			enc, err := crypto.Encrypt(secret, crypto.PurposeCredential, nil)
			if err != nil {
				return err
			}
			secretEnc = enc
		}
		// Personal: the token entered here is the creator's own, not shared.
		var ownEnc []byte
		if mode == enums.CredentialPersonal {
			ownEnc, secretEnc = secretEnc, nil
		}
		var secretAt time.Time
		if secretEnc != nil {
			secretAt = time.Now().UTC()
		}
		conn := &model.Connection{
			SecretAt: secretAt,
			SpaceID:  spaceID, Key: util.Unique(util.Slug(label, string(service)), taken), Name: label,
			Service: string(service), URL: strings.TrimRight(strings.TrimSpace(url), "/"),
			CredentialMode: mode, SecretEnc: secretEnc, VerifyTLS: tls == TLSVerify, Options: orEmpty(options),
			CreatedAt: time.Now().UTC(),
		}
		if err := content.AddConnection(tx, conn); err != nil {
			return err
		}
		id = conn.ID
		if ownEnc != nil {
			if err := content.SetCredential(tx, conn.ID, who.UserID, ownEnc); err != nil {
				return err
			}
		}
		return audit.Log(tx, &who.UserID, "connection.created", conn.Name, "", nil)
	})
	return id, err
}

// Update changes a connection's mutable fields. Requires MANAGE.
func Update(d *sql.DB, who *access.Principal, connID int64, name, url string, mode enums.CredentialMode,
	secret *string, tls TLS, options map[string]any) error {
	defer svcdata.Forget(connID) // cached data may be stale now

	return db.WithTx(d, func(tx *sql.Tx) error {
		conn, err := content.Connection(tx, connID)
		if err != nil {
			return err
		}
		if conn == nil {
			return ErrNotFound
		}
		granted, err := rightOf(tx, who, conn)
		if err != nil {
			return err
		}
		if err := access.Need(granted, enums.RightManage); err != nil {
			return err
		}
		space, err := access.SpaceOf(tx, who, conn.SpaceID)
		if err != nil {
			return err
		}
		if err := checkLocationSharing(tx, enums.ServiceType(conn.Service), mode, space); err != nil {
			return err
		}

		if n := strings.TrimSpace(name); n != "" {
			conn.Name = n
		}
		conn.URL = strings.TrimRight(strings.TrimSpace(url), "/")
		if err := keepEditorToken(tx, who, conn, mode); err != nil {
			return err
		}
		conn.CredentialMode = mode
		conn.VerifyTLS = tls == TLSVerify
		if options != nil {
			conn.Options = options
		}
		if secret != nil && *secret != "" {
			enc, err := crypto.Encrypt(*secret, crypto.PurposeCredential, nil)
			if err != nil {
				return err
			}
			if err := storeSecret(tx, who, conn, enc); err != nil {
				return err
			}
			if err := audit.Log(tx, &who.UserID, "connection.secret_changed", conn.Name, "", nil); err != nil {
				return err
			}
		}
		if err := content.UpdateConnection(tx, conn); err != nil {
			return err
		}
		return audit.Log(tx, &who.UserID, "connection.updated", conn.Name, "", nil)
	})
}

// storeSecret keeps a token where the connection's mode reads it: on the
// connection when shared, as the editor's own token when personal.
func storeSecret(tx *sql.Tx, who *access.Principal, conn *model.Connection, enc []byte) error {
	if conn.CredentialMode == enums.CredentialPersonal {
		return content.SetCredential(tx, conn.ID, who.UserID, enc)
	}
	conn.SecretEnc = enc
	conn.SecretAt = time.Now().UTC()
	return nil
}

// keepEditorToken: a shared connection switched to personal would leave
// its editor without a token; the shared one becomes theirs unless they
// already have their own.
func keepEditorToken(tx *sql.Tx, who *access.Principal, conn *model.Connection, mode enums.CredentialMode) error {
	if mode != enums.CredentialPersonal || conn.CredentialMode == enums.CredentialPersonal || len(conn.SecretEnc) == 0 {
		return nil
	}
	own, err := content.Credential(tx, conn.ID, who.UserID)
	if err != nil || own != nil {
		return err
	}
	return content.SetCredential(tx, conn.ID, who.UserID, conn.SecretEnc)
}

// SetOptions replaces a connection's service-specific options (e.g. the
// Dawarich area -> Kimai customer mapping). Requires MANAGE.
func SetOptions(d *sql.DB, who *access.Principal, connID int64, options map[string]any) error {
	defer svcdata.Forget(connID) // cached data may be stale now

	return db.WithTx(d, func(tx *sql.Tx) error {
		conn, err := content.Connection(tx, connID)
		if err != nil {
			return err
		}
		if conn == nil {
			return ErrNotFound
		}
		granted, err := rightOf(tx, who, conn)
		if err != nil {
			return err
		}
		if err := access.Need(granted, enums.RightManage); err != nil {
			return err
		}
		conn.Options = options
		if err := content.UpdateConnection(tx, conn); err != nil {
			return err
		}
		return audit.Log(tx, &who.UserID, "connection.options", conn.Name, "", nil)
	})
}

// Delete removes a connection and its shares. Requires MANAGE.
func Delete(d *sql.DB, who *access.Principal, connID int64) error {
	defer svcdata.Forget(connID) // cached data may be stale now

	return db.WithTx(d, func(tx *sql.Tx) error {
		conn, err := content.Connection(tx, connID)
		if err != nil || conn == nil {
			return err
		}
		granted, err := rightOf(tx, who, conn)
		if err != nil {
			return err
		}
		if err := access.Need(granted, enums.RightManage); err != nil {
			return err
		}
		if err := misc.DropShares(tx, enums.ResourceConnection, conn.ID); err != nil {
			return err
		}
		if err := audit.Log(tx, &who.UserID, "connection.deleted", conn.Name, "", nil); err != nil {
			return err
		}
		return content.RemoveConnection(tx, conn.ID)
	})
}

// SetMine stores the caller's own personal token for a connection they may use.
func SetMine(d *sql.DB, who *access.Principal, connID int64, secret string) error {
	defer svcdata.Forget(connID) // cached data may be stale now

	return db.WithTx(d, func(tx *sql.Tx) error {
		conn, err := content.Connection(tx, connID)
		if err != nil {
			return err
		}
		if conn == nil {
			return ErrNotFound
		}
		granted, err := rightOf(tx, who, conn)
		if err != nil {
			return err
		}
		if err := access.Need(granted, enums.RightView); err != nil {
			return err
		}
		enc, err := crypto.Encrypt(secret, crypto.PurposeCredential, nil)
		if err != nil {
			return err
		}
		if err := content.SetCredential(tx, conn.ID, who.UserID, enc); err != nil {
			return err
		}
		return audit.Log(tx, &who.UserID, "credential.set", conn.Name, "", nil)
	})
}

// DropMine removes the caller's own personal token for a connection.
func DropMine(d *sql.DB, who *access.Principal, connID int64) error {
	defer svcdata.Forget(connID) // cached data may be stale now

	return db.WithTx(d, func(tx *sql.Tx) error {
		if err := content.RemoveGrant(tx, connID, who.UserID); err != nil {
			return err
		}
		return content.RemoveCredential(tx, connID, who.UserID)
	})
}

// PersonalNeeded returns visible connections that need the caller's own
// personal credentials.
func PersonalNeeded(d *sql.DB, who *access.Principal) ([]View, error) {
	all, err := Listing(d, who, enums.RightView)
	if err != nil {
		return nil, err
	}
	var out []View
	for _, c := range all {
		if c.Mode == enums.CredentialPersonal {
			out = append(out, c)
		}
	}
	return out, nil
}

// TestResult is the outcome of a live connection test.
type TestResult struct {
	Ok      bool
	Message string
	Version string
}

// Test calls the service's test source (a version check) with the stored
// credentials. Requires USE.
func Test(ctx context.Context, d *sql.DB, who *access.Principal, connID int64) (TestResult, error) {
	var conn *model.Connection
	err := db.WithTx(d, func(tx *sql.Tx) error {
		c, err := content.Connection(tx, connID)
		if err != nil {
			return err
		}
		if c == nil {
			return ErrNotFound
		}
		granted, err := rightOf(tx, who, c)
		if err != nil {
			return err
		}
		if err := access.Need(granted, enums.RightUse); err != nil {
			return err
		}
		conn = c
		return nil
	})
	if err != nil {
		return TestResult{}, err
	}

	result, err := svcdata.Get(ctx, d, conn.Service+".test", nil, conn, &who.UserID, svcdata.Force)
	if err != nil {
		if errors.Is(err, svcdata.ErrMissingCredential) {
			return TestResult{Ok: false, Message: "credential.missing"}, nil
		}
		return TestResult{}, err
	}
	if !result.Ok() {
		msg := result.Error
		if msg == "" {
			msg = "error"
		}
		return TestResult{Ok: false, Message: msg}, nil
	}
	version, _ := result.Data.(map[string]any)["version"].(string)
	return TestResult{Ok: true, Message: "ok", Version: version}, nil
}

// ByID returns a raw connection for internal jobs (rules). No access
// check: callers are jobs, not requests on a user's behalf.
func ByID(d *sql.DB, connID int64) (*model.Connection, error) {
	var conn *model.Connection
	err := db.WithTx(d, func(tx *sql.Tx) error {
		var err error
		conn, err = content.Connection(tx, connID)
		return err
	})
	return conn, err
}

func orEmpty(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}
