package rules_test

import (
	"testing"
	"time"

	"dashboard/internal/enums"
	"dashboard/internal/metrics"
	"dashboard/internal/rules"
	"dashboard/internal/sources"
)

// series builds daily points ending the day before today.
func series(today time.Time, values ...float64) []metrics.Point {
	out := make([]metrics.Point, len(values))
	for i, v := range values {
		out[i] = metrics.Point{Day: today.AddDate(0, 0, i-len(values)), Value: v}
	}
	return out
}

func TestStorageForecastAndUnsaved(t *testing.T) {
	today := day("2026-09-25")
	h := &metrics.History{Series: map[string][]metrics.Point{
		"truenas.pool.tank.used": series(today, 0.80, 0.81, 0.82, 0.83, 0.84, 0.85, 0.86, 0.87, 0.88, 0.89),
		"immich.items":           series(today, 1000, 1000, 1000, 1200, 1340),
	}}
	borg := &sources.BorgDataset{LastBackup: today.AddDate(0, 0, -3)}
	env := crossEnv("2026-09-25", map[string]any{metrics.HistoryDataset: h, "borgbackup": borg})
	if got := run(t, "system.storage_forecast", nil, env); len(got) != 1 || got[0].Severity != enums.SeverityCritical || got[0].Params["days"] != 11 {
		t.Fatalf("forecast: %+v", got)
	}
	if got := run(t, "backups.unsaved", nil, env); len(got) != 1 || got[0].Params["items"] != "immich: 340" {
		t.Fatalf("unsaved: %+v", got)
	}
}

func TestSlowerSinceUpdate(t *testing.T) {
	today := day("2026-09-25")
	values := []float64{200, 210, 205, 190, 200, 210, 200, 900, 850, 880}
	h := &metrics.History{Series: map[string][]metrics.Point{"kuma.ms.immich": series(today, values...)},
		Events: []metrics.Event{{At: today.AddDate(0, 0, -4).Add(10 * time.Hour), Kind: metrics.EventUpdate, Subject: "Immich", Detail: "v1 → v2"}}}
	got := run(t, "system.slower_since_update", nil, crossEnv("2026-09-25", map[string]any{metrics.HistoryDataset: h}))
	if len(got) != 1 || got[0].Params["monitor"] != "immich" {
		t.Fatalf("slower: %+v", got)
	}
}

func TestExposureLoginsDNS(t *testing.T) {
	today := day("2026-09-25")
	pangolin := &sources.PangolinDataset{Resources: []sources.PResource{
		{Name: "Wiki", Domain: "wiki.example.org", Enabled: true},
		{Name: "Immich", Domain: "photos.example.org", Enabled: true, SSO: true},
	}}
	komodo := &sources.KomodoDataset{Stacks: []sources.KStack{{Name: "wiki", Updates: []string{"app"}}}}
	env := crossEnv("2026-09-25", map[string]any{"pangolin": pangolin, "komodo": komodo})
	if got := run(t, "pangolin.exposure", nil, env); len(got) != 1 || got[0].Params["name"] != "Wiki" {
		t.Fatalf("exposure: %+v", got)
	}

	h := &metrics.History{Series: map[string][]metrics.Point{
		"authentik.country.alex.de": series(today, 1, 1, 1, 1, 1, 1, 1),
		"dns.q.192_168_1_20":        series(today, 900, 1000, 1100, 950, 1000, 1050, 1000),
	}}
	ak := &sources.AuthentikDataset{Logins: []sources.AKLogin{
		{User: "alex", Country: "SG", At: today.Add(2 * time.Hour)},
		{User: "alex", Country: "DE", Lat: 48.14, Lon: 11.58, At: today.Add(3 * time.Hour)},
	}}
	lat, lon := 52.52, 13.40
	geo := &sources.DawarichDataset{Visits: []sources.DawarichVisit{{Start: "2026-09-25T01:00:00Z", End: "2026-09-25T05:00:00Z", Lat: &lat, Lon: &lon}}}
	dns := &sources.DNSFilterDataset{URL: "http://pihole", TopClients: []sources.DNSClient{{IP: "192.168.1.20", Name: "laptop", Queries: 9000}, {IP: "192.168.1.87", Queries: 3000}}}
	env = crossEnv("2026-09-25", map[string]any{metrics.HistoryDataset: h, "authentik": ak, "dawarich": geo, "pihole": dns})
	env.Today = today.Add(6 * time.Hour)
	got := run(t, "authentik.login_anomaly", nil, env)
	if len(got) != 2 || got[0].Message != "authentik.new_country" || got[1].Message != "authentik.far_login" {
		t.Fatalf("logins: %+v", got)
	}
	if got := run(t, "dns.device_spike", nil, env); len(got) != 1 || got[0].Params["device"] != "laptop" {
		t.Fatalf("spike: %+v", got)
	}
	if got := run(t, "dns.new_device", nil, env); len(got) != 1 || got[0].Params["ip"] != "192.168.1.87" {
		t.Fatalf("new device: %+v", got)
	}
}

