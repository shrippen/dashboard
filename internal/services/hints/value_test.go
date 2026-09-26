package hints_test

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"andon/internal/crypto"
	"andon/internal/db"
	"andon/internal/db/dbtest"
	"andon/internal/enums"
	"andon/internal/rules"
	"andon/internal/services/hints"
)

// TestValueAndNoisyRules: hints carry the largest money amount they name;
// a rule whose hints are all dismissed shows as noisy.
func TestValueAndNoisyRules(t *testing.T) {
	crypto.Init("test-master-key")
	d, err := db.Open(filepath.Join(t.TempDir(), "v.db"), dbtest.Key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	who := person(t, d, "a@x.de")
	sid := ownSpace(who)

	var found []rules.Finding
	for i := range 5 {
		found = append(found, rules.Finding{Fingerprint: fmt.Sprintf("n%d", i), Rule: "in.invoice_overdue", Severity: enums.SeverityInfo,
			Message: "in.overdue", Params: map[string]any{"amount": rules.Money(float64(100*(i+1)), "EUR")}})
	}
	if _, err := hints.Sync(d, sid, nil, nil, []string{"in.invoice_overdue"}, found); err != nil {
		t.Fatal(err)
	}
	views, err := hints.Active(d, who, enums.SeverityInfo, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	hints.ByValue(views)
	if len(views) != 5 || views[0].Value != 500 || views[0].Currency != "EUR" {
		t.Fatalf("value order: %+v", views)
	}

	for _, v := range views {
		if err := hints.Act(d, who, v.ID, hints.ActionAck, 0, ""); err != nil {
			t.Fatal(err)
		}
	}
	noisy, err := hints.NoisyRules(d, who, time.Now().UTC())
	if err != nil || len(noisy) != 1 || noisy[0].Rule != "in.invoice_overdue" || noisy[0].Dismissed != 5 {
		t.Fatalf("noisy: %+v %v", noisy, err)
	}
}
