package boards_test

import (
	"errors"
	"testing"

	"andon/internal/enums"
	"andon/internal/repos/content"
	"andon/internal/services/access"
	"andon/internal/services/boards"
)

func TestBulkMoveRemoveAndDuplicate(t *testing.T) {
	d := openTestDB(t)
	u := addUser(t, d, "a@b.c", enums.RoleUser)
	who, _ := access.Load(d, u.ID)
	space, _ := content.PersonalSpace(d, u.ID)
	boardID, _ := boards.Create(d, who, space.ID, "B")

	view, _ := boards.View(d, who, boardID)
	first := view.Sections[0].ID
	p1, _ := boards.Place(d, who, first, addWidget(t, d, space.ID, "n1").ID, view.Version)
	view, _ = boards.View(d, who, boardID)
	p2, _ := boards.Place(d, who, first, addWidget(t, d, space.ID, "n2").ID, view.Version)
	view, _ = boards.View(d, who, boardID)
	if _, err := boards.AddSection(d, who, boardID, view.Version, "Zwei"); err != nil {
		t.Fatal(err)
	}
	view, _ = boards.View(d, who, boardID)
	second := view.Sections[1].ID

	if err := boards.Bulk(d, who, boardID, view.Version, nil, boards.BulkChange{Action: boards.BulkMove}); !errors.Is(err, boards.ErrBulk) {
		t.Fatalf("empty selection: %v", err)
	}
	if err := boards.Bulk(d, who, boardID, view.Version, []int64{p1, p2}, boards.BulkChange{Action: boards.BulkMove, SectionID: second}); err != nil {
		t.Fatal(err)
	}
	view, _ = boards.View(d, who, boardID)
	if len(view.Sections[0].Tiles) != 0 || len(view.Sections[1].Tiles) != 2 {
		t.Fatalf("move: %+v", view.Sections)
	}

	copyID, err := boards.Duplicate(d, who, boardID, "Kopie")
	if err != nil {
		t.Fatal(err)
	}
	dup, _ := boards.View(d, who, copyID)
	if dup.Name != "Kopie" || len(dup.Sections) != 2 || len(dup.Sections[1].Tiles) != 2 {
		t.Fatalf("duplicate: %+v", dup)
	}

	if err := boards.Bulk(d, who, boardID, view.Version, []int64{p1}, boards.BulkChange{Action: boards.BulkRemove}); err != nil {
		t.Fatal(err)
	}
	view, _ = boards.View(d, who, boardID)
	dup, _ = boards.View(d, who, copyID)
	if len(view.Sections[1].Tiles) != 1 || len(dup.Sections[1].Tiles) != 2 {
		t.Fatalf("remove touched copy: %+v / %+v", view.Sections, dup.Sections)
	}
}
