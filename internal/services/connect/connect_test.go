package connect_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"andon/internal/crypto"
	"andon/internal/enums"
	"andon/internal/repos/content"
	"andon/internal/services/connect"
	"andon/internal/services/connections"
	"andon/internal/settings"
	"andon/internal/sources"
	"andon/internal/testkit"
)

var env = settings.Settings{BaseURL: "https://andon.test"}

func secretOf(t *testing.T, enc []byte) string {
	t.Helper()
	raw, err := crypto.Decrypt(enc, crypto.PurposeCredential)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	return raw
}

// TestGiteaSignIn: authorize URL with PKCE and scopes, callback exchanges
// the code with the verifier, the connection then holds a grant.
func TestGiteaSignIn(t *testing.T) {
	var challenge string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v := r.FormValue("code_verifier")
		sum := sha256.Sum256([]byte(v))
		if r.URL.Path != "/login/oauth/access_token" || r.FormValue("code") != "c1" ||
			base64.RawURLEncoding.EncodeToString(sum[:]) != challenge || r.FormValue("client_secret") != "cs" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Write([]byte(`{"access_token":"a1","refresh_token":"r1","expires_in":3600}`))
	}))
	defer srv.Close()

	d := testkit.DB(t)
	who, space := testkit.User(t, d, "a@b.c", enums.RoleAdmin)
	other, _ := testkit.User(t, d, "x@b.c", enums.RoleUser)
	id := testkit.Conn(t, d, who, space, enums.ServiceGitea, srv.URL)

	ctx := context.Background()
	if _, err := connect.Start(ctx, d, who, env, id, "/connections"); !errors.Is(err, connect.ErrNoClient) {
		t.Fatalf("expected ErrNoClient before registration, got %v", err)
	}
	if err := connections.SetOAuthClient(d, who, id, "cid", "cs"); err != nil {
		t.Fatal(err)
	}
	step, err := connect.Start(ctx, d, who, env, id, "/connections")
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	u, _ := url.Parse(step.Redirect)
	q := u.Query()
	if u.Path != "/login/oauth/authorize" || q.Get("client_id") != "cid" || q.Get("redirect_uri") != "https://andon.test"+connect.CallbackPath ||
		!strings.Contains(q.Get("scope"), "read:repository") {
		t.Fatalf("authorize url: %s", step.Redirect)
	}
	challenge = q.Get("code_challenge")

	// Someone else's browser cannot finish this flow.
	if _, err := connect.Callback(ctx, d, other, env, url.Values{"state": {q.Get("state")}, "code": {"c1"}}); !errors.Is(err, connect.ErrFlow) {
		t.Fatalf("foreign callback: %v", err)
	}
	if _, err := connect.Callback(ctx, d, who, env, url.Values{"state": {q.Get("state")}, "code": {"c1"}}); err != nil {
		t.Fatalf("callback: %v", err)
	}

	conn, _ := content.Connection(d, id)
	if secretOf(t, conn.SecretEnc) != sources.GrantMarker {
		t.Fatal("connection does not point at its grant")
	}
	var g sources.Grant
	enc, _ := content.Grant(d, id, 0)
	_ = json.Unmarshal([]byte(secretOf(t, enc)), &g)
	if g.Access != "a1" || g.Refresh != "r1" {
		t.Fatalf("grant: %+v", g)
	}

	// The flow is used up.
	if _, err := connect.Callback(ctx, d, who, env, url.Values{"state": {q.Get("state")}, "code": {"c1"}}); !errors.Is(err, connect.ErrFlow) {
		t.Fatalf("replayed callback: %v", err)
	}
}

// TestNextcloudPersonalSignIn: a personal connection stores the app
// password as the caller's own credential once the login finished.
func TestNextcloudPersonalSignIn(t *testing.T) {
	var polls atomic.Int32
	var srv *httptest.Server
	mux := http.NewServeMux()
	mux.HandleFunc("POST /index.php/login/v2", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"login": srv.URL + "/flow",
			"poll": map[string]string{"token": "pt", "endpoint": srv.URL + "/poll"}})
	})
	mux.HandleFunc("POST /poll", func(w http.ResponseWriter, r *http.Request) {
		if polls.Add(1) == 1 {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Write([]byte(`{"loginName":"arian","appPassword":"pw"}`))
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	d := testkit.DB(t)
	who, space := testkit.User(t, d, "a@b.c", enums.RoleAdmin)
	id, err := connections.Create(d, who, space, enums.ServiceNextcloud, "NC", srv.URL, enums.CredentialPersonal, "", connections.TLSVerify, nil)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	step, err := connect.Start(ctx, d, who, env, id, "/connections")
	if err != nil || step.Link == "" || step.Flow == "" {
		t.Fatalf("start: %+v %v", step, err)
	}
	if step, _, err = connect.Poll(ctx, d, who, step.Flow); err != nil || step.Done {
		t.Fatalf("first poll: %+v %v", step, err)
	}
	if step, _, err = connect.Poll(ctx, d, who, step.Flow); err != nil || !step.Done {
		t.Fatalf("second poll: %+v %v", step, err)
	}
	cred, _ := content.Credential(d, id, who.UserID)
	if cred == nil || secretOf(t, cred.SecretEnc) != "arian:pw" {
		t.Fatal("personal credential not stored")
	}
}

// TestHassAuthorizeURL: Home Assistant needs no registration; Andon's own
// address is the client id.
func TestHassAuthorizeURL(t *testing.T) {
	d := testkit.DB(t)
	who, space := testkit.User(t, d, "a@b.c", enums.RoleAdmin)
	id := testkit.Conn(t, d, who, space, enums.ServiceHomeAssistant, "https://ha.test")

	step, err := connect.Start(context.Background(), d, who, env, id, "/connections")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := url.Parse(step.Redirect)
	if u.Host != "ha.test" || u.Path != "/auth/authorize" || u.Query().Get("client_id") != "https://andon.test/" {
		t.Fatalf("authorize url: %s", step.Redirect)
	}
}
