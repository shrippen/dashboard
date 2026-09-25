// Package admin manages instance-wide user accounts: the admin-only user
// list, role changes, activation, and deletion.
package admin

import (
	"database/sql"
	"errors"

	"dashboard/internal/db"
	"dashboard/internal/enums"
	"dashboard/internal/model"
	"dashboard/internal/repos/users"
	"dashboard/internal/services/access"
	"dashboard/internal/services/audit"
)

// ErrDenied means the caller is not an admin.
var ErrDenied = errors.New("admin: access denied")

// ErrNotFound means the target user does not exist.
var ErrNotFound = errors.New("admin: user not found")

// ErrLastAdmin means the change would leave the instance without an admin.
var ErrLastAdmin = errors.New("admin: cannot remove the last admin")

// Users lists every account. Admin only.
func Users(d *sql.DB, who *access.Principal) ([]*model.User, error) {
	if !who.IsAdmin() {
		return nil, ErrDenied
	}
	return users.All(d)
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

// SetActive enables or disables a user's account. Refuses to disable the
// last active admin.
func SetActive(d *sql.DB, who *access.Principal, userID int64, active bool, ip string) error {
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
		return audit.Log(tx, &who.UserID, "user.active_set", u.Email, ip, map[string]any{"active": active})
	})
}

// Delete removes a user. Refuses to delete the last admin.
func Delete(d *sql.DB, who *access.Principal, userID int64, ip string) error {
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
