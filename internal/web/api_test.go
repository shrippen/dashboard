package web_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// newAPIToken creates a token of scope via the security page and returns it.
func newAPIToken(t *testing.T, srv string, client *http.Client, csrf, scope string) string {
	t.Helper()
	resp, err := client.PostForm(srv+"/me/security/tokens", url.Values{"csrf": {csrf}, "name": {scope}, "scope": {scope}})
	if err != nil {
		t.Fatalf("create token: %v", err)
	}
	defer resp.Body.Close()
	match := regexp.MustCompile(`(dsh_[A-Za-z0-9_-]+)`).FindStringSubmatch(readAll(t, resp))
	if match == nil {
		t.Fatal("no token on page")
	}
	return match[1]
}

func TestTokenAPIAndCalendar(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)
	login(t, srv, client)
	csrf := csrfToken(t, srv, client)
	read := newAPIToken(t, srv.URL, client, csrf, "read")
	embed := newAPIToken(t, srv.URL, client, csrf, "embed")
	anon := freshClient(t)

	cases := []struct {
		path, token, want string
		status            int
	}{
		{"/api/summary", "", "", http.StatusUnauthorized},
		{"/api/summary", embed, "", http.StatusUnauthorized},
		{"/api/summary", read, `"hints":{`, http.StatusOK},
		{"/api/hints", read, `[]`, http.StatusOK},
		{"/calendar.ics", read, "BEGIN:VCALENDAR", http.StatusOK},
		{"/embed/hints", embed, `class="hints"`, http.StatusOK},
	}
	for _, c := range cases {
		target := srv.URL + c.path
		if c.token != "" {
			target += "?token=" + c.token
		}
		resp, err := anon.Get(target)
		if err != nil {
			t.Fatalf("get %s: %v", c.path, err)
		}
		body := readAll(t, resp)
		resp.Body.Close()
		if resp.StatusCode != c.status || !strings.Contains(body, c.want) {
			t.Fatalf("%s with %q: got %d, want %d containing %q:\n%s", c.path, c.token, resp.StatusCode, c.status, c.want, body)
		}
		if c.path == "/embed/hints" && strings.Contains(body, "app-nav") {
			t.Fatal("embed must not show the app nav")
		}
	}
}
