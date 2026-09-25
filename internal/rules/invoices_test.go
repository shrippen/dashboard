package rules_test

import (
	"testing"
	"time"

	"dashboard/internal/enums"
	"dashboard/internal/sources"
)

func TestIncomingInvoicesAgainstNinja(t *testing.T) {
	env := todayEnv(nil)
	day := func(n int) string { return env.Today.AddDate(0, 0, -n).Format("2006-01-02") }
	ninja := &sources.NinjaDataset{URL: "https://in.demo", Currency: "EUR",
		Vendors:  []sources.NinjaVendor{{Key: "v1", Name: "Telekom"}},
		Expenses: []sources.NinjaExpense{{Date: day(5), Amount: 41.65}, {Date: day(20), Amount: 99, VendorKey: "v1"}},
	}
	mail := sources.DemoMail(time.Now())
	docs := sources.DemoPaperless(time.Now())
	docs.Invoices = append(docs.Invoices, sources.PaperlessDoc{ID: 312, Title: "Scan", Correspondent: "Stadtwerke", Created: day(40)})
	env.Datasets = map[string]any{"mail": mail, "paperless": docs, "invoiceninja": ninja}

	// Hetzner 41.65 matches the expense; JetBrains 289 does not.
	got := run(t, "mail.invoice_unrecorded", nil, env)
	if len(got) != 1 || got[0].Params["sender"] != "JetBrains" || got[0].Severity != enums.SeverityInfo {
		t.Fatalf("mail: %+v", got)
	}
	// Telekom 39.95 ≠ 99, but amount known → no vendor fallback; Stadtwerke
	// without amount and without vendor → unrecorded, 40 days old → warn.
	got = run(t, "paperless.invoice_unrecorded", nil, env)
	if len(got) != 2 || got[1].Params["sender"] != "Stadtwerke" || got[1].Severity != enums.SeverityWarn {
		t.Fatalf("paperless: %+v", got)
	}

	// Without an amount, a vendor named like the sender counts.
	docs.Invoices = []sources.PaperlessDoc{{ID: 1, Title: "Rechnung", Correspondent: "Telekom Deutschland GmbH", Created: day(18)}}
	if got := run(t, "paperless.invoice_unrecorded", nil, env); len(got) != 0 {
		t.Fatalf("vendor match: %+v", got)
	}
}
