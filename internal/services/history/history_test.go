package history_test

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"andon/internal/crypto"
	"andon/internal/db"
	"andon/internal/db/dbtest"
	"andon/internal/enums"
	data "andon/internal/repos/data"
	"andon/internal/rules"
	"andon/internal/services/access"
	"andon/internal/services/accounts"
	"andon/internal/services/hints"
	"andon/internal/services/history"
)

// TestBeforeShowsUpdateBeforeOutage: an update recorded minutes before a
// hint appears shows as its prehistory; the hint and later events do not.
func TestBeforeShowsUpdateBeforeOutage(t *testing.T) {
	crypto.Init("test-master-key")
	d, err := db.Open(filepath.Join(t.TempDir(), "t.db"), dbtest.Key)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	var uid int64
	err = db.WithTx(d, func(tx *sql.Tx) error {
		u, err := accounts.Create(tx, "a@x.de", "A", nil, enums.RoleUser, enums.LocaleDE, "")
		uid = u.ID
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	who, _ := access.Load(d, uid)
	space := access.Personal(who).ID

	now := time.Now().UTC()
	if err := data.AddEvent(d, space, data.Event{At: now.Add(-4 * time.Minute), Kind: "update", Subject: "Nextcloud", Detail: "31.0.1 → 31.0.2"}); err != nil {
		t.Fatal(err)
	}
	if err := data.AddEvent(d, space, data.Event{At: now.Add(-3 * time.Hour), Kind: "update", Subject: "Old"}); err != nil {
		t.Fatal(err)
	}
	if _, err := hints.Sync(d, space, nil, nil, []string{"kuma.monitor_down"}, []rules.Finding{{Fingerprint: "down:Nextcloud",
		Rule: "kuma.monitor_down", Severity: enums.SeverityCritical, Message: "kuma.down", Params: map[string]any{"monitor": "Nextcloud"}}}); err != nil {
		t.Fatal(err)
	}
	var hintID int64
	if err := d.QueryRow("SELECT id FROM hints").Scan(&hintID); err != nil {
		t.Fatal(err)
	}

	before, err := history.Before(d, who, hintID)
	if err != nil || len(before) != 1 || before[0].Subject != "Nextcloud" {
		t.Fatalf("before: %+v %v", before, err)
	}
	timeline, err := history.Timeline(d, who, now.Add(-24*time.Hour), 10)
	if err != nil || len(timeline) != 3 || timeline[0].HintID != hintID {
		t.Fatalf("timeline: %+v %v", timeline, err)
	}
}
