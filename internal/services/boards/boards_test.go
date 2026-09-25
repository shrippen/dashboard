package boards_test

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"dashboard/internal/db"
	"dashboard/internal/enums"
	"dashboard/internal/model"
	"dashboard/internal/repos/content"
	"dashboard/internal/repos/users"
	"dashboard/internal/services/access"
	"dashboard/internal/services/boards"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func addUser(t *testing.T, q db.Queryer, email string, role enums.InstanceRole) *model.User {
	t.Helper()
	u := &model.User{Email: email, Name: email, Role: role, IsActive: true,
		Locale: enums.LocaleDE, ColorMode: enums.ColorAuto, CreatedAt: time.Now().UTC()}
	if err := users.Add(q, u); err != nil {
		t.Fatalf("add user: %v", err)
	}
	personal := &model.Space{Kind: enums.SpacePersonal, Name: u.Name, OwnerUserID: &u.ID, Version: 1}
	if err := content.AddSpace(q, personal); err != nil {
		t.Fatalf("add personal space: %v", err)
	}
	return u
}

func addWidget(t *testing.T, q db.Queryer, spaceID int64, key string) *model.Widget {
	t.Helper()
	w := &model.Widget{SpaceID: spaceID, Key: key, Type: "note", Title: "Note", Version: 1, UpdatedAt: time.Now().UTC()}
	if err := content.AddWidget(q, w); err != nil {
		t.Fatalf("add widget: %v", err)
	}
	return w
}

func TestCreateAndViewBoard(t *testing.T) {
	d := openTestDB(t)
	u := addUser(t, d, "a@b.c", enums.RoleUser)
	who, _ := access.Load(d, u.ID)
	space, _ := content.PersonalSpace(d, u.ID)

	id, err := boards.Create(d, who, space.ID, "My Board")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	view, err := boards.View(d, who, id)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	if view.Name != "My Board" || !view.CanEdit || len(view.Sections) != 1 {
		t.Fatalf("unexpected view: %+v", view)
	}
}

func TestPlaceAndUnplaceWidget(t *testing.T) {
	d := openTestDB(t)
	u := addUser(t, d, "a@b.c", enums.RoleUser)
	who, _ := access.Load(d, u.ID)
	space, _ := content.PersonalSpace(d, u.ID)
	boardID, _ := boards.Create(d, who, space.ID, "B")
	w := addWidget(t, d, space.ID, "note1")

	view, _ := boards.View(d, who, boardID)
	sectionID := view.Sections[0].ID

	placementID, err := boards.Place(d, who, sectionID, w.ID, view.Version)
	if err != nil {
		t.Fatalf("place: %v", err)
	}
	view, _ = boards.View(d, who, boardID)
	if len(view.Sections[0].Tiles) != 1 || view.Sections[0].Tiles[0].WidgetID != w.ID {
		t.Fatalf("expected 1 tile, got %+v", view.Sections[0].Tiles)
	}

	if err := boards.Unplace(d, who, placementID, view.Version); err != nil {
		t.Fatalf("unplace: %v", err)
	}
	view, _ = boards.View(d, who, boardID)
	if len(view.Sections[0].Tiles) != 0 {
		t.Fatalf("expected 0 tiles after unplace, got %+v", view.Sections[0].Tiles)
	}
}

func TestVersionConflict(t *testing.T) {
	d := openTestDB(t)
	u := addUser(t, d, "a@b.c", enums.RoleUser)
	who, _ := access.Load(d, u.ID)
	space, _ := content.PersonalSpace(d, u.ID)
	boardID, _ := boards.Create(d, who, space.ID, "B")

	err := boards.Rename(d, who, boardID, 999, "New Name", nil, nil)
	if !errors.Is(err, boards.ErrConflict) {
		t.Fatalf("expected conflict for stale version, got %v", err)
	}
}

func TestOtherUserCannotEditPersonalBoard(t *testing.T) {
	d := openTestDB(t)
	owner := addUser(t, d, "owner@x.de", enums.RoleUser)
	stranger := addUser(t, d, "stranger@x.de", enums.RoleUser)
	ownerWho, _ := access.Load(d, owner.ID)
	strangerWho, _ := access.Load(d, stranger.ID)
	space, _ := content.PersonalSpace(d, owner.ID)

	boardID, err := boards.Create(d, ownerWho, space.ID, "Private")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := boards.View(d, strangerWho, boardID); err == nil {
		t.Fatal("expected stranger to be denied viewing a personal board")
	}
}

