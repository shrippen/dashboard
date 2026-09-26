package web_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// TestTestFromOverview: the overview and the edit page run the connection
// test without reloading; the answer is a short toast naming the
// connection, not a page.
func TestTestFromOverview(t *testing.T) {
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

	list := string(mustGet(t, srv, client, "/connections"))
	button := `hx-post="/connections/` + id + `/check" hx-target="#toast"`
	if !strings.Contains(list, button) {
		t.Fatalf("no test button in the overview:\n%s", list)
	}
	if edit := string(mustGet(t, srv, client, "/connections/"+id+"/edit")); !strings.Contains(edit, button) || !strings.Contains(edit, `id="toast"`) {
		t.Fatalf("edit page test button still reloads the page:\n%s", edit)
	}

	res, err := client.PostForm(srv.URL+"/connections/"+id+"/check", url.Values{"csrf": {csrf}})
	if err != nil {
		t.Fatal(err)
	}
	body := readBody(t, res)
	if res.StatusCode != http.StatusOK || !strings.Contains(body, "✗") || !strings.Contains(body, "K") || strings.Contains(body, "<html") {
		t.Fatalf("expected a short failed result, got %d:\n%s", res.StatusCode, body)
	}
}
