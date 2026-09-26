package data_test

import (
	"path/filepath"
	"testing"
	"time"

	"andon/internal/db"
	"andon/internal/db/dbtest"
	"andon/internal/enums"
	"andon/internal/model"
	"andon/internal/repos/content"
	"andon/internal/repos/data"
	"andon/internal/repos/users"
)

func openTestDB(t *testing.T) db.Queryer {
	t.Helper()
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"), dbtest.Key)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// spaceID creates a real space row, since hints have a foreign key on it.
func spaceID(t *testing.T, q db.Queryer) int64 {
	t.Helper()
	sp := &model.Space{Kind: enums.SpacePersonal, Name: "x", Version: 1}
	if err := content.AddSpace(q, sp); err != nil {
		t.Fatalf("add space: %v", err)
	}
	return sp.ID
}

// userID creates a real user row, since several tables key marks/hints/log
// entries to it via foreign key.
func userID(t *testing.T, q db.Queryer, email string) int64 {
	t.Helper()
	u := &model.User{Email: email, Name: "x", Role: enums.RoleUser, IsActive: true,
		Locale: enums.LocaleDE, ColorMode: enums.ColorAuto, CreatedAt: time.Now().UTC()}
	if err := users.Add(q, u); err != nil {
		t.Fatalf("add user: %v", err)
	}
	return u.ID
}

func TestHintByPrintScopedByOwner(t *testing.T) {
	q := openTestDB(t)
	sid := spaceID(t, q)
	now := time.Now().UTC()
	shared := &model.Hint{SpaceID: sid, Fingerprint: "1:x", Rule: "r", Severity: enums.SeverityWarn,
		Message: "m", FirstSeen: now, LastSeen: now}
	if err := data.AddHint(q, shared); err != nil {
		t.Fatalf("add shared hint: %v", err)
	}
	owner := userID(t, q, "owner@x.y")
	personal := &model.Hint{SpaceID: sid, UserID: &owner, Fingerprint: "1:x", Rule: "r",
		Severity: enums.SeverityWarn, Message: "m", FirstSeen: now, LastSeen: now}
	if err := data.AddHint(q, personal); err != nil {
		t.Fatalf("add personal hint: %v", err)
	}

	got, err := data.HintByPrint(q, sid, nil, "1:x")
	if err != nil || got == nil || got.ID != shared.ID {
		t.Fatalf("expected shared hint, got %+v err=%v", got, err)
	}
	got, err = data.HintByPrint(q, sid, &owner, "1:x")
	if err != nil || got == nil || got.ID != personal.ID {
		t.Fatalf("expected personal hint, got %+v err=%v", got, err)
	}
}

func TestHintsInVisibleToOwnerAndSharedOnly(t *testing.T) {
	q := openTestDB(t)
	sid := spaceID(t, q)
	now := time.Now().UTC()
	owner := userID(t, q, "owner@x.y")
	other := userID(t, q, "other@x.y")
	data.AddHint(q, &model.Hint{SpaceID: sid, Fingerprint: "a", Rule: "r", Severity: enums.SeverityInfo,
		Message: "shared", FirstSeen: now, LastSeen: now})
	data.AddHint(q, &model.Hint{SpaceID: sid, UserID: &owner, Fingerprint: "b", Rule: "r",
		Severity: enums.SeverityInfo, Message: "mine", FirstSeen: now, LastSeen: now})
	data.AddHint(q, &model.Hint{SpaceID: sid, UserID: &other, Fingerprint: "c", Rule: "r",
		Severity: enums.SeverityInfo, Message: "not mine", FirstSeen: now, LastSeen: now})

	got, err := data.HintsIn(q, []int64{sid}, owner)
	if err != nil {
		t.Fatalf("hints in: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected shared + own hint (2), got %d", len(got))
	}
}

func TestMarkTeamWideVsPerUser(t *testing.T) {
	q := openTestDB(t)
	sid := spaceID(t, q)
	now := time.Now().UTC()
	h := &model.Hint{SpaceID: sid, Fingerprint: "x", Rule: "r", Severity: enums.SeverityWarn,
		Message: "m", FirstSeen: now, LastSeen: now}
	data.AddHint(q, h)

	if err := data.SetMark(q, &model.HintMark{HintID: h.ID, State: enums.HintAcknowledged, At: now}); err != nil {
		t.Fatalf("set team mark: %v", err)
	}
	got, err := data.Mark(q, h.ID, nil)
	if err != nil || got == nil || got.State != enums.HintAcknowledged {
		t.Fatalf("expected team-wide mark, got %+v err=%v", got, err)
	}

	uid := userID(t, q, "u@x.y")
	if err := data.SetMark(q, &model.HintMark{HintID: h.ID, UserID: &uid, State: enums.HintSnoozed, At: now}); err != nil {
		t.Fatalf("set per-user mark: %v", err)
	}
	got, err = data.Mark(q, h.ID, &uid)
	if err != nil || got == nil || got.State != enums.HintSnoozed {
		t.Fatalf("expected per-user mark, got %+v err=%v", got, err)
	}
	// Team-wide mark must be unaffected by the per-user one.
	got, err = data.Mark(q, h.ID, nil)
	if err != nil || got == nil || got.State != enums.HintAcknowledged {
		t.Fatalf("expected team-wide mark unchanged, got %+v err=%v", got, err)
	}
}

func TestDropMarksRemovesAll(t *testing.T) {
	q := openTestDB(t)
	sid := spaceID(t, q)
	now := time.Now().UTC()
	h := &model.Hint{SpaceID: sid, Fingerprint: "x", Rule: "r", Severity: enums.SeverityWarn,
		Message: "m", FirstSeen: now, LastSeen: now}
	data.AddHint(q, h)
	data.SetMark(q, &model.HintMark{HintID: h.ID, State: enums.HintAcknowledged, At: now})

	if err := data.DropMarks(q, h.ID); err != nil {
		t.Fatalf("drop marks: %v", err)
	}
	got, err := data.Mark(q, h.ID, nil)
	if err != nil || got != nil {
		t.Fatalf("expected no marks left, got %+v err=%v", got, err)
	}
}

func TestPutPointUpsert(t *testing.T) {
	q := openTestDB(t)
	if err := data.PutPoint(q, "scope", "revenue", "2026-01-01", 100); err != nil {
		t.Fatalf("put point: %v", err)
	}
	if err := data.PutPoint(q, "scope", "revenue", "2026-01-01", 150); err != nil {
		t.Fatalf("put point again: %v", err)
	}
	points, err := data.Points(q, "scope", "revenue", "2026-01-01")
	if err != nil || len(points) != 1 || points[0].Value != 150 {
		t.Fatalf("expected single updated point, got %+v err=%v", points, err)
	}
}

func TestNotifyLogPreventsDuplicates(t *testing.T) {
	q := openTestDB(t)
	sid := spaceID(t, q)
	uid := userID(t, q, "u@x.y")
	now := time.Now().UTC()
	h := &model.Hint{SpaceID: sid, Fingerprint: "x", Rule: "r", Severity: enums.SeverityWarn,
		Message: "m", FirstSeen: now, LastSeen: now}
	if err := data.AddHint(q, h); err != nil {
		t.Fatalf("add hint: %v", err)
	}

	sent, err := data.WasSent(q, uid, h.ID)
	if err != nil || sent {
		t.Fatalf("expected not sent yet, got %v err=%v", sent, err)
	}
	if err := data.LogSent(q, uid, h.ID); err != nil {
		t.Fatalf("log sent: %v", err)
	}
	sent, err = data.WasSent(q, uid, h.ID)
	if err != nil || !sent {
		t.Fatalf("expected sent, got %v err=%v", sent, err)
	}
}
