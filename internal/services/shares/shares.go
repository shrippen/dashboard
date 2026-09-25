// Package shares handles explicit grants: the "who has access?" dialog for
// boards, widgets, connections and themes.
package shares

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"

	"dashboard/internal/db"
	"dashboard/internal/enums"
	"dashboard/internal/model"
	"dashboard/internal/repos/content"
	"dashboard/internal/repos/misc"
	"dashboard/internal/repos/users"
	"dashboard/internal/services/access"
	"dashboard/internal/services/audit"
)

// ErrRight means an unshareable right was requested (only VIEW..MANAGE may
// be granted; NONE is meaningless as a share).
var ErrRight = errors.New("shares: right not shareable")

// ErrNotFound means the resource does not exist.
var ErrNotFound = errors.New("shares: not found")

// ErrLocationPersonal means a Dawarich connection may not be shared while
// location sharing is disabled instance-wide.
var ErrLocationPersonal = errors.New("shares: location data must stay personal")

const sharedLocationKey = "dawarich_shared"

func shareableRight(r enums.Right) bool {
	return r == enums.RightView || r == enums.RightUse || r == enums.RightEdit || r == enums.RightManage
}

// resource is the minimal shape shares needs from any shareable entity.
type resource struct {
	id          int64
	spaceID     int64
	name        string
	minTeamRole *enums.TeamRole
	service     string               // connections only
	credMode    enums.CredentialMode // connections only
}

func loadResource(q db.Queryer, kind enums.ResourceKind, resourceID int64) (*resource, error) {
	switch kind {
	case enums.ResourceBoard:
		b, err := content.Board(q, resourceID)
		if err != nil || b == nil {
			return nil, orNotFound(err)
		}
		return &resource{id: b.ID, spaceID: b.SpaceID, name: b.Name, minTeamRole: b.MinTeamRole}, nil
	case enums.ResourceWidget:
		w, err := content.Widget(q, resourceID)
		if err != nil || w == nil {
			return nil, orNotFound(err)
		}
		return &resource{id: w.ID, spaceID: w.SpaceID, name: w.Title, minTeamRole: w.MinTeamRole}, nil
	case enums.ResourceConnection:
		c, err := content.Connection(q, resourceID)
		if err != nil || c == nil {
			return nil, orNotFound(err)
		}
		return &resource{id: c.ID, spaceID: c.SpaceID, name: c.Name, service: c.Service, credMode: c.CredentialMode}, nil
	case enums.ResourceTheme:
		t, err := misc.Theme(q, resourceID)
		if err != nil || t == nil {
			return nil, orNotFound(err)
		}
		var spaceID int64
		if t.SpaceID != nil {
			spaceID = *t.SpaceID
		}
		return &resource{id: t.ID, spaceID: spaceID, name: t.Name}, nil
	default:
		return nil, ErrNotFound
	}
}

func orNotFound(err error) error {
	if err != nil {
		return err
	}
	return ErrNotFound
}

func needManage(q db.Queryer, who *access.Principal, kind enums.ResourceKind, item *resource) error {
	space, err := access.SpaceOf(q, who, item.spaceID)
	if err != nil {
		return err
	}
	granted := access.Right(who, kind, item.id, space, item.minTeamRole)
	return access.Need(granted, enums.RightManage)
}

// View is one existing share, resolved to its grantee's display name.
type View struct {
	ID          int64
	GranteeKind enums.GranteeKind
	GranteeID   int64
	GranteeName string
	Right       enums.Right
}

// Audience describes who already has access through the space itself.
type Audience struct {
	SpaceName string
	SpaceKind enums.SpaceKind
	Members   []MemberRole
}

// MemberRole is one space member and their role label ("owner", "editor", ...).
type MemberRole struct {
	Name string
	Role string
}

// ShareInfo is everything the sharing dialog needs.
type ShareInfo struct {
	Title          string
	Kind           enums.ResourceKind
	ResourceID     int64
	Shares         []View
	Audience       Audience
	WarnSharedData bool
	Users          []NamedID
	Teams          []NamedID
}

// NamedID is an id/name pair for the grantee picker.
type NamedID struct {
	ID   int64
	Name string
}

