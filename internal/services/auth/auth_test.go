package auth_test

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"dashboard/internal/crypto"
	"dashboard/internal/db"
	"dashboard/internal/db/dbtest"
	"dashboard/internal/enums"
	"dashboard/internal/repos/users"
	"dashboard/internal/services/access"
	"dashboard/internal/services/accounts"
	"dashboard/internal/services/auth"
	"dashboard/internal/settings"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	crypto.Init("test-master-key")
	auth.ResetThrottle()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"), dbtest.Key)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func testCfg() settings.Settings {
	return settings.Settings{
		SessionAbsoluteHours: 720, OIDCSessionHours: 12, SessionIdleMinutes: 60 * 24 * 7,
	}
}

func TestSetupFlow(t *testing.T) {
	q := openTestDB(t)

	needed, err := auth.SetupNeeded(q)
	if err != nil || !needed {
		t.Fatalf("expected setup needed, got %v err=%v", needed, err)
	}

	code, err := auth.EnsureSetupCode(q)
	if err != nil || code == "" {
		t.Fatalf("expected a setup code, got %q err=%v", code, err)
	}

	if err := auth.CreateAdmin(q, "wrong-code", "a@b.c", "Admin", "s3cret-password", enums.LocaleDE); err == nil {
		t.Fatal("expected wrong code to be rejected")
	}
	if err := auth.CreateAdmin(q, code, "a@b.c", "Admin", "s3cret-password", enums.LocaleDE); err != nil {
		t.Fatalf("create admin: %v", err)
	}

	needed, err = auth.SetupNeeded(q)
	if err != nil || needed {
		t.Fatalf("expected setup done, got %v err=%v", needed, err)
	}
	if err := auth.CreateAdmin(q, code, "b@b.c", "Admin2", "s3cret-password", enums.LocaleDE); !errors.Is(err, auth.ErrSetupClosed) {
		t.Fatalf("expected setup closed on second attempt, got %v", err)
	}
}

func addActiveUser(t *testing.T, q db.Queryer, email, password string) int64 {
	t.Helper()
	pw := password
	u, err := accounts.Create(q, email, "Name", &pw, enums.RoleUser, enums.LocaleDE, "")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	return u.ID
}

func TestLoginSuccessAndFailure(t *testing.T) {
	q := openTestDB(t)
	addActiveUser(t, q, "a@b.c", "correct-password")

	res, err := auth.Login(q, testCfg(), "a@b.c", "correct-password", "1.2.3.4", "agent")
	if err != nil || res.Token == "" || res.Step != auth.StepDone {
		t.Fatalf("expected successful login, got %+v err=%v", res, err)
	}

	if _, err := auth.Login(q, testCfg(), "a@b.c", "wrong", "1.2.3.4", "agent"); !errors.Is(err, auth.ErrLoginFailed) {
		t.Fatalf("expected login failed, got %v", err)
	}
	if _, err := auth.Login(q, testCfg(), "nobody@x.y", "whatever", "1.2.3.4", "agent"); !errors.Is(err, auth.ErrLoginFailed) {
		t.Fatalf("expected same error for unknown email (no user enumeration), got %v", err)
	}
}

func TestLoginThrottlesRepeatedFailures(t *testing.T) {
	q := openTestDB(t)
	addActiveUser(t, q, "a@b.c", "correct-password")

	var lastErr error
	for i := 0; i < 6; i++ {
		_, lastErr = auth.Login(q, testCfg(), "a@b.c", "wrong", "9.9.9.9", "agent")
	}
	if !errors.Is(lastErr, auth.ErrThrottled) {
		t.Fatalf("expected throttled after repeated failures, got %v", lastErr)
	}
}

func TestSessionResolveAndLogout(t *testing.T) {
	q := openTestDB(t)
	addActiveUser(t, q, "a@b.c", "correct-password")
	res, err := auth.Login(q, testCfg(), "a@b.c", "correct-password", "1.2.3.4", "agent")
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	info, err := auth.Resolve(q, testCfg(), res.Token)
	if err != nil || info == nil || info.Principal == nil {
		t.Fatalf("expected resolved session, got %+v err=%v", info, err)
	}
	if info.Pending2FA {
		t.Fatal("expected no pending 2FA for a TOTP-less account")
	}

	if _, err := auth.Logout(q, res.Token); err != nil {
		t.Fatalf("logout: %v", err)
	}
	info, err = auth.Resolve(q, testCfg(), res.Token)
	if err != nil || info != nil {
		t.Fatalf("expected session gone after logout, got %+v err=%v", info, err)
	}
}

