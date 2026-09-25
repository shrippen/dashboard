// Package auth handles login, sessions, the second factor and API tokens.
//
//	browser ──cookie(token)──► session row (hashed token)
//	                              │ pending_2fa? ──► /login/totp only
//	                              ▼
//	                           Principal (user, teams, grants, spaces)
package auth

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/pquerna/otp/totp"

	"dashboard/internal/crypto"
	"dashboard/internal/db"
	"dashboard/internal/enums"
	"dashboard/internal/model"
	"dashboard/internal/repos/auth"
	"dashboard/internal/repos/misc"
	"dashboard/internal/repos/users"
	"dashboard/internal/services/access"
	"dashboard/internal/services/accounts"
	auditsvc "dashboard/internal/services/audit"
	"dashboard/internal/services/mail"
	"dashboard/internal/settings"
)

const (
	setupKey       = "setup"
	setupCodeBytes = 5 // hex-encoded -> 10 chars
	totpIssuer     = "dashboard"
	recoveryCodes  = 10
	tokenPrefixLen = 12
	touchInterval  = 5 * time.Minute
	accountLimit   = 5
	ipLimit        = 20
	throttleWindow = 15 * time.Minute
)

// AuthError is a generic login failure; its message never reveals which
// part (email vs. password vs. TOTP) was wrong.
type AuthError struct{ msg string }

func (e AuthError) Error() string { return e.msg }

var (
	ErrLoginFailed = AuthError{"login.failed"}
	ErrOIDCOnly    = AuthError{"login.oidc_only"}
	ErrThrottled   = AuthError{"throttled"}
	ErrTOTPInvalid = AuthError{"totp.invalid"}
	ErrSetupClosed = AuthError{"setup.closed"}
	ErrSetupCode   = AuthError{"setup.bad_code"}
)

// Step is where a login attempt currently stands.
type Step string

const (
	StepDone Step = "done"
	StepTOTP Step = "totp"
)

// LoginResult is returned by Login: the session cookie value and whether a
// second factor is still required.
type LoginResult struct {
	Token string
	Step  Step
}

// ── Throttling (in memory: one container, few users) ──

var (
	failsMu sync.Mutex
	fails   = map[string][]time.Time{}
)

func recent(key string, now time.Time) []time.Time {
	var out []time.Time
	for _, t := range fails[key] {
		if now.Sub(t) < throttleWindow {
			out = append(out, t)
		}
	}
	return out
}

func checkThrottle(email, ip string) error {
	now := time.Now()
	failsMu.Lock()
	defer failsMu.Unlock()
	if len(recent("a:"+strings.ToLower(email), now)) >= accountLimit {
		return ErrThrottled
	}
	if len(recent("i:"+ip, now)) >= ipLimit {
		return ErrThrottled
	}
	return nil
}

func noteFail(email, ip string) {
	now := time.Now()
	failsMu.Lock()
	defer failsMu.Unlock()
	for _, key := range []string{"a:" + strings.ToLower(email), "i:" + ip} {
		fails[key] = append(recent(key, now), now)
	}
}

func clearFails(email string) {
	failsMu.Lock()
	defer failsMu.Unlock()
	delete(fails, "a:"+strings.ToLower(email))
}

// ResetThrottle clears every recorded failure. Tests only.
func ResetThrottle() {
	failsMu.Lock()
	defer failsMu.Unlock()
	fails = map[string][]time.Time{}
}

// ── First start ──

// SetupNeeded reports whether no user exists yet.
func SetupNeeded(q db.Queryer) (bool, error) {
	n, err := users.Count(q)
	return n == 0, err
}

// EnsureSetupCode returns a fresh one-time code (logged, never returned to
// an HTTP caller) if no user exists yet, or "" once setup is done.
func EnsureSetupCode(d *sql.DB) (string, error) {
	var code string
	err := db.WithTx(d, func(tx *sql.Tx) error {
		n, err := users.Count(tx)
		if err != nil || n > 0 {
			return err
		}
		raw := make([]byte, setupCodeBytes)
		if _, err := rand.Read(raw); err != nil {
			return err
		}
		code = strings.ToUpper(hex.EncodeToString(raw))
		return misc.SetSetting(tx, setupKey, map[string]any{"hash": crypto.TokenHash(code)})
	})
	if err != nil || code == "" {
		return "", err
	}
	slog.Warn("SETUP CODE (first admin)", "code", code)
	return code, nil
}