func TestSpeedStormVPN(t *testing.T) {
	today := day("2026-09-25")
	h := &metrics.History{Series: map[string][]metrics.Point{"speedtest.down": series(today, 240, 100, 90, 110, 245)},
		Events: []metrics.Event{
			{At: today.AddDate(0, 0, -3), Kind: "opened", Subject: "gateway.wan_down", Detail: "wan:WAN"},
			{At: today.AddDate(0, 0, -3).Add(47 * time.Minute), Kind: "resolved", Subject: "gateway.wan_down", Detail: "wan:WAN"},
		}}
	st := &sources.SpeedtestDataset{ExpectDown: 250}
	got := run(t, "speedtest.contract", nil, crossEnv("2026-09-25", map[string]any{metrics.HistoryDataset: h, "speedtest": st}))
	if len(got) != 1 || got[0].Params["below"] != 3 || got[0].Params["outages"] != 1 {
		t.Fatalf("contract: %+v", got)
	}

	dwd := &sources.DWDDataset{Warnings: []sources.WeatherWarning{{ID: "w", Event: "GEWITTER", Headline: "Amtliche WARNUNG vor GEWITTER",
		Severity: sources.WarnModerate, Onset: today.Add(5 * time.Hour)}}}
	borg := &sources.BorgDataset{LastBackup: today.AddDate(0, 0, -2)}
	if got := run(t, "dwd.storm_prep", nil, crossEnv("2026-09-25", map[string]any{"dwd": dwd, "borgbackup": borg})); len(got) != 1 {
		t.Fatalf("storm: %+v", got)
	}

	vpn := &sources.GluetunDataset{Status: "stopped"}
	sab := &sources.SabnzbdDataset{Slots: 3, SpeedKB: 38000}
	if got := run(t, "gluetun.downloads_exposed", nil, crossEnv("2026-09-25", map[string]any{"gluetun": vpn, "sabnzbd": sab})); len(got) != 1 {
		t.Fatalf("vpn: %+v", got)
	}
}

func TestDomainExpiringNamesDependents(t *testing.T) {
	domains := &sources.DomainsDataset{Domains: []sources.DomainInfo{{Name: "example.org", Expires: day("2026-10-01")}}}
	pangolin := &sources.PangolinDataset{Resources: []sources.PResource{{Name: "Wiki", Domain: "wiki.example.org", Enabled: true}}}
	links := []rules.Link{{Title: "Shop", URL: "https://shop.example.org"}, {Title: "Other", URL: "https://other.net"}}
	env := crossEnv("2026-09-25", map[string]any{"pangolin": pangolin, rules.LinksDataset: links})
	got := run(t, "domains.expiring", domains, env)
	if len(got) != 1 || got[0].Message != "domains.expiring_deps" || got[0].Params["deps"] != 2 {
		t.Fatalf("domain: %+v", got)
	}
}
