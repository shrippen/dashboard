// Package oidc is single sign-on with authentik (authorization code + PKCE).
//
//	/auth/oidc/login ──► authentik ──► /auth/oidc/callback
//	                                      │ state/nonce/verifier (memory, 10 min)
//	                                      ▼
//	          user by sub │ link to logged-in user │ verified e-mail │ auto-create
//	                                      │
//	          groups ──► initial role and teams (only when the account is created)
package oidc

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"andon/internal/crypto"
	"andon/internal/db"
	"andon/internal/enums"
	"andon/internal/model"
	"andon/internal/repos/misc"
	"andon/internal/repos/users"
	"andon/internal/services/access"
	"andon/internal/services/accounts"
	"andon/internal/services/audit"
	"andon/internal/settings"
	"andon/internal/sources"
)

const (
	settingKey   = "oidc"
	stateTTL     = 10 * time.Minute
	scopes       = "openid profile email"
	CallbackPath = "/auth/oidc/callback"
	groupsPref   = "oidc_groups"
	defaultLabel = "authentik"
	randomBytes  = 24
)

// Errors carry catalog keys (shown translated on the login page).
var (
	ErrDenied      = errors.New("error.denied")
	ErrDisabled    = errors.New("oidc.disabled")
	ErrUnreachable = errors.New("oidc.unreachable")
	ErrCancelled   = errors.New("oidc.denied")
	ErrState       = errors.New("oidc.state")
	ErrFailed      = errors.New("oidc.failed")
	ErrNoAccount   = errors.New("oidc.no_account")
	ErrSubTaken    = errors.New("oidc.sub_taken")
	ErrInactive    = errors.New("login.failed")
)

// GroupRule maps an authentik group to initial values for new accounts.
type GroupRule struct {
	Group    string
	Role     enums.InstanceRole // "" = no change
	Team     string
	TeamRole enums.TeamRole
}

// Config is the stored SSO configuration (the secret stays encrypted).
type Config struct {
	Enabled    bool
	Issuer     string
	ClientID   string
	HasSecret  bool
	Label      string
	Only       bool
	AutoCreate bool
	EmailLink  bool
	Rules      []GroupRule
}

type pending struct {
	nonce, verifier, next string
	linkUser              *int64
	at                    time.Time
}

var (
	pendingMu sync.Mutex
	inFlight  = map[string]pending{}
)

// ── Configuration ──

func raw(q db.Queryer, env settings.Settings) (map[string]any, error) {
	stored, err := misc.Setting(q, settingKey)
	if err != nil {
		return nil, err
	}
	if s, _ := stored["issuer"].(string); s == "" && env.OIDCIssuer != "" {
		merged := map[string]any{"enabled": true, "issuer": env.OIDCIssuer, "client_id": env.OIDCClientID}
		for k, v := range stored {
			merged[k] = v
		}
		return merged, nil
	}
	return stored, nil
}

func str(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return s
}

func flag(m map[string]any, key string, def bool) bool {
	if v, ok := m[key].(bool); ok {
		return v
	}
	return def
}

// Load reads the SSO configuration (database first, env as fallback).
func Load(q db.Queryer, env settings.Settings) (Config, error) {
	r, err := raw(q, env)
	if err != nil {
		return Config{}, err
	}
	cfg := Config{
		Enabled: flag(r, "enabled", false), Issuer: str(r, "issuer"), ClientID: str(r, "client_id"),
		HasSecret: str(r, "secret_enc") != "" || env.OIDCClientSecret != "",
		Label:     str(r, "label"), Only: flag(r, "only", false),
		AutoCreate: flag(r, "auto_create", true), EmailLink: flag(r, "email_link", false),
	}
	if cfg.Label == "" {
		cfg.Label = defaultLabel
	}
	list, _ := r["rules"].([]any)
	for _, item := range list {
		m, _ := item.(map[string]any)
		rule := GroupRule{Group: str(m, "group"), Role: enums.InstanceRole(str(m, "role")),
			Team: str(m, "team"), TeamRole: enums.TeamRole(str(m, "team_role"))}
		if rule.TeamRole == "" {
			rule.TeamRole = enums.TeamViewer
		}
		cfg.Rules = append(cfg.Rules, rule)
	}
	return cfg, nil
}

