// Package access decides who may do what.
//
//	right = max( space role right  (minus team restriction),
//	             explicit shares to the user or one of his teams )
//
//	space        owner/admin   editor   viewer/member   other
//	personal     MANAGE        –        –               NONE
//	team         MANAGE        EDIT     USE             NONE
//	instance     MANAGE(admin) –        USE             USE
//
// Instance admins manage accounts, never the content of personal spaces.
package access

import (
	"errors"

	"dashboard/internal/db"
	"dashboard/internal/enums"
	"dashboard/internal/repos/content"
	"dashboard/internal/repos/misc"
	"dashboard/internal/repos/users"
)

var teamRank = map[enums.TeamRole]int{
	enums.TeamViewer: 1,
	enums.TeamEditor: 2,
	enums.TeamOwner:  3,
}

var teamRight = map[enums.TeamRole]enums.Right{
	enums.TeamOwner:  enums.RightManage,
	enums.TeamEditor: enums.RightEdit,
	enums.TeamViewer: enums.RightUse,
}

// ErrDenied is returned by Need when the granted right is insufficient.
var ErrDenied = errors.New("access denied")

// SpaceRef is a lightweight reference to a space, enough for right checks.
type SpaceRef struct {
	ID          int64
	Kind        enums.SpaceKind
	OwnerUserID *int64
	TeamID      *int64
	Name        string
}

type grantKey struct {
	kind enums.ResourceKind
	id   int64
}

// Principal is the acting user with everything needed for access decisions.
type Principal struct {
	UserID      int64
	Name        string
	Email       string
	Role        enums.InstanceRole
	Locale      enums.Locale
	Teams       map[int64]enums.TeamRole
	Grants      map[grantKey]enums.Right
	Spaces      map[int64]SpaceRef
	SessionID   *int64
	TokenBoards []int64 // nil = unrestricted (no board-scoped API token)
}

// IsAdmin reports whether the principal is an instance admin.
func (p Principal) IsAdmin() bool { return p.Role == enums.RoleAdmin }

// Load builds a Principal for userID: teams, shares and reachable spaces.
// Returns nil if the user does not exist or is inactive.
func Load(q db.Queryer, userID int64) (*Principal, error) {
	u, err := users.Get(q, userID)
	if err != nil {
		return nil, err
	}
	if u == nil || !u.IsActive {
		return nil, nil
	}

	memberships, err := users.Memberships(q, userID)
	if err != nil {
		return nil, err
	}
	teams := make(map[int64]enums.TeamRole, len(memberships))
	teamIDs := make([]int64, 0, len(memberships))
	for _, m := range memberships {
		teams[m.TeamID] = m.Role
		teamIDs = append(teamIDs, m.TeamID)
	}

	who := &Principal{
		UserID: u.ID, Name: u.Name, Email: u.Email, Role: u.Role, Locale: u.Locale,
		Teams: teams, Grants: map[grantKey]enums.Right{}, Spaces: map[int64]SpaceRef{},
	}

	shares, err := misc.SharesTo(q, userID, teamIDs)
	if err != nil {
		return nil, err
	}
	for _, sh := range shares {
		key := grantKey{sh.ResourceKind, sh.ResourceID}
		if sh.Right > who.Grants[key] {
			who.Grants[key] = sh.Right
		}
	}

	spaces, err := reachableSpaces(q, who)
	if err != nil {
		return nil, err
	}
	for _, sp := range spaces {
		who.Spaces[sp.ID] = sp
	}

	return who, nil
}

func reachableSpaces(q db.Queryer, who *Principal) ([]SpaceRef, error) {
	var out []SpaceRef

	mine, err := content.PersonalSpace(q, who.UserID)
	if err != nil {
		return nil, err
	}
	if mine != nil {
		out = append(out, toRef(mine.ID, mine.Kind, mine.OwnerUserID, mine.TeamID, mine.Name))
	}

	for teamID := range who.Teams {
		sp, err := content.TeamSpace(q, teamID)
		if err != nil {
			return nil, err
		}
		if sp != nil {
			out = append(out, toRef(sp.ID, sp.Kind, sp.OwnerUserID, sp.TeamID, sp.Name))
		}
	}

	shared, err := content.InstanceSpace(q)
	if err != nil {
		return nil, err
	}
	if shared != nil {
		out = append(out, toRef(shared.ID, shared.Kind, shared.OwnerUserID, shared.TeamID, shared.Name))
	}

	return out, nil
}

func toRef(id int64, kind enums.SpaceKind, owner, team *int64, name string) SpaceRef {
	return SpaceRef{ID: id, Kind: kind, OwnerUserID: owner, TeamID: team, Name: name}
}

// SpaceRight returns the right a principal has from their space role alone
// (no explicit shares).
func SpaceRight(who *Principal, space *SpaceRef) enums.Right {
	if space == nil {
		return enums.RightNone
	}

	switch space.Kind {
	case enums.SpacePersonal:
		if space.OwnerUserID != nil && *space.OwnerUserID == who.UserID {
			return enums.RightManage
		}
		return enums.RightNone

	case enums.SpaceInstance:
		if who.IsAdmin() {
			return enums.RightManage
		}
		return enums.RightUse

	default: // team
		var teamID int64
		if space.TeamID != nil {
			teamID = *space.TeamID
		}
		if role, ok := who.Teams[teamID]; ok {
			return teamRight[role]
		}
		return enums.RightNone
	}
}

// Right returns the effective right a principal has on one resource: the
// space role right (possibly capped by minRole for team spaces), maxed with
// any explicit share.
func Right(who *Principal, kind enums.ResourceKind, resourceID int64, space *SpaceRef, minRole *enums.TeamRole) enums.Right {
	base := SpaceRight(who, space)

	if minRole != nil && space != nil && space.Kind == enums.SpaceTeam && base < enums.RightManage {
		var teamID int64
		if space.TeamID != nil {
			teamID = *space.TeamID
		}
		role, ok := who.Teams[teamID]
		if !ok || teamRank[role] < teamRank[*minRole] {
			base = enums.RightNone
		}
	}

	granted := who.Grants[grantKey{kind, resourceID}]
	if granted > base {
		return granted
	}
	return base
}

// SpaceOf resolves a space reference, including spaces reached only through
// a share (not one of the principal's own/team/instance spaces).
func SpaceOf(q db.Queryer, who *Principal, spaceID int64) (*SpaceRef, error) {
	if known, ok := who.Spaces[spaceID]; ok {
		return &known, nil
	}

	sp, err := content.Space(q, spaceID)
	if err != nil || sp == nil {
		return nil, err
	}
	ref := toRef(sp.ID, sp.Kind, sp.OwnerUserID, sp.TeamID, sp.Name)
	return &ref, nil
}

// Need raises ErrDenied if granted is below required.
func Need(granted, required enums.Right) error {
	if granted < required {
		return ErrDenied
	}
	return nil
}

// EditableSpaces returns the principal's spaces where they have at least
// EDIT right.
func EditableSpaces(who *Principal) []SpaceRef {
	var out []SpaceRef
	for _, sp := range who.Spaces {
		space := sp
		if SpaceRight(who, &space) >= enums.RightEdit {
			out = append(out, space)
		}
	}
	return out
}

// Personal returns the principal's own personal space, or nil.
func Personal(who *Principal) *SpaceRef {
	for _, sp := range who.Spaces {
		if sp.Kind == enums.SpacePersonal {
			space := sp
			return &space
		}
	}
	return nil
}
