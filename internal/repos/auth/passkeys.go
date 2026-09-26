package auth

import (
	"database/sql"
	"errors"
	"time"

	"andon/internal/db"
	"andon/internal/model"
)

const passkeyCols = "id, user_id, cred_id, name, data, created_at, last_used_at"

func scanPasskey(row interface{ Scan(...any) error }) (*model.Passkey, error) {
	var p model.Passkey
	var createdAt string
	var usedAt sql.NullString
	if err := row.Scan(&p.ID, &p.UserID, &p.CredID, &p.Name, &p.Data, &createdAt, &usedAt); err != nil {
		return nil, err
	}
	var err error
	if p.CreatedAt, err = db.ParseTime(createdAt); err != nil {
		return nil, err
	}
	if p.LastUsedAt, err = nullTime(usedAt); err != nil {
		return nil, err
	}
	return &p, nil
}

// PasskeysOf returns a user's passkeys, oldest first.
func PasskeysOf(q db.Queryer, userID int64) ([]*model.Passkey, error) {
	rows, err := q.Query("SELECT "+passkeyCols+" FROM passkeys WHERE user_id = ? ORDER BY id", userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*model.Passkey
	for rows.Next() {
		p, err := scanPasskey(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Passkey returns a passkey by id, or nil.
func Passkey(q db.Queryer, passkeyID int64) (*model.Passkey, error) {
	p, err := scanPasskey(q.QueryRow("SELECT "+passkeyCols+" FROM passkeys WHERE id = ?", passkeyID))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return p, err
}

// AddPasskey inserts a passkey and sets its ID.
func AddPasskey(q db.Queryer, p *model.Passkey) error {
	res, err := q.Exec("INSERT INTO passkeys (user_id, cred_id, name, data, created_at) VALUES (?,?,?,?,?)",
		p.UserID, p.CredID, p.Name, p.Data, db.TimeStr(p.CreatedAt))
	if err != nil {
		return err
	}
	p.ID, err = res.LastInsertId()
	return err
}

// TouchPasskey stores the updated credential (sign count) after a login.
func TouchPasskey(q db.Queryer, passkeyID int64, data string, at time.Time) error {
	_, err := q.Exec("UPDATE passkeys SET data = ?, last_used_at = ? WHERE id = ?", data, db.TimeStr(at), passkeyID)
	return err
}

// RemovePasskey deletes a passkey.
func RemovePasskey(q db.Queryer, passkeyID int64) error {
	_, err := q.Exec("DELETE FROM passkeys WHERE id = ?", passkeyID)
	return err
}
