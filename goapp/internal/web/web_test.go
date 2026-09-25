package web_test

import (
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"dashboard/internal/crypto"
	"dashboard/internal/db"
	"dashboard/internal/services/auth"
	"dashboard/internal/settings"
	"dashboard/internal/web"
)

func newTestServer(t *testing.T) (*httptest.Server, *http.Client, string) {
	t.Helper()
	crypto.Init("test-master-key")
	auth.ResetThrottle()
	database, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	deps := web.Deps{DB: database, Settings: settings.Settings{
		SessionAbsoluteHours: 24, SessionIdleMinutes: 60,
	}}
	mux := http.NewServeMux()
	deps.RegisterAuthRoutes(mux)
	deps.RegisterBoardRoutes(mux)

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	client := &http.Client{
		Jar: jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	code, err := auth.EnsureSetupCode(database)
	if err != nil || code == "" {
		t.Fatalf("setup code: %q err=%v", code, err)
	}
	return srv, client, code
}

func setupAdmin(t *testing.T, srv *httptest.Server, client *http.Client, code string) {
	t.Helper()
	resp, err := client.PostForm(srv.URL+"/setup", url.Values{
		"code": {code}, "email": {"admin@x.de"}, "name": {"Admin"}, "password": {"s3cret-password-long"},
	})
	if err != nil {
		t.Fatalf("setup post: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303 from setup, got %d", resp.StatusCode)
	}
}

func login(t *testing.T, srv *httptest.Server, client *http.Client) {
	t.Helper()
	resp, err := client.PostForm(srv.URL+"/login", url.Values{
		"email": {"admin@x.de"}, "password": {"s3cret-password-long"},
	})
	if err != nil {
		t.Fatalf("login post: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303 from login, got %d", resp.StatusCode)
	}
}

var csrfRe = regexp.MustCompile(`name="csrf" value="([^"]+)"`)

func csrfToken(t *testing.T, srv *httptest.Server, client *http.Client) string {
	t.Helper()
	resp, err := client.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("get home: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	m := csrfRe.FindSubmatch(body)
	if m == nil {
		t.Fatalf("no csrf token found in home page:\n%s", body)
	}
	return string(m[1])
}

func TestFullLoginLogoutFlow(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)

	// Not logged in: home redirects to /login.
	resp, err := client.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("get home: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/login" {
		t.Fatalf("expected redirect to /login, got %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}

	login(t, srv, client)

	resp, err = client.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("get home after login: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "Admin") {
		t.Fatalf("expected home page showing the user, got %d:\n%s", resp.StatusCode, body)
	}
}

// TestLogoutRequiresCSRF guards against a regression to the bug found while
// hand-testing: logout without a CSRF token must be refused, not silently
// end the session (a bare cross-site <form> could otherwise force a logout).
func TestLogoutRequiresCSRF(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)
	login(t, srv, client)

	resp, err := client.PostForm(srv.URL+"/logout", url.Values{})
	if err != nil {
		t.Fatalf("logout without csrf: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for logout without csrf, got %d", resp.StatusCode)
	}

	// Session must still be alive.
	resp, err = client.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("get home: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected still logged in after rejected logout, got %d", resp.StatusCode)
	}

	// With the real CSRF token, logout succeeds and the session ends.
	token := csrfToken(t, srv, client)
	resp, err = client.PostForm(srv.URL+"/logout", url.Values{"csrf": {token}})
	if err != nil {
		t.Fatalf("logout with csrf: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303 for logout with valid csrf, got %d", resp.StatusCode)
	}

	resp, err = client.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("get home after logout: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != "/login" {
		t.Fatalf("expected logged out after valid-csrf logout, got %d", resp.StatusCode)
	}
}

func TestLoginWrongPasswordStaysOnLoginPage(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)

	resp, err := client.PostForm(srv.URL+"/login", url.Values{
		"email": {"admin@x.de"}, "password": {"wrong"},
	})
	if err != nil {
		t.Fatalf("login post: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized || !strings.Contains(string(body), "fehlgeschlagen") {
		t.Fatalf("expected 401 with error message, got %d:\n%s", resp.StatusCode, body)
	}
}
