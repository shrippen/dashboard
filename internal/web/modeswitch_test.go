package web_test

import (
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// TestModeSwitchAsksFirst: switching a connection between shared and
// personal credentials shows what happens and changes nothing until
// confirmed.
func TestModeSwitchAsksFirst(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)
	login(t, srv, client)
	csrf := csrfToken(t, srv, client)
	form := string(mustGet(t, srv, client, "/connections/new?service=kimai"))
	space := regexp.MustCompile(`<option value="(\d+)">`).FindStringSubmatch(form)[1]

	noFollow := *client
	noFollow.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp := postForm(t, &noFollow, srv.URL+"/connections", url.Values{"csrf": {csrf}, "space_id": {space}, "service": {"kimai"},
		"name": {"K"}, "url": {"http://127.0.0.1:1"}, "mode": {"shared"}, "secret": {"tok"}, "tls": {"verify"}})
	id := regexp.MustCompile(`/connections/(\d+)/edit`).FindStringSubmatch(resp.Header.Get("Location"))[1]
	edit := srv.URL + "/connections/" + id + "/edit"

	change := url.Values{"csrf": {csrf}, "name": {"K"}, "url": {"http://127.0.0.1:1"}, "mode": {"personal"}, "tls": {"verify"}}
	resp, err := noFollow.PostForm(edit, change)
	if err != nil {
		t.Fatal(err)
	}
	body := readBody(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, `name="mode_confirmed"`) {
		t.Fatalf("expected the hint screen, got %d:\n%s", resp.StatusCode, body)
	}
	if page := string(mustGet(t, srv, client, "/connections/"+id+"/edit")); !strings.Contains(page, `value="shared" selected`) {
		t.Fatal("mode changed before confirming")
	}

	change.Set("mode_confirmed", "1")
	resp = postForm(t, &noFollow, edit, change)
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("confirmed switch: %d", resp.StatusCode)
	}
	if page := string(mustGet(t, srv, client, "/connections/"+id+"/edit")); !strings.Contains(page, `value="personal" selected`) {
		t.Fatal("mode not changed after confirming")
	}
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
