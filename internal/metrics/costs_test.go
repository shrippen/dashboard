package metrics_test

import (
	"testing"
	"time"

	"andon/internal/metrics"
	"andon/internal/sources"
)

func TestHomelabCostAndShares(t *testing.T) {
	today := time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC)
	settings := metrics.HomelabSettingsOf(map[string]any{"homelab": map[string]any{"power_entity": "sensor.server",
		"power_price": 0.30, "hardware_years": 5.0, "domain_yearly": 12.0, "hosting_words": "hetzner"}})
	in := metrics.CostInputs{
		Hass:    &sources.HassDataset{Entities: []sources.Entity{{ID: "sensor.server", State: "50", Unit: "W"}}},
		Snipe:   &sources.SnipeDataset{Assets: []sources.SnipeAsset{{Name: "NAS", PurchaseDate: "2024-01-01", PurchaseCost: 1200}, {Name: "Alt", PurchaseDate: "2015-01-01", PurchaseCost: 900}}},
		Domains: &sources.DomainsDataset{Domains: []sources.DomainInfo{{Name: "a.de"}, {Name: "b.org"}}},
		Sure:    &sources.SureDataset{Recurring: []sources.SureRecurring{{Name: "Hetzner Storage Box", Status: "active", Expense: true, Amount: 3.81}}},
	}
	bill := metrics.HomelabCost(in, settings, today)
	// power 50 W × 730 h × 0.30 = 10.95; NAS 1200/60 = 20; domains 2; hosting 3.81
	if len(bill.Items) != 4 || bill.Total != 36.76 {
		t.Fatalf("bill: %+v", bill)
	}

	proxmox := &sources.ProxmoxDataset{Guests: []sources.ProxmoxGuest{{Name: "docker", Running: true, CPU: 3}, {Name: "ha", Running: true, CPU: 1}}}
	split := metrics.PowerPerService(proxmox, 40)
	if len(split) != 2 || split[0].Name != "docker" || split[0].Monthly != 30 {
		t.Fatalf("split: %+v", split)
	}

	kimai := &sources.KimaiDataset{Customers: []sources.KimaiCustomer{{Name: "Acme GmbH"}}}
	share, n, all := metrics.BusinessShare(kimai, []string{"acme-shop", "immich", "dotfiles", "acme-api"})
	if n != 2 || all != 4 || share != 0.5 {
		t.Fatalf("share: %v %d %d", share, n, all)
	}
}

func TestWeekStory(t *testing.T) {
	now := time.Date(2026, 9, 25, 12, 0, 0, 0, time.UTC)
	kimai := &sources.KimaiDataset{Customers: []sources.KimaiCustomer{{ID: 1, Name: "Acme"}, {ID: 2, Name: "Beta"}},
		Timesheets: []sources.KimaiSheet{{Begin: "2026-09-22T09:00:00Z", Minutes: 360, CustomerID: 1}, {Begin: "2026-09-23T09:00:00Z", Minutes: 240, CustomerID: 2}}}
	ninja := &sources.NinjaDataset{Currency: "EUR", Payments: []sources.NinjaPayment{{Date: "2026-09-24", Amount: 1190}}}
	h := &metrics.History{Series: map[string][]metrics.Point{"truenas.pool.tank.used": {
		{Day: time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC), Value: 0.78}, {Day: time.Date(2026, 9, 25, 0, 0, 0, 0, time.UTC), Value: 0.81}}}}
	lines := metrics.WeekStory(map[string]any{"kimai": kimai, "invoiceninja": ninja}, h, now)
	if len(lines) != 3 || lines[0].Key != "hours" || lines[0].Params["customer"] != "Acme" || lines[1].Key != "paid" || lines[2].Key != "storage" {
		t.Fatalf("lines: %+v", lines)
	}
}
