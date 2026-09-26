package web_test

import (
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// TestIntegrationWidgetsRender: each new integration widget renders its
// demo connection's data.
func TestIntegrationWidgetsRender(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)
	login(t, srv, client)
	space := string(regexp.MustCompile(`<option value="(\d+)">`).FindSubmatch(mustGet(t, srv, client, "/connections/new?service=kimai"))[1])

	cases := []struct{ service, widget, want string }{
		{"tailscale", "tailscale", "laptop"},
		{"mediaserver", "mediaserver", "Arrival"},
		{"arr", "arr_upcoming", "Severance 2x09"},
		{"grocy", "grocy", "Joghurt"},
		{"dwd", "dwd", "STURMBÖEN"},
		{"github", "github", "shrippen/dotfiles"},
		{"speedtest", "speedtest", "243"},
		{"tibber", "energy", "trend-line"},
	}
	for _, c := range cases {
		resp := postForm(t, client, srv.URL+"/connections", url.Values{
			"csrf": {csrfToken(t, srv, client)}, "space_id": {space}, "service": {c.service}, "name": {c.service},
			"url": {"demo://" + c.service}, "mode": {"shared"}, "tls": {"verify"},
		})
		conn := regexp.MustCompile(`/connections/(\d+)/edit`).FindStringSubmatch(resp.Header.Get("Location"))
		if conn == nil {
			t.Fatalf("%s: no connection", c.service)
		}
		title := "W-" + c.widget
		postForm(t, client, srv.URL+"/widgets", url.Values{
			"csrf": {csrfToken(t, srv, client)}, "space_id": {space}, "type": {c.widget}, "title": {title}, "connection_id": {conn[1]},
		})
		boardURL, sectionID, version, widgetID := placeTarget(t, srv, client, title)
		postForm(t, client, srv.URL+"/boards/"+boardIDFrom(boardURL)+"/sections/"+sectionID+"/place", url.Values{
			"csrf": {csrfToken(t, srv, client)}, "widget_id": {widgetID}, "version": {version},
		})
		placement := string(regexp.MustCompile(`/placements/(\d+)/unplace`).FindSubmatch(mustGet(t, srv, client, boardURL+"?edit"))[1])
		frag := string(awaitFragment(t, srv, client, placement, c.want))
		if !strings.Contains(frag, c.want) {
			t.Errorf("%s: %q missing:\n%s", c.widget, c.want, frag)
		}
		version = regexp.MustCompile(`data-version="(\d+)"`).FindStringSubmatch(string(mustGet(t, srv, client, boardURL)))[1]
		postForm(t, client, srv.URL+"/placements/"+placement+"/unplace", url.Values{"csrf": {csrfToken(t, srv, client)}, "version": {version}})
	}
}

// TestBackupNow: an admin starts a backup; the settings page shows the
// verified copy.
func TestBackupNow(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)
	login(t, srv, client)

	postForm(t, client, srv.URL+"/admin/settings/backup", url.Values{"csrf": {csrfToken(t, srv, client)}})
	page := string(mustGet(t, srv, client, "/admin/settings"))
	if !strings.Contains(page, `data-state="ok"`) || !regexp.MustCompile(`andon-\d{8}-\d{6}\.db`).MatchString(page) {
		t.Fatalf("no verified backup:\n%s", page)
	}
}
