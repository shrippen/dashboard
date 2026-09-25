package web_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

// pngPixel is a 1×1 transparent PNG.
var pngPixel = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89" +
	"\x00\x00\x00\rIDATx\x9cc\xf8\x0f\x00\x00\x01\x01\x00\x05\x18\xd8N\x00\x00\x00\x00IEND\xaeB`\x82")

func TestImageWidgetInlinesPicture(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)
	login(t, srv, client)

	img := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write(pngPixel)
	}))
	defer img.Close()

	space := regexp.MustCompile(`space=(\d+)`).FindSubmatch(mustGet(t, srv, client, "/widgets/new"))
	if space == nil {
		t.Fatal("no space")
	}
	resp, err := client.PostForm(srv.URL+"/widget-preview", url.Values{
		"csrf": {csrfToken(t, srv, client)}, "type": {"image"}, "title": {"Cam"},
		"cfg.url": {img.URL}, "space_id": {string(space[1])},
	})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	body := readAll(t, resp)
	if !strings.Contains(body, `src="data:image/png;base64,`) {
		t.Fatalf("image not inlined:\n%s", body)
	}
}

// TestCustomAPIWidgetUsesSealedHeader: the header typed into the form
// reaches the API, but neither the form nor the export shows it.
func TestCustomAPIWidgetUsesSealedHeader(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)
	login(t, srv, client)
	csrf := csrfToken(t, srv, client)

	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Key") != "top-secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{"stats":{"users":42}}`))
	}))
	defer api.Close()

	space := string(regexp.MustCompile(`space=(\d+)`).FindSubmatch(mustGet(t, srv, client, "/widgets/new"))[1])
	postForm(t, client, srv.URL+"/widgets", url.Values{"csrf": {csrf}, "space_id": {space}, "type": {"custom_api"}, "title": {"Stats"},
		"cfg.url": {api.URL}, "cfg.fields": {"Users = stats.users"}, "cfg.headers": {"X-Key: top-secret"}})
	boardURL, section, version, widget := placeTarget(t, srv, client, "Stats")
	postForm(t, client, srv.URL+"/boards/"+boardIDFrom(boardURL)+"/sections/"+section+"/place", url.Values{"csrf": {csrf}, "widget_id": {widget}, "version": {version}})

	placement := regexp.MustCompile(`/placements/(\d+)/unplace`).FindSubmatch(mustGet(t, srv, client, boardURL+"?edit"))[1]
	if frag := string(awaitFragment(t, srv, client, string(placement), "42")); !strings.Contains(frag, "<dd>42</dd>") {
		t.Fatalf("fragment:\n%s", frag)
	}
	if strings.Contains(string(mustGet(t, srv, client, "/widgets/"+widget+"/edit")), "top-secret") {
		t.Fatal("header shown in the form")
	}
}
