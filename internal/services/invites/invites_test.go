package invites_test

import (
	"errors"
	"path"
	"testing"

	"andon/internal/enums"
	"andon/internal/services/invites"
	"andon/internal/testkit"
)

// An invite link works once; only admins create invites.
func TestInviteAcceptOnce(t *testing.T) {
	d := testkit.DB(t)
	admin, _ := testkit.User(t, d, "admin@x.de", enums.RoleAdmin)
	user, _ := testkit.User(t, d, "user@x.de", enums.RoleUser)

	if _, err := invites.Create(d, user, "new@x.de", enums.RoleUser, nil, enums.LocaleDE); !errors.Is(err, invites.ErrDenied) {
		t.Fatalf("user invite: %v", err)
	}
	if _, err := invites.Create(d, admin, "user@x.de", enums.RoleUser, nil, enums.LocaleDE); !errors.Is(err, invites.ErrEmailTaken) {
		t.Fatalf("taken email: %v", err)
	}
	link, err := invites.Create(d, admin, "new@x.de", enums.RoleUser, nil, enums.LocaleDE)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	token := path.Base(link)

	if email, err := invites.Accept(d, token, "New", "a long enough passphrase", enums.LocaleDE); err != nil || email != "new@x.de" {
		t.Fatalf("accept: %q %v", email, err)
	}
	if _, err := invites.Accept(d, token, "Again", "a long enough passphrase", enums.LocaleDE); !errors.Is(err, invites.ErrInviteInvalid) {
		t.Fatalf("second accept: %v", err)
	}
}

// A reset link sets a password once.
func TestResetOnce(t *testing.T) {
	d := testkit.DB(t)
	admin, _ := testkit.User(t, d, "admin@x.de", enums.RoleAdmin)
	user, _ := testkit.User(t, d, "user@x.de", enums.RoleUser)

	link, err := invites.AdminResetLink(d, admin, user.UserID)
	if err != nil {
		t.Fatalf("link: %v", err)
	}
	token := path.Base(link)
	if ok, _ := invites.ResetValid(d, token); !ok {
		t.Fatal("fresh link must be valid")
	}
	if err := invites.Reset(d, token, "a long enough passphrase", ""); err != nil {
		t.Fatalf("reset: %v", err)
	}
	if err := invites.Reset(d, token, "another long passphrase", ""); !errors.Is(err, invites.ErrResetInvalid) {
		t.Fatalf("second reset: %v", err)
	}
}
