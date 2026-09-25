package web_test

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"regexp"
	"strings"
	"testing"
)

func uploadIcon(t *testing.T, client *http.Client, srv, csrf string) string {
	t.Helper()
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	_ = w.WriteField("csrf", csrf)
	h := textproto.MIMEHeader{}
	h.Set("Content-Disposition", `form-data; name="file"; filename="i.svg"`)
	h.Set("Content-Type", "image/svg+xml")
	part, _ := w.CreatePart(h)
	_, _ = part.Write([]byte(`<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"><script>x</script><rect/></svg>`))
	_ = w.Close()
	resp, err := client.Post(srv+"/icons/upload", w.FormDataContentType(), &body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	spec := readAll(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(spec, "upload:") {
		t.Fatalf("icon upload: %d %q", resp.StatusCode, spec)
	}
	return spec
}

// TestLinkTileAndLayout: a link tile renders inline with its icon,
// hotkey and search text; fold, hide, arrange and history work.
func TestLinkTileAndLayout(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)
	login(t, srv, client)
	csrf := csrfToken(t, srv, client)

	spec := uploadIcon(t, client, srv.URL, csrf)
	icon := mustGet(t, srv, client, "/icons/"+strings.TrimPrefix(spec, "upload:"))
	if bytes.Contains(icon, []byte("script")) || bytes.Contains(icon, []byte("onload")) {
		t.Fatalf("uploaded SVG not cleaned: %s", icon)
	}

	space := regexp.MustCompile(`<option value="(\d+)">`).FindSubmatch(mustGet(t, srv, client, "/widgets/new"))[1]
	for _, w := range []struct{ title, config string }{
		{"Kimai", `{"url":"https://kimai.example","icon":"` + spec + `","hotkey":"k","status":"off"}`},
		{"Wiki Docs", `{"url":"https://wiki.example","status":"off"}`},
	} {
		postForm(t, client, srv.URL+"/widgets", url.Values{"csrf": {csrf}, "space_id": {string(space)}, "type": {"link"}, "title": {w.title}, "config": {w.config}})
		boardURL, section, version, widget := placeTarget(t, srv, client, w.title)
		postForm(t, client, srv.URL+"/boards/"+boardIDFrom(boardURL)+"/sections/"+section+"/place", url.Values{"csrf": {csrf}, "widget_id": {widget}, "version": {version}})
	}

	resp := getFollowingRedirect(t, srv, client, "/")
	board := readAll(t, resp)
	resp.Body.Close()
	boardURL := resp.Request.URL.Path
	for _, want := range []string{`data-hotkey="k"`, `src="/icons/`, `<span class="mono">WD</span>`, `data-search="Kimai `, `id="search"`} {
		if !strings.Contains(board, want) {
			t.Fatalf("board missing %q:\n%s", want, board)
		}
	}

	section := regexp.MustCompile(`data-section="(\d+)"`).FindStringSubmatch(board)[1]
	placements := regexp.MustCompile(`data-placement="(\d+)"`).FindAllStringSubmatch(board, -1)
	if len(placements) != 2 {
		t.Fatalf("expected two tiles, got %v", placements)
	}

	req, _ := http.NewRequest(http.MethodPost, srv.URL+boardURL+"/fold/"+section, strings.NewReader("state=closed"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := client.Do(req)
	if err != nil || resp.StatusCode != http.StatusNoContent {
		t.Fatalf("fold: %v %d", err, resp.StatusCode)
	}
	if !strings.Contains(string(mustGet(t, srv, client, boardURL)), `class="dsec is-collapsed"`) {
		t.Fatal("fold not stored in the overlay")
	}

	postForm(t, client, srv.URL+boardURL+"/show/"+placements[0][1], url.Values{"csrf": {csrf}, "state": {"hidden"}})
	if strings.Contains(string(mustGet(t, srv, client, boardURL)), `data-placement="`+placements[0][1]+`"`) {
		t.Fatal("hidden tile still shown")
	}
	if !strings.Contains(string(mustGet(t, srv, client, boardURL+"?layout")), "is-hidden") {
		t.Fatal("layout mode should show hidden tiles dimmed")
	}

	version := regexp.MustCompile(`data-version="(\d+)"`).FindStringSubmatch(string(mustGet(t, srv, client, boardURL)))[1]
	payload := []byte(`{"version":` + version + `,"layout":{"` + section + `":[` + placements[1][1] + `,` + placements[0][1] + `]}}`)
	req, _ = http.NewRequest(http.MethodPost, srv.URL+boardURL+"/arrange", bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err = client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("arrange: %v %d %s", err, resp.StatusCode, payload)
	}

	if !strings.Contains(string(mustGet(t, srv, client, boardURL+"/history")), "Wiederherstellen") {
		t.Fatal("history should offer restoring older versions")
	}

	manifest := mustGet(t, srv, freshClient(t), "/manifest.webmanifest")
	if !bytes.Contains(manifest, []byte(`"display":"standalone"`)) {
		t.Fatalf("manifest: %s", manifest)
	}
}
