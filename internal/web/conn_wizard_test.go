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
