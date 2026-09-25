package hints_test

import (
	"path/filepath"
	"testing"
	"time"

	"dashboard/internal/db"
	"dashboard/internal/enums"
	"dashboard/internal/model"
	"dashboard/internal/repos/content"
	"dashboard/internal/rules"
	"dashboard/internal/services/hints"
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

func spaceID(t *testing.T, q db.Queryer) int64 {
	t.Helper()
	sp := &model.Space{Kind: enums.SpacePersonal, Name: "x", Version: 1}
	if err := content.AddSpace(q, sp); err != nil {
		t.Fatalf("add space: %v", err)
	}
	return sp.ID
}

func finding(fp string) rules.Finding {
	return rules.Finding{
		Fingerprint: fp, Rule: "kimai.missing_day", Severity: enums.SeverityInfo, Message: "kimai.missing_day",
		Params: map[string]any{"day": map[string]any{"$day": "2026-03-01"}},
	}
}

func TestSyncCreatesNewHint(t *testing.T) {
	q := openTestDB(t)
	sid := spaceID(t, q)

	n, err := hints.Sync(q, sid, nil, nil, []string{"kimai.missing_day"}, []rules.Finding{finding("missing:2026-03-01")})
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if n != 1 {
		t.Fatalf("expected 1 fresh hint, got %d", n)
	}
}

func TestSyncResolvesGoneFindings(t *testing.T) {
	q := openTestDB(t)
	sid := spaceID(t, q)

	f := finding("missing:2026-03-01")
	if _, err := hints.Sync(q, sid, nil, nil, []string{"kimai.missing_day"}, []rules.Finding{f}); err != nil {
		t.Fatalf("first sync: %v", err)
	}
	// Second run without that finding: it should resolve.
	n, err := hints.Sync(q, sid, nil, nil, []string{"kimai.missing_day"}, nil)
	if err != nil {
		t.Fatalf("second sync: %v", err)
	}
	if n != 0 {
		t.Fatalf("expected 0 fresh hints on the resolving run, got %d", n)
	}

	stillOpen, err := dataHintsOfScope(q, sid)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	if len(stillOpen) != 0 {
		t.Fatalf("expected the hint to be resolved (not open), got %d open", len(stillOpen))
	}
}

func TestSyncReopensResolvedHint(t *testing.T) {
	q := openTestDB(t)
	sid := spaceID(t, q)
	f := finding("missing:2026-03-01")

	if _, err := hints.Sync(q, sid, nil, nil, []string{"kimai.missing_day"}, []rules.Finding{f}); err != nil {
		t.Fatalf("sync 1: %v", err)
	}
	if _, err := hints.Sync(q, sid, nil, nil, []string{"kimai.missing_day"}, nil); err != nil {
		t.Fatalf("sync 2 (resolve): %v", err)
	}
	n, err := hints.Sync(q, sid, nil, nil, []string{"kimai.missing_day"}, []rules.Finding{f})
	if err != nil {
		t.Fatalf("sync 3 (reopen): %v", err)
	}
	if n != 1 {
		t.Fatalf("expected the reappearing finding to count as fresh (reopened), got %d", n)
	}
}

func addConnection(t *testing.T, q db.Queryer, spaceID int64, key string) int64 {
	t.Helper()
	c := &model.Connection{
		SpaceID: spaceID, Key: key, Name: key, Service: "kimai", URL: "https://x",
		CredentialMode: enums.CredentialShared, VerifyTLS: true, CreatedAt: time.Now().UTC(),
	}
	if err := content.AddConnection(q, c); err != nil {
		t.Fatalf("add connection: %v", err)
	}
	return c.ID
}

func TestSyncFingerprintsPerConnection(t *testing.T) {
	q := openTestDB(t)
	sid := spaceID(t, q)
	connA := addConnection(t, q, sid, "a")
	connB := addConnection(t, q, sid, "b")
	f := finding("dup")

	nA, err := hints.Sync(q, sid, nil, &connA, []string{"kimai.missing_day"}, []rules.Finding{f})
	if err != nil {
		t.Fatalf("sync conn A: %v", err)
	}
	nB, err := hints.Sync(q, sid, nil, &connB, []string{"kimai.missing_day"}, []rules.Finding{f})
	if err != nil {
		t.Fatalf("sync conn B: %v", err)
	}
	if nA != 1 || nB != 1 {
		t.Fatalf("expected both connections to get their own hint, got %d and %d", nA, nB)
	}
}

// dataHintsOfScope is a tiny local helper avoiding an import cycle with the
// data repo's HintsOfScope for the "still open" assertion above.
func dataHintsOfScope(q db.Queryer, spaceID int64) ([]int64, error) {
	rows, err := q.Query("SELECT id FROM hints WHERE space_id = ? AND resolved_at IS NULL", spaceID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
