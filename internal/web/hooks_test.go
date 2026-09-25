package web_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// TestPGBackWebHook: the edit page shows a signed URL; events sent to it
// show up in the connection test; a wrong signature is rejected.
func TestPGBackWebHook(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)
	login(t, srv, client)

	space := regexp.MustCompile(`<option value="(\d+)">`).FindSubmatch(mustGet(t, srv, client, "/connections/new"))
	resp := postForm(t, client, srv.URL+"/connections", url.Values{
		"csrf": {csrfToken(t, srv, client)}, "space_id": {string(space[1])}, "service": {"pgbackweb"}, "name": {"PG"},
		"url": {"https://pgback.lan"}, "mode": {"shared"}, "tls": {"verify"},
	})
	edit := resp.Header.Get("Location")
	hook := regexp.MustCompile(`http://dash\.test(/hooks/\d+/[A-Za-z0-9_-]+)`).FindStringSubmatch(string(mustGet(t, srv, client, edit)))
	if hook == nil {
		t.Fatal("no hook URL on the edit page")
	}

	post := func(path, body string) int {
		r, err := http.Post(srv.URL+path, "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		r.Body.Close()
		return r.StatusCode
	}
	if got := post(hook[1], `{"event":"execution_success","name":"kimai"}`); got != http.StatusNoContent {
		t.Fatalf("hook: %d", got)
	}
	if got := post(hook[1]+"x", `{"event":"execution_failed","name":"kimai"}`); got != http.StatusNotFound {
		t.Fatalf("bad signature: %d", got)
	}

	id := regexp.MustCompile(`/connections/(\d+)/edit`).FindStringSubmatch(edit)[1]
	test := readAll(t, postForm2(t, client, srv.URL+"/connections/"+id+"/test", url.Values{"csrf": {csrfToken(t, srv, client)}}))
	if !strings.Contains(test, "✓") {
		t.Fatalf("connection test failed:\n%s", test)
	}
}

// postForm2 posts and keeps the body open.
func postForm2(t *testing.T, client *http.Client, target string, form url.Values) *http.Response {
	t.Helper()
	resp, err := client.PostForm(target, form)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}
