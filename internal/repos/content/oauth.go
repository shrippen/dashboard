package content

import (
	"database/sql"
	"errors"
	"time"

	"andon/internal/db"
)

// OAuthClient returns a connection's encrypted OAuth client ("id:secret"),
// or nil when none is registered.
func OAuthClient(q db.Queryer, connID int64) ([]byte, error) {
	var enc []byte
	err := q.QueryRow("SELECT oauth_client_enc FROM connections WHERE id = ?", connID).Scan(&enc)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return enc, err
}

// SetOAuthClient stores a connection's encrypted OAuth client.
func SetOAuthClient(q db.Queryer, connID int64, enc []byte) error {
	_, err := q.Exec("UPDATE connections SET oauth_client_enc = ? WHERE id = ?", enc, connID)
	return err
}

// Grant returns the encrypted token grant of a connection for one user
// (0 = shared), or nil.
func Grant(q db.Queryer, connID, userID int64) ([]byte, error) {
	var enc []byte
	err := q.QueryRow("SELECT grant_enc FROM oauth_grants WHERE connection_id = ? AND user_id = ?", connID, userID).Scan(&enc)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return enc, err
}

// SetGrant inserts or replaces a token grant.
func SetGrant(q db.Queryer, connID, userID int64, enc []byte) error {
	_, err := q.Exec(`INSERT INTO oauth_grants (connection_id, user_id, grant_enc, updated_at) VALUES (?,?,?,?)
		ON CONFLICT (connection_id, user_id) DO UPDATE SET grant_enc = excluded.grant_enc, updated_at = excluded.updated_at`,
		connID, userID, enc, db.TimeStr(time.Now().UTC()))
	return err
}

// RemoveGrant deletes a token grant, if any.
func RemoveGrant(q db.Queryer, connID, userID int64) error {
	_, err := q.Exec("DELETE FROM oauth_grants WHERE connection_id = ? AND user_id = ?", connID, userID)
	return err
}
