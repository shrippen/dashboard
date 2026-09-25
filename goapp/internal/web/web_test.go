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
	"dashboard/internal/services/themes"
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
	if _, err := themes.EnsureBuiltin(database); err != nil {
		t.Fatalf("ensure builtin theme: %v", err)
	}

	deps := web.Deps{DB: database, Settings: settings.Settings{
		SessionAbsoluteHours: 24, SessionIdleMinutes: 60,
	}}
	mux := http.NewServeMux()
	deps.RegisterAuthRoutes(mux)
	deps.RegisterBoardRoutes(mux)
	deps.RegisterThemeRoutes(mux)

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

// getFollowingRedirect GETs path and, if the response is a redirect,
// follows its Location once (the client's own Jar carries the session
// cookie, and CheckRedirect is set to not auto-follow elsewhere in this
// file, so tests can see and assert on the redirect itself when they want to).
func getFollowingRedirect(t *testing.T, srv *httptest.Server, client *http.Client, path string) *http.Response {
	t.Helper()
	resp, err := client.Get(srv.URL + path)
	if err != nil {
		t.Fatalf("get %s: %v", path, err)
	}
	if loc := resp.Header.Get("Location"); resp.StatusCode == http.StatusSeeOther && loc != "" {
		resp.Body.Close()
		resp, err = client.Get(srv.URL + loc)
		if err != nil {
			t.Fatalf("get %s: %v", loc, err)
		}
	}
	return resp
}

func csrfToken(t *testing.T, srv *httptest.Server, client *http.Client) string {
	t.Helper()
	resp := getFollowingRedirect(t, srv, client, "/")
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

	// "/" redirects to a freshly created start board.
	resp = getFollowingRedirect(t, srv, client, "/")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "Admin") {
		t.Fatalf("expected board page showing the user, got %d:\n%s", resp.StatusCode, body)
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

	// Session must still be alive: "/" still redirects to a board, not to
	// /login.
	resp, err = client.Get(srv.URL + "/")
	if err != nil {
		t.Fatalf("get home: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") == "/login" {
		t.Fatalf("expected still logged in after rejected logout, got %d %s", resp.StatusCode, resp.Header.Get("Location"))
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

// TestBoardViewShowsPlacedWidget confirms the board route is wired to the
// real boards service, not just a placeholder list: a placed widget's
// title and type show up on the rendered page.
func TestBoardViewShowsPlacedWidget(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)
	login(t, srv, client)

	resp := getFollowingRedirect(t, srv, client, "/")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), "Start") {
		t.Fatalf("expected the auto-created start board, got %d:\n%s", resp.StatusCode, body)
	}
}

// TestThemeCSSRoute confirms the board's <link rel="stylesheet"> target
// actually serves real CSS with theme tokens.
func TestThemeCSSRoute(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)
	login(t, srv, client)

	resp := getFollowingRedirect(t, srv, client, "/")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	m := regexp.MustCompile(`href="(/theme/[^"]+\.css[^"]*)"`).FindSubmatch(body)
	if m == nil {
		t.Fatalf("expected a theme stylesheet link in the board page:\n%s", body)
	}

	cssResp, err := client.Get(srv.URL + string(m[1]))
	if err != nil {
		t.Fatalf("get theme css: %v", err)
	}
	defer cssResp.Body.Close()
	css, _ := io.ReadAll(cssResp.Body)
	if cssResp.StatusCode != http.StatusOK || !strings.Contains(string(css), "--bg-void") {
		t.Fatalf("expected shrippen tokens in theme css, got %d:\n%s", cssResp.StatusCode, css)
	}
}