// Save stores the configuration; an empty secret keeps the stored one.
func Save(d *sql.DB, who *access.Principal, cfg Config, secret, ip string) error {
	if !who.IsAdmin() {
		return ErrDenied
	}
	return db.WithTx(d, func(tx *sql.Tx) error {
		stored, err := misc.Setting(tx, settingKey)
		if err != nil {
			return err
		}
		label := strings.TrimSpace(cfg.Label)
		if label == "" {
			label = defaultLabel
		}
		rules := []any{}
		for _, r := range cfg.Rules {
			if strings.TrimSpace(r.Group) == "" {
				continue
			}
			rules = append(rules, map[string]any{"group": strings.TrimSpace(r.Group), "role": string(r.Role),
				"team": strings.TrimSpace(r.Team), "team_role": string(r.TeamRole)})
		}
		stored["enabled"], stored["issuer"], stored["client_id"] = cfg.Enabled, strings.TrimSpace(cfg.Issuer), strings.TrimSpace(cfg.ClientID)
		stored["label"], stored["only"], stored["auto_create"], stored["email_link"] = label, cfg.Only, cfg.AutoCreate, cfg.EmailLink
		stored["rules"] = rules

		if secret != "" {
			sealed, err := crypto.Encrypt(secret, crypto.PurposeSetting, nil)
			if err != nil {
				return err
			}
			stored["secret_enc"] = base64.StdEncoding.EncodeToString(sealed)
		}
		if err := misc.SetSetting(tx, settingKey, stored); err != nil {
			return err
		}
		return audit.Log(tx, &who.UserID, "oidc.saved", "", ip, nil)
	})
}

func clientSecret(q db.Queryer, env settings.Settings) (string, error) {
	r, err := raw(q, env)
	if err != nil {
		return "", err
	}
	sealed := str(r, "secret_enc")
	if sealed == "" {
		return env.OIDCClientSecret, nil
	}
	blob, err := base64.StdEncoding.DecodeString(sealed)
	if err != nil {
		return "", err
	}
	return crypto.Decrypt(blob, crypto.PurposeSetting)
}

// Button returns the login button label, or "" when SSO is not usable.
func Button(q db.Queryer, env settings.Settings) string {
	cfg, err := Load(q, env)
	if err != nil || !cfg.Enabled || cfg.Issuer == "" || cfg.ClientID == "" {
		return ""
	}
	return cfg.Label
}

// Test checks the issuer's discovery document. Returns the issuer.
func Test(ctx context.Context, d *sql.DB, env settings.Settings, who *access.Principal) (string, error) {
	if !who.IsAdmin() {
		return "", ErrDenied
	}
	cfg, err := Load(d, env)
	if err != nil {
		return "", err
	}
	p, err := sources.Discover(ctx, cfg.Issuer)
	if err != nil {
		return "", err
	}
	return p.Issuer, nil
}

// ── Flow ──

func redirectURI(env settings.Settings) string {
	return strings.TrimRight(env.BaseURL, "/") + CallbackPath
}

