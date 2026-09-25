package data

import (
	"database/sql"
	"errors"
	"time"

	"dashboard/internal/db"
)

// Advice returns a stored answer for a hint if its digest still matches.
func Advice(q db.Queryer, hintID int64, locale, digest string) (string, bool, error) {
	var text string
	err := q.QueryRow("SELECT text FROM hint_advice WHERE hint_id = ? AND locale = ? AND digest = ?", hintID, locale, digest).Scan(&text)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return text, err == nil, err
}

// SaveAdvice stores or replaces a hint's answer.
func SaveAdvice(q db.Queryer, hintID int64, locale, digest, text string) error {
	_, err := q.Exec(`INSERT INTO hint_advice (hint_id, locale, digest, text, created_at) VALUES (?,?,?,?,?)
		ON CONFLICT (hint_id, locale) DO UPDATE SET digest = excluded.digest, text = excluded.text, created_at = excluded.created_at`,
		hintID, locale, digest, text, db.TimeStr(time.Now().UTC()))
	return err
}

// MailReads returns the stored reads of a mailbox by mail uid.
func MailReads(q db.Queryer, connID int64) (map[uint32]map[string]any, error) {
	rows, err := q.Query("SELECT uid, data FROM mail_reads WHERE connection_id = ?", connID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[uint32]map[string]any{}
	for rows.Next() {
		var uid uint32
		var raw string
		if err := rows.Scan(&uid, &raw); err != nil {
			return nil, err
		}
		fields := map[string]any{}
		if err := db.FromJSON(raw, &fields); err != nil {
			return nil, err
		}
		out[uid] = fields
	}
	return out, rows.Err()
}

// SaveMailRead stores or replaces the fields read from one mail.
func SaveMailRead(q db.Queryer, connID int64, uid uint32, fields map[string]any) error {
	raw, err := db.ToJSON(fields)
	if err != nil {
		return err
	}
	_, err = q.Exec(`INSERT INTO mail_reads (connection_id, uid, data, created_at) VALUES (?,?,?,?)
		ON CONFLICT (connection_id, uid) DO UPDATE SET data = excluded.data, created_at = excluded.created_at`,
		connID, uid, raw, db.TimeStr(time.Now().UTC()))
	return err
}
