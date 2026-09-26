package web_test

import (
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// createConnection sets up a shared connection through the form and
// returns its id.
func createConnection(t *testing.T, srvURL string, client *http.Client, csrf, space, service, target string) string {
	t.Helper()
	resp := postForm(t, client, srvURL+"/connections", url.Values{"csrf": {csrf}, "space_id": {space}, "service": {service},
		"name": {service}, "url": {target}, "mode": {"shared"}, "tls": {"verify"}})
	m := regexp.MustCompile(`/connections/(\d+)/edit`).FindStringSubmatch(resp.Header.Get("Location"))
	if m == nil {
		t.Fatalf("create %s: %s", service, resp.Header.Get("Location"))
	}
	return m[1]
}

// TestSignInNextToToken: services with a sign-in offer it next to the
// token field; starting it leaves for the service; a callback for an
// unknown flow comes back with an error; Gitea asks for its OAuth client
// before it offers the button.
func TestSignInNextToToken(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)
	login(t, srv, client)
	csrf := csrfToken(t, srv, client)
	form := string(mustGet(t, srv, client, "/connections/new?service=homeassistant"))
	space := regexp.MustCompile(`<option value="(\d+)">`).FindStringSubmatch(form)[1]

	noFollow := *client
	noFollow.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	ha := createConnection(t, srv.URL, &noFollow, csrf, space, "homeassistant", "https://ha.example")
	edit := string(mustGet(t, srv, client, "/connections/"+ha+"/edit"))
	if !strings.Contains(edit, `id="connect-form"`) || !strings.Contains(edit, `hx-boost="false"`) || !strings.Contains(edit, `name="secret"`) {
		t.Fatalf("edit page lacks token and sign-in side by side:\n%s", edit)
	}
	resp := postForm(t, &noFollow, srv.URL+"/connections/"+ha+"/connect", url.Values{"csrf": {csrf}})
	if loc := resp.Header.Get("Location"); !strings.HasPrefix(loc, "https://ha.example/auth/authorize?") {
		t.Fatalf("start did not leave for Home Assistant: %d %q", resp.StatusCode, loc)
	}

	resp, err := noFollow.Get(srv.URL + "/connections/connect/callback?state=forged&code=x")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if loc := resp.Header.Get("Location"); !strings.Contains(loc, "error=connect.flow") {
		t.Fatalf("forged callback: %q", loc)
	}

	gitea := createConnection(t, srv.URL, &noFollow, csrf, space, "gitea", "https://git.example")
	edit = string(mustGet(t, srv, client, "/connections/"+gitea+"/edit"))
	if !strings.Contains(edit, `name="client_id"`) || strings.Contains(edit, `form="connect-form"`) {
		t.Fatalf("gitea should ask for its client first:\n%s", edit)
	}
	postForm(t, &noFollow, srv.URL+"/connections/"+gitea+"/oauth-client", url.Values{"csrf": {csrf}, "client_id": {"cid"}, "client_secret": {"cs"}})
	edit = string(mustGet(t, srv, client, "/connections/"+gitea+"/edit"))
	if !strings.Contains(edit, `form="connect-form"`) {
		t.Fatalf("gitea sign-in not offered after registering the client:\n%s", edit)
	}
}
