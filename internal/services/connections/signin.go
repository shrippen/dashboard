package connections

// Sign-in as an alternative to a pasted token. Where the result goes
// follows the connection's mode, like a token typed into the form:
//
//	shared    the connection's own credential   needs MANAGE
//	personal  the caller's personal credential  needs VIEW (like SetMine)

import (
	"database/sql"
	"errors"
	"time"

	"andon/internal/crypto"
	"andon/internal/db"
	"andon/internal/enums"
	"andon/internal/model"
	"andon/internal/repos/content"
	"andon/internal/services/access"
	"andon/internal/services/audit"
	"andon/internal/services/svcdata"
	"andon/internal/sources"
)

// ErrNoClient means the service needs an OAuth client registered first.
var ErrNoClient = errors.New("connect.no_client")

// sharedOwner is the grant row of a shared credential.
const sharedOwner = 0

// Target returns the connection a sign-in of who would store its token
// into, after checking who may do that.
func Target(d *sql.DB, who *access.Principal, connID int64) (*model.Connection, error) {
	var out *model.Connection
	err := db.WithRead(d, func(tx *sql.Tx) error {
		var err error
		out, err = target(tx, who, connID)
		return err
	})
	return out, err
}

func target(tx *sql.Tx, who *access.Principal, connID int64) (*model.Connection, error) {
	conn, err := content.Connection(tx, connID)
	if err != nil {
		return nil, err
	}
	if conn == nil {
		return nil, ErrNotFound
	}
	granted, err := rightOf(tx, who, conn)
	if err != nil {
		return nil, err
	}
	need := enums.RightManage
	if conn.CredentialMode == enums.CredentialPersonal {
		need = enums.RightView
	}
	if err := access.Need(granted, need); err != nil {
		return nil, err
	}
	return conn, nil
}

// StoreSignIn stores the result of a sign-in: a lasting token (secret),
// or a grant whose tokens renew themselves (secret is then ignored).
func StoreSignIn(d *sql.DB, who *access.Principal, connID int64, secret string, grant *sources.Grant) error {
	defer svcdata.Forget(connID) // cached data may be stale now

	return db.WithTx(d, func(tx *sql.Tx) error {
		conn, err := target(tx, who, connID)
		if err != nil {
			return err
		}
		owner := int64(sharedOwner)
		if conn.CredentialMode == enums.CredentialPersonal {
			owner = who.UserID
		}

		if grant != nil {
			secret = sources.GrantMarker
			if err := svcdata.StoreGrant(tx, conn.ID, owner, *grant); err != nil {
				return err
			}
		} else if err := content.RemoveGrant(tx, conn.ID, owner); err != nil {
			return err
		}

		enc, err := crypto.Encrypt(secret, crypto.PurposeCredential, nil)
		if err != nil {
			return err
		}
		if conn.CredentialMode == enums.CredentialPersonal {
			err = content.SetCredential(tx, conn.ID, who.UserID, enc)
		} else {
			conn.SecretEnc, conn.SecretAt = enc, time.Now().UTC()
			err = content.UpdateConnection(tx, conn)
		}
		if err != nil {
			return err
		}
		return audit.Log(tx, &who.UserID, "connection.connected", conn.Name, "", nil)
	})
}

// SetOAuthClient registers the OAuth client created in the service
// (Gitea, Snipe-IT, Tailscale). Requires MANAGE.
func SetOAuthClient(d *sql.DB, who *access.Principal, connID int64, id, secret string) error {
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
		enc, err := crypto.Encrypt(id+":"+secret, crypto.PurposeCredential, nil)
		if err != nil {
			return err
		}
		if err := content.SetOAuthClient(tx, conn.ID, enc); err != nil {
			return err
		}
		return audit.Log(tx, &who.UserID, "connection.oauth_client", conn.Name, "", nil)
	})
}

// OAuthClientOf returns a connection's registered client. Callers check
// rights first (Target).
func OAuthClientOf(d *sql.DB, connID int64) (sources.OAuthClient, error) {
	var enc []byte
	err := db.WithRead(d, func(tx *sql.Tx) error {
		var err error
		enc, err = content.OAuthClient(tx, connID)
		return err
	})
	if err != nil {
		return sources.OAuthClient{}, err
	}
	if enc == nil {
		return sources.OAuthClient{}, ErrNoClient
	}
	raw, err := crypto.Decrypt(enc, crypto.PurposeCredential)
	if err != nil {
		return sources.OAuthClient{}, err
	}
	return sources.ParseClient(raw), nil
}

// HasOAuthClient reports whether a client is registered, for the form.
func HasOAuthClient(d *sql.DB, connID int64) bool {
	_, err := OAuthClientOf(d, connID)
	return err == nil
}
