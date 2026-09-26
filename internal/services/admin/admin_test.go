package admin_test

import (
	"errors"
	"testing"

	"dashboard/internal/enums"
	"dashboard/internal/services/admin"
	"dashboard/internal/testkit"
)

// The instance never loses its last admin, and only admins manage users.
func TestLastAdminStays(t *testing.T) {
	d := testkit.DB(t)
	boss, _ := testkit.User(t, d, "admin@x.de", enums.RoleAdmin)
	user, _ := testkit.User(t, d, "user@x.de", enums.RoleUser)

	if _, err := admin.Users(d, user); !errors.Is(err, admin.ErrDenied) {
		t.Fatalf("user lists users: %v", err)
	}
	if err := admin.SetRole(d, boss, boss.UserID, enums.RoleUser, ""); !errors.Is(err, admin.ErrLastAdmin) {
		t.Fatalf("demote last admin: %v", err)
	}
	if err := admin.SetActive(d, boss, boss.UserID, admin.Off, ""); err == nil {
		t.Fatal("an admin must not disable themselves")
	}

	if err := admin.SetRole(d, boss, user.UserID, enums.RoleAdmin, ""); err != nil {
		t.Fatalf("promote: %v", err)
	}
	if err := admin.SetRole(d, boss, boss.UserID, enums.RoleUser, ""); err != nil {
		t.Fatalf("demote with a second admin: %v", err)
	}
}
