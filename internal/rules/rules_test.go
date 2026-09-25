package rules_test

import (
	"testing"
	"time"

	"dashboard/internal/enums"
	"dashboard/internal/i18n"
	"dashboard/internal/rules"
	"dashboard/internal/sources"
)

func day(s string) time.Time {
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		panic(err)
	}
	return t
}

func run(t *testing.T, id string, data any, env rules.Env) []rules.Finding {
	t.Helper()
	for _, spec := range rules.AllRules() {
		if spec.ID == id {
			cfg := rules.Config(spec, env.Settings)
			return spec.Run(data, cfg, env)
		}
	}
	t.Fatalf("rule not registered: %s", id)
	return nil
}

func TestKimaiTimerRunningLong(t *testing.T) {
	data := &sources.KimaiDataset{
		Active: []sources.KimaiSheet{{ID: 1, Begin: time.Now().UTC().Add(-11 * time.Hour).Format(time.RFC3339)}},
	}
	found := run(t, "kimai.timer_running_long", data, rules.Env{Today: day("2026-03-01")})
	if len(found) != 1 || found[0].Severity != enums.SeverityWarn {
		t.Fatalf("expected 1 warning, got %+v", found)
	}
}

func TestKimaiBudgetBurnCritical(t *testing.T) {
	data := &sources.KimaiDataset{
		Projects: []sources.KimaiProject{{ID: 1, Name: "Website", Budget: 1000, UsedMoney: 1100}},
	}
	found := run(t, "kimai.budget_burn", data, rules.Env{Today: day("2026-03-01"), Settings: map[string]any{}})
	if len(found) != 1 || found[0].Severity != enums.SeverityCritical {
		t.Fatalf("expected 1 critical finding, got %+v", found)
	}
}

func TestKimaiBudgetBurnNoFindingBelowThreshold(t *testing.T) {
	data := &sources.KimaiDataset{
		Projects: []sources.KimaiProject{{ID: 1, Name: "Website", Budget: 1000, UsedMoney: 100}},
	}
	found := run(t, "kimai.budget_burn", data, rules.Env{Today: day("2026-03-01"), Settings: map[string]any{}})
	if len(found) != 0 {
		t.Fatalf("expected no findings, got %+v", found)
	}
}

func TestKimaiUnbilledHoursConfigOverride(t *testing.T) {
	data := &sources.KimaiDataset{
		Timesheets: []sources.KimaiSheet{
			{ID: 1, Begin: "2026-01-01T09:00:00", End: "2026-01-01T11:00:00", Minutes: 120, Rate: 200,
				Billable: true, CustomerID: 1},
		},
		Customers: []sources.KimaiCustomer{{ID: 1, Name: "Acme"}},
	}
	env := rules.Env{Today: day("2026-01-20"), Settings: map[string]any{}} // only 19 days old
	if found := run(t, "kimai.unbilled_hours", data, env); len(found) != 0 {
		t.Fatalf("expected no finding at default warn_days=30, got %+v", found)
	}

	// Override warn_days down to 10 via space settings.
	env.Settings = map[string]any{"rules": map[string]any{
		"kimai.unbilled_hours": map[string]any{"warn_days": 10.0},
	}}
	found := run(t, "kimai.unbilled_hours", data, env)
	if len(found) != 1 || found[0].Params["customer"] != "Acme" {
		t.Fatalf("expected 1 finding with overridden warn_days, got %+v", found)
	}
}

func TestNinjaInvoiceOverdueDunning(t *testing.T) {
	data := &sources.NinjaDataset{
		Invoices: []sources.NinjaInvoice{
			{ID: 1, ClientID: 1, Status: "sent", DueDate: "2026-01-01", Balance: 500},
		},
		Clients: []sources.NinjaClient{{ID: 1, Name: "Acme"}},
	}
	env := rules.Env{Today: day("2026-02-01"), Settings: map[string]any{}} // 31 days overdue
	found := run(t, "in.invoice_overdue", data, env)
	if len(found) != 1 || found[0].Message != "in.overdue_dunning" || found[0].Severity != enums.SeverityCritical {
		t.Fatalf("expected dunning finding, got %+v", found)
	}
}

