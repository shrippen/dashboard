package web_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
)

// fakeHass answers /api/states and records service calls.
func fakeHass(t *testing.T) (*httptest.Server, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var calls []string
	on := false
	mux := http.NewServeMux()
	mux.HandleFunc("/api/states", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		state := "off"
		if on {
			state = "on"
		}
		mu.Unlock()
		json.NewEncoder(w).Encode([]any{
			map[string]any{"entity_id": "light.desk", "state": state, "attributes": map[string]any{"friendly_name": "Desk"}},
			map[string]any{"entity_id": "switch.heater", "state": "off", "attributes": map[string]any{"friendly_name": "Heater"}},
		})
	})
	mux.HandleFunc("/api/services/", func(w http.ResponseWriter, r *http.Request) {
		var body map[string]string
		json.NewDecoder(r.Body).Decode(&body)
		mu.Lock()
		calls = append(calls, r.URL.Path+" "+body["entity_id"])
		on = !on
		mu.Unlock()
		w.Write([]byte(`[]`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string(nil), calls...) }
}

// TestHassToggle: a listed switchable entity toggles and the tile comes
// back refreshed; an entity not listed in the widget is refused.
func TestHassToggle(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)
	login(t, srv, client)
	ha, calls := fakeHass(t)

	space := regexp.MustCompile(`<option value="(\d+)">`).FindSubmatch(mustGet(t, srv, client, "/connections/new"))
	resp := postForm(t, client, srv.URL+"/connections", url.Values{
		"csrf": {csrfToken(t, srv, client)}, "space_id": {string(space[1])}, "service": {"homeassistant"}, "name": {"HA"},
		"url": {ha.URL}, "mode": {"shared"}, "secret": {"tok"}, "tls": {"verify"},
	})
	connID := regexp.MustCompile(`/connections/(\d+)/edit`).FindStringSubmatch(resp.Header.Get("Location"))[1]

	resp = postForm(t, client, srv.URL+"/widgets", url.Values{
		"csrf": {csrfToken(t, srv, client)}, "space_id": {string(space[1])}, "type": {"hass"}, "title": {"Home"},
		"connection_id": {connID}, "cfg.entities": {"light.desk"},
	})
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("create widget: %d", resp.StatusCode)
	}
	boardURL, sectionID, version, widgetID := placeTarget(t, srv, client, "Home")
	postForm(t, client, srv.URL+"/boards/"+boardIDFrom(boardURL)+"/sections/"+sectionID+"/place", url.Values{
		"csrf": {csrfToken(t, srv, client)}, "widget_id": {widgetID}, "version": {version},
	})
	placement := string(regexp.MustCompile(`/placements/(\d+)/unplace`).FindSubmatch(mustGet(t, srv, client, boardURL+"?edit"))[1])

	frag := string(mustGet(t, srv, client, "/widget-fragments/"+placement))
	if !strings.Contains(frag, `aria-pressed="false"`) || !strings.Contains(frag, "/widget-fragments/"+placement+"/toggle") {
		t.Fatalf("no toggle rendered:\n%s", frag)
	}

	toggle := func(entity string) *http.Response {
		r, err := client.PostForm(srv.URL+"/widget-fragments/"+placement+"/toggle", url.Values{
			"csrf": {csrfToken(t, srv, client)}, "entity": {entity},
		})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	r := toggle("light.desk")
	body := readAll(t, r)
	if r.StatusCode != http.StatusOK || !strings.Contains(body, `aria-pressed="true"`) {
		t.Fatalf("toggle: %d\n%s", r.StatusCode, body)
	}
	if got := calls(); len(got) != 1 || got[0] != "/api/services/light/toggle light.desk" {
		t.Fatalf("calls: %v", got)
	}

	if r := toggle("switch.heater"); r.StatusCode != http.StatusForbidden {
		t.Fatalf("unlisted entity: %d", r.StatusCode)
	}
	if got := calls(); len(got) != 1 {
		t.Fatalf("unlisted entity reached Home Assistant: %v", got)
	}
}
