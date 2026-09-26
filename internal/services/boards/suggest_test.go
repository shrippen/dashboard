package boards_test

import (
	"errors"
	"testing"

	"andon/internal/enums"
	"andon/internal/repos/content"
	"andon/internal/services/boards"
	"andon/internal/services/widgetlib"
	"andon/internal/testkit"
)

// titles lists section titles and tile types (new tiles marked "+").
func titles(s boards.Suggestion) map[string][]string {
	out := map[string][]string{}
	for _, sec := range s.Sections {
		for _, tile := range sec.Tiles {
			name := tile.Type
			if tile.New() {
				name = "+" + name
			}
			out[sec.Title] = append(out[sec.Title], name)
		}
	}
	return out
}

// TestSuggestAndApply: existing tiles are grouped by topic with the
// overview first and links in their own section; each connection without a
// tile gets its starter; a hints tile is added. Applying replaces the
// layout, undo brings it back.
func TestSuggestAndApply(t *testing.T) {
	d := testkit.DB(t)
	who, space := testkit.User(t, d, "a@b.c", enums.RoleAdmin)

	kimai := testkit.Conn(t, d, who, space, enums.ServiceKimai, "https://kimai.test")
	testkit.Conn(t, d, who, space, enums.ServiceScrutiny, "https://scrutiny.test")
	testkit.Conn(t, d, who, space, enums.ServiceProxmox, "https://pve.test") // no tile type of its own
	placement := testkit.Place(t, d, who, space, "clock", nil, nil)
	if _, err := widgetlib.Create(d, who, space, "link", "Docs", map[string]any{"url": "https://docs.test"}, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := widgetlib.Create(d, who, space, "kimai_week", "", nil, &kimai, nil); err != nil {
		t.Fatal(err)
	}

	placed, _ := content.Placement(d, placement)
	sec, _ := content.Section(d, placed.SectionID)
	board := sec.BoardID

	s, err := boards.Suggest(d, who, board)
	if err != nil {
		t.Fatalf("suggest: %v", err)
	}
	got := titles(s)
	if len(s.Sections) < 3 || s.Sections[0].Title != "Überblick" || s.Sections[1].Title != "Links" || !s.Sections[1].Links {
		t.Fatalf("section order: %+v", got)
	}
	if o := got["Überblick"]; len(o) != 2 || o[0] != "+hints" || o[1] != "clock" {
		t.Fatalf("overview: %v", o)
	}
	if w := got["Arbeit & Geld"]; len(w) != 1 || w[0] != "kimai_week" {
		t.Fatalf("kimai already has a tile, no second one: %v", w)
	}
	if h := got["Homelab"]; len(h) != 1 || h[0] != "+disks" {
		t.Fatalf("scrutiny starter: %v", h)
	}

	if err := boards.ApplySuggestion(d, who, board, s.Version-1); !errors.Is(err, boards.ErrConflict) {
		t.Fatalf("stale version: %v", err)
	}
	if err := boards.ApplySuggestion(d, who, board, s.Version); err != nil {
		t.Fatalf("apply: %v", err)
	}
	view, err := boards.View(d, who, board)
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Sections) != len(s.Sections) {
		t.Fatalf("applied %d sections, suggested %d", len(view.Sections), len(s.Sections))
	}
	ws, _ := content.Widgets(d, []int64{space})
	types := map[string]bool{}
	for _, w := range ws {
		types[w.Type] = true
	}
	if !types["disks"] || !types["hints"] {
		t.Fatalf("new tiles not in the library: %v", types)
	}

	if err := boards.Undo(d, who, board); err != nil {
		t.Fatalf("undo: %v", err)
	}
	view, _ = boards.View(d, who, board)
	if len(view.Sections) != 1 {
		t.Fatalf("undo left %d sections, want the original one", len(view.Sections))
	}
}
