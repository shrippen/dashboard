package connections_test

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"dashboard/internal/crypto"
	"dashboard/internal/db"
	"dashboard/internal/db/dbtest"
	"dashboard/internal/enums"
	"dashboard/internal/model"
	"dashboard/internal/repos/content"
	"dashboard/internal/repos/users"
	"dashboard/internal/services/access"
	"dashboard/internal/services/connections"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	crypto.Init("test-master-key")
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

func TestCreateGetUpdateDelete(t *testing.T) {
	d := openTestDB(t)
	u := addUser(t, d, "a@b.c")
	who, _ := access.Load(d, u.ID)
	space, _ := content.PersonalSpace(d, u.ID)

	id, err := connections.Create(d, who, space.ID, enums.ServiceKimai, "My Kimai", "https://kimai.example/",
		enums.CredentialShared, "secret-token", connections.TLSVerify, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	view, err := connections.Get(d, who, id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if view.Name != "My Kimai" || view.URL != "https://kimai.example" || !view.HasSecret {
		t.Fatalf("unexpected view: %+v", view)
	}

	newSecret := "rotated-token"
	if err := connections.Update(d, who, id, "Renamed", "https://kimai2.example", enums.CredentialShared,
		&newSecret, connections.TLSVerify, nil); err != nil {
		t.Fatalf("update: %v", err)
	}
	view, err = connections.Get(d, who, id)
	if err != nil || view.Name != "Renamed" {
		t.Fatalf("expected rename to persist, got %+v err=%v", view, err)
	}

	if err := connections.Delete(d, who, id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := connections.Get(d, who, id); err == nil {
		t.Fatal("expected deleted connection to be gone")
	}
}

func TestSetMineAndDropMine(t *testing.T) {
	d := openTestDB(t)
	u := addUser(t, d, "a@b.c")
	who, _ := access.Load(d, u.ID)
	space, _ := content.PersonalSpace(d, u.ID)

	id, err := connections.Create(d, who, space.ID, enums.ServiceKimai, "Kimai", "https://kimai.example",
		enums.CredentialPersonal, "", connections.TLSVerify, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	view, _ := connections.Get(d, who, id)
	if view.HasMine {
		t.Fatal("expected no personal credential yet")
	}
	if err := connections.SetMine(d, who, id, "my-token"); err != nil {
		t.Fatalf("set mine: %v", err)
	}
	view, _ = connections.Get(d, who, id)
	if !view.HasMine {
		t.Fatal("expected personal credential set")
	}
	if err := connections.DropMine(d, who, id); err != nil {
		t.Fatalf("drop mine: %v", err)
	}
	view, _ = connections.Get(d, who, id)
	if view.HasMine {
		t.Fatal("expected personal credential dropped")
	}
}

func TestTestConnectionReportsMissingCredential(t *testing.T) {
	d := openTestDB(t)
	u := addUser(t, d, "a@b.c")
	who, _ := access.Load(d, u.ID)
	space, _ := content.PersonalSpace(d, u.ID)

	id, err := connections.Create(d, who, space.ID, enums.ServiceKimai, "Kimai", "https://kimai.example",
		enums.CredentialPersonal, "", connections.TLSVerify, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	result, err := connections.Test(context.Background(), d, who, id)
	if err != nil {
		t.Fatalf("test: %v", err)
	}
	if result.Ok || result.Message != "credential.missing" {
		t.Fatalf("expected credential.missing, got %+v", result)
	}
}

func TestTestConnectionSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"version": "2.30.0"}`))
	}))
	defer srv.Close()

	d := openTestDB(t)
	u := addUser(t, d, "a@b.c")
	who, _ := access.Load(d, u.ID)
	space, _ := content.PersonalSpace(d, u.ID)

	id, err := connections.Create(d, who, space.ID, enums.ServiceKimai, "Kimai", srv.URL,
		enums.CredentialShared, "tok", connections.TLSVerify, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	result, err := connections.Test(context.Background(), d, who, id)
	if err != nil {
		t.Fatalf("test: %v", err)
	}
	if !result.Ok || result.Version != "2.30.0" {
		t.Fatalf("expected successful test with version, got %+v", result)
	}
}