// CreateAdmin verifies the setup code and creates the first admin account.
func CreateAdmin(d *sql.DB, code, email, name, password string, locale enums.Locale) error {
	return db.WithTx(d, func(tx *sql.Tx) error {
		setting, err := misc.Setting(tx, setupKey)
		if err != nil {
			return err
		}
		stored, _ := setting["hash"].(string)
		n, err := users.Count(tx)
		if err != nil {
			return err
		}
		if n > 0 || stored == "" {
			return ErrSetupClosed
		}
		if !crypto.Same(stored, crypto.TokenHash(strings.ToUpper(strings.TrimSpace(code)))) {
			return ErrSetupCode
		}

		user, err := accounts.Create(tx, email, name, &password, enums.RoleAdmin, locale, "")
		if err != nil {
			return err
		}
		user.IsBreakglass = true
		if err := users.Update(tx, user); err != nil {
			return err
		}
		if err := misc.SetSetting(tx, setupKey, map[string]any{}); err != nil {
			return err
		}
		return auditsvc.Log(tx, &user.ID, "setup.admin_created", "", "", nil)
	})
}

// ── Password login ──

// Login checks credentials and opens a session. On success but before a
// TOTP-enabled account passes its second factor, the returned token is only
// good for the TOTP endpoint.
func Login(d *sql.DB, cfg settings.Settings, email, password, ip, agent string) (LoginResult, error) {
	if err := checkThrottle(email, ip); err != nil {
		return LoginResult{}, err
	}

	var result LoginResult
	var userID int64
	err := db.WithTx(d, func(tx *sql.Tx) error {
		user, err := users.ByEmail(tx, strings.TrimSpace(email))
		if err != nil {
			return err
		}
		var stored string
		if user != nil && user.IsActive {
			stored = user.PasswordHash
		}
		if !crypto.CheckPassword(stored, password) {
			noteFail(email, ip)
			var uid *int64
			if user != nil {
				uid = &user.ID
			}
			_ = auditsvc.Log(tx, uid, "login.failed", email, ip, nil)
			return ErrLoginFailed
		}

		oidcOnly, err := isOIDCOnly(tx)
		if err != nil {
			return err
		}
		if oidcOnly && !user.IsBreakglass {
			return ErrOIDCOnly
		}

		clearFails(email)
		step := StepDone
		if user.TOTPEnabled {
			step = StepTOTP
		}
		token, err := openSession(tx, cfg, user, enums.AuthPassword, ip, agent, step, "")
		if err != nil {
			return err
		}
		if err := auditsvc.Log(tx, &user.ID, "login.password", "", ip, nil); err != nil {
			return err
		}
		result = LoginResult{Token: token, Step: step}
		userID = user.ID
		return nil
	})
	if err != nil {
		return LoginResult{}, err
	}

	if result.Step == StepDone {
		noteLogin(d, userID, ip, agent)
	}
	return result, nil
}

func isOIDCOnly(q db.Queryer) (bool, error) {
	setting, err := misc.Setting(q, "oidc")
	if err != nil {
		return false, err
	}
	only, _ := setting["only"].(bool)
	return only, nil
}

func openSession(q db.Queryer, cfg settings.Settings, user *model.User, method enums.AuthMethod, ip, agent string, step Step, idToken string) (string, error) {
	hours := cfg.SessionAbsoluteHours
	if method == enums.AuthOIDC {
		hours = cfg.OIDCSessionHours
	}
	token := crypto.NewToken()
	now := time.Now().UTC()

	session := &model.LoginSession{
		TokenHash: crypto.TokenHash(token), UserID: user.ID, Method: method,
		CSRF: hex.EncodeToString(randBytes(16)), CreatedAt: now, LastSeen: now,
		ExpiresAt: now.Add(time.Duration(hours) * time.Hour), IP: truncate(ip, 64),
		UserAgent: truncate(agent, 300), Pending2FA: step == StepTOTP, IDToken: idToken,
	}
	if err := auth.AddSession(q, session); err != nil {
		return "", err
	}
	user.LastLoginAt = &now
	if err := users.Update(q, user); err != nil {
		return "", err
	}
	return token, nil
}

