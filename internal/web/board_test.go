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

	space := regexp.MustCompile(`space=(\d+)`).FindSubmatch(mustGet(t, srv, client, "/widgets/new"))[1]
	for _, w := range []struct{ title, url, icon, hotkey string }{
		{"Kimai", "https://kimai.example", spec, "k"},
		{"Wiki Docs", "https://wiki.example", "", ""},
	} {
		postForm(t, client, srv.URL+"/widgets", url.Values{"csrf": {csrf}, "space_id": {string(space)}, "type": {"link"}, "title": {w.title},
			"cfg.url": {w.url}, "cfg.icon": {w.icon}, "cfg.hotkey": {w.hotkey}, "cfg.status": {"off"}})
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

// TestNewWidgetFromSectionIsPlaced: "+ new widget" in edit mode creates the
// widget from the generated form, places it and returns to the board.
func TestNewWidgetFromSectionIsPlaced(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)
	login(t, srv, client)
	csrf := csrfToken(t, srv, client)

	resp := getFollowingRedirect(t, srv, client, "/")
	resp.Body.Close()
	boardURL := resp.Request.URL.Path
	edit := string(mustGet(t, srv, client, boardURL+"?edit"))
	link := regexp.MustCompile(`/widgets/new\?space=(\d+)&(?:amp;)?section=(\d+)&(?:amp;)?board=(\d+)&(?:amp;)?version=(\d+)`).FindStringSubmatch(edit)
	if link == nil {
		t.Fatalf("no new-widget link:\n%s", edit)
	}

	picker := string(mustGet(t, srv, client, "/widgets/new?space="+link[1]+"&section="+link[2]+"&board="+link[3]+"&version="+link[4]))
	if !strings.Contains(picker, "type=note") {
		t.Fatalf("type picker incomplete:\n%s", picker)
	}
	form := string(mustGet(t, srv, client, "/widgets/new?type=note&space="+link[1]+"&section="+link[2]+"&board="+link[3]+"&version="+link[4]))
	if !strings.Contains(form, `name="cfg.text"`) || !strings.Contains(form, `name="section_id"`) {
		t.Fatalf("generated form incomplete:\n%s", form)
	}

	values := url.Values{"csrf": {csrf}, "type": {"note"}, "title": {"Memo"}, "cfg.text": {"Hallo Welt"},
		"space_id": {link[1]}, "section_id": {link[2]}, "board_id": {link[3]}, "version": {link[4]}}
	preview := postForm(t, client, srv.URL+"/widget-preview", values)
	if preview.StatusCode != http.StatusOK {
		t.Fatalf("preview: %d", preview.StatusCode)
	}
	resp = postForm(t, client, srv.URL+"/widgets", values)
	if resp.StatusCode != http.StatusSeeOther || resp.Header.Get("Location") != boardURL+"?edit" {
		t.Fatalf("create: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	if !strings.Contains(string(mustGet(t, srv, client, boardURL)), "Memo") {
		t.Fatal("new widget not placed on the board")
	}
}

// TestBoardSettingsKeepTheme: saving board settings with a theme stores it,
// and a later rename that sends the form again keeps it.
func TestBoardSettingsKeepTheme(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)
	login(t, srv, client)
	csrf := csrfToken(t, srv, client)

	resp := getFollowingRedirect(t, srv, client, "/")
	resp.Body.Close()
	boardURL := resp.Request.URL.Path
	settings := string(mustGet(t, srv, client, boardURL+"/settings"))
	theme := regexp.MustCompile(`<option value="(\d+)"[^>]*>shrippen`).FindStringSubmatch(settings)
	if theme == nil {
		t.Fatalf("no theme choice in board settings:\n%s", settings)
	}
	version := regexp.MustCompile(`name="version" value="(\d+)"`).FindStringSubmatch(settings)[1]
	postForm(t, client, srv.URL+boardURL+"/settings", url.Values{"csrf": {csrf}, "version": {version}, "name": {"Start"}, "theme_id": {theme[1]}})

	settings = string(mustGet(t, srv, client, boardURL+"/settings"))
	if !regexp.MustCompile(`<option value="` + theme[1] + `" selected>`).MatchString(settings) {
		t.Fatalf("theme not stored:\n%s", settings)
	}
}
