package svcdata

import (
	"context"
	"database/sql"
	"encoding/json"
	"sync"
	"time"

	"andon/internal/crypto"
	"andon/internal/db"
	"andon/internal/drivers/httpclient"
	"andon/internal/model"
	"andon/internal/repos/content"
	"andon/internal/sources"
)

// grantMu renews one grant at a time: services may revoke a refresh token
// once it was used, so two parallel renewals would lock the second out.
var grantMu sync.Mutex

// sharedOwner is the grant row of a shared credential.
const sharedOwner = 0

// grantToken turns a connection's grant into a current access token,
// renewing and storing it when it is about to expire.
func grantToken(ctx context.Context, d *sql.DB, conn *model.Connection, owner *int64) (string, error) {
	user := int64(sharedOwner)
	if owner != nil {
		user = *owner
	}

	grantMu.Lock()
	defer grantMu.Unlock()

	var grantEnc, clientEnc []byte
	err := db.WithRead(d, func(tx *sql.Tx) error {
		var err error
		if grantEnc, err = content.Grant(tx, conn.ID, user); err != nil {
			return err
		}
		clientEnc, err = content.OAuthClient(tx, conn.ID)
		return err
	})
	if err != nil {
		return "", err
	}
	if grantEnc == nil {
		return "", ErrMissingCredential
	}

	g, err := decryptGrant(grantEnc)
	if err != nil {
		return "", err
	}
	var client sources.OAuthClient
	if clientEnc != nil {
		raw, err := crypto.Decrypt(clientEnc, crypto.PurposeCredential)
		if err != nil {
			return "", err
		}
		client = sources.ParseClient(raw)
	}

	next, renewed, err := g.Fresh(ctx, client, httpclient.TLSOf(conn.VerifyTLS), time.Now().UTC())
	if err != nil {
		return "", err
	}
	if renewed {
		if err := StoreGrant(d, conn.ID, user, next); err != nil {
			return "", err
		}
	}
	return next.Access, nil
}

// StoreGrant encrypts and stores a grant for a connection and user (0 =
// shared).
func StoreGrant(q db.Queryer, connID, userID int64, g sources.Grant) error {
	raw, err := json.Marshal(g)
	if err != nil {
		return err
	}
	enc, err := crypto.Encrypt(string(raw), crypto.PurposeCredential, nil)
	if err != nil {
		return err
	}
	return content.SetGrant(q, connID, userID, enc)
}

func decryptGrant(enc []byte) (sources.Grant, error) {
	var g sources.Grant
	raw, err := crypto.Decrypt(enc, crypto.PurposeCredential)
	if err != nil {
		return g, err
	}
	err = json.Unmarshal([]byte(raw), &g)
	return g, err
}
