package web_test

import (
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

// Within each topic the gallery lists types A–Z by displayed name, and
// types without a connection preview demo data.
func TestGallerySortsByName(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)
	login(t, srv, client)

	page := string(mustGet(t, srv, client, "/widgets/new"))
	sorter := collate.New(language.German, collate.IgnoreCase)
	sections := regexp.MustCompile(`(?s)<section id="topic-(\w+)".*?</section>`).FindAllStringSubmatch(page, -1)
	if len(sections) < 5 {
		t.Fatalf("expected topic sections, got %d", len(sections))
	}
	for _, sec := range sections {
		var names []string
		for _, m := range regexp.MustCompile(`<b>([^<]+)</b>`).FindAllStringSubmatch(sec[0], -1) {
			names = append(names, m[1])
		}
		if !slices.IsSortedFunc(names, sorter.CompareString) {
			t.Errorf("%s not sorted: %v", sec[1], names)
		}
	}
	if !strings.Contains(page, `href="/connections/new?service=kimai"`) {
		t.Fatalf("expected a connect link for Kimai without a connection")
	}

	space := regexp.MustCompile(`space=(\d+)`).FindStringSubmatch(page)[1]
	sample := string(mustGet(t, srv, client, "/widget-sample/kimai_week?space="+space))
	if !strings.Contains(sample, "weekcol") {
		t.Fatalf("expected demo week columns:\n%s", sample)
	}
}

// From the gallery a set-up tile is placed as a copy, and a new tile can
// be two rows high.
func TestGalleryCopyAndRows(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)
	login(t, srv, client)

	space := regexp.MustCompile(`space=(\d+)`).FindSubmatch(mustGet(t, srv, client, "/widgets/new"))[1]
	resp, err := client.PostForm(srv.URL+"/widgets", url.Values{
		"csrf": {csrfToken(t, srv, client)}, "space_id": {string(space)}, "type": {"note"}, "title": {"My Note"}, "cfg.text": {"hi"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	resp.Body.Close()

	boardURL, section, version, widget := placeTarget(t, srv, client, "My Note")
	board := boardIDFrom(boardURL)
	target := url.Values{"space_id": {string(space)}, "section_id": {section}, "board_id": {board}, "version": {version}}

	form := url.Values{"csrf": {csrfToken(t, srv, client)}}
	for k, v := range target {
		form[k] = v
	}
	resp, err = client.PostForm(srv.URL+"/widgets/"+widget+"/copy", form)
	if err != nil {
		t.Fatalf("copy: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther || !strings.Contains(resp.Header.Get("Location"), "/edit?board_id="+board) {
		t.Fatalf("copy: %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	if body := string(mustGet(t, srv, client, boardURL+"?edit")); !strings.Contains(body, "My Note") {
		t.Fatalf("expected the copy on the board:\n%s", body)
	}

	// The copy bumped the board version once.
	form = url.Values{"csrf": {csrfToken(t, srv, client)}, "type": {"note"}, "title": {"Tall"}, "cfg.text": {"x"}, "rows": {"2"}}
	for k, v := range target {
		form[k] = v
	}
	form.Set("version", nextVersion(t, version))
	resp, err = client.PostForm(srv.URL+"/widgets", form)
	if err != nil {
		t.Fatalf("create tall: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusSeeOther {
		t.Fatalf("create tall: %d", resp.StatusCode)
	}
	if body := string(mustGet(t, srv, client, boardURL+"?edit")); !strings.Contains(body, `data-rows="2"`) {
		t.Fatalf("expected a two-row tile:\n%s", body)
	}
}

func nextVersion(t *testing.T, v string) string {
	t.Helper()
	n, err := strconv.Atoi(v)
	if err != nil {
		t.Fatalf("version %q: %v", v, err)
	}
	return strconv.Itoa(n + 1)
}

// A tile arrives with the page, so the board doesn't grow as fragments
// load; a note has nothing to refresh and asks for no fragment at all.
func TestBoardRendersTilesWithPage(t *testing.T) {
	srv, client, code := newTestServer(t)
	setupAdmin(t, srv, client, code)
	login(t, srv, client)

	space := regexp.MustCompile(`space=(\d+)`).FindSubmatch(mustGet(t, srv, client, "/widgets/new"))[1]
	resp, err := client.PostForm(srv.URL+"/widgets", url.Values{
		"csrf": {csrfToken(t, srv, client)}, "space_id": {string(space)}, "type": {"note"}, "title": {"My Note"}, "cfg.text": {"Inline body"},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	resp.Body.Close()
	boardURL, section, version, widget := placeTarget(t, srv, client, "My Note")
	resp, err = client.PostForm(srv.URL+"/boards/"+boardIDFrom(boardURL)+"/sections/"+section+"/place", url.Values{
		"csrf": {csrfToken(t, srv, client)}, "widget_id": {widget}, "version": {version},
	})
	if err != nil {
		t.Fatalf("place: %v", err)
	}
	resp.Body.Close()

	page := string(mustGet(t, srv, client, boardURL))
	tile := regexp.MustCompile(`(?s)<div class="tile-slot w-note".*?</article>`).FindString(page)
	if !strings.Contains(tile, "Inline body") || strings.Contains(tile, "hx-get") {
		t.Fatalf("expected the note rendered inline without a fragment request:\n%s", tile)
	}
}
