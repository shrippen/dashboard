// Package users provides database access for users, teams and memberships.
package users

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	"andon/internal/db"
	"andon/internal/enums"
	"andon/internal/model"
)

func now() time.Time { return time.Now().UTC() }

var ErrNotFound = errors.New("users: not found")

func scanUser(row interface{ Scan(...any) error }) (*model.User, error) {
	var u model.User
	var themeID, startBoardID sql.NullInt64
	var lastLoginAt sql.NullString
	var prefs, recoveryCodes, createdAt string
	var passwordHash, searchEngine, oidcSub sql.NullString

	err := row.Scan(
		&u.ID, &u.Email, &u.Name, &passwordHash, &u.Role, &u.IsActive, &u.IsBreakglass,
		&u.Locale, &u.ColorMode, &themeID, &startBoardID, &searchEngine, &prefs,
		&u.TOTPSecretEnc, &u.TOTPEnabled, &recoveryCodes, &oidcSub, &createdAt, &lastLoginAt,
	)
	if err != nil {
		return nil, err
	}
	u.PasswordHash = passwordHash.String
	u.SearchEngine = searchEngine.String
	u.OIDCSub = oidcSub.String

	if themeID.Valid {
		u.ThemeID = &themeID.Int64
	}
	if startBoardID.Valid {
		u.StartBoardID = &startBoardID.Int64
	}
	if u.CreatedAt, err = db.ParseTime(createdAt); err != nil {
		return nil, err
	}
	if lastLoginAt.Valid {
		t, err := db.ParseTime(lastLoginAt.String)
		if err != nil {
			return nil, err
		}
		u.LastLoginAt = &t
	}
	u.Prefs = map[string]any{}
	if err := db.FromJSON(prefs, &u.Prefs); err != nil {
		return nil, err
	}
	if err := db.FromJSON(recoveryCodes, &u.RecoveryCodes); err != nil {
		return nil, err
	}
	return &u, nil
}

const userCols = `id, email, name, password_hash, role, is_active, is_breakglass,
	locale, color_mode, theme_id, start_board_id, search_engine, prefs,
	totp_secret_enc, totp_enabled, recovery_codes, oidc_sub, created_at, last_login_at`

// Get returns a user by id, or nil if none exists.
func Get(q db.Queryer, userID int64) (*model.User, error) {
	row := q.QueryRow("SELECT "+userCols+" FROM users WHERE id = ?", userID)
	u, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return u, err
}

// ByEmail looks up a user case-insensitively by email.
func ByEmail(q db.Queryer, email string) (*model.User, error) {
	row := q.QueryRow("SELECT "+userCols+" FROM users WHERE lower(email) = ?", strings.ToLower(email))
	u, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return u, err
}

// BySub looks up a user by their OIDC subject.
func BySub(q db.Queryer, sub string) (*model.User, error) {
	row := q.QueryRow("SELECT "+userCols+" FROM users WHERE oidc_sub = ?", sub)
	u, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return u, err
}

