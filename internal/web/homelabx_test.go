package web_test

import (
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// TestHomelabWidgetsAndPages: the new homelab widgets render with demo
// connections; timeline and provider report pages open.
func TestHomelabWidgetsAndPages(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)
	login(t, srv, client)
	space := string(regexp.MustCompile(`<option value="(\d+)">`).FindSubmatch(mustGet(t, srv, client, "/connections/new?service=kimai"))[1])

	conns := map[string]string{}
	for _, service := range []string{"pangolin", "domains", "immich"} {
		resp := postForm(t, client, srv.URL+"/connections", url.Values{"csrf": {csrfToken(t, srv, client)}, "space_id": {space},
			"service": {service}, "name": {service}, "url": {"demo://" + service}, "mode": {"shared"}, "tls": {"verify"}})
		conns[service] = regexp.MustCompile(`/connections/(\d+)/edit`).FindStringSubmatch(resp.Header.Get("Location"))[1]
	}

	cases := []struct{ widget, conn, table, want string }{
		{"table", "pangolin", "exposure", "photos.example.org"},
		{"table", "domains", "domain_chain", "example.org"},
		{"update_window", "", "", "v1.131.0 → v1.132.3"},
		{"storage_forecast", "", "", "Noch zu wenig Verlauf"},
	}
	for _, c := range cases {
		title := "H-" + c.widget + c.table
		form := url.Values{"csrf": {csrfToken(t, srv, client)}, "space_id": {space}, "type": {c.widget}, "title": {title}}
		if c.conn != "" {
			form.Set("connection_id", conns[c.conn])
		}
		if c.table != "" {
			form.Set("cfg.table", c.table)
		}
		postForm(t, client, srv.URL+"/widgets", form)
		boardURL, sectionID, version, widgetID := placeTarget(t, srv, client, title)
		postForm(t, client, srv.URL+"/boards/"+boardIDFrom(boardURL)+"/sections/"+sectionID+"/place", url.Values{
			"csrf": {csrfToken(t, srv, client)}, "widget_id": {widgetID}, "version": {version}})
		placement := string(regexp.MustCompile(`/placements/(\d+)/unplace`).FindSubmatch(mustGet(t, srv, client, boardURL+"?edit"))[1])
		frag := string(awaitFragment(t, srv, client, placement, c.want))
		if !strings.Contains(frag, c.want) {
			t.Errorf("%s %s: %q missing:\n%s", c.widget, c.table, c.want, frag)
		}
		version = regexp.MustCompile(`data-version="(\d+)"`).FindStringSubmatch(string(mustGet(t, srv, client, boardURL)))[1]
		postForm(t, client, srv.URL+"/placements/"+placement+"/unplace", url.Values{"csrf": {csrfToken(t, srv, client)}, "version": {version}})
	}

	if page := string(mustGet(t, srv, client, "/timeline")); !strings.Contains(page, "Zeitleiste") {
		t.Fatalf("timeline:\n%s", page)
	}
	if page := string(mustGet(t, srv, client, "/reports/isp")); !strings.Contains(page, "Internet gegen Vertrag") {
		t.Fatalf("isp:\n%s", page)
	}
}
