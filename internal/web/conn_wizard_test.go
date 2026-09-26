package web_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// TestConnectionWizard: pick a service, create it, land on the edit page
// with a test result and matching widgets; expiry and budget are stored,
// bad dates refused.
func TestConnectionWizard(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)
	login(t, srv, client)

	pick := string(mustGet(t, srv, client, "/connections/new"))
	if !strings.Contains(pick, `href="/connections/new?service=gitea"`) {
		t.Fatalf("no service picker:\n%s", pick)
	}
	form := string(mustGet(t, srv, client, "/connections/new?service=kimai"))
	if !strings.Contains(form, `name="service" value="kimai"`) {
		t.Fatalf("service not fixed:\n%s", form)
	}
	space := regexp.MustCompile(`<option value="(\d+)">`).FindStringSubmatch(form)[1]
	csrf := csrfToken(t, srv, client)

	noFollow := *client
	noFollow.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp := postForm(t, &noFollow, srv.URL+"/connections", url.Values{"csrf": {csrf}, "space_id": {space}, "service": {"kimai"},
		"name": {"K"}, "url": {"http://127.0.0.1:1"}, "mode": {"shared"}, "secret": {"tok"}, "tls": {"verify"}})
	welcome := resp.Header.Get("Location")
	if !strings.HasSuffix(welcome, "/edit?welcome") {
		t.Fatalf("redirect: %q", welcome)
	}
	page := string(mustGet(t, srv, client, welcome))
	if !strings.Contains(page, "✗") || !strings.Contains(page, `href="/widgets/new?type=kimai_`) {
		t.Fatalf("welcome page lacks test or widgets:\n%s", page)
	}

	base := strings.TrimSuffix(welcome, "/edit?welcome")
	postForm(t, client, srv.URL+base+"/hygiene", url.Values{"csrf": {csrf}, "expires": {"2027-01-31"}, "budget": {"50"}})
	page = string(mustGet(t, srv, client, base+"/edit"))
	if !strings.Contains(page, `value="2027-01-31"`) || !strings.Contains(page, `name="budget" type="number" min="0" value="50"`) {
		t.Fatalf("hygiene not stored:\n%s", page)
	}
	resp = postForm(t, &noFollow, srv.URL+base+"/hygiene", url.Values{"csrf": {csrf}, "expires": {"31.01.2027"}})
	if !strings.Contains(resp.Header.Get("Location"), "error=connection.bad_date") {
		t.Fatalf("bad date accepted: %v", resp.Header)
	}
}

// TestConnectionTwoPartCredential: a service whose secret is "a:b" (e.g.
// FreshRSS) gets two labeled fields instead of one the user must format
// themselves; they're joined server-side into the stored secret.
func TestConnectionTwoPartCredential(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)
	login(t, srv, client)

	form := string(mustGet(t, srv, client, "/connections/new?service=freshrss"))
	if !strings.Contains(form, `name="secret_a"`) || !strings.Contains(form, `name="secret_b"`) || strings.Contains(form, `name="secret"`) {
		t.Fatalf("expected two-part credential fields, not one:\n%s", form)
	}
	space := regexp.MustCompile(`<option value="(\d+)">`).FindStringSubmatch(form)[1]
	csrf := csrfToken(t, srv, client)

	resp := postForm(t, client, srv.URL+"/connections", url.Values{"csrf": {csrf}, "space_id": {space}, "service": {"freshrss"},
		"name": {"F"}, "url": {"http://127.0.0.1:1"}, "mode": {"shared"}, "secret_a": {"bob"}, "secret_b": {"pw123"}, "tls": {"verify"}})
	welcome := resp.Header.Get("Location")
	base := strings.TrimSuffix(welcome, "/edit?welcome")

	page := string(mustGet(t, srv, client, base+"/edit"))
	if strings.Count(page, "(unverändert lassen)") != 2 {
		t.Fatalf("expected both credential fields to show the stored placeholder:\n%s", page)
	}

	// A key:secret credential (e.g. Komodo) gets its own pair of fields too.
	form = string(mustGet(t, srv, client, "/connections/new?service=komodo"))
	if !strings.Contains(form, `name="secret_a"`) || !strings.Contains(form, `name="secret_b"`) || strings.Contains(form, `name="secret"`) {
		t.Fatalf("expected key:secret fields for komodo:\n%s", form)
	}
}

