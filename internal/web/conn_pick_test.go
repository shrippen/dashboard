package web_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

// TestServicePickSortedWithCounts: the service picker lists services by
// their shown name and badges how many connections of each exist.
func TestServicePickSortedWithCounts(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)
	login(t, srv, client)

	pick := string(mustGet(t, srv, client, "/connections/new"))
	names := regexp.MustCompile(`<a class="type-card"[^>]*><b>([^<]+)</b>`).FindAllStringSubmatch(pick, -1)
	if len(names) < 10 {
		t.Fatalf("picker lists %d services:\n%s", len(names), pick)
	}
	shown := make([]string, len(names))
	for i, m := range names {
		shown[i] = m[1]
	}
	sorted := append([]string(nil), shown...)
	collate.New(language.German, collate.IgnoreCase).SortStrings(sorted)
	if strings.Join(shown, "|") != strings.Join(sorted, "|") {
		t.Fatalf("not alphabetical:\n%v", shown)
	}

	form := string(mustGet(t, srv, client, "/connections/new?service=kimai"))
	space := regexp.MustCompile(`<option value="(\d+)">`).FindStringSubmatch(form)[1]
	noFollow := *client
	noFollow.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	postForm(t, &noFollow, srv.URL+"/connections", url.Values{"csrf": {csrfToken(t, srv, client)}, "space_id": {space}, "service": {"kimai"},
		"name": {"K"}, "url": {"http://127.0.0.1:1"}, "mode": {"shared"}, "secret": {"tok"}, "tls": {"verify"}})

	pick = string(mustGet(t, srv, client, "/connections/new"))
	if !regexp.MustCompile(`href="/connections/new\?service=kimai"><b>Kimai<span class="nav-count"[^>]*>1</span></b>`).MatchString(pick) {
		t.Fatalf("kimai card lacks its count:\n%s", pick)
	}
}
