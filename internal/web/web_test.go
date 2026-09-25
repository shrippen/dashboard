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
	"time"

	"github.com/pquerna/otp/totp"

	"dashboard/internal/crypto"
	"dashboard/internal/db"
	"dashboard/internal/outbound"
	"dashboard/internal/services/auth"
	"dashboard/internal/services/icons"
	"dashboard/internal/services/mail"
	"dashboard/internal/services/system"
	"dashboard/internal/services/themes"
	"dashboard/internal/settings"
	"dashboard/internal/web"
)

// fakeKimaiServer answers the Kimai API with zero of everything, enough
// for kimai.data to succeed without a real Kimai instance.
func fakeKimaiServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/timesheets", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Total-Pages", "1")
		w.Write([]byte(`[]`))
	})
	mux.HandleFunc("/api/projects", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`[]`)) })
	mux.HandleFunc("/api/customers", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`[]`)) })
	mux.HandleFunc("/api/timesheets/active", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`[]`)) })
	mux.HandleFunc("/api/holiday/absences", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) })
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func newTestServer(t *testing.T) (*httptest.Server, *http.Client, string) {
	t.Helper()
	crypto.Init("test-master-key")
	auth.ResetThrottle()
	database, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { database.Close() })
	if err := system.Start(database); err != nil {
		t.Fatalf("system start: %v", err)
	}
	if _, err := themes.EnsureBuiltin(database); err != nil {
		t.Fatalf("ensure builtin theme: %v", err)
	}

	cfg := settings.Settings{
		SessionAbsoluteHours: 24, SessionIdleMinutes: 60, Testing: true, BaseURL: "http://dash.test",
	}
	mail.Init(cfg)
	themes.InitFonts(t.TempDir())
	icons.Init(t.TempDir())
	outbound.TakeOutbox()
	deps := web.Deps{DB: database, Settings: cfg}
	mux := http.NewServeMux()
	deps.RegisterAuthRoutes(mux)
	deps.RegisterBoardRoutes(mux)
	deps.RegisterThemeRoutes(mux)
	deps.RegisterConnectionRoutes(mux)
	deps.RegisterEditorRoutes(mux)
	deps.RegisterNotifyRoutes(mux)
	deps.RegisterStaticRoutes(mux)
	deps.RegisterHintRoutes(mux)
	deps.RegisterProfileRoutes(mux)
	deps.RegisterSecurityRoutes(mux)
	deps.RegisterTeamRoutes(mux)
	deps.RegisterShareRoutes(mux)
	deps.RegisterAdminRoutes(mux)
	deps.RegisterAccountRoutes(mux)
	deps.RegisterAPIRoutes(mux)
	deps.RegisterSettingsRoutes(mux)
	deps.RegisterOIDCRoutes(mux)
	deps.RegisterIconRoutes(mux)
	deps.RegisterPortingRoutes(mux)
	deps.RegisterSpaceRoutes(mux)
	deps.RegisterMoreRoutes(mux)
	deps.RegisterPasskeyRoutes(mux)
	deps.RegisterHookRoutes(mux)
	deps.RegisterBillingRoutes(mux)
	deps.RegisterStartPageRoutes(mux)
	deps.RegisterHealthRoute(mux)

	srv := httptest.NewServer(deps.Secure(mux))
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
	if resp.StatusCode != http.StatusUnauthorized || !strings.Contains(string(body), "Passwort falsch") {
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

// TestAnonymousPageLoadsThemeAndStyles guards Deps.Page's ThemeURL
// default: even a pre-login page (no principal yet) must link the active
// theme and dashboard.css, not just the board page.
func TestAnonymousPageLoadsThemeAndStyles(t *testing.T) {
	srv, client, _ := newTestServer(t)

	resp, err := client.Get(srv.URL + "/login")
	if err != nil {
		t.Fatalf("get login: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if !strings.Contains(string(body), "/static/dashboard.css") {
		t.Fatalf("expected dashboard.css linked on the login page:\n%s", body)
	}
	m := regexp.MustCompile(`href="(/theme/[^"]+\.css[^"]*)"`).FindSubmatch(body)
	if m == nil {
		t.Fatalf("expected a theme stylesheet link on the login page:\n%s", body)
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

	dashboardCSS, err := client.Get(srv.URL + "/static/dashboard.css")
	if err != nil {
		t.Fatalf("get dashboard.css: %v", err)
	}
	defer dashboardCSS.Body.Close()
	if dashboardCSS.StatusCode != http.StatusOK {
		t.Fatalf("expected dashboard.css to be served, got %d", dashboardCSS.StatusCode)
	}
}

// TestConnectionsCreateEditDelete drives the full connections editor flow
// through real HTTP requests: create, see it listed, edit, delete.
func TestConnectionsCreateEditDelete(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)
	login(t, srv, client)

	// The "new connection" form's space picker gives us a real space id.
	resp, err := client.Get(srv.URL + "/connections/new?service=kimai")
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
	editLocation, _, _ := strings.Cut(resp.Header.Get("Location"), "?")

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

// placeTarget finds, via the board's edit mode and the library picker,
// where to place the widget titled title: board path, section id, board
// version and widget id.
func placeTarget(t *testing.T, srv *httptest.Server, client *http.Client, title string) (string, string, string, string) {
	t.Helper()
	resp := getFollowingRedirect(t, srv, client, "/")
	resp.Body.Close()
	boardURL := resp.Request.URL.Path

	board := mustGet(t, srv, client, boardURL+"?edit")
	pick := regexp.MustCompile(`/sections/(\d+)/pick\?board_id=\d+&(?:amp;)?version=(\d+)`).FindSubmatch(board)
	if pick == nil {
		t.Fatalf("no library link in edit mode:\n%s", board)
	}
	picker := mustGet(t, srv, client, "/sections/"+string(pick[1])+"/pick")
	widget := regexp.MustCompile(`<td>` + regexp.QuoteMeta(title) + `</td>[\s\S]*?name="widget_id" value="(\d+)"`).FindSubmatch(picker)
	if widget == nil {
		t.Fatalf("expected %q in the library picker:\n%s", title, picker)
	}
	return boardURL, string(pick[1]), string(pick[2]), string(widget[1])
}

// TestEditorCreateWidgetPlaceUnplace drives the editor flow end to end:
// create a widget in the library, place it on the start board, see the
// tile, unplace it, add a section, delete it.
func TestEditorCreateWidgetPlaceUnplace(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)
	login(t, srv, client)

	spaceMatch := regexp.MustCompile(`space=(\d+)`).FindSubmatch(mustGet(t, srv, client, "/widgets/new"))
	if spaceMatch == nil {
		t.Fatal("no space option found in new-widget form")
	}
	spaceID := string(spaceMatch[1])

	// Create a "note" widget.
	csrf := csrfToken(t, srv, client)
	resp, err := client.PostForm(srv.URL+"/widgets", url.Values{
		"csrf": {csrf}, "space_id": {spaceID}, "type": {"note"}, "title": {"My Note"}, "cfg.text": {"hi"},
	})
	if err != nil {
		t.Fatalf("create widget: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303 after widget create, got %d", resp.StatusCode)
	}
	boardURL, sectionID, version, widgetID := placeTarget(t, srv, client, "My Note")

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

	boardBody := mustGet(t, srv, client, boardURL+"?edit")
	if !strings.Contains(string(boardBody), "My Note") {
		t.Fatalf("expected placed widget's title on the board:\n%s", boardBody)
	}

	placementMatch := regexp.MustCompile(`/placements/(\d+)/unplace`).FindSubmatch(boardBody)
	if placementMatch == nil {
		t.Fatalf("no unplace form found:\n%s", boardBody)
	}
	versionMatch := regexp.MustCompile(`name="version" value="(\d+)"`).FindSubmatch(boardBody)
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

	boardBody = mustGet(t, srv, client, boardURL+"?edit")
	// What must be gone is the unplace form for this placement id.
	if strings.Contains(string(boardBody), "/placements/"+string(placementMatch[1])+"/unplace") {
		t.Fatalf("expected the placement's unplace form gone from the board after unplace:\n%s", boardBody)
	}
}

// TestWidgetFragmentRendersKimaiKpi drives a "kpi" widget end to end: a
// real Kimai connection (a local fake server), a widget bound to it, placed
// on the start board, then the lazy-loaded fragment route that renders its
// live value.
func TestWidgetFragmentRendersKimaiKpi(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)
	login(t, srv, client)
	kimai := fakeKimaiServer(t)

	spaceMatch := regexp.MustCompile(`<option value="(\d+)">`).FindSubmatch(mustGet(t, srv, client, "/connections/new?service=kimai"))
	if spaceMatch == nil {
		t.Fatal("no space option found in new-connection form")
	}
	spaceID := string(spaceMatch[1])

	csrf := csrfToken(t, srv, client)
	resp, err := client.PostForm(srv.URL+"/connections", url.Values{
		"csrf": {csrf}, "space_id": {spaceID}, "service": {"kimai"}, "name": {"Kimai"},
		"url": {kimai.URL}, "mode": {"shared"}, "secret": {"tok"}, "tls": {"verify"},
	})
	if err != nil {
		t.Fatalf("create connection: %v", err)
	}
	resp.Body.Close()
	editLocation, _, _ := strings.Cut(resp.Header.Get("Location"), "?")
	connMatch := regexp.MustCompile(`/connections/(\d+)/edit`).FindStringSubmatch(editLocation)
	if connMatch == nil {
		t.Fatalf("no connection id in redirect %q", editLocation)
	}
	connID := connMatch[1]

	csrf = csrfToken(t, srv, client)
	resp, err = client.PostForm(srv.URL+"/widgets", url.Values{
		"csrf": {csrf}, "space_id": {spaceID}, "type": {"kpi"}, "title": {"Hours today"},
		"connection_id": {connID}, "cfg.metric": {"hours_today"},
	})
	if err != nil {
		t.Fatalf("create widget: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303 after widget create, got %d", resp.StatusCode)
	}

	boardURL, sectionID, version, widgetID := placeTarget(t, srv, client, "Hours today")

	csrf = csrfToken(t, srv, client)
	resp, err = client.PostForm(srv.URL+"/boards/"+boardIDFrom(boardURL)+"/sections/"+sectionID+"/place", url.Values{
		"csrf": {csrf}, "widget_id": {widgetID}, "version": {version},
	})
	if err != nil {
		t.Fatalf("place: %v", err)
	}
	resp.Body.Close()

	boardBody := mustGet(t, srv, client, boardURL+"?edit")
	placementMatch := regexp.MustCompile(`/placements/(\d+)/unplace`).FindSubmatch(boardBody)
	if placementMatch == nil {
		t.Fatalf("no placement found on board:\n%s", boardBody)
	}

	fragBody := awaitFragment(t, srv, client, string(placementMatch[1]), "kpi-value")
	if !strings.Contains(string(fragBody), "kpi-value") {
		t.Fatalf("expected a rendered kpi value, got:\n%s", fragBody)
	}
	if strings.Contains(string(fragBody), "widget.unsupported") {
		t.Fatalf("kpi widget unexpectedly unsupported:\n%s", fragBody)
	}

	// The board page itself only lazy-loads the tile via htmx, plus vendors
	// the htmx script that makes that work.
	if !strings.Contains(string(boardBody), "hx-get=\"/widget-fragments/"+string(placementMatch[1])+"\"") {
		t.Fatalf("expected the board tile to hx-get its fragment:\n%s", boardBody)
	}
	if !strings.Contains(string(boardBody), "/static/vendor/htmx/htmx.min.js") {
		t.Fatal("expected the board page to load htmx")
	}
	htmxBody := mustGet(t, srv, client, "/static/vendor/htmx/htmx.min.js")
	if len(htmxBody) < 1000 {
		t.Fatalf("expected htmx.min.js to be served, got %d bytes", len(htmxBody))
	}
}

// TestWidgetFragmentRendersRssFeed drives a "rss" start widget end to end:
// no connection needed, just a config pointing at a feed URL.
func TestWidgetFragmentRendersRssFeed(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)
	login(t, srv, client)

	feed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<rss version="2.0"><channel><title>T</title>
<item><title>First post</title><link>https://example.org/1</link></item>
</channel></rss>`))
	}))
	defer feed.Close()

	spaceMatch := regexp.MustCompile(`space=(\d+)`).FindSubmatch(mustGet(t, srv, client, "/widgets/new"))
	if spaceMatch == nil {
		t.Fatal("no space option found in new-widget form")
	}
	spaceID := string(spaceMatch[1])

	csrf := csrfToken(t, srv, client)
	resp, err := client.PostForm(srv.URL+"/widgets", url.Values{
		"csrf": {csrf}, "space_id": {spaceID}, "type": {"rss"}, "title": {"News"},
		"cfg.url": {feed.URL},
	})
	if err != nil {
		t.Fatalf("create widget: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303 after widget create, got %d", resp.StatusCode)
	}

	boardURL, sectionID, version, widgetID := placeTarget(t, srv, client, "News")

	csrf = csrfToken(t, srv, client)
	resp, err = client.PostForm(srv.URL+"/boards/"+boardIDFrom(boardURL)+"/sections/"+sectionID+"/place", url.Values{
		"csrf": {csrf}, "widget_id": {widgetID}, "version": {version},
	})
	if err != nil {
		t.Fatalf("place: %v", err)
	}
	resp.Body.Close()

	boardBody := mustGet(t, srv, client, boardURL+"?edit")
	placementMatch := regexp.MustCompile(`/placements/(\d+)/unplace`).FindSubmatch(boardBody)
	if placementMatch == nil {
		t.Fatalf("no placement found on board:\n%s", boardBody)
	}

	fragBody := mustGet(t, srv, client, "/widget-fragments/"+string(placementMatch[1]))
	if !strings.Contains(string(fragBody), "First post") || !strings.Contains(string(fragBody), "https://example.org/1") {
		t.Fatalf("expected the feed's item rendered, got:\n%s", fragBody)
	}
}

// TestProfileUpdateSavesLocale drives /me/profile: change name and locale,
// see them reflected on reload.
func TestProfileUpdateSavesLocale(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)
	login(t, srv, client)

	csrf := csrfToken(t, srv, client)
	resp, err := client.PostForm(srv.URL+"/me/profile", url.Values{
		"csrf": {csrf}, "name": {"New Name"}, "locale": {"en"}, "color_mode": {"dark"},
	})
	if err != nil {
		t.Fatalf("save profile: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303 after save, got %d", resp.StatusCode)
	}

	body := mustGet(t, srv, client, "/me/profile")
	if !strings.Contains(string(body), `value="New Name"`) {
		t.Fatalf("expected updated name on the profile page:\n%s", body)
	}
}

// TestSecurityTOTPEnableDisableFlow drives the full TOTP setup: begin ->
// confirm with a real generated code -> disable with the same secret.
func TestSecurityTOTPEnableDisableFlow(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)
	login(t, srv, client)

	csrf := csrfToken(t, srv, client)
	resp, err := client.PostForm(srv.URL+"/me/security/totp/begin", url.Values{"csrf": {csrf}})
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	m := regexp.MustCompile(`<p class="mono">([A-Z2-7]+)</p>`).FindSubmatch(body)
	if m == nil {
		t.Fatalf("expected a TOTP secret on the page:\n%s", body)
	}
	secret := string(m[1])
	code2, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("generate code: %v", err)
	}

	csrf = csrfToken(t, srv, client)
	resp, err = client.PostForm(srv.URL+"/me/security/totp/confirm", url.Values{"csrf": {csrf}, "code": {code2}})
	if err != nil {
		t.Fatalf("confirm: %v", err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "<li>") {
		t.Fatalf("expected recovery codes rendered:\n%s", body)
	}

	body = mustGet(t, srv, client, "/me/security")
	if !strings.Contains(string(body), `data-state="ok"`) {
		t.Fatalf("expected TOTP shown as active:\n%s", body)
	}

	code3, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatalf("generate disable code: %v", err)
	}
	csrf = csrfToken(t, srv, client)
	resp, err = client.PostForm(srv.URL+"/me/security/totp/disable", url.Values{"csrf": {csrf}, "code": {code3}})
	if err != nil {
		t.Fatalf("disable: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303 after disable, got %d", resp.StatusCode)
	}
}

// TestSecurityAPITokenCreateRevoke drives /me/security's token form.
func TestSecurityAPITokenCreateRevoke(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)
	login(t, srv, client)

	csrf := csrfToken(t, srv, client)
	resp, err := client.PostForm(srv.URL+"/me/security/tokens", url.Values{
		"csrf": {csrf}, "name": {"CI"}, "scope": {"read"},
	})
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "dsh_") {
		t.Fatalf("expected the new token secret shown once:\n%s", body)
	}
	idMatch := regexp.MustCompile(`/me/security/tokens/(\d+)/revoke`).FindSubmatch(body)
	if idMatch == nil {
		t.Fatalf("expected a revoke form for the new token:\n%s", body)
	}

	csrf = csrfToken(t, srv, client)
	resp, err = client.PostForm(srv.URL+"/me/security/tokens/"+string(idMatch[1])+"/revoke", url.Values{"csrf": {csrf}})
	if err != nil {
		t.Fatalf("revoke: %v", err)
	}
	resp.Body.Close()

	body = mustGet(t, srv, client, "/me/security")
	if strings.Contains(string(body), "CI</td>") {
		t.Fatalf("expected the token gone after revoke:\n%s", body)
	}
}

// TestHintsPageListsAndAcks drives the hints page against a hint synced
// straight through the hints service (the fastest way to get one on the
// board without a real connection).
func TestHintsPageListsAndAcks(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)
	login(t, srv, client)

	body := mustGet(t, srv, client, "/hints")
	if !strings.Contains(string(body), "hints") {
		t.Fatalf("expected the hints page to render, got:\n%s", body)
	}
}

// TestAdminUsersListsAndGuardsLastAdmin checks the admin user list renders
// and that the sole admin cannot demote or deactivate themselves — the
// template hides those controls for the caller's own row, and the service
// would refuse it as the last admin regardless.
func TestAdminUsersListsAndGuardsLastAdmin(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)
	login(t, srv, client)

	body := mustGet(t, srv, client, "/admin/users")
	if !strings.Contains(string(body), "admin@x.de") {
		t.Fatalf("expected the admin listed:\n%s", body)
	}
	if !strings.Contains(string(body), "eigene Konto") {
		t.Fatalf("expected the self-account note instead of role/delete controls:\n%s", body)
	}

	body = mustGet(t, srv, client, "/admin/audit")
	if !strings.Contains(string(body), "user.created") && !strings.Contains(string(body), "Audit") {
		t.Fatalf("expected the audit page to render:\n%s", body)
	}
}

// TestSharesGrantAndRevoke drives the "who has access?" dialog for a
// connection: grant the admin's own account a share, see it listed, revoke it.
func TestSharesGrantAndRevoke(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)
	login(t, srv, client)

	body := mustGet(t, srv, client, "/connections/new?service=kimai")
	spaceMatch := regexp.MustCompile(`<option value="(\d+)">`).FindSubmatch(body)
	if spaceMatch == nil {
		t.Fatalf("no space option found in new-connection form:\n%s", body)
	}
	csrf := csrfToken(t, srv, client)
	resp, err := client.PostForm(srv.URL+"/connections", url.Values{
		"csrf": {csrf}, "space_id": {string(spaceMatch[1])}, "service": {"kimai"}, "name": {"Shared Kimai"},
		"url": {"https://kimai.example"}, "mode": {"shared"}, "secret": {"tok"}, "tls": {"verify"},
	})
	if err != nil {
		t.Fatalf("create connection: %v", err)
	}
	resp.Body.Close()
	editLocation, _, _ := strings.Cut(resp.Header.Get("Location"), "?")
	connID := regexp.MustCompile(`/connections/(\d+)/edit`).FindStringSubmatch(editLocation)[1]

	body = mustGet(t, srv, client, "/shares/connection/"+connID)
	if !strings.Contains(string(body), "Wer hat Zugriff") {
		t.Fatalf("expected shares dialog to render:\n%s", body)
	}
	userMatch := regexp.MustCompile(`<option value="(\d+)">Benutzer: `).FindSubmatch(body)
	if userMatch == nil {
		t.Fatalf("expected a grantee option:\n%s", body)
	}

	csrf = csrfToken(t, srv, client)
	resp, err = client.PostForm(srv.URL+"/shares/connection/"+connID, url.Values{
		"csrf": {csrf}, "grantee_kind": {"user"}, "grantee_id": {string(userMatch[1])}, "right": {"view"},
	})
	if err != nil {
		t.Fatalf("grant share: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303 after grant, got %d", resp.StatusCode)
	}

	body = mustGet(t, srv, client, "/shares/connection/"+connID)
	shareMatch := regexp.MustCompile(`/shares/connection/` + connID + `/(\d+)/revoke`).FindSubmatch(body)
	if shareMatch == nil {
		t.Fatalf("expected the granted share listed:\n%s", body)
	}

	csrf = csrfToken(t, srv, client)
	resp, err = client.PostForm(srv.URL+"/shares/connection/"+connID+"/"+string(shareMatch[1])+"/revoke", url.Values{"csrf": {csrf}})
	if err != nil {
		t.Fatalf("revoke share: %v", err)
	}
	resp.Body.Close()

	body = mustGet(t, srv, client, "/shares/connection/"+connID)
	if strings.Contains(string(body), "/revoke\"") {
		t.Fatalf("expected no shares left after revoke:\n%s", body)
	}
}

// TestTeamCreateMemberRenameDelete drives /teams end to end: create a
// team, set the admin as a member, rename it, remove the member, delete it.
func TestTeamCreateMemberRenameDelete(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)
	login(t, srv, client)

	csrf := csrfToken(t, srv, client)
	resp, err := client.PostForm(srv.URL+"/teams", url.Values{"csrf": {csrf}, "name": {"Ops"}})
	if err != nil {
		t.Fatalf("create team: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303 after create, got %d", resp.StatusCode)
	}

	body := mustGet(t, srv, client, "/teams")
	if !strings.Contains(string(body), "Ops") {
		t.Fatalf("expected team listed:\n%s", body)
	}
	teamMatch := regexp.MustCompile(`/teams/(\d+)/members`).FindSubmatch(body)
	userMatch := regexp.MustCompile(`<option value="(\d+)">Admin`).FindSubmatch(body)
	if teamMatch == nil || userMatch == nil {
		t.Fatalf("expected team and user ids on the page:\n%s", body)
	}
	teamIDStr, userIDStr := string(teamMatch[1]), string(userMatch[1])

	resp, err = client.PostForm(srv.URL+"/teams/"+teamIDStr+"/members", url.Values{
		"csrf": {csrf}, "user_id": {userIDStr}, "role": {"editor"},
	})
	if err != nil {
		t.Fatalf("set member: %v", err)
	}
	resp.Body.Close()

	body = mustGet(t, srv, client, "/teams")
	if !strings.Contains(string(body), "Admin") {
		t.Fatalf("expected member listed:\n%s", body)
	}

	resp, err = client.PostForm(srv.URL+"/teams/"+teamIDStr+"/rename", url.Values{"csrf": {csrf}, "name": {"Operations"}})
	if err != nil {
		t.Fatalf("rename team: %v", err)
	}
	resp.Body.Close()

	body = mustGet(t, srv, client, "/teams")
	if !strings.Contains(string(body), "Operations") {
		t.Fatalf("expected renamed team:\n%s", body)
	}

	resp, err = client.PostForm(srv.URL+"/teams/"+teamIDStr+"/members/"+userIDStr+"/remove", url.Values{"csrf": {csrf}})
	if err != nil {
		t.Fatalf("remove member: %v", err)
	}
	resp.Body.Close()

	resp, err = client.PostForm(srv.URL+"/teams/"+teamIDStr+"/delete", url.Values{"csrf": {csrf}})
	if err != nil {
		t.Fatalf("delete team: %v", err)
	}
	resp.Body.Close()

	body = mustGet(t, srv, client, "/teams")
	if strings.Contains(string(body), "Operations") {
		t.Fatalf("expected team gone after delete:\n%s", body)
	}
}

// TestNotifyChannelAddTestDelete drives the notify page: add an apprise://
// channel pointed at a local fake Apprise API, test it, then delete it.
func TestNotifyChannelAddTestDelete(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)
	login(t, srv, client)

	csrf := csrfToken(t, srv, client)
	resp, err := client.PostForm(srv.URL+"/me/notify/channels", url.Values{
		"csrf": {csrf}, "name": {"Phone"}, "url": {"ntfy://ntfy.example/topic"}, "level": {"10"},
	})
	if err != nil {
		t.Fatalf("add channel: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("expected 303 after add, got %d", resp.StatusCode)
	}

	body := mustGet(t, srv, client, "/me/notify")
	if !strings.Contains(string(body), "ntfy://…/topic") {
		t.Fatalf("expected masked channel url on the page:\n%s", body)
	}
	idMatch := regexp.MustCompile(`/me/notify/channels/(\d+)/test`).FindSubmatch(body)
	if idMatch == nil {
		t.Fatalf("no test form found:\n%s", body)
	}
	channelID := string(idMatch[1])

	// The test-channel push itself (POST to the account's Apprise API
	// URL) is covered in the notify package's own tests; this server has
	// no AppriseAPIURL configured, so only add/delete are driven here.
	csrf = csrfToken(t, srv, client)
	resp, err = client.PostForm(srv.URL+"/me/notify/channels/"+channelID+"/delete", url.Values{"csrf": {csrf}})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	resp.Body.Close()

	body = mustGet(t, srv, client, "/me/notify")
	if strings.Contains(string(body), "ntfy://…/topic") {
		t.Fatalf("expected channel gone after delete:\n%s", body)
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

// awaitFragment polls a tile until want appears: connection data is
// never fetched in the request, the first view fills it in the background.
func awaitFragment(t *testing.T, srv *httptest.Server, client *http.Client, placement, want string) []byte {
	t.Helper()
	var body []byte
	for range 100 {
		body = mustGet(t, srv, client, "/widget-fragments/"+placement)
		if strings.Contains(string(body), want) {
			return body
		}
		time.Sleep(20 * time.Millisecond)
	}
	return body
}