func randomToken() string {
	b := make([]byte, randomBytes)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// challenge is the PKCE S256 code challenge of verifier.
func challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func cleanup(now time.Time) {
	for k, v := range inFlight {
		if now.Sub(v.at) > stateTTL {
			delete(inFlight, k)
		}
	}
}

// AuthorizeURL starts a login (or, with linkUser, an account link).
func AuthorizeURL(ctx context.Context, d *sql.DB, env settings.Settings, next string, linkUser *int64) (string, error) {
	cfg, err := Load(d, env)
	if err != nil {
		return "", err
	}
	if Button(d, env) == "" {
		return "", ErrDisabled
	}
	p, err := sources.Discover(ctx, cfg.Issuer)
	if err != nil {
		return "", ErrUnreachable
	}

	state, nonce, verifier := randomToken(), randomToken(), randomToken()
	now := time.Now()
	pendingMu.Lock()
	cleanup(now)
	inFlight[state] = pending{nonce: nonce, verifier: verifier, next: next, linkUser: linkUser, at: now}
	pendingMu.Unlock()

	query := url.Values{
		"response_type": {"code"}, "client_id": {cfg.ClientID}, "redirect_uri": {redirectURI(env)},
		"scope": {scopes}, "state": {state}, "nonce": {nonce},
		"code_challenge": {challenge(verifier)}, "code_challenge_method": {"S256"},
	}
	return p.Authorize + "?" + query.Encode(), nil
}

// Result is a finished login.
type Result struct {
	UserID  int64
	IDToken string
	Next    string
}

// Complete finishes the callback: state check, code exchange, token check,
// account resolution.
func Complete(ctx context.Context, d *sql.DB, env settings.Settings, params url.Values) (Result, error) {
	if params.Get("error") != "" {
		return Result{}, ErrCancelled
	}
	pendingMu.Lock()
	p, ok := inFlight[params.Get("state")]
	delete(inFlight, params.Get("state"))
	pendingMu.Unlock()
	if !ok || time.Since(p.at) > stateTTL {
		return Result{}, ErrState
	}

	cfg, err := Load(d, env)
	if err != nil {
		return Result{}, err
	}
	secret, err := clientSecret(d, env)
	if err != nil {
		return Result{}, err
	}
	provider, err := sources.Discover(ctx, cfg.Issuer)
	if err != nil {
		return Result{}, ErrFailed
	}
	tokens, err := sources.Exchange(ctx, provider, params.Get("code"), redirectURI(env), cfg.ClientID, secret, p.verifier)
	if err != nil {
		return Result{}, ErrFailed
	}
	claims, err := sources.Claims(ctx, provider, tokens.IDToken, cfg.ClientID, secret, p.nonce)
	if err != nil {
		return Result{}, ErrFailed
	}

	userID, err := resolveAccount(d, cfg, claims, p.linkUser)
	if err != nil {
		return Result{}, err
	}
	return Result{UserID: userID, IDToken: tokens.IDToken, Next: p.next}, nil
}

func claimGroups(claims map[string]any) []string {
	list, _ := claims["groups"].([]any)
	out := make([]string, 0, len(list))
	for _, g := range list {
		if s, ok := g.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

// resolveAccount finds (or links, or creates) the user behind claims.
func resolveAccount(d *sql.DB, cfg Config, claims map[string]any, linkUser *int64) (int64, error) {
	sub := str(claims, "sub")
	email := strings.TrimSpace(str(claims, "email"))
	groups := claimGroups(claims)
	if sub == "" {
		return 0, ErrFailed
	}

	var userID int64
	err := db.WithTx(d, func(tx *sql.Tx) error {
		user, err := users.BySub(tx, sub)
		if err != nil {
			return err
		}

		switch {
		case linkUser != nil:
			if user != nil && user.ID != *linkUser {
				return ErrSubTaken
			}
			if user, err = users.Get(tx, *linkUser); err != nil || user == nil {
				return ErrFailed
			}
			user.OIDCSub = sub
			if err := audit.Log(tx, &user.ID, "oidc.linked", "", "", nil); err != nil {
				return err
			}
		case user == nil && cfg.EmailLink && flag(claims, "email_verified", false) && email != "":
			if user, err = users.ByEmail(tx, email); err != nil {
				return err
			}
			if user != nil {
				user.OIDCSub = sub
				if err := audit.Log(tx, &user.ID, "oidc.linked_by_email", "", "", nil); err != nil {
					return err
				}
			}
		}

		if user == nil {
			if !cfg.AutoCreate || email == "" {
				return ErrNoAccount
			}
			if user, err = create(tx, cfg, claims, sub, email, groups); err != nil {
				return err
			}
		}
		if !user.IsActive {
			return ErrInactive
		}

		prefs := map[string]any{}
		for k, v := range user.Prefs {
			prefs[k] = v
		}
		prefs[groupsPref] = groups
		user.Prefs = prefs
		userID = user.ID
		return users.Update(tx, user)
	})
	return userID, err
}

func create(tx *sql.Tx, cfg Config, claims map[string]any, sub, email string, groups []string) (*model.User, error) {
	name := str(claims, "name")
	if name == "" {
		name = str(claims, "preferred_username")
	}
	role, teams := InitialValues(cfg.Rules, groups)
	user, err := accounts.Create(tx, email, name, nil, role, enums.LocaleDE, sub)
	if err != nil {
		return nil, err
	}
	if err := accounts.JoinTeams(tx, user.ID, teams); err != nil {
		return nil, err
	}
	return user, audit.Log(tx, &user.ID, "oidc.account_created", email, "", map[string]any{"groups": groups})
}

var teamRank = map[enums.TeamRole]int{enums.TeamViewer: 1, enums.TeamEditor: 2, enums.TeamOwner: 3}

// InitialValues derives role and teams from groups. Later logins don't change them.
func InitialValues(rules []GroupRule, groups []string) (enums.InstanceRole, []accounts.TeamAssignment) {
	member := map[string]bool{}
	for _, g := range groups {
		member[g] = true
	}

	role := enums.RoleUser
	best := map[string]enums.TeamRole{}
	var order []string
	for _, r := range rules {
		if !member[r.Group] {
			continue
		}
		if r.Role == enums.RoleAdmin {
			role = enums.RoleAdmin
		}
		if r.Team == "" {
			continue
		}
		current, seen := best[r.Team]
		if !seen {
			order = append(order, r.Team)
		}
		if !seen || teamRank[r.TeamRole] > teamRank[current] {
			best[r.Team] = r.TeamRole
		}
	}

	teams := make([]accounts.TeamAssignment, 0, len(order))
	for _, name := range order {
		teams = append(teams, accounts.TeamAssignment{Team: name, Role: best[name]})
	}
	return role, teams
}

// ── Reapply (admin) ──

// Plan is what reapplying the last seen groups would set.
type Plan struct {
	Role   enums.InstanceRole
	Teams  []accounts.TeamAssignment
	Groups []string
}

// PreviewReapply computes the plan for a user. Admin only.
func PreviewReapply(d *sql.DB, env settings.Settings, who *access.Principal, userID int64) (Plan, error) {
	if !who.IsAdmin() {
		return Plan{}, ErrDenied
	}
	user, err := users.Get(d, userID)
	if err != nil || user == nil {
		return Plan{}, ErrFailed
	}
	cfg, err := Load(d, env)
	if err != nil {
		return Plan{}, err
	}
	list, _ := user.Prefs[groupsPref].([]any)
	groups := make([]string, 0, len(list))
	for _, g := range list {
		if s, ok := g.(string); ok {
			groups = append(groups, s)
		}
	}
	role, teams := InitialValues(cfg.Rules, groups)
	return Plan{Role: role, Teams: teams, Groups: groups}, nil
}

// Reapply sets role and teams again from the last seen groups. An admin
// never demotes themselves this way.
func Reapply(d *sql.DB, env settings.Settings, who *access.Principal, userID int64, ip string) error {
	plan, err := PreviewReapply(d, env, who, userID)
	if err != nil {
		return err
	}
	return db.WithTx(d, func(tx *sql.Tx) error {
		user, err := users.Get(tx, userID)
		if err != nil || user == nil {
			return ErrFailed
		}
		if user.ID != who.UserID || plan.Role == enums.RoleAdmin {
			user.Role = plan.Role
		}
		if err := users.Update(tx, user); err != nil {
			return err
		}
		if err := accounts.JoinTeams(tx, user.ID, plan.Teams); err != nil {
			return err
		}
		return audit.Log(tx, &who.UserID, "oidc.reapplied", strconv.FormatInt(userID, 10), ip,
			map[string]any{"role": string(plan.Role)})
	})
}

// LogoutURL is the provider's end-session URL for idToken, or "".
func LogoutURL(ctx context.Context, d *sql.DB, env settings.Settings, idToken string) string {
	cfg, err := Load(d, env)
	if err != nil || cfg.Issuer == "" {
		return ""
	}
	p, err := sources.Discover(ctx, cfg.Issuer)
	if err != nil || p.EndSession == "" {
		return ""
	}
	query := url.Values{
		"id_token_hint":            {idToken},
		"post_logout_redirect_uri": {strings.TrimRight(env.BaseURL, "/") + "/login"},
	}
	return p.EndSession + "?" + query.Encode()
}