func TestSessionResolveExpired(t *testing.T) {
	q := openTestDB(t)
	addActiveUser(t, q, "a@b.c", "correct-password")
	cfg := testCfg()
	cfg.SessionAbsoluteHours = 0 // expires immediately
	res, err := auth.Login(q, cfg, "a@b.c", "correct-password", "1.2.3.4", "agent")
	if err != nil {
		t.Fatalf("login: %v", err)
	}

	info, err := auth.Resolve(q, cfg, res.Token)
	if err != nil || info != nil {
		t.Fatalf("expected expired session to resolve to nil, got %+v err=%v", info, err)
	}
}

func TestTOTPLifecycle(t *testing.T) {
	q := openTestDB(t)
	uid := addActiveUser(t, q, "a@b.c", "correct-password")
	u, _ := users.Get(q, uid)
	who := &access.Principal{UserID: u.ID}

	secret, uri, err := auth.TOTPBegin(q, who)
	if err != nil || secret == "" || uri == "" {
		t.Fatalf("totp begin: secret=%q uri=%q err=%v", secret, uri, err)
	}

	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("generate code: %v", err)
	}
	codes, err := auth.TOTPConfirm(q, who, code, "1.2.3.4")
	if err != nil || len(codes) == 0 {
		t.Fatalf("totp confirm: codes=%v err=%v", codes, err)
	}

	// Now login must stop at the TOTP step.
	res, err := auth.Login(q, testCfg(), "a@b.c", "correct-password", "1.2.3.4", "agent")
	if err != nil || res.Step != auth.StepTOTP {
		t.Fatalf("expected TOTP step after enabling 2FA, got %+v err=%v", res, err)
	}

	badCode, _ := totp.GenerateCode(secret, time.Now().Add(-time.Hour))
	if err := auth.TOTPVerify(q, res.Token, badCode, "1.2.3.4", "agent"); err == nil {
		t.Fatal("expected stale code to be rejected")
	}
	freshCode, _ := totp.GenerateCode(secret, time.Now())
	if err := auth.TOTPVerify(q, res.Token, freshCode, "1.2.3.4", "agent"); err != nil {
		t.Fatalf("totp verify: %v", err)
	}

	info, err := auth.Resolve(q, testCfg(), res.Token)
	if err != nil || info == nil || info.Pending2FA {
		t.Fatalf("expected 2FA cleared after verify, got %+v err=%v", info, err)
	}

	if err := auth.TOTPDisable(q, who, codes[0], "1.2.3.4"); err != nil {
		t.Fatalf("totp disable via recovery code: %v", err)
	}
	u, _ = users.Get(q, uid)
	if u.TOTPEnabled {
		t.Fatal("expected TOTP disabled")
	}
}

func TestAPITokenLifecycle(t *testing.T) {
	q := openTestDB(t)
	uid := addActiveUser(t, q, "a@b.c", "correct-password")
	who := &access.Principal{UserID: uid}

	tok, err := auth.CreateToken(q, who, "CLI", enums.TokenRead, nil, nil)
	if err != nil || tok.Secret == "" {
		t.Fatalf("create token: %+v err=%v", tok, err)
	}

	resolved, err := auth.PrincipalForToken(q, tok.Secret, enums.TokenRead)
	if err != nil || resolved == nil || resolved.UserID != uid {
		t.Fatalf("expected token to resolve to user, got %+v err=%v", resolved, err)
	}

	other := &access.Principal{UserID: 999999}
	if err := auth.RevokeToken(q, other, tok.ID); !errors.Is(err, auth.ErrForbidden) {
		t.Fatalf("expected forbidden revoking someone else's token, got %v", err)
	}
	if err := auth.RevokeToken(q, who, tok.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if resolved, err := auth.PrincipalForToken(q, tok.Secret, enums.TokenRead); err != nil || resolved != nil {
		t.Fatalf("expected revoked token to no longer resolve, got %+v err=%v", resolved, err)
	}
}
