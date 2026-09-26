package porting_test

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"dashboard/internal/crypto"
	"dashboard/internal/db"
	"dashboard/internal/db/dbtest"
	"dashboard/internal/enums"
	"dashboard/internal/services/access"
	"dashboard/internal/services/accounts"
	"dashboard/internal/services/boards"
	"dashboard/internal/services/porting"
	"dashboard/internal/services/spaces"
	"dashboard/internal/services/themes"
	"dashboard/internal/services/widgetlib"
	"dashboard/internal/widgets"
)

const dashy = `
pageInfo:
  title: Homelab
  navLinks: [{title: Git, path: "https://git.lan"}]
appConfig:
  theme: nord
  statusCheck: true
  webSearch: {searchEngine: duckduckgo}
sections:
  - name: Media
    displayData: {collapsed: true, cols: 3, itemSize: small}
    items:
      - {title: Jellyfin, url: "https://jf.lan", icon: hl-jellyfin, hotkey: 1}
      - {title: Broken, url: "not-a-url"}
      - {title: FA, url: "https://fa.lan", icon: "fas fa-rocket", hideForGuests: true}
  - name: Info
    widgets:
      - {type: rss-feed, options: {rssUrl: "https://news.lan/feed", limit: 5}}
      - {type: clock, options: {timeZone: Europe/Berlin}}
      - {type: gl-current-cpu, options: {hostname: "http://glances.lan"}}
      - {type: crypto-watch-list, options: {assets: [bitcoin], currency: EUR}}
`

