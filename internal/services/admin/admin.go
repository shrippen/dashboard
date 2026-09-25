// Package admin manages instance-wide user accounts: the admin-only user
// list, role changes, activation, and deletion.
package admin

import (
	"database/sql"
	"errors"

	"dashboard/internal/db"
	"dashboard/internal/enums"
	"dashboard/internal/model"
	authrepo "dashboard/internal/repos/auth"
	"dashboard/internal/repos/misc"
	"dashboard/internal/repos/users"
	"dashboard/internal/services/access"
	"dashboard/internal/services/accounts"
	"dashboard/internal/services/audit"
)

// Errors carry catalog keys so the web layer shows them translated.
var (
	ErrDenied    = errors.New("error.denied")
	ErrNotFound  = errors.New("error.not_found")
	ErrLastAdmin = errors.New("admin.last_admin")
	ErrSelf      = errors.New("admin.self")
	ErrClosed    = errors.New("register.closed")
)

const registrationKey = "registration"

// Switch turns a flag on or off (instead of a bare bool parameter).
type Switch string

const (
	On  Switch = "on"
	Off Switch = "off"
)

// TeamLabel is one membership shown in the user list.
type TeamLabel struct {
	Name string
	Role enums.TeamRole
}

// UserRow is one account in the admin list. No personal content.
type UserRow struct {
	*model.User
	Teams []TeamLabel
}

// Users lists every account with its team memberships. Admin only.
func Users(d *sql.DB, who *access.Principal) ([]UserRow, error) {
	if !who.IsAdmin() {
		return nil, ErrDenied
	}
	all, err := users.All(d)
	if err != nil {
		return nil, err
	}
	teams, err := users.Teams(d)
	if err != nil {
		return nil, err
	}
	names := make(map[int64]string, len(teams))
	for _, t := range teams {
		names[t.ID] = t.Name
	}

	rows := make([]UserRow, 0, len(all))
	for _, u := range all {
		memberships, err := users.Memberships(d, u.ID)
		if err != nil {
			return nil, err
		}
		row := UserRow{User: u}
		for _, m := range memberships {
			row.Teams = append(row.Teams, TeamLabel{Name: names[m.TeamID], Role: m.Role})
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// SetRole promotes or demotes a user. Refuses to demote the last admin.
func SetRole(d *sql.DB, who *access.Principal, userID int64, role enums.InstanceRole, ip string) error {
	if !who.IsAdmin() {
		return ErrDenied
	}
	return db.WithTx(d, func(tx *sql.Tx) error {
		u, err := users.Get(tx, userID)
		if err != nil {
			return err
		}
		if u == nil {
			return ErrNotFound
		}
		if u.Role == enums.RoleAdmin && role != enums.RoleAdmin {
			if err := failIfLastAdmin(tx); err != nil {
				return err
			}
		}
		u.Role = role
		if err := users.Update(tx, u); err != nil {
			return err
		}
		return audit.Log(tx, &who.UserID, "user.role_set", u.Email, ip, map[string]any{"role": string(role)})
	})
}

// SetActive enables or disables a user's account and ends its sessions on
// disable. Refuses to disable oneself or the last admin.
func SetActive(d *sql.DB, who *access.Principal, userID int64, state Switch, ip string) error {
	active := state == On
	if userID == who.UserID && !active {
		return ErrSelf
	}
	if !who.IsAdmin() {
		return ErrDenied
	}
	return db.WithTx(d, func(tx *sql.Tx) error {
		u, err := users.Get(tx, userID)
		if err != nil {
			return err
		}
		if u == nil {
			return ErrNotFound
		}
		if u.Role == enums.RoleAdmin && !active {
			if err := failIfLastAdmin(tx); err != nil {
				return err
			}
		}
		u.IsActive = active
		if err := users.Update(tx, u); err != nil {
			return err
		}
		if !active {
			if err := authrepo.DropSessions(tx, u.ID, nil); err != nil {
				return err
			}
		}
		return audit.Log(tx, &who.UserID, "user.active_set", u.Email, ip, map[string]any{"active": active})
	})
}

// SetBreakglass marks an account as emergency access (local login even in
// OIDC-only mode).
func SetBreakglass(d *sql.DB, who *access.Principal, userID int64, state Switch, ip string) error {
	if !who.IsAdmin() {
		return ErrDenied
	}
	return db.WithTx(d, func(tx *sql.Tx) error {
		u, err := users.Get(tx, userID)
		if err != nil {
			return err
		}
		if u == nil {
			return ErrNotFound
		}
		u.IsBreakglass = state == On
		if err := users.Update(tx, u); err != nil {
			return err
		}
		return audit.Log(tx, &who.UserID, "user.breakglass", u.Email, ip, map[string]any{"on": u.IsBreakglass})
	})
}

// Delete removes a user and their personal space. Refuses to delete
// oneself or the last admin.
func Delete(d *sql.DB, who *access.Principal, userID int64, ip string) error {
	if !who.IsAdmin() {
		return ErrDenied
	}
	if userID == who.UserID {
		return ErrSelf
	}
	return db.WithTx(d, func(tx *sql.Tx) error {
		u, err := users.Get(tx, userID)
		if err != nil {
			return err
		}
		if u == nil {
			return ErrNotFound
		}
		if u.Role == enums.RoleAdmin {
			if err := failIfLastAdmin(tx); err != nil {
				return err
			}
		}
		if err := audit.Log(tx, &who.UserID, "user.deleted", u.Email, ip, nil); err != nil {
			return err
		}
		return users.Delete(tx, userID)
	})
}

// RegistrationOpen reports whether self-registration is switched on.
func RegistrationOpen(q db.Queryer) (bool, error) {
	setting, err := misc.Setting(q, registrationKey)
	if err != nil {
		return false, err
	}
	open, _ := setting["open"].(bool)
	return open, nil
}

// Register creates a plain user account when self-registration is open.
func Register(d *sql.DB, email, name, password string, locale enums.Locale) (string, error) {
	var created string
	err := db.WithTx(d, func(tx *sql.Tx) error {
		open, err := RegistrationOpen(tx)
		if err != nil {
			return err
		}
		if !open {
			return ErrClosed
		}
		user, err := accounts.Create(tx, email, name, &password, enums.RoleUser, locale, "")
		if err != nil {
			return err
		}
		created = user.Email
		return audit.Log(tx, &user.ID, "user.registered", user.Email, "", nil)
	})
	return created, err
}

func failIfLastAdmin(q db.Queryer) error {
	n, err := users.CountAdmins(q)
	if err != nil {
		return err
	}
	if n <= 1 {
		return ErrLastAdmin
	}
	return nil
}
