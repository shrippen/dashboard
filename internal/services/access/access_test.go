package access_test

import (
	"path/filepath"
	"testing"
	"time"

	"andon/internal/db"
	"andon/internal/db/dbtest"
	"andon/internal/enums"
	"andon/internal/model"
	"andon/internal/repos/content"
	"andon/internal/repos/misc"
	"andon/internal/repos/users"
	"andon/internal/services/access"
)

func openTestDB(t *testing.T) db.Queryer {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"), dbtest.Key)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func addUser(t *testing.T, q db.Queryer, email string, role enums.InstanceRole) *model.User {
	t.Helper()
	u := &model.User{Email: email, Name: email, Role: role, IsActive: true,
		Locale: enums.LocaleDE, ColorMode: enums.ColorAuto, CreatedAt: time.Now().UTC()}
	if err := users.Add(q, u); err != nil {
		t.Fatalf("add user %s: %v", email, err)
	}
	personal := &model.Space{Kind: enums.SpacePersonal, Name: u.Name, OwnerUserID: &u.ID, Version: 1}
	if err := content.AddSpace(q, personal); err != nil {
		t.Fatalf("add personal space for %s: %v", email, err)
	}
	return u
}

// TestPersonalSpacesAreIsolated:
// another user has no right at all on someone else's personal space.
func TestPersonalSpacesAreIsolated(t *testing.T) {
	q := openTestDB(t)
	a := addUser(t, q, "a@x.de", enums.RoleUser)
	addUser(t, q, "b@x.de", enums.RoleUser)

	who, err := access.Load(q, a.ID)
	if err != nil || who == nil {
		t.Fatalf("load a: %v", err)
	}
	spaceA := access.Personal(who)
	if spaceA == nil {
		t.Fatal("expected a to have a personal space")
	}

	stranger := addUser(t, q, "c@x.de", enums.RoleUser)
	strangerWho, _ := access.Load(q, stranger.ID)
	right := access.SpaceRight(strangerWho, spaceA)
	if right != enums.RightNone {
		t.Fatalf("expected unrelated user to have NONE on a's personal space, got %v", right)
	}
}

// TestAdminCannotReadPersonalContent: an instance admin gets NONE on
// another user's personal space, never MANAGE.
func TestAdminCannotReadPersonalContent(t *testing.T) {
	q := openTestDB(t)
	user := addUser(t, q, "u@x.de", enums.RoleUser)
	admin := addUser(t, q, "admin@x.de", enums.RoleAdmin)

	userWho, _ := access.Load(q, user.ID)
	space := access.Personal(userWho)

	adminWho, err := access.Load(q, admin.ID)
	if err != nil {
		t.Fatalf("load admin: %v", err)
	}
	if got := access.SpaceRight(adminWho, space); got != enums.RightNone {
		t.Fatalf("expected admin to have NONE on user's personal space, got %v", got)
	}
}

func addTeamSpace(t *testing.T, q db.Queryer, name string) (*model.Team, *model.Space) {
	t.Helper()
	team, err := users.AddTeam(q, name)
	if err != nil {
		t.Fatalf("add team: %v", err)
	}
	sp := &model.Space{Kind: enums.SpaceTeam, Name: name, TeamID: &team.ID, Version: 1}
	if err := content.AddSpace(q, sp); err != nil {
		t.Fatalf("add team space: %v", err)
	}
	return team, sp
}

// TestTeamRoles: viewers cannot edit or
// create, editors and owners can edit.
func TestTeamRoles(t *testing.T) {
	q := openTestDB(t)
	team, sp := addTeamSpace(t, q, "IT")

	owner := addUser(t, q, "o@x.de", enums.RoleUser)
	editor := addUser(t, q, "e@x.de", enums.RoleUser)
	viewer := addUser(t, q, "v@x.de", enums.RoleUser)
	users.SetMember(q, owner.ID, team.ID, enums.TeamOwner)
	users.SetMember(q, editor.ID, team.ID, enums.TeamEditor)
	users.SetMember(q, viewer.ID, team.ID, enums.TeamViewer)

	ownerWho, _ := access.Load(q, owner.ID)
	editorWho, _ := access.Load(q, editor.ID)
	viewerWho, _ := access.Load(q, viewer.ID)
	ref := &access.SpaceRef{ID: sp.ID, Kind: sp.Kind, TeamID: sp.TeamID, Name: sp.Name}

	if got := access.SpaceRight(ownerWho, ref); got != enums.RightManage {
		t.Fatalf("expected owner MANAGE, got %v", got)
	}
	if got := access.SpaceRight(editorWho, ref); got != enums.RightEdit {
		t.Fatalf("expected editor EDIT, got %v", got)
	}
	if got := access.SpaceRight(viewerWho, ref); got != enums.RightUse {
		t.Fatalf("expected viewer USE, got %v", got)
	}
	if err := access.Need(access.SpaceRight(viewerWho, ref), enums.RightEdit); err == nil {
		t.Fatal("expected viewer to be denied EDIT")
	}
}

