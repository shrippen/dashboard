package auth_test

import (
	"path/filepath"
	"testing"
	"time"

	"dashboard/internal/db"
	"dashboard/internal/enums"
	"dashboard/internal/model"
	"dashboard/internal/repos/auth"
	"dashboard/internal/repos/users"
)

func openTestDB(t *testing.T) db.Queryer {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func userID(t *testing.T, q db.Queryer) int64 {
	t.Helper()
	u := &model.User{Email: "a@b.c", Name: "A", Role: enums.RoleUser, IsActive: true,
		Locale: enums.LocaleDE, ColorMode: enums.ColorAuto, CreatedAt: time.Now().UTC()}
	if err := users.Add(q, u); err != nil {
		t.Fatalf("add user: %v", err)
	}
	return u.ID
}

func TestSessionRoundTripAndDrop(t *testing.T) {
	q := openTestDB(t)
	uid := userID(t, q)
	now := time.Now().UTC()

	s := &model.LoginSession{TokenHash: "hash1", UserID: uid, Method: enums.AuthPassword,
		CSRF: "csrf", CreatedAt: now, LastSeen: now, ExpiresAt: now.Add(time.Hour)}
	if err := auth.AddSession(q, s); err != nil {
		t.Fatalf("add session: %v", err)
	}

	got, err := auth.SessionByHash(q, "hash1")
	if err != nil || got == nil || got.UserID != uid {
		t.Fatalf("expected session, got %+v err=%v", got, err)
	}

	if err := auth.TouchSession(q, s.ID, now.Add(time.Minute), false); err != nil {
		t.Fatalf("touch: %v", err)
	}
	got, _ = auth.SessionByHash(q, "hash1")
	if got.Pending2FA {
		t.Fatal("expected pending_2fa cleared")
	}

	// A second session for the same user, kept when dropping the rest.
	s2 := &model.LoginSession{TokenHash: "hash2", UserID: uid, Method: enums.AuthPassword,
		CSRF: "csrf2", CreatedAt: now, LastSeen: now, ExpiresAt: now.Add(time.Hour)}
	auth.AddSession(q, s2)

	if err := auth.DropSessions(q, uid, &s2.ID); err != nil {
		t.Fatalf("drop sessions: %v", err)
	}
	if got, _ := auth.SessionByHash(q, "hash1"); got != nil {
		t.Fatal("expected hash1 session dropped")
	}
	if got, _ := auth.SessionByHash(q, "hash2"); got == nil {
		t.Fatal("expected hash2 session kept")
	}
}

func TestPurgeExpiredRemovesOnlyPast(t *testing.T) {
	q := openTestDB(t)
	uid := userID(t, q)
	now := time.Now().UTC()

	expired := &model.LoginSession{TokenHash: "old", UserID: uid, Method: enums.AuthPassword,
		CSRF: "c", CreatedAt: now, LastSeen: now, ExpiresAt: now.Add(-time.Hour)}
	fresh := &model.LoginSession{TokenHash: "new", UserID: uid, Method: enums.AuthPassword,
		CSRF: "c", CreatedAt: now, LastSeen: now, ExpiresAt: now.Add(time.Hour)}
	auth.AddSession(q, expired)
	auth.AddSession(q, fresh)

	if err := auth.PurgeExpired(q, now); err != nil {
		t.Fatalf("purge: %v", err)
	}
	if got, _ := auth.SessionByHash(q, "old"); got != nil {
		t.Fatal("expected expired session purged")
	}
	if got, _ := auth.SessionByHash(q, "new"); got == nil {
		t.Fatal("expected fresh session kept")
	}
}

func TestApiTokenRoundTrip(t *testing.T) {
	q := openTestDB(t)
	uid := userID(t, q)
	tok := &model.ApiToken{UserID: uid, Name: "CLI", TokenHash: "th", Prefix: "abcd",
		Scope: enums.TokenRead, BoardIDs: []int64{1, 2}, CreatedAt: time.Now().UTC()}
	if err := auth.AddToken(q, tok); err != nil {
		t.Fatalf("add token: %v", err)
	}
	got, err := auth.TokenByHash(q, "th")
	if err != nil || got == nil || len(got.BoardIDs) != 2 {
		t.Fatalf("expected token with board ids, got %+v err=%v", got, err)
	}

	if err := auth.RemoveToken(q, got.ID); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if got, _ := auth.TokenByHash(q, "th"); got != nil {
		t.Fatal("expected token removed")
	}
}

func TestInviteLifecycle(t *testing.T) {
	q := openTestDB(t)
	inv := &model.Invite{Email: "new@x.y", TokenHash: "ih", Role: enums.RoleUser,
		Teams: []int64{1}, ExpiresAt: time.Now().UTC().Add(24 * time.Hour)}
	if err := auth.AddInvite(q, inv); err != nil {
		t.Fatalf("add invite: %v", err)
	}

	open, err := auth.OpenInvites(q)
	if err != nil || len(open) != 1 {
		t.Fatalf("expected 1 open invite, got %d err=%v", len(open), err)
	}

	if err := auth.MarkInviteUsed(q, inv.ID, time.Now().UTC()); err != nil {
		t.Fatalf("mark used: %v", err)
	}
	open, err = auth.OpenInvites(q)
	if err != nil || len(open) != 0 {
		t.Fatalf("expected 0 open invites after use, got %d err=%v", len(open), err)
	}
}

func TestResetTokenLifecycle(t *testing.T) {
	q := openTestDB(t)
	uid := userID(t, q)
	r := &model.ResetToken{UserID: uid, TokenHash: "rh", ExpiresAt: time.Now().UTC().Add(time.Hour)}
	if err := auth.AddReset(q, r); err != nil {
		t.Fatalf("add reset: %v", err)
	}
	got, err := auth.ResetByHash(q, "rh")
	if err != nil || got == nil || got.UsedAt != nil {
		t.Fatalf("expected unused reset token, got %+v err=%v", got, err)
	}

	if err := auth.MarkResetUsed(q, r.ID, time.Now().UTC()); err != nil {
		t.Fatalf("mark used: %v", err)
	}
	got, err = auth.ResetByHash(q, "rh")
	if err != nil || got == nil || got.UsedAt == nil {
		t.Fatalf("expected used_at set, got %+v err=%v", got, err)
	}
}
