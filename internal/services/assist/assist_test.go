package assist

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"andon/internal/crypto"
	"andon/internal/db"
	"andon/internal/db/dbtest"
	"andon/internal/drivers/llm"
	"andon/internal/enums"
	"andon/internal/rules"
	"andon/internal/services/access"
	"andon/internal/services/accounts"
	"andon/internal/services/hints"
	"andon/internal/settings"
)

func setup(t *testing.T) (*sql.DB, *access.Principal, int64) {
	t.Helper()
	crypto.Init("test-master-key")
	d, err := db.Open(filepath.Join(t.TempDir(), "t.db"), dbtest.Key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	var id int64
	err = db.WithTx(d, func(tx *sql.Tx) error {
		u, err := accounts.Create(tx, "a@x.de", "A", nil, enums.RoleUser, enums.LocaleDE, "")
		if err == nil {
			id = u.ID
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	who, err := access.Load(d, id)
	if err != nil {
		t.Fatal(err)
	}
	return d, who, access.Personal(who).ID
}

func openHint(t *testing.T, d *sql.DB, space int64, rule string) int64 {
	t.Helper()
	if _, err := hints.Sync(d, space, nil, nil, []string{rule}, []rules.Finding{{Fingerprint: rule, Rule: rule,
		Severity: enums.SeverityWarn, Message: "github.ci_failed", Params: map[string]any{"repo": "a/b"}, Sources: []string{"github"}}}); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := d.QueryRow("SELECT id FROM hints WHERE rule = ?", rule).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// TestAdviseOnceAndPrivate: the hint text goes out once and is kept;
// location hints never leave; without a key nothing is sent.
func TestAdviseOnceAndPrivate(t *testing.T) {
	d, who, space := setup(t)
	calls, prompt := 0, ""
	real := complete
	complete = func(_ context.Context, _, _, p string, _ []llm.File) (string, error) {
		calls++
		prompt = p
		return "1. Log öffnen", nil
	}
	t.Cleanup(func() { complete = real; Init(settings.Settings{}) })

	id := openHint(t, d, space, "github.ci_failed")
	if _, err := Advise(context.Background(), d, who, id); !errors.Is(err, ErrOff) {
		t.Fatalf("without key: %v", err)
	}
	Init(settings.Settings{AnthropicAPIKey: "k"})
	for range 2 {
		text, err := Advise(context.Background(), d, who, id)
		if err != nil || text != "1. Log öffnen" {
			t.Fatalf("advice: %q %v", text, err)
		}
	}
	if calls != 1 || !strings.Contains(prompt, "a/b") {
		t.Fatalf("calls=%d prompt=%q", calls, prompt)
	}

	geo := openHint(t, d, space, "geo.visit_without_time")
	if _, err := Advise(context.Background(), d, who, geo); !errors.Is(err, ErrPrivate) {
		t.Fatalf("location hint sent: %v", err)
	}
}

func TestReadInvoice(t *testing.T) {
	real := complete
	t.Cleanup(func() { complete = real; Init(settings.Settings{}) })
	Init(settings.Settings{AnthropicAPIKey: "k"})

	complete = func(context.Context, string, string, string, []llm.File) (string, error) {
		return "Hier:\n{\"vendor\": \"Hetzner\", \"number\": \"R1\", \"gross\": 12.5, \"currency\": \"EUR\"}", nil
	}
	inv, err := ReadInvoice(context.Background(), []llm.File{{Media: "application/pdf", Content: []byte("%PDF")}})
	if err != nil || inv.Title() != "Hetzner R1" || inv.Gross != 12.5 {
		t.Fatalf("invoice: %+v %v", inv, err)
	}
	complete = func(context.Context, string, string, string, []llm.File) (string, error) { return "nicht lesbar", nil }
	if _, err := ReadInvoice(context.Background(), nil); !errors.Is(err, ErrUnreadable) {
		t.Fatalf("garbage accepted: %v", err)
	}
}
