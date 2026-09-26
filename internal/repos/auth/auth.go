// Package auth provides database access for login sessions, API tokens,
// invitations and password resets.
package auth

import (
	"database/sql"
	"errors"
	"time"

	"andon/internal/db"
	"andon/internal/model"
)

const sessionCols = `id, token_hash, user_id, method, csrf, created_at, last_seen,
	expires_at, ip, user_agent, pending_2fa, id_token`

func scanSession(row interface{ Scan(...any) error }) (*model.LoginSession, error) {
	var s model.LoginSession
	var createdAt, lastSeen, expiresAt string
	var idToken sql.NullString

	err := row.Scan(
		&s.ID, &s.TokenHash, &s.UserID, &s.Method, &s.CSRF, &createdAt, &lastSeen,
		&expiresAt, &s.IP, &s.UserAgent, &s.Pending2FA, &idToken,
	)
	if err != nil {
		return nil, err
	}
	s.IDToken = idToken.String
	if s.CreatedAt, err = db.ParseTime(createdAt); err != nil {
		return nil, err
	}
	if s.LastSeen, err = db.ParseTime(lastSeen); err != nil {
		return nil, err
	}
	if s.ExpiresAt, err = db.ParseTime(expiresAt); err != nil {
		return nil, err
	}
	return &s, nil
}

// SessionByHash returns a login session by its token hash, or nil.
func SessionByHash(q db.Queryer, tokenHash string) (*model.LoginSession, error) {
	row := q.QueryRow("SELECT "+sessionCols+" FROM sessions WHERE token_hash = ?", tokenHash)
	s, err := scanSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return s, err
}

