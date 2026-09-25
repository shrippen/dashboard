package misc_test

import (
	"path/filepath"
	"testing"
	"time"

	"dashboard/internal/db"
	"dashboard/internal/enums"
	"dashboard/internal/model"
	"dashboard/internal/repos/misc"
)

func openTestDB(t *testing.T) db.Queryer {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestSharesToUserAndTeam(t *testing.T) {
	q := openTestDB(t)
	if err := misc.AddShare(q, &model.Share{
		ResourceKind: enums.ResourceBoard, ResourceID: 1,
		GranteeKind: enums.GranteeUser, GranteeID: 42, Right: enums.RightView,
	}); err != nil {
		t.Fatalf("add user share: %v", err)
	}
	if err := misc.AddShare(q, &model.Share{
		ResourceKind: enums.ResourceBoard, ResourceID: 2,
		GranteeKind: enums.GranteeTeam, GranteeID: 7, Right: enums.RightEdit,
	}); err != nil {
		t.Fatalf("add team share: %v", err)
	}

	shares, err := misc.SharesTo(q, 42, []int64{7})
	if err != nil {
		t.Fatalf("shares to: %v", err)
	}
	if len(shares) != 2 {
		t.Fatalf("expected 2 shares (direct + via team), got %d", len(shares))
	}

	none, err := misc.SharesTo(q, 999, nil)
	if err != nil {
		t.Fatalf("shares to (none): %v", err)
	}
	if len(none) != 0 {
		t.Fatalf("expected no shares for unrelated user, got %d", len(none))
	}
}

func TestDropSharesRemovesAllOnResource(t *testing.T) {
	q := openTestDB(t)
	misc.AddShare(q, &model.Share{ResourceKind: enums.ResourceBoard, ResourceID: 1,
		GranteeKind: enums.GranteeUser, GranteeID: 1, Right: enums.RightView})
	misc.AddShare(q, &model.Share{ResourceKind: enums.ResourceBoard, ResourceID: 1,
		GranteeKind: enums.GranteeUser, GranteeID: 2, Right: enums.RightUse})

	if err := misc.DropShares(q, enums.ResourceBoard, 1); err != nil {
		t.Fatalf("drop shares: %v", err)
	}
	left, err := misc.SharesFor(q, enums.ResourceBoard, 1)
	if err != nil || len(left) != 0 {
		t.Fatalf("expected no shares left, got %d err=%v", len(left), err)
	}
}

func TestThemeRoundTrip(t *testing.T) {
	q := openTestDB(t)
	th := &model.Theme{Slug: "shrippen", Name: "Shrippen", Builtin: true, Contract: 1,
		Dark: map[string]any{"bg": "#000"}, Light: map[string]any{"bg": "#fff"},
		Fonts: []string{"Inter"}, Version: 1}
	if err := misc.AddTheme(q, th); err != nil {
		t.Fatalf("add theme: %v", err)
	}

	got, err := misc.BuiltinTheme(q, "shrippen")
	if err != nil || got == nil {
		t.Fatalf("expected builtin theme, got %+v err=%v", got, err)
	}
	if got.Dark["bg"] != "#000" || len(got.Fonts) != 1 {
		t.Fatalf("theme JSON not round-tripped: %+v", got)
	}
}

func TestInstanceSettingUpsert(t *testing.T) {
	q := openTestDB(t)
	if err := misc.SetSetting(q, "branding", map[string]any{"name": "IT"}); err != nil {
		t.Fatalf("set: %v", err)
	}
	got, err := misc.Setting(q, "branding")
	if err != nil || got["name"] != "IT" {
		t.Fatalf("expected setting round-trip, got %+v err=%v", got, err)
	}

	if err := misc.SetSetting(q, "branding", map[string]any{"name": "IT2"}); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err = misc.Setting(q, "branding")
	if err != nil || got["name"] != "IT2" {
		t.Fatalf("expected updated setting, got %+v err=%v", got, err)
	}
}

func TestSettingMissingReturnsEmptyMap(t *testing.T) {
	q := openTestDB(t)
	got, err := misc.Setting(q, "nope")
	if err != nil {
		t.Fatalf("setting: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty map, got %+v", got)
	}
}

func TestAuditPageOrdersNewestFirst(t *testing.T) {
	q := openTestDB(t)
	base, err := misc.AuditPage(q, nil)
	if err != nil {
		t.Fatalf("audit page: %v", err)
	}
	if len(base) != 0 {
		t.Fatalf("expected empty audit log, got %d", len(base))
	}

	now := time.Now().UTC()
	older := now.Add(-time.Hour)
	if err := misc.Audit(q, &model.AuditEntry{At: older, Action: "login", Target: "a@b.c"}); err != nil {
		t.Fatalf("audit 1: %v", err)
	}
	if err := misc.Audit(q, &model.AuditEntry{At: now, Action: "logout", Target: "a@b.c"}); err != nil {
		t.Fatalf("audit 2: %v", err)
	}

	page, err := misc.AuditPage(q, nil)
	if err != nil || len(page) != 2 {
		t.Fatalf("expected 2 entries, got %d err=%v", len(page), err)
	}
	if page[0].Action != "logout" {
		t.Fatalf("expected newest first, got %+v", page[0])
	}
}
