package seed_test

import (
	"context"
	"path/filepath"
	"testing"

	"dashboard/internal/crypto"
	"dashboard/internal/db"
	"dashboard/internal/enums"
	"dashboard/internal/repos/users"
	"dashboard/internal/services/access"
	"dashboard/internal/services/boards"
	"dashboard/internal/services/hints"
	"dashboard/internal/services/seed"
	"dashboard/internal/services/system"
)

// TestDemoFillsBoardsAndFiresRules: the demo instance imports every widget
// and the analysis turns the demo datasets into hints.
func TestDemoFillsBoardsAndFiresRules(t *testing.T) {
	crypto.Init("test-master-key")
	d, err := db.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := system.Start(d); err != nil {
		t.Fatal(err)
	}

	if err := seed.Demo(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	user, _ := users.ByEmail(d, seed.DemoUser)
	who, _ := access.Load(d, user.ID)

	visible, _ := boards.Visible(d, who)
	if len(visible) < 5 {
		t.Fatalf("expected personal and instance boards, got %d", len(visible))
	}
	found, err := hints.Active(d, who, enums.SeverityInfo, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	rules := map[string]bool{}
	for _, h := range found {
		rules[h.Rule] = true
	}
	for _, want := range []string{"kimai.timer_running_long", "in.invoice_overdue", "snipe.warranty_expiring", "geo.visit_without_time"} {
		if !rules[want] {
			t.Errorf("expected hint %s from the demo data, got %v", want, rules)
		}
	}

	if err := seed.Demo(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	if n, _ := users.Count(d); n != 2 {
		t.Fatalf("demo must run only once, got %d users", n)
	}
}
