package web_test

import (
	"strings"
	"testing"
)

// TestNavCurrent: the menu marks the page you are on, including pages
// below a menu entry, and nothing else.
func TestNavCurrent(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)
	login(t, srv, client)

	cases := []struct{ path, cur, other string }{
		{"/hints", `href="/hints" aria-current="page"`, `href="/billing" aria-current`},
		{"/connections/new", `href="/connections" aria-current="page"`, `href="/hints" aria-current`},
	}
	for _, c := range cases {
		page := string(mustGet(t, srv, client, c.path))
		if !strings.Contains(page, c.cur) {
			t.Fatalf("%s: lacks %q", c.path, c.cur)
		}
		if strings.Contains(page, c.other) {
			t.Fatalf("%s: also marks %q", c.path, c.other)
		}
	}
}
