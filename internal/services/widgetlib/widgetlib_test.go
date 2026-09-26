package widgetlib_test

import (
	"context"
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
	"andon/internal/rules"
	"andon/internal/services/access"
	"andon/internal/services/hints"
	"andon/internal/services/svcdata"
	"andon/internal/services/widgetlib"
	"andon/internal/widgets"
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

func TestCreateGetUpdateDelete(t *testing.T) {
	d := openTestDB(t)
	u := addUser(t, d, "a@b.c")
	who, _ := access.Load(d, u.ID)
	space, _ := content.PersonalSpace(d, u.ID)

	id, err := widgetlib.Create(d, who, space.ID, "note", "My Note", map[string]any{"text": "hi"}, nil, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	w, right, err := widgetlib.Detail(d, who, id)
	if err != nil || w.Title != "My Note" || right < enums.RightEdit {
		t.Fatalf("detail: %+v right=%v err=%v", w, right, err)
	}

	if err := widgetlib.Update(d, who, id, w.Version, "Renamed", map[string]any{"text": "bye"}, nil, nil); err != nil {
		t.Fatalf("update: %v", err)
	}
	w, _, _ = widgetlib.Detail(d, who, id)
	if w.Title != "Renamed" || w.Config["text"] != "bye" {
		t.Fatalf("expected update to persist, got %+v", w)
	}

	if err := widgetlib.Delete(d, who, id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, _, err := widgetlib.Detail(d, who, id); !errors.Is(err, widgetlib.ErrNotFound) {
		t.Fatalf("expected not found after delete, got %v", err)
	}
}

func TestCreateRejectsUnknownType(t *testing.T) {
	d := openTestDB(t)
	u := addUser(t, d, "a@b.c")
	who, _ := access.Load(d, u.ID)
	space, _ := content.PersonalSpace(d, u.ID)

	if _, err := widgetlib.Create(d, who, space.ID, "nope", "X", nil, nil, nil); !errors.Is(err, widgetlib.ErrUnknownType) {
		t.Fatalf("expected ErrUnknownType, got %v", err)
	}
}

func TestCreateRequiresConnectionForServiceWidget(t *testing.T) {
	d := openTestDB(t)
	u := addUser(t, d, "a@b.c")
	who, _ := access.Load(d, u.ID)
	space, _ := content.PersonalSpace(d, u.ID)

	// "sysinfo" is tied to ServiceGlances and needs a connection.
	if _, err := widgetlib.Create(d, who, space.ID, "sysinfo", "Sys", nil, nil, nil); !errors.Is(err, widgetlib.ErrConnRequired) {
		t.Fatalf("expected ErrConnRequired, got %v", err)
	}
}

func TestUpdateConflict(t *testing.T) {
	d := openTestDB(t)
	u := addUser(t, d, "a@b.c")
	who, _ := access.Load(d, u.ID)
	space, _ := content.PersonalSpace(d, u.ID)
	id, _ := widgetlib.Create(d, who, space.ID, "note", "N", nil, nil, nil)

	if err := widgetlib.Update(d, who, id, 999, "X", nil, nil, nil); !errors.Is(err, widgetlib.ErrConflict) {
		t.Fatalf("expected conflict, got %v", err)
	}
}

func TestCopyCreatesIndependentWidget(t *testing.T) {
	d := openTestDB(t)
	u := addUser(t, d, "a@b.c")
	who, _ := access.Load(d, u.ID)
	space, _ := content.PersonalSpace(d, u.ID)
	id, _ := widgetlib.Create(d, who, space.ID, "note", "Original", map[string]any{"text": "x"}, nil, nil)

	copyID, err := widgetlib.Copy(d, who, id, space.ID)
	if err != nil {
		t.Fatalf("copy: %v", err)
	}
	if copyID == id {
		t.Fatal("expected a distinct widget id")
	}
	copied, _, _ := widgetlib.Detail(d, who, copyID)
	if copied.Title != "Original" || copied.Config["text"] != "x" {
		t.Fatalf("expected copy to match original, got %+v", copied)
	}
}

func TestLibraryFiltersByRight(t *testing.T) {
	d := openTestDB(t)
	owner := addUser(t, d, "owner@x.de")
	stranger := addUser(t, d, "stranger@x.de")
	ownerWho, _ := access.Load(d, owner.ID)
	strangerWho, _ := access.Load(d, stranger.ID)
	space, _ := content.PersonalSpace(d, owner.ID)
	widgetlib.Create(d, ownerWho, space.ID, "note", "Private", nil, nil, nil)

	lib, err := widgetlib.Library(d, ownerWho)
	if err != nil || len(lib) != 1 {
		t.Fatalf("expected owner to see 1 widget, got %d err=%v", len(lib), err)
	}
	lib, err = widgetlib.Library(d, strangerWho)
	if err != nil || len(lib) != 0 {
		t.Fatalf("expected stranger to see 0 widgets, got %d err=%v", len(lib), err)
	}
}

func TestEffectiveRateUsesPeerKimai(t *testing.T) {
	d := openTestDB(t)
	u := addUser(t, d, "rate@b.c")
	who, _ := access.Load(d, u.ID)
	space, _ := content.PersonalSpace(d, u.ID)

	// Demo Kimai and Invoice Ninja share customer names.
	var ninjaID int64
	for _, svc := range []enums.ServiceType{enums.ServiceKimai, enums.ServiceInvoiceNinja} {
		c := &model.Connection{SpaceID: space.ID, Key: string(svc), Name: string(svc), Service: string(svc),
			URL: "demo://" + string(svc), CredentialMode: enums.CredentialShared, VerifyTLS: true, CreatedAt: time.Now().UTC()}
		if err := content.AddConnection(d, c); err != nil {
			t.Fatalf("add connection: %v", err)
		}
		ninjaID = c.ID
	}

	id, err := widgetlib.Create(d, who, space.ID, "kpi", "Rate", map[string]any{"metric": "effective_rate"}, &ninjaID, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	w, _, _ := widgetlib.Detail(d, who, id)

	// Page views never fetch: the first load is pending and fills the data
	// in the background.
	var frag *widgetlib.Fragment
	for range 100 {
		frag, err = widgetlib.Load(context.Background(), d, who, w, svcdata.Cached)
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if !frag.Slots["data"].Pending && !frag.Slots["kimai"].Pending {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	kpi, ok := frag.View["KPI"].(*widgets.KpiResult)
	if !ok || kpi.Value <= 0 || kpi.SubCount == 0 {
		t.Fatalf("kpi = %+v, slots %+v", kpi, frag.Slots)
	}
}

// TestLiveModeMixesWithBackground: a live KPI fetches its own connection
// on view, while the Kimai peer still comes from the background run.
func TestLiveModeMixesWithBackground(t *testing.T) {
	d := openTestDB(t)
	u := addUser(t, d, "mix@b.c")
	who, _ := access.Load(d, u.ID)
	space, _ := content.PersonalSpace(d, u.ID)

	var ninjaID int64
	for _, svc := range []enums.ServiceType{enums.ServiceKimai, enums.ServiceInvoiceNinja} {
		// A distinct URL per test keeps the process-wide cache apart.
		c := &model.Connection{SpaceID: space.ID, Key: string(svc), Name: string(svc), Service: string(svc),
			URL: "demo://" + string(svc) + "/mix", CredentialMode: enums.CredentialShared, VerifyTLS: true, CreatedAt: time.Now().UTC()}
		if err := content.AddConnection(d, c); err != nil {
			t.Fatal(err)
		}
		ninjaID = c.ID
	}
	id, err := widgetlib.Create(d, who, space.ID, "kpi", "Rate", map[string]any{"metric": "effective_rate", "data_mode": "data_live"}, &ninjaID, nil)
	if err != nil {
		t.Fatal(err)
	}
	w, _, _ := widgetlib.Detail(d, who, id)

	frag, err := widgetlib.Load(context.Background(), d, who, w, svcdata.Cached)
	if err != nil {
		t.Fatal(err)
	}
	if frag.Slots["data"].Pending || frag.Slots["data"].Data == nil {
		t.Fatalf("live slot not fetched on view: %+v", frag.Slots["data"])
	}
	if !frag.Slots["kimai"].Pending {
		t.Fatalf("peer slot fetched on view: %+v", frag.Slots["kimai"])
	}
}

// TestLinkCountsHintsOfSameHost: a link tile without an info connection
// shows the hints of the connection that serves the same host.
func TestLinkCountsHintsOfSameHost(t *testing.T) {
	d := openTestDB(t)
	u := addUser(t, d, "host@b.c")
	who, _ := access.Load(d, u.ID)
	space, _ := content.PersonalSpace(d, u.ID)

	conn := &model.Connection{SpaceID: space.ID, Key: "paperless", Name: "Paperless", Service: string(enums.ServicePaperless),
		URL: "https://pl.example.org", CredentialMode: enums.CredentialShared, VerifyTLS: true, CreatedAt: time.Now().UTC()}
	if err := content.AddConnection(d, conn); err != nil {
		t.Fatalf("add connection: %v", err)
	}
	finding := rules.Finding{Fingerprint: "inbox", Rule: "paperless.inbox", Severity: enums.SeverityWarn, Message: "paperless.inbox",
		Sources: []string{string(enums.ServicePaperless)}}
	if _, err := hints.Sync(d, space.ID, nil, &conn.ID, []string{"paperless.inbox"}, []rules.Finding{finding}); err != nil {
		t.Fatalf("sync: %v", err)
	}

	id, err := widgetlib.Create(d, who, space.ID, "link", "Paperless", map[string]any{"url": "https://pl.example.org/documents"}, nil, nil)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	w, _, _ := widgetlib.Detail(d, who, id)
	frag, err := widgetlib.Load(context.Background(), d, who, w, svcdata.Cached)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if frag.HintCount != 1 || frag.HintConn != conn.ID {
		t.Fatalf("hint count %d on connection %d, want 1 on %d", frag.HintCount, frag.HintConn, conn.ID)
	}
}

// Every service-bound type fills its connection queries from demo data,
// so the gallery can show an example before a connection exists.
func TestDemoFillsServiceTypes(t *testing.T) {
	d := openTestDB(t)
	u := addUser(t, d, "a@b.c")
	who, _ := access.Load(d, u.ID)
	space, _ := content.PersonalSpace(d, u.ID)

	for _, kind := range widgets.AllTypes() {
		if kind.Service == "" {
			continue
		}
		frag, err := widgetlib.Demo(context.Background(), d, who, space.ID, kind.Key, "", nil)
		if err != nil {
			t.Fatalf("%s: %v", kind.Key, err)
		}
		cfg, _ := widgets.Decode(kind.Key, nil)
		for _, q := range kind.Queries(cfg) {
			if q.Conn == widgets.ConnNone {
				continue
			}
			if slot := frag.Slots[q.Name]; slot.Data == nil {
				t.Errorf("%s/%s: no demo data (%q)", kind.Key, q.Name, slot.Error)
			}
		}
	}
}
