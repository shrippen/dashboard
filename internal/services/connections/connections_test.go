package connections_test

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
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
	"andon/internal/services/connections"
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

// tokenServer answers the Kimai version check only for the token "tok".
func tokenServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Authorization"), "tok") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{"version": "2.30.0"}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestPersonalTokenFromForm: a token entered with a personal connection
// is the creator's own token, so their test succeeds.
func TestPersonalTokenFromForm(t *testing.T) {
	srv := tokenServer(t)
	d := openTestDB(t)
	u := addUser(t, d, "a@b.c")
	who, _ := access.Load(d, u.ID)
	space, _ := content.PersonalSpace(d, u.ID)

	id, err := connections.Create(d, who, space.ID, enums.ServiceKimai, "Kimai", srv.URL,
		enums.CredentialPersonal, "tok", connections.TLSVerify, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if result, err := connections.Test(context.Background(), d, who, id); err != nil || !result.Ok {
		t.Fatalf("create personal: expected ok, got %+v err=%v", result, err)
	}

	tok := "tok"
	shared, err := connections.Create(d, who, space.ID, enums.ServiceKimai, "Kimai 2", srv.URL,
		enums.CredentialShared, "old", connections.TLSVerify, nil)
	if err != nil {
		t.Fatalf("create shared: %v", err)
	}
	if err := connections.Update(d, who, shared, "Kimai 2", srv.URL, enums.CredentialPersonal, &tok, connections.TLSVerify, nil); err != nil {
		t.Fatalf("update: %v", err)
	}
	if result, err := connections.Test(context.Background(), d, who, shared); err != nil || !result.Ok {
		t.Fatalf("update to personal: expected ok, got %+v err=%v", result, err)
	}
}

// TestSwitchToPersonalKeepsEditorToken: switching a shared connection to
// personal without a new token keeps the old one as the editor's own.
func TestSwitchToPersonalKeepsEditorToken(t *testing.T) {
	srv := tokenServer(t)
	d := openTestDB(t)
	u := addUser(t, d, "a@b.c")
	who, _ := access.Load(d, u.ID)
	space, _ := content.PersonalSpace(d, u.ID)

	id, err := connections.Create(d, who, space.ID, enums.ServiceKimai, "Kimai", srv.URL,
		enums.CredentialShared, "tok", connections.TLSVerify, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := connections.Update(d, who, id, "Kimai", srv.URL, enums.CredentialPersonal, nil, connections.TLSVerify, nil); err != nil {
		t.Fatalf("update: %v", err)
	}
	if result, err := connections.Test(context.Background(), d, who, id); err != nil || !result.Ok {
		t.Fatalf("expected ok after switch, got %+v err=%v", result, err)
	}
}
