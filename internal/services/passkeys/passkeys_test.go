package passkeys_test

import (
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"andon/internal/db"
	"andon/internal/db/dbtest"
	"andon/internal/enums"
	"andon/internal/model"
	authrepo "andon/internal/repos/auth"
	"andon/internal/repos/users"
	"andon/internal/services/access"
	"andon/internal/services/passkeys"
	"andon/internal/services/util"
	"andon/internal/settings"
)

var cfg = settings.Settings{BaseURL: "https://dash.example.org:8443"}

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"), dbtest.Key)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func addUser(t *testing.T, d *sql.DB, email string) *access.Principal {
	t.Helper()
	u := &model.User{Email: email, Name: email, Role: enums.RoleUser, IsActive: true,
		Locale: enums.LocaleDE, ColorMode: enums.ColorAuto, CreatedAt: time.Now().UTC()}
	if err := users.Add(d, u); err != nil {
		t.Fatalf("add user: %v", err)
	}
	who, err := access.Load(d, u.ID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	return who
}

func TestBeginUsesBaseURLHost(t *testing.T) {
	d := openTestDB(t)
	who := addUser(t, d, "a@b.c")

	raw, err := passkeys.Begin(d, cfg, who)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	var options struct {
		PublicKey struct {
			RP struct{ ID string }
		}
	}
	if err := json.Unmarshal(raw, &options); err != nil || options.PublicKey.RP.ID != "dash.example.org" {
		t.Fatalf("rp id: %+v err=%v", options, err)
	}
}

func TestFinishLoginNeedsKnownCeremony(t *testing.T) {
	d := openTestDB(t)
	_, err := passkeys.FinishLogin(d, cfg, "unknown", strings.NewReader("{}"), "", "")
	if !errors.Is(err, passkeys.ErrCeremony) {
		t.Fatalf("err = %v", err)
	}

	// A ceremony is good once.
	id, _, err := passkeys.BeginLogin(cfg)
	if err != nil {
		t.Fatalf("begin login: %v", err)
	}
	if _, err := passkeys.FinishLogin(d, cfg, id, strings.NewReader("{}"), "", ""); !errors.Is(err, passkeys.ErrInvalid) {
		t.Fatalf("first finish: %v", err)
	}
	if _, err := passkeys.FinishLogin(d, cfg, id, strings.NewReader("{}"), "", ""); !errors.Is(err, passkeys.ErrCeremony) {
		t.Fatalf("second finish: %v", err)
	}
}

func TestRemoveOnlyOwnPasskey(t *testing.T) {
	d := openTestDB(t)
	owner := addUser(t, d, "a@b.c")
	other := addUser(t, d, "x@y.z")
	key := &model.Passkey{UserID: owner.UserID, CredID: "c1", Name: "Laptop", Data: "{}", CreatedAt: time.Now().UTC()}
	if err := authrepo.AddPasskey(d, key); err != nil {
		t.Fatalf("add: %v", err)
	}

	if err := passkeys.Remove(d, other, key.ID, ""); !errors.Is(err, util.ErrNotFound) {
		t.Fatalf("foreign remove: %v", err)
	}
	if err := passkeys.Remove(d, owner, key.ID, ""); err != nil {
		t.Fatalf("own remove: %v", err)
	}
}
