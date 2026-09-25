package web_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestKimaiTimerStops: the tile shows the running timer and its stop
// button patches Kimai.
func TestKimaiTimerStops(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)
	login(t, srv, client)

	var mu sync.Mutex
	var writes []string
	begin := time.Now().Add(-time.Hour).Format(time.RFC3339)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/timesheets/active", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"id": 77, "begin": "` + begin + `", "project": {"id": 3, "name": "Relaunch", "customer": {"name": "Acme"}}, "activity": {"id": 7, "name": "Dev"}}]`))
	})
	mux.HandleFunc("GET /api/timesheets/recent", func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`[]`)) })
	mux.HandleFunc("GET /api/timesheets", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Total-Pages", "1")
		w.Write([]byte(`[{"duration": 3600}]`))
	})
	mux.HandleFunc("PATCH /api/timesheets/{id}/stop", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		writes = append(writes, r.URL.Path)
		mu.Unlock()
		w.Write([]byte(`{}`))
	})
	kimai := httptest.NewServer(mux)
	defer kimai.Close()

	space := string(regexp.MustCompile(`<option value="(\d+)">`).FindSubmatch(mustGet(t, srv, client, "/connections/new"))[1])
	csrf := csrfToken(t, srv, client)
	resp := postForm(t, client, srv.URL+"/connections", url.Values{"csrf": {csrf}, "space_id": {space}, "service": {"kimai"}, "name": {"Kimai"},
		"url": {kimai.URL}, "mode": {"shared"}, "secret": {"tok"}, "tls": {"verify"}})
	connID := regexp.MustCompile(`/connections/(\d+)/edit`).FindStringSubmatch(resp.Header.Get("Location"))[1]

	postForm(t, client, srv.URL+"/widgets", url.Values{"csrf": {csrf}, "space_id": {space}, "type": {"kimai_timer"}, "title": {"Timer"}, "connection_id": {connID}})
	boardURL, section, version, widget := placeTarget(t, srv, client, "Timer")
	postForm(t, client, srv.URL+"/boards/"+boardIDFrom(boardURL)+"/sections/"+section+"/place", url.Values{"csrf": {csrf}, "widget_id": {widget}, "version": {version}})
	placement := string(regexp.MustCompile(`/placements/(\d+)/unplace`).FindSubmatch(mustGet(t, srv, client, boardURL+"?edit"))[1])

	frag := string(awaitFragment(t, srv, client, placement, "Relaunch"))
	if !strings.Contains(frag, `name="sheet" value="77"`) || !strings.Contains(frag, "2:00 h") {
		t.Fatalf("fragment:\n%s", frag)
	}

	resp = postForm(t, client, srv.URL+"/widget-fragments/"+placement+"/kimai", url.Values{"csrf": {csrf}, "action": {"stop"}, "sheet": {"77"}})
	if resp.StatusCode != http.StatusOK || len(writes) != 1 || writes[0] != "/api/timesheets/77/stop" {
		t.Fatalf("stop: %d %v", resp.StatusCode, writes)
	}
	if r := postForm(t, client, srv.URL+"/widget-fragments/"+placement+"/kimai", url.Values{"csrf": {csrf}, "action": {"start"}}); r.StatusCode != http.StatusForbidden {
		t.Fatalf("start without ids: %d", r.StatusCode)
	}
}
