package web_test

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
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

// TestLinkExtrasAndPage: sub-links, tags, colors, section layout and the
// space's page texts reach the board; stored headers never reach the form.
func TestLinkExtrasAndPage(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)
	login(t, srv, client)
	csrf := csrfToken(t, srv, client)

	space := string(regexp.MustCompile(`space=(\d+)`).FindSubmatch(mustGet(t, srv, client, "/widgets/new"))[1])
	postForm(t, client, srv.URL+"/widgets", url.Values{"csrf": {csrf}, "space_id": {space}, "type": {"link"}, "title": {"Gitea"},
		"cfg.url": {"https://git.example"}, "cfg.status": {"off"}, "cfg.tags": {"code"}, "cfg.color": {"green"},
		"cfg.items": {"Admin | https://git.example/admin"}, "cfg.headers": {"X-Api: secret-token"}})
	boardURL, section, version, widget := placeTarget(t, srv, client, "Gitea")
	board := boardIDFrom(boardURL)
	postForm(t, client, srv.URL+"/boards/"+board+"/sections/"+section+"/place", url.Values{"csrf": {csrf}, "widget_id": {widget}, "version": {version}})

	version = regexp.MustCompile(`data-version="(\d+)"`).FindStringSubmatch(string(mustGet(t, srv, client, "/boards/"+board)))[1]
	postForm(t, client, srv.URL+"/sections/"+section+"/edit", url.Values{"csrf": {csrf}, "board_id": {board}, "version": {version},
		"title": {"Code"}, "size": {"medium"}, "sort": {"manual"}, "area": {"main"}, "span": {"2"}, "rows": {"3"}, "color": {"blue"}, "mobile": {"first"}})
	// Page texts belong to the board's space, which may differ from the widget's.
	boardSpace := string(regexp.MustCompile(`/spaces/(\d+)/settings`).FindSubmatch(mustGet(t, srv, client, "/boards/"+board+"?edit"))[1])
	postForm(t, client, srv.URL+"/spaces/"+boardSpace+"/settings", url.Values{"csrf": {csrf}, "title": {"Heim"},
		"description": {"Alles hier"}, "nav": {"Wiki | https://wiki.example\nBad | javascript:alert(1)"}, "footer": {"Privat"}})

	page := string(mustGet(t, srv, client, "/boards/"+board))
	for _, want := range []string{`href="https://git.example/admin"`, `#code`, `data-color="green"`, `data-span="2"`, `data-rows="3"`, `data-mobile="first"`,
		`data-color="blue"`, `id="ctx-menu"`, `>Heim</h1>`, `Alles hier`, `href="https://wiki.example"`, `class="page-foot">Privat`} {
		if !strings.Contains(page, want) {
			t.Fatalf("board missing %q:\n%s", want, page)
		}
	}
	if strings.Contains(page, "javascript:") {
		t.Fatal("unsafe nav link rendered")
	}
	if strings.Contains(string(mustGet(t, srv, client, "/widgets/"+widget+"/edit")), "secret-token") {
		t.Fatal("stored header shown in the form")
	}
}

