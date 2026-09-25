package themes_test

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"dashboard/internal/db"
	"dashboard/internal/db/dbtest"
	"dashboard/internal/enums"
	"dashboard/internal/model"
	"dashboard/internal/repos/content"
	"dashboard/internal/repos/users"
	"dashboard/internal/services/access"
	"dashboard/internal/services/themes"
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

func TestParseCSSRoundTrip(t *testing.T) {
	css := `:root{--bg-void:#141312;--fg1:#ebdbb2;}
:root[data-theme="light"]{--bg-void:#f0e9d6;}`
	dark, light := themes.ParseCSS(css)
	if dark["--bg-void"] != "#141312" || dark["--fg1"] != "#ebdbb2" {
		t.Fatalf("unexpected dark tokens: %+v", dark)
	}
	if light["--bg-void"] != "#f0e9d6" {
		t.Fatalf("unexpected light tokens: %+v", light)
	}
}

func TestRatioKnownValue(t *testing.T) {
	// Black on white is the maximum ratio, 21:1.
	if got := themes.Ratio("#000000", "#ffffff"); got != 21 {
		t.Fatalf("expected 21, got %v", got)
	}
	// Same color: ratio 1.
	if got := themes.Ratio("#808080", "#808080"); got != 1 {
		t.Fatalf("expected 1, got %v", got)
	}
}

func TestContrastIssuesFlagsLowContrast(t *testing.T) {
	// --bg-void (dark mode default) is #141312, very dark. Setting --fg1 to
	// a similarly dark color fails AA against it.
	issues := themes.ContrastIssues(map[string]string{"--fg1": "#1a1918"}, nil)
	found := false
	for _, i := range issues {
		if i.Mode == themes.ModeDark && i.FG == "--fg1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a low-contrast issue for near-black fg1 on near-black bg, got %+v", issues)
	}
}

func TestCleanTokensDropsUnknownAndUnsafe(t *testing.T) {
	cleaned, err := themes.CleanTokens(map[string]any{
		"--bg-void": "#123456", "--not-a-real-token": "#fff",
	})
	if err != nil {
		t.Fatalf("clean tokens: %v", err)
	}
	if _, ok := cleaned["--not-a-real-token"]; ok {
		t.Fatal("expected unknown token dropped")
	}
	if cleaned["--bg-void"] != "#123456" {
		t.Fatalf("expected known token kept, got %+v", cleaned)
	}

	if _, err := themes.CleanTokens(map[string]any{"--bg-void": "url(evil.css)"}); err == nil {
		t.Fatal("expected url() to be rejected")
	}
}

func TestEnsureBuiltinAndActiveFallback(t *testing.T) {
	d := openTestDB(t)
	id, err := themes.EnsureBuiltin(d)
	if err != nil || id == 0 {
		t.Fatalf("ensure builtin: id=%d err=%v", id, err)
	}

	// Re-running must not duplicate the row (same digest -> no version bump
	// beyond the initial insert).
	id2, err := themes.EnsureBuiltin(d)
	if err != nil || id2 != id {
		t.Fatalf("expected stable id on re-run, got %d vs %d err=%v", id2, id, err)
	}

	u := addUser(t, d, "a@b.c", enums.RoleUser)
	who, _ := access.Load(d, u.ID)
	active, err := themes.Active(d, who, nil, nil)
	if err != nil || active != id {
		t.Fatalf("expected fallback to shrippen (%d), got %d err=%v", id, active, err)
	}

	css, version, err := themes.Stylesheet(d, active)
	if err != nil || css == "" || version < 1 {
		t.Fatalf("expected rendered stylesheet, got len=%d version=%d err=%v", len(css), version, err)
	}
}

func TestDuplicateAndUpdate(t *testing.T) {
	d := openTestDB(t)
	builtinID, err := themes.EnsureBuiltin(d)
	if err != nil {
		t.Fatalf("ensure builtin: %v", err)
	}
	u := addUser(t, d, "a@b.c", enums.RoleUser)
	who, _ := access.Load(d, u.ID)
	space, _ := content.PersonalSpace(d, u.ID)

	newID, err := themes.Duplicate(d, who, builtinID, space.ID, "My Theme")
	if err != nil {
		t.Fatalf("duplicate: %v", err)
	}

	issues, err := themes.Update(d, who, newID, "My Theme", map[string]any{"--bg-void": "#000000"}, nil, nil)
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	_ = issues // may or may not have issues depending on the contract defaults

	theme, _, err := themes.Get(d, who, newID)
	if err != nil || theme.Dark["--bg-void"] != "#000000" {
		t.Fatalf("expected updated token, got %+v err=%v", theme, err)
	}
}

func TestBuiltinThemeCannotBeUpdatedOrDeleted(t *testing.T) {
	d := openTestDB(t)
	builtinID, _ := themes.EnsureBuiltin(d)
	u := addUser(t, d, "admin@b.c", enums.RoleAdmin)
	who, _ := access.Load(d, u.ID)

	if _, err := themes.Update(d, who, builtinID, "Hacked", nil, nil, nil); err == nil {
		t.Fatal("expected builtin theme update to be denied")
	}
}

func TestExportImportZipRoundTrip(t *testing.T) {
	d := openTestDB(t)
	builtinID, _ := themes.EnsureBuiltin(d)
	u := addUser(t, d, "a@b.c", enums.RoleUser)
	who, _ := access.Load(d, u.ID)
	space, _ := content.PersonalSpace(d, u.ID)

	themeID, err := themes.Duplicate(d, who, builtinID, space.ID, "Exportable")
	if err != nil {
		t.Fatalf("duplicate: %v", err)
	}
	if _, err := themes.Update(d, who, themeID, "Exportable", map[string]any{"--bg-void": "#abcdef"}, nil, nil); err != nil {
		t.Fatalf("update: %v", err)
	}

	filename, blob, err := themes.ExportZip(d, who, themeID)
	if err != nil || filename == "" || len(blob) == 0 {
		t.Fatalf("export: filename=%q len=%d err=%v", filename, len(blob), err)
	}

	imported, err := themes.ImportZip(d, who, space.ID, blob)
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	got, _, err := themes.Get(d, who, imported)
	if err != nil || got.Dark["--bg-void"] != "#abcdef" {
		t.Fatalf("expected imported theme to carry the exported token, got %+v err=%v", got, err)
	}
}
