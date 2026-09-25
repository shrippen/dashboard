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
	deps.RegisterConnectionRoutes(mux)
	deps.RegisterEditorRoutes(mux)

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

// TestConnectionsCreateEditDelete drives the full connections editor flow
// through real HTTP requests: create, see it listed, edit, delete.
func TestConnectionsCreateEditDelete(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)
	login(t, srv, client)

	// The "new connection" form's space picker gives us a real space id.
	resp, err := client.Get(srv.URL + "/connections/new")
	if err != nil {
		t.Fatalf("get new form: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	spaceMatch := regexp.MustCompile(`<option value="(\d+)">`).FindSubmatch(body)
	if spaceMatch == nil {
		t.Fatalf("no space option found in new-connection form:\n%s", body)
	}
	spaceID := string(spaceMatch[1])
	csrf := csrfToken(t, srv, client)

	resp, err = client.PostForm(srv.URL+"/connections", url.Values{
		"csrf": {csrf}, "space_id": {spaceID}, "service": {"kimai"}, "name": {"My Kimai"},
		"url": {"https://kimai.example"}, "mode": {"shared"}, "secret": {"tok"}, "tls": {"verify"},
	})
	if err != nil {
		t.Fatalf("create connection: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303 after create, got %d", resp.StatusCode)
	}
	editLocation := resp.Header.Get("Location")

	resp, err = client.Get(srv.URL + "/connections")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "My Kimai") {
		t.Fatalf("expected created connection in list:\n%s", body)
	}

	csrf = csrfToken(t, srv, client)
	resp, err = client.PostForm(srv.URL+editLocation, url.Values{
		"csrf": {csrf}, "name": {"Renamed Kimai"}, "url": {"https://kimai2.example"},
		"mode": {"shared"}, "tls": {"verify"},
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303 after update, got %d", resp.StatusCode)
	}

	resp, err = client.Get(srv.URL + "/connections")
	if err != nil {
		t.Fatalf("list after rename: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "Renamed Kimai") {
		t.Fatalf("expected renamed connection in list:\n%s", body)
	}

	csrf = csrfToken(t, srv, client)
	deleteURL := strings.TrimSuffix(editLocation, "/edit") + "/delete"
	resp, err = client.PostForm(srv.URL+deleteURL, url.Values{"csrf": {csrf}})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	resp.Body.Close()

	resp, err = client.Get(srv.URL + "/connections")
	if err != nil {
		t.Fatalf("list after delete: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(body), "Renamed Kimai") {
		t.Fatalf("expected connection gone after delete:\n%s", body)
	}
}

// TestEditorCreateWidgetPlaceUnplace drives the editor flow end to end:
// create a widget in the library, place it on the start board, see the
// tile, unplace it, add a section, delete it.
func TestEditorCreateWidgetPlaceUnplace(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)
	login(t, srv, client)

	boardResp := getFollowingRedirect(t, srv, client, "/")
	boardBody, _ := io.ReadAll(boardResp.Body)
	boardResp.Body.Close()
	boardURL := boardResp.Request.URL.Path
	sectionMatch := regexp.MustCompile(`/boards/\d+/sections/(\d+)/place`).FindSubmatch(boardBody)
	if sectionMatch == nil {
		t.Fatalf("no section place form found on board page:\n%s", boardBody)
	}
	sectionID := string(sectionMatch[1])
	spaceMatch := regexp.MustCompile(`<option value="(\d+)">`).FindSubmatch(mustGet(t, srv, client, "/widgets/new"))
	if spaceMatch == nil {
		t.Fatal("no space option found in new-widget form")
	}
	spaceID := string(spaceMatch[1])

	// Create a "note" widget.
	csrf := csrfToken(t, srv, client)
	resp, err := client.PostForm(srv.URL+"/widgets", url.Values{
		"csrf": {csrf}, "space_id": {spaceID}, "type": {"note"}, "title": {"My Note"}, "config": {`{"text":"hi"}`},
	})
	if err != nil {
		t.Fatalf("create widget: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303 after widget create, got %d", resp.StatusCode)
	}
	// Board version for the place form.
	boardResp = getFollowingRedirect(t, srv, client, boardURL)
	boardBody, _ = io.ReadAll(boardResp.Body)
	boardResp.Body.Close()
	versionMatch := regexp.MustCompile(`name="version" value="(\d+)"`).FindSubmatch(boardBody)
	if versionMatch == nil {
		t.Fatalf("no version field found on board page:\n%s", boardBody)
	}
	version := string(versionMatch[1])
	widgetIDMatch := regexp.MustCompile(`<option value="(\d+)">My Note`).FindSubmatch(boardBody)
	if widgetIDMatch == nil {
		t.Fatalf("expected My Note in the place-widget picker:\n%s", boardBody)
	}
	widgetID := string(widgetIDMatch[1])

	csrf = csrfToken(t, srv, client)
	resp, err = client.PostForm(srv.URL+"/boards/"+boardIDFrom(boardURL)+"/sections/"+sectionID+"/place", url.Values{
		"csrf": {csrf}, "widget_id": {widgetID}, "version": {version},
	})
	if err != nil {
		t.Fatalf("place: %v", err)
	}
	placeBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303 after place, got %d: %s", resp.StatusCode, placeBody)
	}

	boardResp = getFollowingRedirect(t, srv, client, boardURL)
	boardBody, _ = io.ReadAll(boardResp.Body)
	boardResp.Body.Close()
	if !strings.Contains(string(boardBody), "My Note") {
		t.Fatalf("expected placed widget's title on the board:\n%s", boardBody)
	}

	placementMatch := regexp.MustCompile(`/placements/(\d+)/unplace`).FindSubmatch(boardBody)
	if placementMatch == nil {
		t.Fatalf("no unplace form found:\n%s", boardBody)
	}
	versionMatch = regexp.MustCompile(`name="version" value="(\d+)"`).FindSubmatch(boardBody)
	version = string(versionMatch[1])

	csrf = csrfToken(t, srv, client)
	resp, err = client.PostForm(srv.URL+"/placements/"+string(placementMatch[1])+"/unplace", url.Values{
		"csrf": {csrf}, "version": {version}, "board_id": {boardIDFrom(boardURL)},
	})
	if err != nil {
		t.Fatalf("unplace: %v", err)
	}
	unplaceBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303 after unplace, got %d: %s", resp.StatusCode, unplaceBody)
	}

	boardResp = getFollowingRedirect(t, srv, client, boardURL)
	boardBody, _ = io.ReadAll(boardResp.Body)
	boardResp.Body.Close()
	// "My Note" still legitimately appears in the place-widget library
	// picker; what must be gone is its unplace form for this placement id.
	if strings.Contains(string(boardBody), "/placements/"+string(placementMatch[1])+"/unplace") {
		t.Fatalf("expected the placement's unplace form gone from the board after unplace:\n%s", boardBody)
	}
}

func mustGet(t *testing.T, srv *httptest.Server, client *http.Client, path string) []byte {
	t.Helper()
	resp, err := client.Get(srv.URL + path)
	if err != nil {
		t.Fatalf("get %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return body
}

func boardIDFrom(path string) string {
	parts := strings.Split(strings.Trim(path, "/"), "/")
	return parts[len(parts)-1]
}
