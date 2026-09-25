// Package teams manages teams and memberships. Admins create teams;
// owners manage members.
package teams

import (
	"database/sql"
	"errors"
	"strconv"
	"strings"

	"dashboard/internal/db"
	"dashboard/internal/enums"
	"dashboard/internal/model"
	"dashboard/internal/repos/content"
	"dashboard/internal/repos/users"
	"dashboard/internal/services/access"
	"dashboard/internal/services/audit"
)

// ErrNameMissing/ErrNameTaken/ErrNotFound are TeamError equivalents.
var (
	ErrNameMissing = errors.New("teams: name missing")
	ErrNameTaken   = errors.New("teams: name already taken")
	ErrNotFound    = errors.New("teams: not found")
	ErrDenied      = errors.New("teams: access denied")
)

// CreateIn inserts a team plus its space. The caller owns the transaction.
func CreateIn(q db.Queryer, name string) (*model.Team, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, ErrNameMissing
	}
	existing, err := users.TeamByName(q, name)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return nil, ErrNameTaken
	}

	team, err := users.AddTeam(q, name)
	if err != nil {
		return nil, err
	}
	space := &model.Space{Kind: enums.SpaceTeam, Name: name, TeamID: &team.ID, Version: 1}
	if err := content.AddSpace(q, space); err != nil {
		return nil, err
	}
	return team, nil
}

// Create is the admin-facing entry point: create a team plus its space.
func Create(d *sql.DB, who *access.Principal, name, ip string) (int64, error) {
	if !who.IsAdmin() {
		return 0, ErrDenied
	}
	var id int64
	err := db.WithTx(d, func(tx *sql.Tx) error {
		team, err := CreateIn(tx, name)
		if err != nil {
			return err
		}
		id = team.ID
		return audit.Log(tx, &who.UserID, "team.created", team.Name, ip, nil)
	})
	return id, err
}

// Member is one team member for the overview.
type Member struct {
	UserID int64
	Name   string
	Email  string
	Role   enums.TeamRole
}

// View is one team's overview: name, the caller's own role (if any), and members.
type View struct {
	ID      int64
	Name    string
	SpaceID int64
	MyRole  *enums.TeamRole
	Members []Member
}

// Overview lists every team the principal is a member of (or, for admins,
// every team).
func Overview(d *sql.DB, who *access.Principal) ([]View, error) {
	var out []View
	err := db.WithTx(d, func(tx *sql.Tx) error {
		all, err := users.Teams(tx)
		if err != nil {
			return err
		}
		for _, team := range all {
			role, mine := who.Teams[team.ID]
			if !mine && !who.IsAdmin() {
				continue
			}

			space, err := content.TeamSpace(tx, team.ID)
			if err != nil {
				return err
			}
			memberships, err := users.Members(tx, team.ID)
			if err != nil {
				return err
			}
			members := make([]Member, 0, len(memberships))
			for _, m := range memberships {
				u, err := users.Get(tx, m.UserID)
				if err != nil {
					return err
				}
				if u == nil {
					continue
				}
				members = append(members, Member{UserID: u.ID, Name: u.Name, Email: u.Email, Role: m.Role})
			}

			view := View{ID: team.ID, Name: team.Name, Members: members}
			if space != nil {
				view.SpaceID = space.ID
			}
			if mine {
				r := role
				view.MyRole = &r
			}
			out = append(out, view)
		}
		return nil
	})
	return out, err
}

func mayManage(who *access.Principal, teamID int64) error {
	if who.IsAdmin() || who.Teams[teamID] == enums.TeamOwner {
		return nil
	}
	return ErrDenied
}

// SetMember sets a member's role, creating the membership if needed.
func SetMember(d *sql.DB, who *access.Principal, teamID, userID int64, role enums.TeamRole, ip string) error {
	if err := mayManage(who, teamID); err != nil {
		return err
	}
	return db.WithTx(d, func(tx *sql.Tx) error {
		team, err := users.Team(tx, teamID)
		if err != nil {
			return err
		}
		user, err := users.Get(tx, userID)
		if err != nil {
			return err
		}
		if team == nil || user == nil {
			return ErrNotFound
		}
		if err := users.SetMember(tx, userID, teamID, role); err != nil {
			return err
		}
		return audit.Log(tx, &who.UserID, "team.member_set", strconv.FormatInt(teamID, 10), ip,
			map[string]any{"member": userID, "role": string(role)})
	})
}

// RemoveMember removes a member from a team.
func RemoveMember(d *sql.DB, who *access.Principal, teamID, userID int64, ip string) error {
	if err := mayManage(who, teamID); err != nil {
		return err
	}
	return db.WithTx(d, func(tx *sql.Tx) error {
		if err := users.RemoveMember(tx, userID, teamID); err != nil {
			return err
		}
		return audit.Log(tx, &who.UserID, "team.member_removed", strconv.FormatInt(teamID, 10), ip,
			map[string]any{"member": userID})
	})
}

// Rename renames a team and its space.
func Rename(d *sql.DB, who *access.Principal, teamID int64, name string) error {
	if err := mayManage(who, teamID); err != nil {
		return err
	}
	return db.WithTx(d, func(tx *sql.Tx) error {
		team, err := users.Team(tx, teamID)
		if err != nil {
			return err
		}
		if team == nil {
			return ErrNotFound
		}
		newName := strings.TrimSpace(name)
		if newName == "" {
			newName = team.Name
		}
		if _, err := tx.Exec("UPDATE teams SET name = ? WHERE id = ?", newName, teamID); err != nil {
			return err
		}
		space, err := content.TeamSpace(tx, teamID)
		if err != nil {
			return err
		}
		if space != nil {
			if err := content.RenameSpace(tx, space.ID, newName); err != nil {
				return err
			}
		}
		return nil
	})
}

// Delete removes a team, its space and its memberships. Admin only.
func Delete(d *sql.DB, who *access.Principal, teamID int64, ip string) error {
	if !who.IsAdmin() {
		return ErrDenied
	}
	return db.WithTx(d, func(tx *sql.Tx) error {
		team, err := users.Team(tx, teamID)
		if err != nil || team == nil {
			return err
		}
		space, err := content.TeamSpace(tx, teamID)
		if err != nil {
			return err
		}
		if space != nil {
			if err := content.RemoveSpace(tx, space.ID); err != nil {
				return err
			}
		}
		if err := audit.Log(tx, &who.UserID, "team.deleted", team.Name, ip, nil); err != nil {
			return err
		}
		return users.DeleteTeam(tx, teamID)
	})
}