func setup(t *testing.T) *sql.DB {
	t.Helper()
	crypto.Init("test-master-key")
	d, err := db.Open(filepath.Join(t.TempDir(), "t.db"), dbtest.Key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func user(t *testing.T, d *sql.DB, email string) (*access.Principal, int64) {
	t.Helper()
	var id int64
	err := db.WithTx(d, func(tx *sql.Tx) error {
		u, err := accounts.Create(tx, email, "U", nil, enums.RoleUser, enums.LocaleDE, "")
		if err == nil {
			id = u.ID
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	who, err := access.Load(d, id)
	if err != nil {
		t.Fatal(err)
	}
	return who, access.Personal(who).ID
}

func contains(list []string, part string) bool {
	for _, s := range list {
		if strings.Contains(s, part) {
			return true
		}
	}
	return false
}

func TestDashyImport(t *testing.T) {
	d := setup(t)
	who, space := user(t, d, "a@x.de")

	report, err := porting.ImportDashy(d, who, space, dashy)
	if err != nil {
		t.Fatal(err)
	}
	if report.Boards != 1 || report.Widgets != 5 {
		t.Fatalf("expected 1 board and 5 widgets (2 links + rss + clock + crypto), got %+v", report)
	}
	for _, want := range []string{"not-a-url"} {
		if !contains(report.Skipped, want) {
			t.Fatalf("expected %q skipped: %v", want, report.Skipped)
		}
	}
	for _, want := range []string{"theme", "hideForGuests", "gl-current-cpu"} {
		if !contains(report.Notes, want) {
			t.Fatalf("expected note about %q: %v", want, report.Notes)
		}
	}

	visible, _ := boards.Visible(d, who)
	view, err := boards.View(d, who, visible[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	media := view.Sections[0]
	// Dashy's cols is the section's width in its own grid, not tiles per
	// row: link groups flow in newspaper columns instead.
	if !media.Collapsed || media.Cols != nil || media.Span != boards.SpanFlow || media.Size != enums.TileSmall {
		t.Fatalf("section display not carried over: %+v", media)
	}
	if len(media.Tiles) != 2 || media.Tiles[0].Title != "Jellyfin" || media.Tiles[1].Title != "FA" {
		t.Fatalf("unexpected tiles: %+v", media.Tiles)
	}
	link := media.Tiles[0].Config.(widgets.LinkConfig)
	if link.Status != widgets.StatusHTTP || link.Icon != "hl-jellyfin" || link.Hotkey != "1" {
		t.Fatalf("unexpected link config: %+v", link)
	}

	// appConfig.theme: nord becomes the space's theme.
	settings, _ := spaces.Settings(d, who, space)
	id, ok := settings["theme_id"].(float64)
	if !ok || !contains(report.Notes, "Dashy nord") {
		t.Fatalf("theme not applied: %v %v", settings, report.Notes)
	}
	theme, _, err := themes.Get(d, who, int64(id))
	if err != nil || theme.Dark["--bg-void"] != "#242933" {
		t.Fatalf("theme: %+v %v", theme, err)
	}
}

func TestExportImportRoundtrip(t *testing.T) {
	d := setup(t)
	a, spaceA := user(t, d, "a@x.de")
	b, spaceB := user(t, d, "b@x.de")
	if _, err := porting.ImportDashy(d, a, spaceA, dashy); err != nil {
		t.Fatal(err)
	}

	// A tall tile keeps its height through export and import.
	visible, _ := boards.Visible(d, a)
	view, _ := boards.View(d, a, visible[0].ID)
	if err := boards.SetTileRows(d, a, view.Sections[0].Tiles[0].PlacementID, boards.MaxTileRows, view.Version); err != nil {
		t.Fatal(err)
	}

	text, err := porting.ExportSpace(d, a, spaceA)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "Jellyfin") || !strings.HasPrefix(text, "space: personal") || !strings.Contains(text, "tall:") {
		t.Fatalf("unexpected export:\n%s", text)
	}

	report, err := porting.ImportSpace(d, b, spaceB, text, porting.Replace)
	if err != nil || report.Boards != 1 || report.Widgets != 5 {
		t.Fatalf("reimport: %+v %v", report, err)
	}
	lib, _ := widgetlib.Library(d, b)
	if len(lib) != 5 {
		t.Fatalf("expected 5 widgets in b's library, got %d", len(lib))
	}
	visibleB, _ := boards.Visible(d, b)
	viewB, _ := boards.View(d, b, visibleB[0].ID)
	if viewB.Sections[0].Tiles[0].Rows != boards.MaxTileRows {
		t.Fatalf("tall tile lost on import: %+v", viewB.Sections[0].Tiles[0])
	}

	if _, err := porting.ImportSpace(d, b, spaceA, text, porting.Merge); err == nil {
		t.Fatal("importing into a foreign space must be denied")
	}
}

func TestYAMLErrors(t *testing.T) {
	d := setup(t)
	who, space := user(t, d, "a@x.de")
	if _, err := porting.ImportSpace(d, who, space, "boards: [\n  - name: x\n bad", porting.Merge); err == nil || !strings.Contains(err.Error(), "line") {
		t.Fatalf("expected a YAML error with line, got %v", err)
	}
	if _, err := porting.ImportSpace(d, who, space, "- a\n- b\n", porting.Merge); err != porting.ErrNotMapping {
		t.Fatalf("expected ErrNotMapping, got %v", err)
	}
}

const dashyExtras = `
sections:
  - name: Extras
    widgets:
      - {type: image, options: {imagePath: "https://img.example/cam.jpg", imageHeight: 180}}
      - {type: exchange-rates, options: {inputCurrency: GBP, outputCurrencies: [USD, EUR]}}
      - {type: hackernews-trending}
      - {type: uptime-kuma, options: {url: "https://kuma.lan", apiKey: x}}
`

func TestDashyImportExtraWidgets(t *testing.T) {
	d := setup(t)
	who, space := user(t, d, "a@x.de")

	report, err := porting.ImportDashy(d, who, space, dashyExtras)
	if err != nil {
		t.Fatal(err)
	}
	if report.Widgets != 3 || !contains(report.Notes, "uptime-kuma") {
		t.Fatalf("report: %+v", report)
	}

	visible, _ := boards.Visible(d, who)
	view, err := boards.View(d, who, visible[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	tiles := view.Sections[0].Tiles
	image := tiles[0].Config.(widgets.ImageConfig)
	rates := tiles[1].Config.(widgets.RatesConfig)
	feed := tiles[2].Config.(widgets.RssConfig)
	if image.Height != 180 || rates.Base != "GBP" || len(rates.Symbols) != 2 || feed.URL != "https://hnrss.org/frontpage" {
		t.Fatalf("configs: %+v %+v %+v", image, rates, feed)
	}
}

const dashyLinks = `
pageInfo: {title: Heim, description: Alles an einem Ort, footerText: Privat}
sections:
  - name: Tools
    displayData: {rows: 2}
    items:
      - title: Gitea
        url: "https://git.lan"
        tags: [code, dev]
        statusCheckHeaders: {Authorization: "Bearer abc"}
        subItems:
          - {title: Admin, url: "https://git.lan/admin", icon: hl-gitea}
          - {title: Bad, url: "ftp://x"}
`

// Tags, sub-items and page texts survive the Dashy import; status
// headers are stored sealed and never exported.
func TestDashyLinksAndPage(t *testing.T) {
	d := setup(t)
	who, space := user(t, d, "a@x.de")
	report, err := porting.ImportDashy(d, who, space, dashyLinks)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(report.Skipped, "ftp://x") {
		t.Fatalf("report: %+v", report)
	}

	visible, _ := boards.Visible(d, who)
	view, err := boards.View(d, who, visible[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	sec := view.Sections[0]
	link := sec.Tiles[0].Config.(widgets.LinkConfig)
	if sec.Span != boards.SpanFlow || len(link.Tags) != 2 || len(sec.Tiles[0].Items) != 1 || sec.Tiles[0].Items[0].Title != "Admin" {
		t.Fatalf("section %+v link %+v", sec, link)
	}
	if view.Page.Title != "Heim" || view.Page.Footer != "Privat" {
		t.Fatalf("page: %+v", view.Page)
	}

	text, err := porting.ExportSpace(d, who, space)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "abc") || strings.Contains(text, "headers") {
		t.Fatalf("export leaks headers:\n%s", text)
	}
	if !strings.Contains(text, "span: 7") {
		t.Fatalf("export lost the section span:\n%s", text)
	}
}

// Dashy flight-data carries an API key: it is sealed and not exported.
func TestDashyWidgetKeysSealed(t *testing.T) {
	d := setup(t)
	who, space := user(t, d, "a@x.de")
	yaml := "sections:\n  - name: Travel\n    widgets:\n      - {type: flight-data, options: {airport: MUC, apiKey: rapid-123, direction: arrival}}\n" +
		"      - {type: public-holidays, options: {country: de, region: by}}\n"
	if _, err := porting.ImportDashy(d, who, space, yaml); err != nil {
		t.Fatal(err)
	}
	visible, _ := boards.Visible(d, who)
	view, _ := boards.View(d, who, visible[0].ID)
	tiles := view.Sections[0].Tiles
	flights := tiles[0].Config.(widgets.FlightsConfig)
	holidays := tiles[1].Config.(widgets.HolidaysConfig)
	if flights.Airport != "MUC" || flights.Direction != "Arrival" || flights.APIKey != "" || holidays.State != "DE-BY" {
		t.Fatalf("configs: %+v %+v", flights, holidays)
	}
	text, _ := porting.ExportSpace(d, who, space)
	if strings.Contains(text, "rapid-123") || strings.Contains(text, "api_key") {
		t.Fatalf("export leaks key:\n%s", text)
	}
}

// Every shipped template imports cleanly; unknown keys are refused.
func TestTemplatesApply(t *testing.T) {
	d := setup(t)
	who, space := user(t, d, "t@x.de")
	if len(porting.Templates()) == 0 {
		t.Fatal("no templates")
	}
	for _, key := range porting.Templates() {
		if _, err := porting.ApplyTemplate(d, who, space, key); err != nil {
			t.Fatalf("%s: %v", key, err)
		}
	}
	if _, err := porting.ApplyTemplate(d, who, space, "../porting"); err != porting.ErrNoTemplate {
		t.Fatalf("unknown: %v", err)
	}
}