// All returns every user, ordered by name.
func All(q db.Queryer) ([]*model.User, error) {
	rows, err := q.Query("SELECT " + userCols + " FROM users ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*model.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// Count returns the total number of users.
func Count(q db.Queryer) (int, error) {
	var n int
	err := q.QueryRow("SELECT COUNT(*) FROM users").Scan(&n)
	return n, err
}

// CountAdmins returns the number of active instance admins.
func CountAdmins(q db.Queryer) (int, error) {
	var n int
	err := q.QueryRow(
		"SELECT COUNT(*) FROM users WHERE role = ? AND is_active = 1", enums.RoleAdmin,
	).Scan(&n)
	return n, err
}

// Add inserts a new user and sets its ID.
func Add(q db.Queryer, u *model.User) error {
	prefs, err := db.ToJSON(orEmpty(u.Prefs))
	if err != nil {
		return err
	}
	codes, err := db.ToJSON(orEmptySlice(u.RecoveryCodes))
	if err != nil {
		return err
	}

	res, err := q.Exec(`INSERT INTO users
		(email, name, password_hash, role, is_active, is_breakglass, locale, color_mode,
		 theme_id, start_board_id, search_engine, prefs, totp_secret_enc, totp_enabled,
		 recovery_codes, oidc_sub, created_at, last_login_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		u.Email, u.Name, nullStr(u.PasswordHash), u.Role, u.IsActive, u.IsBreakglass,
		u.Locale, u.ColorMode, u.ThemeID, u.StartBoardID, nullStr(u.SearchEngine), prefs,
		u.TOTPSecretEnc, u.TOTPEnabled, codes, nullStr(u.OIDCSub), db.TimeStr(u.CreatedAt),
		db.NullTimeStr(u.LastLoginAt),
	)
	if err != nil {
		return err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return err
	}
	u.ID = id
	return nil
}

// Update writes back every mutable field of u.
func Update(q db.Queryer, u *model.User) error {
	prefs, err := db.ToJSON(orEmpty(u.Prefs))
	if err != nil {
		return err
	}
	codes, err := db.ToJSON(orEmptySlice(u.RecoveryCodes))
	if err != nil {
		return err
	}

	_, err = q.Exec(`UPDATE users SET
		email=?, name=?, password_hash=?, role=?, is_active=?, is_breakglass=?, locale=?,
		color_mode=?, theme_id=?, start_board_id=?, search_engine=?, prefs=?,
		totp_secret_enc=?, totp_enabled=?, recovery_codes=?, oidc_sub=?, last_login_at=?
		WHERE id=?`,
		u.Email, u.Name, nullStr(u.PasswordHash), u.Role, u.IsActive, u.IsBreakglass,
		u.Locale, u.ColorMode, u.ThemeID, u.StartBoardID, nullStr(u.SearchEngine), prefs,
		u.TOTPSecretEnc, u.TOTPEnabled, codes, nullStr(u.OIDCSub), db.NullTimeStr(u.LastLoginAt),
		u.ID,
	)
	return err
}

// UpdateTOTPSecret rewrites a user's encrypted TOTP secret (key rotation
// only; Update covers the normal CRUD path, but needs a fully-populated
// User to avoid blanking every other column).
func UpdateTOTPSecret(q db.Queryer, userID int64, enc []byte) error {
	_, err := q.Exec("UPDATE users SET totp_secret_enc=? WHERE id=?", enc, userID)
	return err
}

// Delete removes a user (cascades to memberships, sessions, etc.).
func Delete(q db.Queryer, userID int64) error {
	_, err := q.Exec("DELETE FROM users WHERE id = ?", userID)
	return err
}

func scanTeam(row interface{ Scan(...any) error }) (*model.Team, error) {
	var t model.Team
	var createdAt string
	if err := row.Scan(&t.ID, &t.Name, &createdAt); err != nil {
		return nil, err
	}
	var err error
	t.CreatedAt, err = db.ParseTime(createdAt)
	return &t, err
}

// Team returns a team by id, or nil if none exists.
func Team(q db.Queryer, teamID int64) (*model.Team, error) {
	row := q.QueryRow("SELECT id, name, created_at FROM teams WHERE id = ?", teamID)
	t, err := scanTeam(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return t, err
}

// TeamByName looks up a team case-insensitively by name.
func TeamByName(q db.Queryer, name string) (*model.Team, error) {
	row := q.QueryRow("SELECT id, name, created_at FROM teams WHERE lower(name) = ?", strings.ToLower(name))
	t, err := scanTeam(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return t, err
}

// Teams returns every team, ordered by name.
func Teams(q db.Queryer) ([]*model.Team, error) {
	rows, err := q.Query("SELECT id, name, created_at FROM teams ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []*model.Team
	for rows.Next() {
		t, err := scanTeam(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// AddTeam inserts a new team.
func AddTeam(q db.Queryer, name string) (*model.Team, error) {
	res, err := q.Exec(
		"INSERT INTO teams (name, created_at) VALUES (?, ?)", name, db.TimeStr(now()),
	)
	if err != nil {
		return nil, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return &model.Team{ID: id, Name: name, CreatedAt: now()}, nil
}

// DeleteTeam removes a team (cascades to memberships).
func DeleteTeam(q db.Queryer, teamID int64) error {
	_, err := q.Exec("DELETE FROM teams WHERE id = ?", teamID)
	return err
}

// Memberships returns every team membership of one user.
func Memberships(q db.Queryer, userID int64) ([]*model.Membership, error) {
	rows, err := q.Query("SELECT id, user_id, team_id, role FROM memberships WHERE user_id = ?", userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMemberships(rows)
}

// Members returns every membership of one team.
func Members(q db.Queryer, teamID int64) ([]*model.Membership, error) {
	rows, err := q.Query("SELECT id, user_id, team_id, role FROM memberships WHERE team_id = ?", teamID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMemberships(rows)
}

func scanMemberships(rows *sql.Rows) ([]*model.Membership, error) {
	var out []*model.Membership
	for rows.Next() {
		var m model.Membership
		if err := rows.Scan(&m.ID, &m.UserID, &m.TeamID, &m.Role); err != nil {
			return nil, err
		}
		out = append(out, &m)
	}
	return out, rows.Err()
}

// Membership returns the membership of one user in one team, or nil.
func MembershipOf(q db.Queryer, userID, teamID int64) (*model.Membership, error) {
	var m model.Membership
	err := q.QueryRow(
		"SELECT id, user_id, team_id, role FROM memberships WHERE user_id = ? AND team_id = ?",
		userID, teamID,
	).Scan(&m.ID, &m.UserID, &m.TeamID, &m.Role)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return &m, err
}

// SetMember inserts or updates a membership's role.
func SetMember(q db.Queryer, userID, teamID int64, role enums.TeamRole) error {
	existing, err := MembershipOf(q, userID, teamID)
	if err != nil {
		return err
	}
	if existing == nil {
		_, err := q.Exec(
			"INSERT INTO memberships (user_id, team_id, role) VALUES (?,?,?)", userID, teamID, role,
		)
		return err
	}
	_, err = q.Exec("UPDATE memberships SET role = ? WHERE id = ?", role, existing.ID)
	return err
}

// RemoveMember deletes a membership, if any.
func RemoveMember(q db.Queryer, userID, teamID int64) error {
	_, err := q.Exec("DELETE FROM memberships WHERE user_id = ? AND team_id = ?", userID, teamID)
	return err
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
