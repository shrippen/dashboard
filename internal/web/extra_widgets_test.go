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
