package mailfwd_test

import (
	"context"
	"errors"
	"testing"

	"dashboard/internal/enums"
	"dashboard/internal/services/access"
	"dashboard/internal/services/mailfwd"
	"dashboard/internal/testkit"
)

// Forwarding needs a usable mailbox and a Paperless next to it; both are
// checked before any mail is fetched.
func TestForwardChecksBeforeFetching(t *testing.T) {
	d := testkit.DB(t)
	who, space := testkit.User(t, d, "a@b.c", enums.RoleUser)
	stranger, _ := testkit.User(t, d, "x@y.z", enums.RoleUser)
	box := testkit.Conn(t, d, who, space, enums.ServiceMail, "imaps://mail.example:993")
	ctx := context.Background()

	if _, err := mailfwd.Forward(ctx, d, who, box, 1, ""); !errors.Is(err, mailfwd.ErrNoPaperless) {
		t.Fatalf("without Paperless: %v", err)
	}
	if _, err := mailfwd.Forward(ctx, d, stranger, box, 1, ""); !errors.Is(err, access.ErrDenied) {
		t.Fatalf("foreign mailbox: %v", err)
	}
}
