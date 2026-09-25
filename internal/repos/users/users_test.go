package users_test

import (
	"path/filepath"
	"testing"
	"time"

	"dashboard/internal/db"
	"dashboard/internal/db/dbtest"
	"dashboard/internal/enums"
	"dashboard/internal/model"
	"dashboard/internal/repos/users"
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

func newUser(email string) *model.User {
	return &model.User{
		Email:     email,
		Name:      "Alex",
		Role:      enums.RoleUser,
		IsActive:  true,
		Locale:    enums.LocaleDE,
		ColorMode: enums.ColorAuto,
		CreatedAt: time.Now().UTC(),
	}
}

func TestAddGetByEmailCaseInsensitive(t *testing.T) {
	q := openTestDB(t)

	u := newUser("Alex@Example.com")
	if err := users.Add(q, u); err != nil {
		t.Fatalf("add: %v", err)
	}
	if u.ID == 0 {
		t.Fatal("expected id to be set")
	}

	got, err := users.ByEmail(q, "alex@example.com")
	if err != nil {
		t.Fatalf("by email: %v", err)
	}
	if got == nil || got.ID != u.ID {
		t.Fatalf("expected to find user by lowercased email, got %+v", got)
	}
}

func TestGetMissingReturnsNil(t *testing.T) {
	q := openTestDB(t)
	got, err := users.Get(q, 999)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got != nil {
		t.Fatalf("expected nil for missing user, got %+v", got)
	}
}

func TestUpdateRoundTrip(t *testing.T) {
	q := openTestDB(t)
	u := newUser("a@b.c")
	u.Prefs = map[string]any{"lang": "de"}
	u.RecoveryCodes = []string{"one", "two"}
	if err := users.Add(q, u); err != nil {
		t.Fatalf("add: %v", err)
	}

	u.Name = "Renamed"
	u.IsActive = false
	if err := users.Update(q, u); err != nil {
		t.Fatalf("update: %v", err)
	}

	got, err := users.Get(q, u.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Name != "Renamed" || got.IsActive {
		t.Fatalf("update not persisted: %+v", got)
	}
	if len(got.RecoveryCodes) != 2 || got.RecoveryCodes[0] != "one" {
		t.Fatalf("recovery codes not round-tripped: %+v", got.RecoveryCodes)
	}
	if got.Prefs["lang"] != "de" {
		t.Fatalf("prefs not round-tripped: %+v", got.Prefs)
	}
}

func TestCountAdmins(t *testing.T) {
	q := openTestDB(t)
	admin := newUser("admin@x.y")
	admin.Role = enums.RoleAdmin
	if err := users.Add(q, admin); err != nil {
		t.Fatalf("add admin: %v", err)
	}
	plain := newUser("user@x.y")
	if err := users.Add(q, plain); err != nil {
		t.Fatalf("add user: %v", err)
	}

	n, err := users.CountAdmins(q)
	if err != nil {
		t.Fatalf("count admins: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 admin, got %d", n)
	}
}

func TestTeamMembershipLifecycle(t *testing.T) {
	q := openTestDB(t)
	u := newUser("member@x.y")
	if err := users.Add(q, u); err != nil {
		t.Fatalf("add user: %v", err)
	}
	team, err := users.AddTeam(q, "Ops")
	if err != nil {
		t.Fatalf("add team: %v", err)
	}

	// Reading a team back exercises the same TEXT->time.Time scan path
	// that Add/scanUser already covers for users; catch regressions there too.
	got, err := users.Team(q, team.ID)
	if err != nil || got == nil || got.Name != "Ops" {
		t.Fatalf("expected to read team back, got %+v err=%v", got, err)
	}
	if byName, err := users.TeamByName(q, "ops"); err != nil || byName == nil || byName.ID != team.ID {
		t.Fatalf("expected case-insensitive lookup, got %+v err=%v", byName, err)
	}
	if all, err := users.Teams(q); err != nil || len(all) != 1 {
		t.Fatalf("expected 1 team listed, got %+v err=%v", all, err)
	}

	if err := users.SetMember(q, u.ID, team.ID, enums.TeamEditor); err != nil {
		t.Fatalf("set member: %v", err)
	}
	m, err := users.MembershipOf(q, u.ID, team.ID)
	if err != nil || m == nil || m.Role != enums.TeamEditor {
		t.Fatalf("expected editor membership, got %+v err=%v", m, err)
	}

	// Setting again updates the role rather than duplicating the row.
	if err := users.SetMember(q, u.ID, team.ID, enums.TeamOwner); err != nil {
		t.Fatalf("re-set member: %v", err)
	}
	members, err := users.Members(q, team.ID)
	if err != nil {
		t.Fatalf("members: %v", err)
	}
	if len(members) != 1 || members[0].Role != enums.TeamOwner {
		t.Fatalf("expected single owner membership, got %+v", members)
	}

	if err := users.RemoveMember(q, u.ID, team.ID); err != nil {
		t.Fatalf("remove member: %v", err)
	}
	m, err = users.MembershipOf(q, u.ID, team.ID)
	if err != nil || m != nil {
		t.Fatalf("expected membership removed, got %+v", m)
	}
}
