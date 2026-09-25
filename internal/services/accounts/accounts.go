// Package accounts creates users with their personal space and manages
// profile changes.
package accounts

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	"dashboard/internal/crypto"
	"dashboard/internal/db"
	"dashboard/internal/enums"
	"dashboard/internal/model"
	"dashboard/internal/repos/content"
	"dashboard/internal/repos/users"
	"dashboard/internal/services/access"
	"dashboard/internal/services/audit"
)

// MinPassword is the minimum accepted password length.
const MinPassword = 12

// ErrEmailTaken means the email is already registered.
var ErrEmailTaken = errors.New("accounts: email already registered")

// ErrPasswordTooShort means the password is below MinPassword.
var ErrPasswordTooShort = errors.New("accounts: password too short")

// ErrWrongPassword means the current password did not match.
var ErrWrongPassword = errors.New("accounts: wrong current password")

// CheckPasswordRules validates a new password before hashing it.
func CheckPasswordRules(password string) error {
	if len(password) < MinPassword {
		return ErrPasswordTooShort
	}
	return nil
}

// Profile is the user-facing account summary shown in settings.
type Profile struct {
	ID           int64
	Email        string
	Name         string
	Role         enums.InstanceRole
	Locale       enums.Locale
	ColorMode    enums.ColorMode
	ThemeID      *int64
	StartBoardID *int64
	SearchEngine string
	TOTPEnabled  bool
	HasPassword  bool
	OIDCLinked   bool
	Prefs        map[string]any
}

// Create inserts a user plus their personal space. The caller owns the
// transaction (q may be a *sql.Tx), so this composes into a larger unit of
// work such as first-admin setup.
func Create(q db.Queryer, email, name string, password *string, role enums.InstanceRole, locale enums.Locale, sub string) (*model.User, error) {
	email = strings.TrimSpace(email)
	existing, err := users.ByEmail(q, email)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, ErrEmailTaken
	}

	if password != nil {
		if err := CheckPasswordRules(*password); err != nil {
			return nil, err
		}
	}

	displayName := strings.TrimSpace(name)
	if displayName == "" {
		displayName = email
	}
	var hash string
	if password != nil {
		h, err := crypto.HashPassword(*password)
		if err != nil {
			return nil, err
		}
		hash = h
	}

	user := &model.User{
		Email: email, Name: displayName, PasswordHash: hash, Role: role, IsActive: true,
		Locale: locale, ColorMode: enums.ColorAuto, OIDCSub: sub, CreatedAt: time.Now().UTC(),
	}
	if err := users.Add(q, user); err != nil {
		return nil, err
	}

	space := &model.Space{Kind: enums.SpacePersonal, Name: user.Name, OwnerUserID: &user.ID, Version: 1}
	if err := content.AddSpace(q, space); err != nil {
		return nil, err
	}
	return user, nil
}

// TeamAssignment is one entry of a seed/invite's team list.
type TeamAssignment struct {
	Team string
	Role enums.TeamRole
}

// JoinTeams adds userID to each named team, creating missing teams.
func JoinTeams(q db.Queryer, userID int64, assignments []TeamAssignment) error {
	for _, a := range assignments {
		team, err := users.TeamByName(q, a.Team)
		if err != nil {
			return err
		}
		if team == nil {
			team, err = users.AddTeam(q, a.Team)
			if err != nil {
				return err
			}
		}
		role := a.Role
		if role == "" {
			role = enums.TeamViewer
		}
		if err := users.SetMember(q, userID, team.ID, role); err != nil {
			return err
		}
	}
	return nil
}

// GetProfile returns one user's account profile.
func GetProfile(d *sql.DB, who *access.Principal) (*Profile, error) {
	var p *Profile
	err := db.WithTx(d, func(tx *sql.Tx) error {
		u, err := users.Get(tx, who.UserID)
		if err != nil {
			return err
		}
		if u == nil {
			return sql.ErrNoRows
		}
		p = &Profile{
			ID: u.ID, Email: u.Email, Name: u.Name, Role: u.Role, Locale: u.Locale,
			ColorMode: u.ColorMode, ThemeID: u.ThemeID, StartBoardID: u.StartBoardID,
			SearchEngine: u.SearchEngine, TOTPEnabled: u.TOTPEnabled,
			HasPassword: u.PasswordHash != "", OIDCLinked: u.OIDCSub != "", Prefs: orEmpty(u.Prefs),
		}
		return nil
	})
	return p, err
}

// ProfileChanges lists the mutable profile fields UpdateProfile may set.
type ProfileChanges struct {
	Name         *string
	Locale       *enums.Locale
	ColorMode    *enums.ColorMode
	ThemeID      **int64
	StartBoardID **int64
	SearchEngine *string
}

// UpdateProfile applies the given (non-nil) changes to a user's profile.
func UpdateProfile(d *sql.DB, who *access.Principal, changes ProfileChanges) error {
	return db.WithTx(d, func(tx *sql.Tx) error {
		u, err := users.Get(tx, who.UserID)
		if err != nil {
			return err
		}
		if u == nil {
			return sql.ErrNoRows
		}
		if changes.Name != nil {
			u.Name = *changes.Name
		}
		if changes.Locale != nil {
			u.Locale = *changes.Locale
		}
		if changes.ColorMode != nil {
			u.ColorMode = *changes.ColorMode
		}
		if changes.ThemeID != nil {
			u.ThemeID = *changes.ThemeID
		}
		if changes.StartBoardID != nil {
			u.StartBoardID = *changes.StartBoardID
		}
		if changes.SearchEngine != nil {
			u.SearchEngine = *changes.SearchEngine
		}
		return users.Update(tx, u)
	})
}

// SetPref sets one key in a user's free-form preferences map.
func SetPref(d *sql.DB, who *access.Principal, key string, value any) error {
	return db.WithTx(d, func(tx *sql.Tx) error {
		u, err := users.Get(tx, who.UserID)
		if err != nil {
			return err
		}
		if u == nil {
			return sql.ErrNoRows
		}
		u.Prefs = orEmpty(u.Prefs)
		u.Prefs[key] = value
		return users.Update(tx, u)
	})
}

// ChangePassword verifies the current password (when one is set) and
// stores the new one.
func ChangePassword(d *sql.DB, who *access.Principal, current, newPassword, ip string) error {
	if err := CheckPasswordRules(newPassword); err != nil {
		return err
	}
	return db.WithTx(d, func(tx *sql.Tx) error {
		u, err := users.Get(tx, who.UserID)
		if err != nil {
			return err
		}
		if u == nil {
			return sql.ErrNoRows
		}
		if u.PasswordHash != "" && !crypto.CheckPassword(u.PasswordHash, current) {
			return ErrWrongPassword
		}
		hash, err := crypto.HashPassword(newPassword)
		if err != nil {
			return err
		}
		u.PasswordHash = hash
		if err := users.Update(tx, u); err != nil {
			return err
		}
		return audit.Log(tx, &who.UserID, "password.changed", "", ip, nil)
	})
}

func orEmpty(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	return m
}
