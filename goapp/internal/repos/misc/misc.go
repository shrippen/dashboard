// Package misc provides database access for shares, themes, instance
// settings and the audit log.
package misc

import (
	"database/sql"
	"errors"
	"time"

	"dashboard/internal/db"
	"dashboard/internal/enums"
	"dashboard/internal/model"
)

const auditPage = 200

// ── Shares ──

func scanShare(row interface{ Scan(...any) error }) (*model.Share, error) {
	var sh model.Share
	var createdBy sql.NullInt64
	err := row.Scan(&sh.ID, &sh.ResourceKind, &sh.ResourceID, &sh.GranteeKind, &sh.GranteeID,
		&sh.Right, &createdBy)
	if err != nil {
		return nil, err
	}
	if createdBy.Valid {
		sh.CreatedBy = &createdBy.Int64
	}
	return &sh, nil
}

const shareCols = `id, resource_kind, resource_id, grantee_kind, grantee_id, right, created_by`

// SharesFor returns every share on one resource.
func SharesFor(q db.Queryer, kind enums.ResourceKind, resourceID int64) ([]*model.Share, error) {
	rows, err := q.Query(
		"SELECT "+shareCols+" FROM shares WHERE resource_kind = ? AND resource_id = ?",
		kind, resourceID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanShares(rows)
}

// SharesTo returns every share granted to a user directly or via one of
// their teams.
func SharesTo(q db.Queryer, userID int64, teamIDs []int64) ([]*model.Share, error) {
	placeholders, args := "", []any{enums.GranteeUser, userID, enums.GranteeTeam}
	if len(teamIDs) == 0 {
		rows, err := q.Query(
			"SELECT "+shareCols+" FROM shares WHERE grantee_kind = ? AND grantee_id = ?",
			enums.GranteeUser, userID,
		)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		return scanShares(rows)
	}
	for i, id := range teamIDs {
		if i > 0 {
			placeholders += ","
		}
		placeholders += "?"
		args = append(args, id)
	}
	query := "SELECT " + shareCols + " FROM shares WHERE (grantee_kind = ? AND grantee_id = ?)" +
		" OR (grantee_kind = ? AND grantee_id IN (" + placeholders + "))"
	rows, err := q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanShares(rows)
}

func scanShares(rows *sql.Rows) ([]*model.Share, error) {
	var out []*model.Share
	for rows.Next() {
		sh, err := scanShare(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, sh)
	}
	return out, rows.Err()
}

// ShareByID returns one share by id, or nil.
func ShareByID(q db.Queryer, shareID int64) (*model.Share, error) {
	row := q.QueryRow("SELECT "+shareCols+" FROM shares WHERE id = ?", shareID)
	sh, err := scanShare(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return sh, err
}

// AddShare inserts a new share.
func AddShare(q db.Queryer, sh *model.Share) error {
	res, err := q.Exec(
		"INSERT INTO shares (resource_kind, resource_id, grantee_kind, grantee_id, right, created_by) VALUES (?,?,?,?,?,?)",
		sh.ResourceKind, sh.ResourceID, sh.GranteeKind, sh.GranteeID, sh.Right, sh.CreatedBy,
	)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	sh.ID = id
	return nil
}

// RemoveShare deletes one share.
func RemoveShare(q db.Queryer, shareID int64) error {
	_, err := q.Exec("DELETE FROM shares WHERE id = ?", shareID)
	return err
}

// DropShares deletes every share on one resource (e.g. before deleting it).
func DropShares(q db.Queryer, kind enums.ResourceKind, resourceID int64) error {
	_, err := q.Exec(
		"DELETE FROM shares WHERE resource_kind = ? AND resource_id = ?", kind, resourceID,
	)
	return err
}

// ── Themes ──

const themeCols = `id, space_id, slug, name, builtin, contract, dark, light, custom_css,
	fonts, digest, version, updated_at`

func scanTheme(row interface{ Scan(...any) error }) (*model.Theme, error) {
	var t model.Theme
	var spaceID sql.NullInt64
	var digest sql.NullString
	var dark, light, fonts, updatedAt string

	err := row.Scan(&t.ID, &spaceID, &t.Slug, &t.Name, &t.Builtin, &t.Contract, &dark, &light,
		&t.CustomCSS, &fonts, &digest, &t.Version, &updatedAt)
	if err != nil {
		return nil, err
	}
	if spaceID.Valid {
		t.SpaceID = &spaceID.Int64
	}
	t.Digest = digest.String
	t.Dark, t.Light = map[string]any{}, map[string]any{}
	if err := db.FromJSON(dark, &t.Dark); err != nil {
		return nil, err
	}
	if err := db.FromJSON(light, &t.Light); err != nil {
		return nil, err
	}
	if err := db.FromJSON(fonts, &t.Fonts); err != nil {
		return nil, err
	}
	t.UpdatedAt, err = db.ParseTime(updatedAt)
	return &t, err
}

// Theme returns a theme by id, or nil.
func Theme(q db.Queryer, themeID int64) (*model.Theme, error) {
	row := q.QueryRow("SELECT "+themeCols+" FROM themes WHERE id = ?", themeID)
	t, err := scanTheme(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return t, err
}

// BuiltinTheme returns the shipped theme with the given slug, or nil.
func BuiltinTheme(q db.Queryer, slug string) (*model.Theme, error) {
	row := q.QueryRow("SELECT "+themeCols+" FROM themes WHERE builtin = 1 AND slug = ?", slug)
	t, err := scanTheme(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return t, err
}

// Themes returns every built-in theme plus the custom ones of the given
// spaces, ordered by name.
func Themes(q db.Queryer, spaceIDs []int64) ([]*model.Theme, error) {
	placeholders, args := "", []any{}
	for i, id := range spaceIDs {
		if i > 0 {
			placeholders += ","
		}
		placeholders += "?"
		args = append(args, id)
	}
	query := "SELECT " + themeCols + " FROM themes WHERE builtin = 1"
	if placeholders != "" {
		query += " OR space_id IN (" + placeholders + ")"
	}
	query += " ORDER BY name"

	rows, err := q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*model.Theme
	for rows.Next() {
		t, err := scanTheme(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// AddTheme inserts a new theme.
func AddTheme(q db.Queryer, t *model.Theme) error {
	dark, err := db.ToJSON(orEmpty(t.Dark))
	if err != nil {
		return err
	}
	light, err := db.ToJSON(orEmpty(t.Light))
	if err != nil {
		return err
	}
	fonts, err := db.ToJSON(orEmptySlice(t.Fonts))
	if err != nil {
		return err
	}
	res, err := q.Exec(`INSERT INTO themes
		(space_id, slug, name, builtin, contract, dark, light, custom_css, fonts, digest, version, updated_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		t.SpaceID, t.Slug, t.Name, t.Builtin, t.Contract, dark, light, t.CustomCSS, fonts,
		nullStr(t.Digest), t.Version, db.TimeStr(t.UpdatedAt),
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

// UpdateTheme writes back every mutable field and bumps version/updated_at.
func UpdateTheme(q db.Queryer, t *model.Theme) error {
	dark, err := db.ToJSON(orEmpty(t.Dark))
	if err != nil {
		return err
	}
	light, err := db.ToJSON(orEmpty(t.Light))
	if err != nil {
		return err
	}
	fonts, err := db.ToJSON(orEmptySlice(t.Fonts))
	if err != nil {
		return err
	}
	_, err = q.Exec(`UPDATE themes SET
		name=?, contract=?, dark=?, light=?, custom_css=?, fonts=?, digest=?, version=?, updated_at=?
		WHERE id=?`,
		t.Name, t.Contract, dark, light, t.CustomCSS, fonts, nullStr(t.Digest), t.Version,
		db.TimeStr(t.UpdatedAt), t.ID,
	)
	return err
}

// RemoveTheme deletes a theme.
func RemoveTheme(q db.Queryer, themeID int64) error {
	_, err := q.Exec("DELETE FROM themes WHERE id = ?", themeID)
	return err
}

// ── Instance settings ──

// Setting returns one instance setting value, or an empty map.
func Setting(q db.Queryer, key string) (map[string]any, error) {
	var value string
	err := q.QueryRow("SELECT value FROM instance_settings WHERE key = ?", key).Scan(&value)
	out := map[string]any{}
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	return out, db.FromJSON(value, &out)
}

// SetSetting inserts or replaces one instance setting.
func SetSetting(q db.Queryer, key string, value map[string]any) error {
	text, err := db.ToJSON(orEmpty(value))
	if err != nil {
		return err
	}
	_, err = q.Exec(
		"INSERT INTO instance_settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value",
		key, text,
	)
	return err
}

// ── Audit ──

// Audit inserts a new audit log entry.
func Audit(q db.Queryer, e *model.AuditEntry) error {
	detail, err := db.ToJSON(orEmpty(e.Detail))
	if err != nil {
		return err
	}
	_, err = q.Exec(
		"INSERT INTO audit_log (at, user_id, action, target, detail, ip) VALUES (?,?,?,?,?,?)",
		db.TimeStr(e.At), e.UserID, e.Action, e.Target, detail, e.IP,
	)
	return err
}

// AuditPage returns up to auditPage entries older than before (or the most
// recent page if before is nil), newest first.
func AuditPage(q db.Queryer, before *time.Time) ([]*model.AuditEntry, error) {
	query := "SELECT id, at, user_id, action, target, detail, ip FROM audit_log"
	args := []any{}
	if before != nil {
		query += " WHERE at < ?"
		args = append(args, db.TimeStr(*before))
	}
	query += " ORDER BY at DESC LIMIT ?"
	args = append(args, auditPage)

	rows, err := q.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*model.AuditEntry
	for rows.Next() {
		var e model.AuditEntry
		var at, detail string
		var userID sql.NullInt64
		if err := rows.Scan(&e.ID, &at, &userID, &e.Action, &e.Target, &detail, &e.IP); err != nil {
			return nil, err
		}
		if userID.Valid {
			e.UserID = &userID.Int64
		}
		if e.At, err = db.ParseTime(at); err != nil {
			return nil, err
		}
		e.Detail = map[string]any{}
		if err := db.FromJSON(detail, &e.Detail); err != nil {
			return nil, err
		}
		out = append(out, &e)
	}
	return out, rows.Err()
}

// PruneAudit deletes audit entries older than olderThan.
func PruneAudit(q db.Queryer, olderThan time.Time) error {
	_, err := q.Exec("DELETE FROM audit_log WHERE at < ?", db.TimeStr(olderThan))
	return err
}

// EncryptedRows returns every row holding an encrypted value, for key
// rotation.
func EncryptedRows(q db.Queryer) (connections []*model.Connection, credentials []*model.UserCredential,
	usersOut []*model.User, channels []*model.NotifyChannel, err error) {

	if connections, err = allConnections(q); err != nil {
		return
	}
	if credentials, err = allCredentials(q); err != nil {
		return
	}
	if usersOut, err = allUsers(q); err != nil {
		return
	}
	channels, err = allChannels(q)
	return
}

func allConnections(q db.Queryer) ([]*model.Connection, error) {
	rows, err := q.Query(`SELECT id, space_id, key, name, service, url, credential_mode,
		secret_enc, options, verify_tls, created_at FROM connections`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*model.Connection
	for rows.Next() {
		var c model.Connection
		var options, createdAt string
		if err := rows.Scan(&c.ID, &c.SpaceID, &c.Key, &c.Name, &c.Service, &c.URL,
			&c.CredentialMode, &c.SecretEnc, &options, &c.VerifyTLS, &createdAt); err != nil {
			return nil, err
		}
		c.Options = map[string]any{}
		_ = db.FromJSON(options, &c.Options)
		c.CreatedAt, _ = db.ParseTime(createdAt)
		out = append(out, &c)
	}
	return out, rows.Err()
}

func allCredentials(q db.Queryer) ([]*model.UserCredential, error) {
	rows, err := q.Query("SELECT id, connection_id, user_id, secret_enc FROM user_credentials")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*model.UserCredential
	for rows.Next() {
		var c model.UserCredential
		if err := rows.Scan(&c.ID, &c.ConnectionID, &c.UserID, &c.SecretEnc); err != nil {
			return nil, err
		}
		out = append(out, &c)
	}
	return out, rows.Err()
}

func allUsers(q db.Queryer) ([]*model.User, error) {
	rows, err := q.Query("SELECT id, totp_secret_enc FROM users")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*model.User
	for rows.Next() {
		var u model.User
		if err := rows.Scan(&u.ID, &u.TOTPSecretEnc); err != nil {
			return nil, err
		}
		out = append(out, &u)
	}
	return out, rows.Err()
}

func allChannels(q db.Queryer) ([]*model.NotifyChannel, error) {
	rows, err := q.Query("SELECT id, user_id, name, url_enc, min_severity, enabled FROM notify_channels")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*model.NotifyChannel
	for rows.Next() {
		var c model.NotifyChannel
		if err := rows.Scan(&c.ID, &c.UserID, &c.Name, &c.URLEnc, &c.MinSeverity, &c.Enabled); err != nil {
			return nil, err
		}
		out = append(out, &c)
	}
	return out, rows.Err()
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func orEmpty(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}

func orEmptySlice[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
