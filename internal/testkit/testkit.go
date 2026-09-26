// Package testkit sets up what service tests share: an encrypted test
// database, users with their personal space, connections and placed
// widgets.
package testkit

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"andon/internal/crypto"
	"andon/internal/db"
	"andon/internal/db/dbtest"
	"andon/internal/enums"
	"andon/internal/model"
	"andon/internal/repos/content"
	"andon/internal/repos/users"
	"andon/internal/services/access"
	"andon/internal/services/boards"
	"andon/internal/services/connections"
	"andon/internal/services/widgetlib"
)

// masterKey encrypts test secrets.
const masterKey = "test-master-key"

// DB opens a fresh, migrated database that closes with the test.
func DB(t *testing.T) *sql.DB {
	t.Helper()
	crypto.Init(masterKey)
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"), dbtest.Key)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// User adds a user with role and a personal space, and returns the
// user's principal and space id.
func User(t *testing.T, d *sql.DB, email string, role enums.InstanceRole) (*access.Principal, int64) {
	t.Helper()
	u := &model.User{Email: email, Name: email, Role: role, IsActive: true,
		Locale: enums.LocaleDE, ColorMode: enums.ColorAuto, CreatedAt: time.Now().UTC()}
	if err := users.Add(d, u); err != nil {
		t.Fatalf("add user: %v", err)
	}
	space := &model.Space{Kind: enums.SpacePersonal, Name: u.Name, OwnerUserID: &u.ID, Version: 1}
	if err := content.AddSpace(d, space); err != nil {
		t.Fatalf("add space: %v", err)
	}
	who, err := access.Load(d, u.ID)
	if err != nil {
		t.Fatalf("load principal: %v", err)
	}
	return who, space.ID
}

// Conn adds a shared connection with token "tok" and returns its id.
func Conn(t *testing.T, d *sql.DB, who *access.Principal, space int64, service enums.ServiceType, url string) int64 {
	t.Helper()
	id, err := connections.Create(d, who, space, service, string(service), url, enums.CredentialShared, "tok", connections.TLSVerify, nil)
	if err != nil {
		t.Fatalf("connection: %v", err)
	}
	return id
}

// Place puts a new widget on a fresh board and returns the placement id.
func Place(t *testing.T, d *sql.DB, who *access.Principal, space int64, typeKey string, config map[string]any, conn *int64) int64 {
	t.Helper()
	widget, err := widgetlib.Create(d, who, space, typeKey, "", config, conn, nil)
	if err != nil {
		t.Fatalf("widget: %v", err)
	}
	board, err := boards.Create(d, who, space, "Test")
	if err != nil {
		t.Fatalf("board: %v", err)
	}
	view, err := boards.View(d, who, board)
	if err != nil {
		t.Fatalf("view: %v", err)
	}
	placement, err := boards.Place(d, who, view.Sections[0].ID, widget, view.Version)
	if err != nil {
		t.Fatalf("place: %v", err)
	}
	return placement
}
