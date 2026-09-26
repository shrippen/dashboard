package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestBackToStaysOnSite(t *testing.T) {
	cases := []struct {
		back, referer, want string
	}{
		{"referer", "http://dash.lan/boards/2?view=compact", "/boards/2?view=compact"},
		{"referer", "http://evil.example/boards/2", "/hints"},
		{"referer", "http://dash.lan//evil.example/x", "/hints"},
		{"", "http://dash.lan/boards/2", "/hints"},
		{"referer", "", "/hints"},
	}
	for _, c := range cases {
		r, _ := http.NewRequest(http.MethodPost, "http://dash.lan/hints/1/snooze", strings.NewReader(url.Values{"back": {c.back}}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		r.Header.Set("Referer", c.referer)
		if got := backTo(r, "/hints"); got != c.want {
			t.Errorf("back=%q referer=%q: got %q, want %q", c.back, c.referer, got, c.want)
		}
	}
}
