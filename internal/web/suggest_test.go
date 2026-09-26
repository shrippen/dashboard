package web_test

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// TestSuggestLayout: the button sits in edit mode only, the preview lists
// the proposed sections, applying returns to edit mode with undo offered.
func TestSuggestLayout(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)
	login(t, srv, client)

	resp := getFollowingRedirect(t, srv, client, "/")
	resp.Body.Close()
	board := resp.Request.URL.Path // /boards/<id>

	if view := string(mustGet(t, srv, client, board)); strings.Contains(view, board+"/suggest") {
		t.Fatal("suggest button outside edit mode")
	}
	if edit := string(mustGet(t, srv, client, board+"?edit")); !strings.Contains(edit, `href="`+board+`/suggest"`) {
		t.Fatalf("no suggest button in edit mode:\n%s", edit)
	}

	preview := string(mustGet(t, srv, client, board+"/suggest"))
	if !strings.Contains(preview, "Überblick") || !strings.Contains(preview, `name="version"`) {
		t.Fatalf("preview lacks sections or the apply form:\n%s", preview)
	}
	version := strings.SplitN(strings.SplitN(preview, `name="version" value="`, 2)[1], `"`, 2)[0]

	noFollow := *client
	noFollow.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res := postForm(t, &noFollow, srv.URL+board+"/suggest", url.Values{"csrf": {csrfToken(t, srv, client)}, "version": {version}})
	if loc := res.Header.Get("Location"); loc != board+"?edit&undo" {
		t.Fatalf("apply: %d %q", res.StatusCode, loc)
	}
	if after := string(mustGet(t, srv, client, board+"?edit")); !strings.Contains(after, "Überblick") {
		t.Fatalf("board not replaced:\n%s", after)
	}
}