func TestNinjaMissingVATDomestic(t *testing.T) {
	data := &sources.NinjaDataset{
		HomeCountryID: "276",
		Invoices: []sources.NinjaInvoice{
			{ID: 1, ClientID: 1, Status: "paid", Date: "2026-01-01", Taxes: 0},
		},
		Clients: []sources.NinjaClient{{ID: 1, Name: "Acme", CountryID: "276"}},
	}
	found := run(t, "in.missing_vat", data, rules.Env{Today: day("2026-01-10"), Settings: map[string]any{}})
	if len(found) != 1 || found[0].Message != "in.missing_vat_domestic" {
		t.Fatalf("expected domestic missing-VAT finding, got %+v", found)
	}
}

func TestNinjaMissingVATEUWithVATNumberIsFine(t *testing.T) {
	data := &sources.NinjaDataset{
		HomeCountryID: "276",
		Invoices: []sources.NinjaInvoice{
			{ID: 1, ClientID: 1, Status: "paid", Date: "2026-01-01", Taxes: 0},
		},
		Clients: []sources.NinjaClient{{ID: 1, Name: "Acme FR", CountryID: "250", VATNumber: "FR123"}},
	}
	found := run(t, "in.missing_vat", data, rules.Env{Today: day("2026-01-10"), Settings: map[string]any{}})
	if len(found) != 0 {
		t.Fatalf("expected no finding for EU client with VAT number, got %+v", found)
	}
}

func TestSnipeGWGHintNetOfVAT(t *testing.T) {
	data := &sources.SnipeDataset{
		Assets: []sources.SnipeAsset{{ID: 1, Name: "Server", PurchaseDate: "2026-02-01", PurchaseCost: 1200}},
	}
	// 1200 gross / 1.19 ≈ 1008.4 net, above the 800 GWG limit.
	found := run(t, "snipe.gwg_hint", data, rules.Env{Today: day("2026-03-01"), Settings: map[string]any{}})
	if len(found) != 1 {
		t.Fatalf("expected 1 GWG hint, got %+v", found)
	}
}

func TestSnipeWarrantyExpiringSeverityByThreshold(t *testing.T) {
	data := &sources.SnipeDataset{
		Assets: []sources.SnipeAsset{
			{ID: 1, Name: "Soon", Tag: "A1", WarrantyExpires: "2026-03-10"},  // 9 days: warn
			{ID: 2, Name: "Later", Tag: "A2", WarrantyExpires: "2026-04-20"}, // 50 days: info
		},
	}
	found := run(t, "snipe.warranty_expiring", data, rules.Env{Today: day("2026-03-01"), Settings: map[string]any{}})
	if len(found) != 2 {
		t.Fatalf("expected 2 findings, got %d", len(found))
	}
	bySeverity := map[enums.Severity]int{}
	for _, f := range found {
		bySeverity[f.Severity]++
	}
	if bySeverity[enums.SeverityWarn] != 1 || bySeverity[enums.SeverityInfo] != 1 {
		t.Fatalf("expected one warn and one info, got %+v", bySeverity)
	}
}

func TestCrossVisitWithoutTime(t *testing.T) {
	kimai := &sources.KimaiDataset{URL: "https://kimai.example", Customers: []sources.KimaiCustomer{{ID: 1, Name: "Acme"}}}
	geo := &sources.DawarichDataset{
		Areas:  []sources.DawarichArea{{ID: 1, Name: "Acme Office", Lat: 52.5, Lon: 13.4, Radius: 100}},
		Visits: []sources.DawarichVisit{{ID: 1, AreaID: 1, Start: "2026-02-20T09:00:00Z", End: "2026-02-20T12:00:00Z", Minutes: 180}},
	}
	env := rules.Env{
		Today: day("2026-03-01"), Settings: map[string]any{},
		Datasets: map[string]any{"kimai": kimai, "dawarich": geo},
		Options: map[string]map[string]any{
			"dawarich": {"areas": map[string]any{"Acme Office": map[string]any{"customer_id": float64(1)}}},
		},
	}
	found := run(t, "geo.visit_without_time", nil, env)
	if len(found) != 1 || found[0].Params["customer"] != "Acme" {
		t.Fatalf("expected 1 unbooked-visit finding, got %+v", found)
	}
}