func randBytes(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

// OpenOIDCSession opens a session for a user that just completed OIDC login.
func OpenOIDCSession(d *sql.DB, cfg settings.Settings, userID int64, ip, agent, idToken string) (string, error) {
	var token string
	err := db.WithTx(d, func(tx *sql.Tx) error {
		user, err := users.Get(tx, userID)
		if err != nil {
			return err
		}
		if user == nil {
			return sql.ErrNoRows
		}
		token, err = openSession(tx, cfg, user, enums.AuthOIDC, ip, agent, StepDone, idToken)
		if err != nil {
			return err
		}
		return auditsvc.Log(tx, &user.ID, "login.oidc", "", ip, nil)
	})
	return token, err
}

// ── Session lookup ──

// SessionInfo is what Resolve returns for a valid session cookie.
type SessionInfo struct {
	Principal  *access.Principal // nil while a second factor is still pending
	CSRF       string
	Pending2FA bool
	Method     enums.AuthMethod
}

// Resolve looks up a session by its cookie token, refreshing last_seen and
// dropping it if expired or idle too long.
func Resolve(d *sql.DB, cfg settings.Settings, token string) (*SessionInfo, error) {
	if token == "" {
		return nil, nil
	}

	var info *SessionInfo
	err := db.WithTx(d, func(tx *sql.Tx) error {
		row, err := auth.SessionByHash(tx, crypto.TokenHash(token))
		if err != nil || row == nil {
			return err
		}

		now := time.Now().UTC()
		idleLimit := row.LastSeen.Add(time.Duration(cfg.SessionIdleMinutes) * time.Minute)
		if row.ExpiresAt.Before(now) || idleLimit.Before(now) {
			return auth.RemoveSession(tx, row.ID)
		}

		if now.Sub(row.LastSeen) > touchInterval {
			if err := auth.TouchSession(tx, row.ID, now, row.Pending2FA); err != nil {
				return err
			}
		}

		var who *access.Principal
		if !row.Pending2FA {
			who, err = access.Load(tx, row.UserID)
			if err != nil {
				return err
			}
			if who == nil {
				return auth.RemoveSession(tx, row.ID)
			}
			sessionID := row.ID
			who.SessionID = &sessionID
		}

		info = &SessionInfo{Principal: who, CSRF: row.CSRF, Pending2FA: row.Pending2FA, Method: row.Method}
		return nil
	})
	return info, err
}

// Logout ends a session and returns its OIDC id_token, if any, for
// RP-initiated logout.
func Logout(d *sql.DB, token string) (string, error) {
	if token == "" {
		return "", nil
	}
	var idToken string
	err := db.WithTx(d, func(tx *sql.Tx) error {
		row, err := auth.SessionByHash(tx, crypto.TokenHash(token))
		if err != nil || row == nil {
			return err
		}
		idToken = row.IDToken
		if err := auditsvc.Log(tx, &row.UserID, "logout", "", "", nil); err != nil {
			return err
		}
		return auth.RemoveSession(tx, row.ID)
	})
	return idToken, err
}

// MySessions lists a principal's active sessions.
func MySessions(d *sql.DB, who *access.Principal) ([]*model.LoginSession, error) {
	var out []*model.LoginSession
	err := db.WithTx(d, func(tx *sql.Tx) error {
		var err error
		out, err = auth.SessionsOf(tx, who.UserID)
		return err
	})
	return out, err
}

// EndSession removes one of the principal's own sessions.
func EndSession(d *sql.DB, who *access.Principal, sessionID int64) error {
	return db.WithTx(d, func(tx *sql.Tx) error {
		sessions, err := auth.SessionsOf(tx, who.UserID)
		if err != nil {
			return err
		}
		for _, row := range sessions {
			if row.ID == sessionID {
				return auth.RemoveSession(tx, row.ID)
			}
		}
		return nil
	})
}

// EndOtherSessions drops every session of the principal except the current
// one (e.g. after a password change).
func EndOtherSessions(d *sql.DB, who *access.Principal) error {
	return db.WithTx(d, func(tx *sql.Tx) error {
		return auth.DropSessions(tx, who.UserID, who.SessionID)
	})
}

// Purge deletes expired sessions, invites and reset tokens.
func Purge(d *sql.DB) error {
	return db.WithTx(d, func(tx *sql.Tx) error {
		return auth.PurgeExpired(tx, time.Now().UTC())
	})
}

// ── Second factor (TOTP) ──

// TOTPBegin generates a new (not yet active) TOTP secret and its
// provisioning URI for a QR code.
func TOTPBegin(d *sql.DB, who *access.Principal) (secret, uri string, err error) {
	err = db.WithTx(d, func(tx *sql.Tx) error {
		user, err := users.Get(tx, who.UserID)
		if err != nil {
			return err
		}
		if user == nil {
			return sql.ErrNoRows
		}
		key, err := totp.Generate(totp.GenerateOpts{Issuer: totpIssuer, AccountName: user.Email})
		if err != nil {
			return err
		}
		secret = key.Secret()
		uri = key.URL()

		enc, err := crypto.Encrypt(secret, crypto.PurposeTOTP, nil)
		if err != nil {
			return err
		}
		user.TOTPSecretEnc = enc
		user.TOTPEnabled = false
		return users.Update(tx, user)
	})
	return secret, uri, err
}

// TOTPConfirm activates TOTP after one valid code and returns fresh
// recovery codes (shown to the user once, never again).
func TOTPConfirm(d *sql.DB, who *access.Principal, code, ip string) ([]string, error) {
	var codes []string
	err := db.WithTx(d, func(tx *sql.Tx) error {
		user, err := users.Get(tx, who.UserID)
		if err != nil {
			return err
		}
		if user == nil || len(user.TOTPSecretEnc) == 0 {
			return ErrTOTPInvalid
		}
		ok, err := totpOK(user, code)
		if err != nil {
			return err
		}
		if !ok {
			return ErrTOTPInvalid
		}

		codes = make([]string, recoveryCodes)
		hashed := make([]string, recoveryCodes)
		for i := range codes {
			codes[i] = hex.EncodeToString(randBytes(5))
			hashed[i] = crypto.TokenHash(codes[i])
		}
		user.RecoveryCodes = hashed
		user.TOTPEnabled = true
		if err := users.Update(tx, user); err != nil {
			return err
		}
		return auditsvc.Log(tx, &user.ID, "totp.enabled", "", ip, nil)
	})
	if err != nil {
		return nil, err
	}
	notify(d, who.UserID, mail.TOTPEnabled)
	return codes, nil
}

// TOTPDisable turns TOTP off after a valid code or recovery code.
func TOTPDisable(d *sql.DB, who *access.Principal, code, ip string) error {
	err := db.WithTx(d, func(tx *sql.Tx) error {
		user, err := users.Get(tx, who.UserID)
		if err != nil {
			return err
		}
		if user == nil {
			return sql.ErrNoRows
		}
		if user.TOTPEnabled {
			ok, err := totpOK(user, code)
			if err != nil {
				return err
			}
			if !ok && !useRecovery(user, code) {
				return ErrTOTPInvalid
			}
		}
		user.TOTPEnabled = false
		user.TOTPSecretEnc = nil
		user.RecoveryCodes = nil
		if err := users.Update(tx, user); err != nil {
			return err
		}
		return auditsvc.Log(tx, &user.ID, "totp.disabled", "", ip, nil)
	})
	if err == nil {
		notify(d, who.UserID, mail.TOTPDisabled)
	}
	return err
}

// TOTPVerify is the second login step: it lifts pending_2fa on success.
func TOTPVerify(d *sql.DB, token, code, ip, agent string) error {
	var userID int64
	err := db.WithTx(d, func(tx *sql.Tx) error {
		row, err := auth.SessionByHash(tx, crypto.TokenHash(token))
		if err != nil {
			return err
		}
		if row == nil || !row.Pending2FA {
			return ErrTOTPInvalid
		}
		user, err := users.Get(tx, row.UserID)
		if err != nil || user == nil {
			return err
		}
		if err := checkThrottle(user.Email, ip); err != nil {
			return err
		}

		ok, err := totpOK(user, code)
		if err != nil {
			return err
		}
		if !ok && !useRecovery(user, code) {
			noteFail(user.Email, ip)
			return ErrTOTPInvalid
		}
		if err := users.Update(tx, user); err != nil { // persists a used recovery code, if any
			return err
		}
		userID = user.ID
		return auth.TouchSession(tx, row.ID, time.Now().UTC(), false)
	})
	if err == nil {
		noteLogin(d, userID, ip, agent)
	}
	return err
}

func totpOK(user *model.User, code string) (bool, error) {
	if len(user.TOTPSecretEnc) == 0 {
		return false, nil
	}
	secret, err := crypto.Decrypt(user.TOTPSecretEnc, crypto.PurposeTOTP)
	if err != nil {
		return false, err
	}
	clean := strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	return totp.Validate(clean, secret), nil
}

func useRecovery(user *model.User, code string) bool {
	hashed := crypto.TokenHash(strings.ToLower(strings.TrimSpace(code)))
	for i, c := range user.RecoveryCodes {
		if c == hashed {
			user.RecoveryCodes = append(user.RecoveryCodes[:i:i], user.RecoveryCodes[i+1:]...)
			return true
		}
	}
	return false
}

// TOTPRequired reports whether an admin must still set up TOTP because it
// is force-enabled instance-wide. With OIDC, authentik owns the second
// factor, so it is never required here.
func TOTPRequired(q db.Queryer, who *access.Principal, method enums.AuthMethod) (bool, error) {
	if method == enums.AuthOIDC || !who.IsAdmin() {
		return false, nil
	}
	setting, err := misc.Setting(q, "security")
	if err != nil {
		return false, err
	}
	forced, _ := setting["force_admin_totp"].(bool)
	if !forced {
		return false, nil
	}
	user, err := users.Get(q, who.UserID)
	if err != nil || user == nil {
		return false, err
	}
	return !user.TOTPEnabled, nil
}

// ── API tokens ──

// NewAPIToken is returned once by CreateToken: the id plus the plaintext
// secret, which is never retrievable again.
type NewAPIToken struct {
	ID     int64
	Secret string
}

// ErrForbidden is returned when a token operation targets another user's token.
var ErrForbidden = errors.New("auth: not your token")

// CreateToken issues a new API token for the principal.
func CreateToken(d *sql.DB, who *access.Principal, name string, scope enums.TokenScope, boardIDs []int64, days *int) (NewAPIToken, error) {
	secret := "dsh_" + crypto.NewToken()
	var out NewAPIToken
	err := db.WithTx(d, func(tx *sql.Tx) error {
		label := strings.TrimSpace(name)
		if label == "" {
			label = string(scope)
		}
		item := &model.ApiToken{
			UserID: who.UserID, Name: label, TokenHash: crypto.TokenHash(secret),
			Prefix: secret[:min(len(secret), tokenPrefixLen)], Scope: scope, BoardIDs: boardIDs,
			CreatedAt: time.Now().UTC(),
		}
		if days != nil {
			exp := time.Now().UTC().AddDate(0, 0, *days)
			item.ExpiresAt = &exp
		}
		if err := auth.AddToken(tx, item); err != nil {
			return err
		}
		if err := auditsvc.Log(tx, &who.UserID, "token.created", item.Name, "", nil); err != nil {
			return err
		}
		out = NewAPIToken{ID: item.ID, Secret: secret}
		return nil
	})
	if err == nil {
		notify(d, who.UserID, mail.TokenCreated)
	}
	return out, err
}

// MyTokens lists a principal's API tokens.
func MyTokens(d *sql.DB, who *access.Principal) ([]*model.ApiToken, error) {
	var out []*model.ApiToken
	err := db.WithTx(d, func(tx *sql.Tx) error {
		var err error
		out, err = auth.TokensOf(tx, who.UserID)
		return err
	})
	return out, err
}

// RevokeToken deletes one of the principal's own API tokens.
func RevokeToken(d *sql.DB, who *access.Principal, tokenID int64) error {
	return db.WithTx(d, func(tx *sql.Tx) error {
		item, err := auth.Token(tx, tokenID)
		if err != nil {
			return err
		}
		if item == nil || item.UserID != who.UserID {
			return ErrForbidden
		}
		if err := auth.RemoveToken(tx, item.ID); err != nil {
			return err
		}
		return auditsvc.Log(tx, &who.UserID, "token.revoked", item.Name, "", nil)
	})
}

// PrincipalForToken resolves an API token's bearer to a Principal, scoped to
// its board list if it has one.
func PrincipalForToken(d *sql.DB, secret string, scope enums.TokenScope) (*access.Principal, error) {
	var who *access.Principal
	err := db.WithTx(d, func(tx *sql.Tx) error {
		item, err := auth.TokenByHash(tx, crypto.TokenHash(secret))
		if err != nil || item == nil {
			return err
		}
		if item.ExpiresAt != nil && item.ExpiresAt.Before(time.Now().UTC()) {
			return nil
		}
		if scope == enums.TokenRead && item.Scope != enums.TokenRead {
			return nil
		}

		now := time.Now().UTC()
		if err := auth.TouchToken(tx, item.ID, now); err != nil {
			return err
		}
		who, err = access.Load(tx, item.UserID)
		if err != nil || who == nil {
			return err
		}
		if len(item.BoardIDs) > 0 {
			who.TokenBoards = item.BoardIDs
		}
		return nil
	})
	return who, err
}

// notify sends a security notice; mail problems never fail the action.
func notify(d *sql.DB, userID int64, kind mail.SecurityKind) {
	if err := mail.SecurityNotice(d, userID, kind); err != nil {
		slog.Warn("security mail", "kind", kind, "err", err)
	}
}

// noteLogin mails a new-device notice; failures are only logged.
func noteLogin(d *sql.DB, userID int64, ip, agent string) {
	if err := mail.NewLogin(d, userID, ip, agent); err != nil {
		slog.Warn("new-login mail", "err", err)
	}
}
