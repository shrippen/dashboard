package audit_test

import (
	"errors"
	"testing"

	"andon/internal/enums"
	"andon/internal/services/audit"
	"andon/internal/testkit"
)

// Only admins read the audit log.
func TestEntriesAdminOnly(t *testing.T) {
	d := testkit.DB(t)
	admin, _ := testkit.User(t, d, "admin@x.de", enums.RoleAdmin)
	user, _ := testkit.User(t, d, "user@x.de", enums.RoleUser)

	if err := audit.Log(d, &user.UserID, "password.changed", "user@x.de", "", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := audit.Entries(d, user); !errors.Is(err, audit.ErrDenied) {
		t.Fatalf("user reads log: %v", err)
	}
	entries, err := audit.Entries(d, admin)
	if err != nil || len(entries) != 1 || entries[0].Action != "password.changed" {
		t.Fatalf("entries: %+v %v", entries, err)
	}
}