// SessionsOf returns a user's sessions, most recently seen first.
func SessionsOf(q db.Queryer, userID int64) ([]*model.LoginSession, error) {
	rows, err := q.Query(
		"SELECT "+sessionCols+" FROM sessions WHERE user_id = ? ORDER BY last_seen DESC", userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*model.LoginSession
	for rows.Next() {
		s, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// AddSession inserts a new login session and sets its ID.
func AddSession(q db.Queryer, s *model.LoginSession) error {
	res, err := q.Exec(`INSERT INTO sessions
		(token_hash, user_id, method, csrf, created_at, last_seen, expires_at, ip,
		 user_agent, pending_2fa, id_token)
		VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		s.TokenHash, s.UserID, s.Method, s.CSRF, db.TimeStr(s.CreatedAt), db.TimeStr(s.LastSeen),
		db.TimeStr(s.ExpiresAt), s.IP, s.UserAgent, s.Pending2FA, nullStr(s.IDToken),
	)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	s.ID = id
	return nil
}

// TouchSession bumps last_seen and, once the second factor is verified,
// clears pending_2fa.
func TouchSession(q db.Queryer, sessionID int64, lastSeen time.Time, pending2FA bool) error {
	_, err := q.Exec(
		"UPDATE sessions SET last_seen = ?, pending_2fa = ? WHERE id = ?",
		db.TimeStr(lastSeen), pending2FA, sessionID,
	)
	return err
}

// RemoveSession deletes one session.
func RemoveSession(q db.Queryer, sessionID int64) error {
	_, err := q.Exec("DELETE FROM sessions WHERE id = ?", sessionID)
	return err
}

// DropSessions deletes every session of a user, optionally keeping one
// (e.g. the session that just changed the password).
func DropSessions(q db.Queryer, userID int64, keepID *int64) error {
	if keepID == nil {
		_, err := q.Exec("DELETE FROM sessions WHERE user_id = ?", userID)
		return err
	}
	_, err := q.Exec("DELETE FROM sessions WHERE user_id = ? AND id != ?", userID, *keepID)
	return err
}

// PurgeExpired removes expired sessions, invites and reset tokens.
func PurgeExpired(q db.Queryer, now time.Time) error {
	ts := db.TimeStr(now)
	if _, err := q.Exec("DELETE FROM sessions WHERE expires_at < ?", ts); err != nil {
		return err
	}
	if _, err := q.Exec(
		"DELETE FROM invites WHERE expires_at < ? AND used_at IS NULL", ts,
	); err != nil {
		return err
	}
	_, err := q.Exec("DELETE FROM reset_tokens WHERE expires_at < ?", ts)
	return err
}

const tokenCols = `id, user_id, name, token_hash, prefix, scope, board_ids, expires_at,
	last_used_at, created_at`

func scanToken(row interface{ Scan(...any) error }) (*model.ApiToken, error) {
	var t model.ApiToken
	var boardIDs, createdAt string
	var expiresAt, lastUsedAt sql.NullString

	err := row.Scan(
		&t.ID, &t.UserID, &t.Name, &t.TokenHash, &t.Prefix, &t.Scope, &boardIDs,
		&expiresAt, &lastUsedAt, &createdAt,
	)
	if err != nil {
		return nil, err
	}
	if err := db.FromJSON(boardIDs, &t.BoardIDs); err != nil {
		return nil, err
	}
	if t.CreatedAt, err = db.ParseTime(createdAt); err != nil {
		return nil, err
	}
	if t.ExpiresAt, err = nullTime(expiresAt); err != nil {
		return nil, err
	}
	if t.LastUsedAt, err = nullTime(lastUsedAt); err != nil {
		return nil, err
	}
	return &t, nil
}

// TokenByHash returns an API token by its hash, or nil.
func TokenByHash(q db.Queryer, tokenHash string) (*model.ApiToken, error) {
	row := q.QueryRow("SELECT "+tokenCols+" FROM api_tokens WHERE token_hash = ?", tokenHash)
	t, err := scanToken(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return t, err
}

// TokensOf returns a user's API tokens, oldest first.
func TokensOf(q db.Queryer, userID int64) ([]*model.ApiToken, error) {
	rows, err := q.Query(
		"SELECT "+tokenCols+" FROM api_tokens WHERE user_id = ? ORDER BY created_at", userID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*model.ApiToken
	for rows.Next() {
		t, err := scanToken(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// Token returns one API token by id, or nil.
func Token(q db.Queryer, tokenID int64) (*model.ApiToken, error) {
	row := q.QueryRow("SELECT "+tokenCols+" FROM api_tokens WHERE id = ?", tokenID)
	t, err := scanToken(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return t, err
}

// AddToken inserts a new API token.
func AddToken(q db.Queryer, t *model.ApiToken) error {
	boardIDs, err := db.ToJSON(orEmptySlice(t.BoardIDs))
	if err != nil {
		return err
	}
	res, err := q.Exec(`INSERT INTO api_tokens
		(user_id, name, token_hash, prefix, scope, board_ids, expires_at, last_used_at, created_at)
		VALUES (?,?,?,?,?,?,?,?,?)`,
		t.UserID, t.Name, t.TokenHash, t.Prefix, t.Scope, boardIDs,
		db.NullTimeStr(t.ExpiresAt), db.NullTimeStr(t.LastUsedAt), db.TimeStr(t.CreatedAt),
	)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	t.ID = id
	return nil
}

// TouchToken records that an API token was just used.
func TouchToken(q db.Queryer, tokenID int64, at time.Time) error {
	_, err := q.Exec("UPDATE api_tokens SET last_used_at = ? WHERE id = ?", db.TimeStr(at), tokenID)
	return err
}

// RemoveToken deletes one API token.
func RemoveToken(q db.Queryer, tokenID int64) error {
	_, err := q.Exec("DELETE FROM api_tokens WHERE id = ?", tokenID)
	return err
}

const inviteCols = `id, email, token_hash, role, teams, created_by, expires_at, used_at`

func scanInvite(row interface{ Scan(...any) error }) (*model.Invite, error) {
	var inv model.Invite
	var teams, expiresAt string
	var createdBy sql.NullInt64
	var usedAt sql.NullString

	err := row.Scan(
		&inv.ID, &inv.Email, &inv.TokenHash, &inv.Role, &teams, &createdBy, &expiresAt, &usedAt,
	)
	if err != nil {
		return nil, err
	}
	if err := db.FromJSON(teams, &inv.Teams); err != nil {
		return nil, err
	}
	if createdBy.Valid {
		inv.CreatedBy = &createdBy.Int64
	}
	if inv.ExpiresAt, err = db.ParseTime(expiresAt); err != nil {
		return nil, err
	}
	if inv.UsedAt, err = nullTime(usedAt); err != nil {
		return nil, err
	}
	return &inv, nil
}

// InviteByHash returns an invite by its token hash, or nil.
func InviteByHash(q db.Queryer, tokenHash string) (*model.Invite, error) {
	row := q.QueryRow("SELECT "+inviteCols+" FROM invites WHERE token_hash = ?", tokenHash)
	inv, err := scanInvite(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return inv, err
}

// OpenInvites returns every not-yet-used invite, soonest expiry first.
func OpenInvites(q db.Queryer) ([]*model.Invite, error) {
	rows, err := q.Query(
		"SELECT " + inviteCols + " FROM invites WHERE used_at IS NULL ORDER BY expires_at",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*model.Invite
	for rows.Next() {
		inv, err := scanInvite(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, inv)
	}
	return out, rows.Err()
}

// AddInvite inserts a new invite.
func AddInvite(q db.Queryer, inv *model.Invite) error {
	teams, err := db.ToJSON(orEmptySlice(inv.Teams))
	if err != nil {
		return err
	}
	res, err := q.Exec(`INSERT INTO invites
		(email, token_hash, role, teams, created_by, expires_at, used_at)
		VALUES (?,?,?,?,?,?,?)`,
		inv.Email, inv.TokenHash, inv.Role, teams, inv.CreatedBy, db.TimeStr(inv.ExpiresAt),
		db.NullTimeStr(inv.UsedAt),
	)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	inv.ID = id
	return nil
}

// MarkInviteUsed sets used_at.
func MarkInviteUsed(q db.Queryer, inviteID int64, at time.Time) error {
	_, err := q.Exec("UPDATE invites SET used_at = ? WHERE id = ?", db.TimeStr(at), inviteID)
	return err
}

// ResetByHash returns a password reset token by its hash, or nil.
func ResetByHash(q db.Queryer, tokenHash string) (*model.ResetToken, error) {
	var r model.ResetToken
	var expiresAt string
	var usedAt sql.NullString

	err := q.QueryRow(
		"SELECT id, user_id, token_hash, expires_at, used_at FROM reset_tokens WHERE token_hash = ?",
		tokenHash,
	).Scan(&r.ID, &r.UserID, &r.TokenHash, &expiresAt, &usedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if r.ExpiresAt, err = db.ParseTime(expiresAt); err != nil {
		return nil, err
	}
	if r.UsedAt, err = nullTime(usedAt); err != nil {
		return nil, err
	}
	return &r, nil
}

// AddReset inserts a new password reset token.
func AddReset(q db.Queryer, r *model.ResetToken) error {
	res, err := q.Exec(
		"INSERT INTO reset_tokens (user_id, token_hash, expires_at, used_at) VALUES (?,?,?,?)",
		r.UserID, r.TokenHash, db.TimeStr(r.ExpiresAt), db.NullTimeStr(r.UsedAt),
	)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	r.ID = id
	return nil
}

// MarkResetUsed sets used_at.
func MarkResetUsed(q db.Queryer, resetID int64, at time.Time) error {
	_, err := q.Exec("UPDATE reset_tokens SET used_at = ? WHERE id = ?", db.TimeStr(at), resetID)
	return err
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullTime(ns sql.NullString) (*time.Time, error) {
	if !ns.Valid {
		return nil, nil
	}
	t, err := db.ParseTime(ns.String)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func orEmptySlice[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// RemoveInvite deletes an invite (admin revoke).
func RemoveInvite(q db.Queryer, inviteID int64) error {
	_, err := q.Exec("DELETE FROM invites WHERE id = ?", inviteID)
	return err
}
