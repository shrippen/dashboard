package content_test

import (
	"path/filepath"
	"testing"
	"time"

	"dashboard/internal/db"
	"dashboard/internal/db/dbtest"
	"dashboard/internal/enums"
	"dashboard/internal/model"
	"dashboard/internal/repos/content"
	"dashboard/internal/repos/users"
)

func openTestDB(t *testing.T) db.Queryer {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"), dbtest.Key)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func addSpace(t *testing.T, q db.Queryer) *model.Space {
	t.Helper()
	sp := &model.Space{Kind: enums.SpacePersonal, Name: "Alex", Version: 1}
	if err := content.AddSpace(q, sp); err != nil {
		t.Fatalf("add space: %v", err)
	}
	return sp
}

func TestBoardWithSectionsAndPlacementsRoundTrip(t *testing.T) {
	q := openTestDB(t)
	sp := addSpace(t, q)

	w := &model.Widget{SpaceID: sp.ID, Key: "clock", Type: "clock", Title: "Uhr", Version: 1}
	if err := content.AddWidget(q, w); err != nil {
		t.Fatalf("add widget: %v", err)
	}

	b := &model.Board{SpaceID: sp.ID, Slug: "start", Name: "Start", Version: 1}
	if err := content.AddBoard(q, b); err != nil {
		t.Fatalf("add board: %v", err)
	}

	sec := &model.Section{BoardID: b.ID, Title: "Oben", Size: enums.TileMedium, Sort: enums.SortManual, Area: "main"}
	if err := content.AddSection(q, sec); err != nil {
		t.Fatalf("add section: %v", err)
	}

	p := &model.Placement{SectionID: sec.ID, WidgetID: w.ID, Position: 0}
	if err := content.AddPlacement(q, p); err != nil {
		t.Fatalf("add placement: %v", err)
	}

	got, err := content.Board(q, b.ID)
	if err != nil {
		t.Fatalf("board: %v", err)
	}
	if len(got.Sections) != 1 {
		t.Fatalf("expected 1 section, got %d", len(got.Sections))
	}
	if len(got.Sections[0].Placements) != 1 {
		t.Fatalf("expected 1 placement, got %d", len(got.Sections[0].Placements))
	}
	if got.Sections[0].Placements[0].Widget == nil || got.Sections[0].Placements[0].Widget.Key != "clock" {
		t.Fatalf("expected placement's widget to be loaded, got %+v", got.Sections[0].Placements[0].Widget)
	}
}

func TestBoardBySlugScopedToSpace(t *testing.T) {
	q := openTestDB(t)
	sp1 := addSpace(t, q)
	sp2 := &model.Space{Kind: enums.SpaceTeam, Name: "Team", Version: 1}
	if err := content.AddSpace(q, sp2); err != nil {
		t.Fatalf("add space2: %v", err)
	}

	b := &model.Board{SpaceID: sp1.ID, Slug: "start", Name: "Start", Version: 1}
	if err := content.AddBoard(q, b); err != nil {
		t.Fatalf("add board: %v", err)
	}

	if got, err := content.BoardBySlug(q, sp2.ID, "start"); err != nil || got != nil {
		t.Fatalf("expected no board in other space, got %+v err=%v", got, err)
	}
	if got, err := content.BoardBySlug(q, sp1.ID, "start"); err != nil || got == nil {
		t.Fatalf("expected board in own space, got %+v err=%v", got, err)
	}
}

func TestWidgetUsesCounts(t *testing.T) {
	q := openTestDB(t)
	sp := addSpace(t, q)
	w := &model.Widget{SpaceID: sp.ID, Key: "w", Type: "note", Version: 1}
	if err := content.AddWidget(q, w); err != nil {
		t.Fatalf("add widget: %v", err)
	}
	b := &model.Board{SpaceID: sp.ID, Slug: "b", Name: "B", Version: 1}
	content.AddBoard(q, b)
	sec := &model.Section{BoardID: b.ID, Size: enums.TileMedium, Sort: enums.SortManual, Area: "main"}
	content.AddSection(q, sec)

	n, err := content.WidgetUses(q, w.ID)
	if err != nil || n != 0 {
		t.Fatalf("expected 0 uses, got %d err=%v", n, err)
	}
	content.AddPlacement(q, &model.Placement{SectionID: sec.ID, WidgetID: w.ID})
	n, err = content.WidgetUses(q, w.ID)
	if err != nil || n != 1 {
		t.Fatalf("expected 1 use, got %d err=%v", n, err)
	}
}