func TestCrossVisitWithoutTimeSkippedWhenBooked(t *testing.T) {
	kimai := &sources.KimaiDataset{
		URL:       "https://kimai.example",
		Customers: []sources.KimaiCustomer{{ID: 1, Name: "Acme"}},
		Timesheets: []sources.KimaiSheet{
			{ID: 1, Begin: "2026-02-20T09:00:00", CustomerID: 1},
		},
	}
	geo := &sources.DawarichDataset{
		Areas:  []sources.DawarichArea{{ID: 1, Name: "Acme Office", Lat: 52.5, Lon: 13.4, Radius: 100}},
		Visits: []sources.DawarichVisit{{ID: 1, AreaID: 1, Start: "2026-02-20T09:00:00Z", End: "2026-02-20T12:00:00Z", Minutes: 180}},
	}
	env := rules.Env{
		Today: day("2026-03-01"), Settings: map[string]any{},
		Datasets: map[string]any{"kimai": kimai, "dawarich": geo},
		Options: map[string]map[string]any{
			"dawarich": {"areas": map[string]any{"Acme Office": map[string]any{"customer_id": float64(1)}}},
		},
	}
	found := run(t, "geo.visit_without_time", nil, env)
	if len(found) != 0 {
		t.Fatalf("expected no finding once the day is booked, got %+v", found)
	}
}

func TestTaxDeadlinesVATReturnAmount(t *testing.T) {
	ninja := &sources.NinjaDataset{
		Invoices: []sources.NinjaInvoice{
			{ID: 1, ClientID: 1, Status: "paid", Date: "2026-02-15", Taxes: 190, Amount: 1190},
		},
		Payments: []sources.NinjaPayment{{ID: 1, ClientID: 1, Date: "2026-02-20", Amount: 1190}},
	}
	env := rules.Env{
		Today: day("2026-03-01"),
		Settings: map[string]any{"tax": map[string]any{
			"vat": map[string]any{"return_interval": "monthly", "method": "soll"},
		}},
		Datasets: map[string]any{"invoiceninja": ninja},
	}
	found := run(t, "tax.deadlines", nil, env)
	if len(found) == 0 {
		t.Fatalf("expected at least one deadline, got none")
	}
	hasAmount := false
	for _, f := range found {
		if f.Message == "tax.vat_return_amount" {
			hasAmount = true
			if f.Params["amount"] == nil {
				t.Fatalf("expected amount param, got %+v", f.Params)
			}
		}
	}
	if !hasAmount {
		t.Fatalf("expected a vat_return_amount finding, got %+v", found)
	}
}

func TestConfigOnlyOverridesKnownKeys(t *testing.T) {
	spec := rules.Spec{ID: "x", Defaults: map[string]any{"a": 1.0}}
	cfg := rules.Config(spec, map[string]any{"rules": map[string]any{
		"x": map[string]any{"a": 2.0, "unknown": "nope"},
	}})
	if cfg["a"] != 2.0 {
		t.Fatalf("expected override to apply, got %+v", cfg)
	}
	if _, ok := cfg["unknown"]; ok {
		t.Fatalf("expected unknown key to be dropped, got %+v", cfg)
	}
}

func TestEveryRuleHasLabels(t *testing.T) {
	for _, spec := range rules.AllRules() {
		for _, locale := range []enums.Locale{enums.LocaleDE, enums.LocaleEN} {
			if key := "rule_name." + spec.ID; i18n.T(key, locale, nil) == key {
				t.Errorf("%s: missing %s", locale, key)
			}
			for param := range spec.Defaults {
				if key := "param." + param; param != rules.Enabled && i18n.T(key, locale, nil) == key {
					t.Errorf("%s: missing %s", locale, key)
				}
			}
		}
	}
}