// TestNoTokenFieldWithoutAuth: services that need no credentials (e.g.
// Scrutiny) show neither a token field nor the shared/personal choice.
func TestNoTokenFieldWithoutAuth(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)
	login(t, srv, client)

	form := string(mustGet(t, srv, client, "/connections/new?service=scrutiny"))
	for _, field := range []string{`name="secret"`, `name="mode"`} {
		if strings.Contains(form, field) {
			t.Fatalf("scrutiny form offers %s:\n%s", field, form)
		}
	}
	kimai := string(mustGet(t, srv, client, "/connections/new?service=kimai"))
	if !strings.Contains(kimai, "Wem die Verbindung gehört") {
		t.Fatalf("space choice is not explained:\n%s", kimai)
	}
	// New connections start with personal credentials.
	if !regexp.MustCompile(`<option value="personal"\s+selected>`).MatchString(kimai) {
		t.Fatalf("personal is not the default:\n%s", kimai)
	}
	if !strings.Contains(kimai, `name="secret"`) {
		t.Fatal("kimai form lost its token field")
	}
	// Where to create the token is said while setting up, not only when editing.
	if !strings.Contains(kimai, "API-Token (Profil → API-Zugang)") {
		t.Fatalf("kimai setup lacks the token hint:\n%s", kimai)
	}
}

// TestPangolinOrgField: Pangolin's required organisation ID is a field of
// the setup form and lands in the connection's options.
func TestPangolinOrgField(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)
	login(t, srv, client)

	form := string(mustGet(t, srv, client, "/connections/new?service=pangolin"))
	if !strings.Contains(form, `name="opt_org"`) {
		t.Fatalf("no organisation field:\n%s", form)
	}
	space := regexp.MustCompile(`<option value="(\d+)">`).FindStringSubmatch(form)[1]
	noFollow := *client
	noFollow.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp := postForm(t, &noFollow, srv.URL+"/connections", url.Values{"csrf": {csrfToken(t, srv, client)}, "space_id": {space},
		"service": {"pangolin"}, "name": {"P"}, "url": {"https://api.example.org/v1"}, "mode": {"shared"}, "secret": {"k"},
		"tls": {"verify"}, "opt_org": {"home"}})
	edit := string(mustGet(t, srv, client, resp.Header.Get("Location")))
	if !strings.Contains(edit, `name="opt_org" value="home"`) {
		t.Fatalf("organisation not stored:\n%s", edit)
	}
}

// TestSetupScreensDescribeService: every setup screen says what the
// service is, and hosted projects link their website.
func TestSetupScreensDescribeService(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)
	login(t, srv, client)

	pick := string(mustGet(t, srv, client, "/connections/new"))
	for _, m := range regexp.MustCompile(`href="/connections/new\?service=([a-z]+)"`).FindAllStringSubmatch(pick, -1) {
		form := string(mustGet(t, srv, client, "/connections/new?service="+m[1]))
		if strings.Contains(form, "conn.what_") {
			t.Errorf("%s: no description", m[1])
		}
	}
	kimai := string(mustGet(t, srv, client, "/connections/new?service=kimai"))
	if !strings.Contains(kimai, `href="https://www.kimai.org"`) {
		t.Fatalf("kimai lacks its project link:\n%s", kimai)
	}
}

// TestDWDPlaceSearch: the DWD setup form offers a place search whose pick
// fills hidden coordinates, stored as numbers.
func TestDWDPlaceSearch(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)
	login(t, srv, client)

	form := string(mustGet(t, srv, client, "/connections/new?service=dwd"))
	for _, want := range []string{`hx-get="/places"`, `name="opt_lat"`, `name="opt_lon"`, `name="opt_place"`} {
		if !strings.Contains(form, want) {
			t.Fatalf("dwd form lacks %s:\n%s", want, form)
		}
	}
	space := regexp.MustCompile(`<option value="(\d+)">`).FindStringSubmatch(form)[1]
	noFollow := *client
	noFollow.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp := postForm(t, &noFollow, srv.URL+"/connections", url.Values{"csrf": {csrfToken(t, srv, client)}, "space_id": {space},
		"service": {"dwd"}, "name": {"Wetter"}, "url": {"https://api.brightsky.dev"}, "tls": {"verify"},
		"opt_place": {"Weimar, Thüringen, Deutschland"}, "opt_lat": {"50.9803"}, "opt_lon": {"11.32903"}})
	edit := string(mustGet(t, srv, client, resp.Header.Get("Location")))
	if !strings.Contains(edit, "lat: 50.9803") || !strings.Contains(edit, `value="Weimar, Thüringen, Deutschland"`) {
		t.Fatalf("place not stored as numbers:\n%s", edit)
	}
}

// TestPlaceSearchReused: the tile editor for weather and the Tibber setup
// form search places like the DWD form.
func TestPlaceSearchReused(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)
	login(t, srv, client)

	for _, path := range []string{"/widgets/new?type=weather", "/connections/new?service=tibber"} {
		if page := string(mustGet(t, srv, client, path)); !strings.Contains(page, `hx-get="/places"`) {
			t.Errorf("%s has no place search", path)
		}
	}
}

// TestSpeedtestKind: the speedtest setup form asks which tool measures.
func TestSpeedtestKind(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)
	login(t, srv, client)

	form := string(mustGet(t, srv, client, "/connections/new?service=speedtest"))
	if !strings.Contains(form, `<select id="opt_kind" name="opt_kind"`) || !strings.Contains(form, `value="myspeed"`) {
		t.Fatalf("no choice between Speedtest Tracker and MySpeed:\n%s", form)
	}
}