func TestOverlaySetOverwrites(t *testing.T) {
	q := openTestDB(t)
	sp := addSpace(t, q)
	b := &model.Board{SpaceID: sp.ID, Slug: "b", Name: "B", Version: 1}
	content.AddBoard(q, b)
	u := &model.User{Email: "a@b.c", Name: "A", Role: enums.RoleUser, IsActive: true,
		Locale: enums.LocaleDE, ColorMode: enums.ColorAuto, CreatedAt: time.Now().UTC()}
	if err := users.Add(q, u); err != nil {
		t.Fatalf("add user: %v", err)
	}

	if err := content.SetOverlay(q, u.ID, b.ID, map[string]any{"collapsed": []any{"1"}}); err != nil {
		t.Fatalf("set overlay: %v", err)
	}
	got, err := content.Overlay(q, u.ID, b.ID)
	if err != nil || got == nil {
		t.Fatalf("expected overlay, got %+v err=%v", got, err)
	}

	// Setting again must overwrite, not duplicate (UNIQUE(user_id, board_id)).
	if err := content.SetOverlay(q, u.ID, b.ID, map[string]any{"collapsed": []any{}}); err != nil {
		t.Fatalf("re-set overlay: %v", err)
	}
	got, err = content.Overlay(q, u.ID, b.ID)
	if err != nil || got == nil {
		t.Fatalf("expected overlay after overwrite, got %+v err=%v", got, err)
	}
	if collapsed, _ := got.Data["collapsed"].([]any); len(collapsed) != 0 {
		t.Fatalf("expected overwritten overlay data, got %+v", got.Data)
	}
}

func TestRevisionRoundTrip(t *testing.T) {
	q := openTestDB(t)
	sp := addSpace(t, q)
	b := &model.Board{SpaceID: sp.ID, Slug: "b", Name: "B", Version: 1}
	content.AddBoard(q, b)

	rev := &model.Revision{Kind: enums.RevisionBoard, EntityID: b.ID, SpaceID: sp.ID, Version: 1,
		Data: map[string]any{"name": "Start"}}
	if err := content.AddRevision(q, rev); err != nil {
		t.Fatalf("add revision: %v", err)
	}
	revs, err := content.Revisions(q, enums.RevisionBoard, b.ID)
	if err != nil || len(revs) != 1 {
		t.Fatalf("expected 1 revision, got %d err=%v", len(revs), err)
	}
	if revs[0].Data["name"] != "Start" {
		t.Fatalf("revision data not round-tripped: %+v", revs[0].Data)
	}
}

func TestPruneRevisionsKeepsOnlyMostRecent(t *testing.T) {
	q := openTestDB(t)
	sp := addSpace(t, q)
	b := &model.Board{SpaceID: sp.ID, Slug: "b", Name: "B", Version: 1}
	content.AddBoard(q, b)

	for i := 0; i < 3; i++ {
		content.AddRevision(q, &model.Revision{
			Kind: enums.RevisionBoard, EntityID: b.ID, SpaceID: sp.ID, Version: i + 1,
			Data: map[string]any{},
		})
	}
	// Prune with a kept-count larger than 3 is a no-op; verify count stays 3
	// (kept constant is 50, so nothing is pruned here — this just guards the
	// query shape doesn't error and returns all rows in the right order).
	if err := content.PruneRevisions(q, enums.RevisionBoard, b.ID); err != nil {
		t.Fatalf("prune: %v", err)
	}
	revs, err := content.Revisions(q, enums.RevisionBoard, b.ID)
	if err != nil || len(revs) != 3 {
		t.Fatalf("expected 3 revisions kept, got %d err=%v", len(revs), err)
	}
	if revs[0].Version != 3 {
		t.Fatalf("expected newest first, got version %d", revs[0].Version)
	}
}
