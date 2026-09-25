package oidc_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"

	"dashboard/internal/crypto"
	"dashboard/internal/db"
	"dashboard/internal/db/dbtest"
	"dashboard/internal/enums"
	"dashboard/internal/repos/users"
	"dashboard/internal/services/access"
	"dashboard/internal/services/accounts"
	"dashboard/internal/services/oidc"
	"dashboard/internal/settings"
)

const clientID = "dash"

// fakeIdP is a minimal OIDC provider: discovery, JWKS, token endpoint.
type fakeIdP struct {
	srv    *httptest.Server
	key    *rsa.PrivateKey
	nonce  string
	claims map[string]any
}

func newIdP(t *testing.T) *fakeIdP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	idp := &fakeIdP{key: key}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		base := idp.srv.URL
		_ = json.NewEncoder(w).Encode(map[string]string{
			"issuer": base + "/", "authorization_endpoint": base + "/authorize",
			"token_endpoint": base + "/token", "jwks_uri": base + "/jwks",
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		set := jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: "k1", Algorithm: "RS256", Use: "sig"}}}
		_ = json.NewEncoder(w).Encode(set)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.FormValue("code_verifier") == "" || r.FormValue("code") != "good" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]string{"id_token": idp.sign(t)})
	})
	idp.srv = httptest.NewServer(mux)
	t.Cleanup(idp.srv.Close)
	return idp
}

func (idp *fakeIdP) sign(t *testing.T) string {
	claims := map[string]any{
		"iss": idp.srv.URL + "/", "aud": clientID, "nonce": idp.nonce,
		"exp": time.Now().Add(time.Hour).Unix(),
	}
	for k, v := range idp.claims {
		claims[k] = v
	}
	payload, _ := json.Marshal(claims)
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: jose.JSONWebKey{Key: idp.key, KeyID: "k1"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	obj, err := signer.Sign(payload)
	if err != nil {
		t.Fatal(err)
	}
	token, _ := obj.CompactSerialize()
	return token
}

func setup(t *testing.T) (*sql.DB, settings.Settings, *access.Principal, *fakeIdP) {
	t.Helper()
	crypto.Init("test-master-key")
	d, err := db.Open(filepath.Join(t.TempDir(), "t.db"), dbtest.Key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })

	var adminID int64
	err = db.WithTx(d, func(tx *sql.Tx) error {
		pw := "admin-password-123"
		u, err := accounts.Create(tx, "admin@x.de", "Admin", &pw, enums.RoleAdmin, enums.LocaleDE, "")
		adminID = u.ID
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	who, err := access.Load(d, adminID)
	if err != nil {
		t.Fatal(err)
	}

	idp := newIdP(t)
	env := settings.Settings{BaseURL: "http://dash.test"}
	cfg := oidc.Config{
		Enabled: true, Issuer: idp.srv.URL + "/", ClientID: clientID, Label: "authentik", AutoCreate: true,
		Rules: []oidc.GroupRule{
			{Group: "ops", Team: "Ops", TeamRole: enums.TeamViewer},
			{Group: "ops-lead", Team: "Ops", TeamRole: enums.TeamOwner},
		},
	}
	if err := oidc.Save(d, who, cfg, "client-secret", ""); err != nil {
		t.Fatal(err)
	}
	return d, env, who, idp
}

// login runs the full flow: authorize URL → callback params → Complete.
func login(t *testing.T, d *sql.DB, env settings.Settings, idp *fakeIdP, code string) (oidc.Result, error) {
	t.Helper()
	target, err := oidc.AuthorizeURL(context.Background(), d, env, "/boards/1", nil)
	if err != nil {
		t.Fatalf("authorize: %v", err)
	}
	u, _ := url.Parse(target)
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" || q.Get("redirect_uri") != "http://dash.test/auth/oidc/callback" {
		t.Fatalf("unexpected authorize query: %v", q)
	}
	idp.nonce = q.Get("nonce")
	return oidc.Complete(context.Background(), d, env, url.Values{"state": {q.Get("state")}, "code": {code}})
}

func TestLoginCreatesAccountWithGroupTeams(t *testing.T) {
	d, env, _, idp := setup(t)
	idp.claims = map[string]any{"sub": "u-1", "email": "neu@x.de", "name": "Neu", "groups": []string{"ops", "ops-lead"}}

	result, err := login(t, d, env, idp, "good")
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if result.Next != "/boards/1" || result.IDToken == "" {
		t.Fatalf("unexpected result: %+v", result)
	}

	user, _ := users.Get(d, result.UserID)
	if user.Email != "neu@x.de" || user.OIDCSub != "u-1" || user.PasswordHash != "" {
		t.Fatalf("unexpected user: %+v", user)
	}
	who, _ := access.Load(d, user.ID)
	var owner bool
	for _, role := range who.Teams {
		owner = owner || role == enums.TeamOwner
	}
	if !owner {
		t.Fatalf("expected highest team role (owner) from groups, got %v", who.Teams)
	}

	again, err := login(t, d, env, idp, "good")
	if err != nil || again.UserID != result.UserID {
		t.Fatalf("second login should find the same account: %+v err=%v", again, err)
	}
}

func TestLoginRejectsReplayAndBadNonce(t *testing.T) {
	d, env, _, idp := setup(t)
	idp.claims = map[string]any{"sub": "u-2", "email": "b@x.de"}

	target, _ := oidc.AuthorizeURL(context.Background(), d, env, "/", nil)
	u, _ := url.Parse(target)
	state := u.Query().Get("state")
	idp.nonce = "wrong"
	if _, err := oidc.Complete(context.Background(), d, env, url.Values{"state": {state}, "code": {"good"}}); err != oidc.ErrFailed {
		t.Fatalf("expected ErrFailed for wrong nonce, got %v", err)
	}
	if _, err := oidc.Complete(context.Background(), d, env, url.Values{"state": {state}, "code": {"good"}}); err != oidc.ErrState {
		t.Fatalf("expected ErrState on replayed state, got %v", err)
	}
}

func TestLinkRefusesTakenSub(t *testing.T) {
	d, env, admin, idp := setup(t)
	idp.claims = map[string]any{"sub": "u-3", "email": "c@x.de"}
	if _, err := login(t, d, env, idp, "good"); err != nil {
		t.Fatal(err)
	}

	target, _ := oidc.AuthorizeURL(context.Background(), d, env, "/me/security", &admin.UserID)
	u, _ := url.Parse(target)
	idp.nonce = u.Query().Get("nonce")
	_, err := oidc.Complete(context.Background(), d, env, url.Values{"state": {u.Query().Get("state")}, "code": {"good"}})
	if err != oidc.ErrSubTaken {
		t.Fatalf("expected ErrSubTaken, got %v", err)
	}
}

func TestInitialValuesPicksHighestTeamRole(t *testing.T) {
	rules := []oidc.GroupRule{
		{Group: "a", Role: enums.RoleAdmin},
		{Group: "b", Team: "T", TeamRole: enums.TeamEditor},
		{Group: "c", Team: "T", TeamRole: enums.TeamViewer},
	}
	role, teams := oidc.InitialValues(rules, []string{"b", "c"})
	if role != enums.RoleUser || len(teams) != 1 || teams[0].Role != enums.TeamEditor {
		t.Fatalf("got %v %+v", role, teams)
	}
	role, _ = oidc.InitialValues(rules, []string{"a"})
	if role != enums.RoleAdmin {
		t.Fatalf("expected admin, got %v", role)
	}
}