// Info loads the sharing dialog's data for one resource. Requires MANAGE.
func Info(d *sql.DB, who *access.Principal, kind enums.ResourceKind, resourceID int64) (*ShareInfo, error) {
	var out *ShareInfo
	err := db.WithTx(d, func(tx *sql.Tx) error {
		item, err := loadResource(tx, kind, resourceID)
		if err != nil {
			return err
		}
		if err := needManage(tx, who, kind, item); err != nil {
			return err
		}

		allUsers, err := users.All(tx)
		if err != nil {
			return err
		}
		names := map[int64]string{}
		namedUsers := make([]NamedID, 0, len(allUsers))
		for _, u := range allUsers {
			names[u.ID] = u.Name
			namedUsers = append(namedUsers, NamedID{ID: u.ID, Name: u.Name})
		}
		allTeams, err := users.Teams(tx)
		if err != nil {
			return err
		}
		teamNames := map[int64]string{}
		namedTeams := make([]NamedID, 0, len(allTeams))
		for _, t := range allTeams {
			teamNames[t.ID] = t.Name
			namedTeams = append(namedTeams, NamedID{ID: t.ID, Name: t.Name})
		}
		sort.Slice(namedUsers, func(i, j int) bool { return namedUsers[i].Name < namedUsers[j].Name })
		sort.Slice(namedTeams, func(i, j int) bool { return namedTeams[i].Name < namedTeams[j].Name })

		rawShares, err := misc.SharesFor(tx, kind, resourceID)
		if err != nil {
			return err
		}
		views := make([]View, 0, len(rawShares))
		for _, sh := range rawShares {
			name := "?"
			if sh.GranteeKind == enums.GranteeUser {
				if n, ok := names[sh.GranteeID]; ok {
					name = n
				}
			} else if n, ok := teamNames[sh.GranteeID]; ok {
				name = n
			}
			views = append(views, View{ID: sh.ID, GranteeKind: sh.GranteeKind, GranteeID: sh.GranteeID, GranteeName: name, Right: sh.Right})
		}

		space, err := content.Space(tx, item.spaceID)
		if err != nil {
			return err
		}
		var members []MemberRole
		if space != nil {
			switch space.Kind {
			case enums.SpaceTeam:
				var teamID int64
				if space.TeamID != nil {
					teamID = *space.TeamID
				}
				memberships, err := users.Members(tx, teamID)
				if err != nil {
					return err
				}
				for _, m := range memberships {
					u, err := users.Get(tx, m.UserID)
					if err != nil {
						return err
					}
					if u != nil {
						members = append(members, MemberRole{Name: u.Name, Role: string(m.Role)})
					}
				}
			case enums.SpacePersonal:
				var owner string
				if space.OwnerUserID != nil {
					owner = names[*space.OwnerUserID]
				}
				if owner == "" {
					owner = "?"
				}
				members = []MemberRole{{Name: owner, Role: "owner"}}
			}
		}

		warn := kind == enums.ResourceConnection && item.credMode == enums.CredentialShared
		spaceName, spaceKind := "", enums.SpaceKind("")
		if space != nil {
			spaceName, spaceKind = space.Name, space.Kind
		}
		out = &ShareInfo{
			Title: item.name, Kind: kind, ResourceID: resourceID, Shares: views,
			Audience:       Audience{SpaceName: spaceName, SpaceKind: spaceKind, Members: members},
			WarnSharedData: warn, Users: namedUsers, Teams: namedTeams,
		}
		return nil
	})
	return out, err
}

// Grant creates or updates a share.
func Grant(d *sql.DB, who *access.Principal, kind enums.ResourceKind, resourceID int64, granteeKind enums.GranteeKind, granteeID int64, right enums.Right) error {
	if !shareableRight(right) {
		return ErrRight
	}
	return db.WithTx(d, func(tx *sql.Tx) error {
		item, err := loadResource(tx, kind, resourceID)
		if err != nil {
			return err
		}
		if err := needManage(tx, who, kind, item); err != nil {
			return err
		}

		if kind == enums.ResourceConnection && item.service == string(enums.ServiceDawarich) &&
			item.credMode == enums.CredentialShared {
			setting, err := misc.Setting(tx, sharedLocationKey)
			if err != nil {
				return err
			}
			if allowed, _ := setting["allowed"].(bool); !allowed {
				return ErrLocationPersonal
			}
		}

		existing, err := misc.SharesFor(tx, kind, resourceID)
		if err != nil {
			return err
		}
		var found *model.Share
		for _, sh := range existing {
			if sh.GranteeKind == granteeKind && sh.GranteeID == granteeID {
				found = sh
				break
			}
		}
		if found != nil {
			found.Right = right
			if _, err := tx.Exec("UPDATE shares SET right = ? WHERE id = ?", right, found.ID); err != nil {
				return err
			}
		} else {
			sh := &model.Share{
				ResourceKind: kind, ResourceID: resourceID, GranteeKind: granteeKind, GranteeID: granteeID,
				Right: right, CreatedBy: &who.UserID,
			}
			if err := misc.AddShare(tx, sh); err != nil {
				return err
			}
		}
		return audit.Log(tx, &who.UserID, "share.granted", fmt.Sprintf("%s:%d", kind, resourceID), "",
			map[string]any{"grantee": fmt.Sprintf("%s:%d", granteeKind, granteeID), "right": right})
	})
}

// Revoke deletes one share.
func Revoke(d *sql.DB, who *access.Principal, shareID int64) error {
	return db.WithTx(d, func(tx *sql.Tx) error {
		sh, err := misc.ShareByID(tx, shareID)
		if err != nil || sh == nil {
			return err
		}
		item, err := loadResource(tx, sh.ResourceKind, sh.ResourceID)
		if err != nil {
			return err
		}
		if err := needManage(tx, who, sh.ResourceKind, item); err != nil {
			return err
		}
		if err := audit.Log(tx, &who.UserID, "share.revoked", fmt.Sprintf("%s:%d", sh.ResourceKind, sh.ResourceID), "", nil); err != nil {
			return err
		}
		return misc.RemoveShare(tx, sh.ID)
	})
}