// TestMinTeamRoleHidesWidget:
// a resource restricted to owners is invisible to a viewer even though the
// viewer has USE on the team space itself.
func TestMinTeamRoleHidesWidget(t *testing.T) {
	q := openTestDB(t)
	team, sp := addTeamSpace(t, q, "IT")
	owner := addUser(t, q, "o@x.de", enums.RoleUser)
	viewer := addUser(t, q, "v@x.de", enums.RoleUser)
	users.SetMember(q, owner.ID, team.ID, enums.TeamOwner)
	users.SetMember(q, viewer.ID, team.ID, enums.TeamViewer)

	ownerWho, _ := access.Load(q, owner.ID)
	viewerWho, _ := access.Load(q, viewer.ID)
	ref := &access.SpaceRef{ID: sp.ID, Kind: sp.Kind, TeamID: sp.TeamID, Name: sp.Name}
	ownerOnly := enums.TeamOwner

	if got := access.Right(ownerWho, enums.ResourceWidget, 1, ref, &ownerOnly); got < enums.RightView {
		t.Fatalf("expected owner to see owner-only widget, got %v", got)
	}
	if got := access.Right(viewerWho, enums.ResourceWidget, 1, ref, &ownerOnly); got != enums.RightNone {
		t.Fatalf("expected viewer to be denied owner-only widget, got %v", got)
	}
}

// TestShareGrantsAccessAcrossSpaces:
// an explicit VIEW share lets a user see a board in someone else's personal
// space, but not edit it.
func TestShareGrantsAccessAcrossSpaces(t *testing.T) {
	q := openTestDB(t)
	a := addUser(t, q, "a@x.de", enums.RoleUser)
	b := addUser(t, q, "b@x.de", enums.RoleUser)

	aWho, _ := access.Load(q, a.ID)
	boardID := int64(42) // a board id in a's personal space, for the share check alone
	if err := misc.AddShare(q, &model.Share{
		ResourceKind: enums.ResourceBoard, ResourceID: boardID,
		GranteeKind: enums.GranteeUser, GranteeID: b.ID, Right: enums.RightView,
	}); err != nil {
		t.Fatalf("add share: %v", err)
	}

	bWho, err := access.Load(q, b.ID)
	if err != nil {
		t.Fatalf("load b: %v", err)
	}
	spaceA := access.Personal(aWho)
	got := access.Right(bWho, enums.ResourceBoard, boardID, spaceA, nil)
	if got != enums.RightView {
		t.Fatalf("expected VIEW via share, got %v", got)
	}
	if err := access.Need(got, enums.RightEdit); err == nil {
		t.Fatal("expected share to not grant EDIT")
	}
}

// TestRightPicksHigherOfSpaceAndShare ensures Right() never lets an
// explicit share downgrade a role the user already has.
func TestRightPicksHigherOfSpaceAndShare(t *testing.T) {
	q := openTestDB(t)
	team, sp := addTeamSpace(t, q, "IT")
	owner := addUser(t, q, "o@x.de", enums.RoleUser)
	users.SetMember(q, owner.ID, team.ID, enums.TeamOwner)

	if err := misc.AddShare(q, &model.Share{
		ResourceKind: enums.ResourceBoard, ResourceID: 1,
		GranteeKind: enums.GranteeUser, GranteeID: owner.ID, Right: enums.RightView,
	}); err != nil {
		t.Fatalf("add share: %v", err)
	}

	who, _ := access.Load(q, owner.ID)
	ref := &access.SpaceRef{ID: sp.ID, Kind: sp.Kind, TeamID: sp.TeamID, Name: sp.Name}
	if got := access.Right(who, enums.ResourceBoard, 1, ref, nil); got != enums.RightManage {
		t.Fatalf("expected space MANAGE to win over share VIEW, got %v", got)
	}
}

func TestInstanceSpaceRights(t *testing.T) {
	q := openTestDB(t)
	instance := &model.Space{Kind: enums.SpaceInstance, Name: "Instanz", Version: 1}
	if err := content.AddSpace(q, instance); err != nil {
		t.Fatalf("add instance space: %v", err)
	}
	admin := addUser(t, q, "admin@x.de", enums.RoleAdmin)
	user := addUser(t, q, "user@x.de", enums.RoleUser)

	adminWho, _ := access.Load(q, admin.ID)
	userWho, _ := access.Load(q, user.ID)
	ref := &access.SpaceRef{ID: instance.ID, Kind: instance.Kind, Name: instance.Name}

	if got := access.SpaceRight(adminWho, ref); got != enums.RightManage {
		t.Fatalf("expected admin MANAGE on instance space, got %v", got)
	}
	if got := access.SpaceRight(userWho, ref); got != enums.RightUse {
		t.Fatalf("expected user USE on instance space, got %v", got)
	}
}

func TestLoadReturnsNilForInactiveOrMissingUser(t *testing.T) {
	q := openTestDB(t)
	who, err := access.Load(q, 999)
	if err != nil {
		t.Fatalf("load missing: %v", err)
	}
	if who != nil {
		t.Fatal("expected nil for missing user")
	}

	u := addUser(t, q, "inactive@x.de", enums.RoleUser)
	u.IsActive = false
	if err := users.Update(q, u); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	who, err = access.Load(q, u.ID)
	if err != nil {
		t.Fatalf("load inactive: %v", err)
	}
	if who != nil {
		t.Fatal("expected nil for inactive user")
	}
}
