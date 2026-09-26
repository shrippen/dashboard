package shares_test

import (
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"andon/internal/db"
	"andon/internal/db/dbtest"
	"andon/internal/enums"
	"andon/internal/model"
	"andon/internal/repos/content"
	"andon/internal/repos/users"
	"andon/internal/services/access"
	"andon/internal/services/shares"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"), dbtest.Key)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func addUser(t *testing.T, q db.Queryer, email string) *model.User {
	t.Helper()
	u := &model.User{Email: email, Name: email, Role: enums.RoleUser, IsActive: true,
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

func TestGrantAndRevoke(t *testing.T) {
	d := openTestDB(t)
	owner := addUser(t, d, "owner@x.de")
	viewer := addUser(t, d, "viewer@x.de")
	ownerWho, _ := access.Load(d, owner.ID)
	viewerWho, _ := access.Load(d, viewer.ID)

	space, _ := content.PersonalSpace(d, owner.ID)
	board := &model.Board{SpaceID: space.ID, Slug: "b", Name: "Board", Version: 1}
	if err := content.AddBoard(d, board); err != nil {
		t.Fatalf("add board: %v", err)
	}

	if err := shares.Grant(d, ownerWho, enums.ResourceBoard, board.ID, enums.GranteeUser, viewer.ID, enums.RightView); err != nil {
		t.Fatalf("grant: %v", err)
	}

	info, err := shares.Info(d, ownerWho, enums.ResourceBoard, board.ID)
	if err != nil || len(info.Shares) != 1 || info.Shares[0].GranteeName != "viewer@x.de" {
		t.Fatalf("expected 1 share to viewer, got %+v err=%v", info, err)
	}

	// The grantee alone may not manage the share dialog.
	if _, err := shares.Info(d, viewerWho, enums.ResourceBoard, board.ID); err == nil {
		t.Fatal("expected viewer to be denied MANAGE on the share dialog")
	}

	if err := shares.Revoke(d, ownerWho, info.Shares[0].ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	info, err = shares.Info(d, ownerWho, enums.ResourceBoard, board.ID)
	if err != nil || len(info.Shares) != 0 {
		t.Fatalf("expected no shares after revoke, got %+v err=%v", info, err)
	}
}

func TestGrantRejectsUnshareableRight(t *testing.T) {
	d := openTestDB(t)
	owner := addUser(t, d, "owner@x.de")
	ownerWho, _ := access.Load(d, owner.ID)
	space, _ := content.PersonalSpace(d, owner.ID)
	board := &model.Board{SpaceID: space.ID, Slug: "b", Name: "Board", Version: 1}
	content.AddBoard(d, board)

	if err := shares.Grant(d, ownerWho, enums.ResourceBoard, board.ID, enums.GranteeUser, 999, enums.RightNone); !errors.Is(err, shares.ErrRight) {
		t.Fatalf("expected ErrRight for NONE, got %v", err)
	}
}