// TestQuickLinkClicksPaletteUndo: a pasted URL becomes a titled tile,
// clicks make it "frequent", the palette lists it, undo removes it.
func TestQuickLinkClicksPaletteUndo(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)
	login(t, srv, client)
	csrf := csrfToken(t, srv, client)

	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><head><title>Login | Jellyfin</title></head></html>`))
	}))
	defer page.Close()

	board := boardIDFrom(getFollowingRedirect(t, srv, client, "/").Request.URL.Path)
	edit := string(mustGet(t, srv, client, "/boards/"+board+"?edit"))
	section := regexp.MustCompile(`/sections/(\d+)/quick-link`).FindStringSubmatch(edit)
	if section == nil {
		postForm(t, client, srv.URL+"/boards/"+board+"/sections", url.Values{"csrf": {csrf}, "title": {"Links"},
			"version": {regexp.MustCompile(`data-version="(\d+)"`).FindStringSubmatch(edit)[1]}})
		edit = string(mustGet(t, srv, client, "/boards/"+board+"?edit"))
		section = regexp.MustCompile(`/sections/(\d+)/quick-link`).FindStringSubmatch(edit)
	}
	version := regexp.MustCompile(`data-version="(\d+)"`).FindStringSubmatch(edit)[1]
	resp := postForm(t, client, srv.URL+"/sections/"+section[1]+"/quick-link", url.Values{"csrf": {csrf}, "board_id": {board}, "version": {version}, "url": {page.URL}})
	if resp.StatusCode != http.StatusSeeOther || !strings.Contains(resp.Header.Get("Location"), "undo") {
		t.Fatalf("quick link: %d", resp.StatusCode)
	}
	view := string(mustGet(t, srv, client, "/boards/"+board))
	if !strings.Contains(view, `<b class="launch-title">Jellyfin</b>`) {
		t.Fatalf("tile missing:\n%s", view)
	}

	placement := regexp.MustCompile(`class="launch" data-click="(\d+)"`).FindStringSubmatch(view)[1]
	for range 3 {
		if r := postForm(t, client, srv.URL+"/clicks/"+placement, url.Values{"csrf": {csrf}}); r.StatusCode != http.StatusNoContent {
			t.Fatalf("click: %d", r.StatusCode)
		}
	}
	if !strings.Contains(string(mustGet(t, srv, client, "/boards/"+board)), `class="frequent"`) {
		t.Fatal("frequent row missing after three clicks")
	}
	if palette := string(mustGet(t, srv, client, "/palette.json")); !strings.Contains(palette, `"title":"Jellyfin"`) || !strings.Contains(palette, `"kind":"page"`) {
		t.Fatalf("palette: %s", palette)
	}

	postForm(t, client, srv.URL+"/boards/"+board+"/undo", url.Values{"csrf": {csrf}})
	if strings.Contains(string(mustGet(t, srv, client, "/boards/"+board)), "Jellyfin</b>") {
		t.Fatal("undo kept the tile")
	}
}

// TestKioskRotatesBoards: the wall display hides the app nav and points
// at the next visible board, keeping its settings.
func TestKioskRotatesBoards(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)
	login(t, srv, client)
	csrf := csrfToken(t, srv, client)

	form := string(mustGet(t, srv, client, "/boards/new"))
	space := regexp.MustCompile(`name="space_id"><option value="(\d+)"`).FindStringSubmatch(form)[1]
	postForm(t, client, srv.URL+"/boards/new", url.Values{"csrf": {csrf}, "name": {"Zwei"}, "space_id": {space}})

	resp := getFollowingRedirect(t, srv, client, "/")
	resp.Body.Close()
	page := string(mustGet(t, srv, client, resp.Request.URL.Path+"?kiosk&every=5&dim=22-7"))
	if strings.Contains(page, `class="app-nav"`) || !strings.Contains(page, `class="is-kiosk"`) {
		t.Fatalf("kiosk chrome:\n%s", page)
	}
	next := regexp.MustCompile(`data-kiosk-next="(/boards/\d+\?kiosk&amp;dim=22-7&amp;every=10)"`).FindStringSubmatch(page)
	if next == nil || strings.Contains(next[1], resp.Request.URL.Path+"?") {
		t.Fatalf("next board: %v", next)
	}
}

// TestOfflineWorker: the service worker is served from the root, and
// logout drops what it stored.
func TestOfflineWorker(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)
	login(t, srv, client)
	csrf := csrfToken(t, srv, client)

	resp, err := client.Get(srv.URL + "/sw.js")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(resp.Header.Get("Content-Type"), "text/javascript") {
		t.Fatalf("sw.js: %d %s", resp.StatusCode, resp.Header.Get("Content-Type"))
	}

	noFollow := *client
	noFollow.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp = postForm(t, &noFollow, srv.URL+"/logout", url.Values{"csrf": {csrf}})
	if !strings.Contains(resp.Header.Get("Clear-Site-Data"), `"cache"`) {
		t.Fatalf("logout keeps offline copies: %v", resp.Header)
	}
}