func TestArrangeByEditorReordersBoard(t *testing.T) {
	d := openTestDB(t)
	u := addUser(t, d, "a@b.c", enums.RoleUser)
	who, _ := access.Load(d, u.ID)
	space, _ := content.PersonalSpace(d, u.ID)
	boardID, _ := boards.Create(d, who, space.ID, "B")
	w1 := addWidget(t, d, space.ID, "w1")
	w2 := addWidget(t, d, space.ID, "w2")

	view, _ := boards.View(d, who, boardID)
	sectionID := view.Sections[0].ID
	p1, _ := boards.Place(d, who, sectionID, w1.ID, view.Version)
	view, _ = boards.View(d, who, boardID)
	p2, _ := boards.Place(d, who, sectionID, w2.ID, view.Version)
	view, _ = boards.View(d, who, boardID)

	target, err := boards.Arrange(d, who, boardID, view.Version, map[int64][]int64{sectionID: {p2, p1}})
	if err != nil {
		t.Fatalf("arrange: %v", err)
	}
	if target != boards.LayoutBoard {
		t.Fatalf("expected editor arrange to target the board, got %v", target)
	}
	view, _ = boards.View(d, who, boardID)
	if view.Sections[0].Tiles[0].WidgetID != w2.ID {
		t.Fatalf("expected w2 first after reorder, got %+v", view.Sections[0].Tiles)
	}
}

func TestFoldAndShowUseOverlay(t *testing.T) {
	d := openTestDB(t)
	u := addUser(t, d, "a@b.c", enums.RoleUser)
	who, _ := access.Load(d, u.ID)
	space, _ := content.PersonalSpace(d, u.ID)
	boardID, _ := boards.Create(d, who, space.ID, "B")
	w := addWidget(t, d, space.ID, "w1")

	view, _ := boards.View(d, who, boardID)
	sectionID := view.Sections[0].ID
	placementID, _ := boards.Place(d, who, sectionID, w.ID, view.Version)

	if err := boards.FoldSection(d, who, boardID, sectionID, boards.FoldClosed); err != nil {
		t.Fatalf("fold: %v", err)
	}
	if err := boards.ShowTile(d, who, boardID, placementID, boards.VisHidden); err != nil {
		t.Fatalf("hide: %v", err)
	}

	view, _ = boards.View(d, who, boardID)
	if !view.Sections[0].Collapsed {
		t.Fatal("expected section collapsed via overlay")
	}
	if !view.Sections[0].Tiles[0].Hidden {
		t.Fatal("expected tile hidden via overlay")
	}
	if !view.HasOverlay {
		t.Fatal("expected HasOverlay true")
	}

	if err := boards.ResetOverlay(d, who, boardID); err != nil {
		t.Fatalf("reset: %v", err)
	}
	view, _ = boards.View(d, who, boardID)
	if view.Sections[0].Collapsed || view.HasOverlay {
		t.Fatal("expected overlay cleared")
	}
}

func TestHistoryAndRestore(t *testing.T) {
	d := openTestDB(t)
	u := addUser(t, d, "a@b.c", enums.RoleUser)
	who, _ := access.Load(d, u.ID)
	space, _ := content.PersonalSpace(d, u.ID)
	boardID, _ := boards.Create(d, who, space.ID, "B")
	w := addWidget(t, d, space.ID, "w1")

	view, _ := boards.View(d, who, boardID)
	sectionID := view.Sections[0].ID
	boards.Place(d, who, sectionID, w.ID, view.Version)

	history, err := boards.History(d, who, boardID)
	if err != nil || len(history) < 2 {
		t.Fatalf("expected at least 2 revisions (create + place), got %d err=%v", len(history), err)
	}

	// Restore the first revision (empty section, no widgets).
	oldest := history[len(history)-1]
	if err := boards.Restore(d, who, boardID, oldest.ID); err != nil {
		t.Fatalf("restore: %v", err)
	}
	view, _ = boards.View(d, who, boardID)
	if len(view.Sections[0].Tiles) != 0 {
		t.Fatalf("expected restored board to have no tiles, got %+v", view.Sections[0].Tiles)
	}
}
