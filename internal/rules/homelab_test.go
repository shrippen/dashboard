package rules_test

import (
	"testing"
	"time"

	"andon/internal/enums"
	"andon/internal/sources"
)

func TestScrutinyRules(t *testing.T) {
	data := sources.DemoScrutiny(time.Now())
	if got := run(t, "scrutiny.disk_failed", data, todayEnv(nil)); len(got) != 1 || got[0].Params["disk"] != "sdb" {
		t.Fatalf("failed: %+v", got)
	}
	// nvme0 at 56 °C → warn.
	if got := run(t, "scrutiny.disk_hot", data, todayEnv(nil)); len(got) != 1 || got[0].Severity != enums.SeverityWarn {
		t.Fatalf("hot: %+v", got)
	}
	data.Disks[0].Seen = time.Now().AddDate(0, 0, -5)
	if got := run(t, "scrutiny.stale", data, todayEnv(nil)); len(got) != 1 || got[0].Params["disk"] != "sda" {
		t.Fatalf("stale: %+v", got)
	}
}

func TestImmichRules(t *testing.T) {
	data := sources.DemoImmich()
	if got := run(t, "immich.storage", data, todayEnv(nil)); len(got) != 1 || got[0].Params["percent"] != 87 {
		t.Fatalf("storage: %+v", got)
	}
	if got := run(t, "immich.jobs_failed", data, todayEnv(nil)); len(got) != 1 || got[0].Params["count"] != 3 {
		t.Fatalf("jobs: %+v", got)
	}
	if got := run(t, "immich.update", data, todayEnv(nil)); len(got) != 1 {
		t.Fatalf("update: %+v", got)
	}
	data.Latest = "v1.99.0"
	if got := run(t, "immich.update", data, todayEnv(nil)); len(got) != 0 {
		t.Fatalf("older release reported: %+v", got)
	}
}

func TestUmamiTrafficDrop(t *testing.T) {
	data := sources.DemoUmami()
	got := run(t, "umami.traffic_drop", data, todayEnv(nil))
	if len(got) != 1 || got[0].Params["site"] != "Shop" || got[0].Params["percent"] != 87 {
		t.Fatalf("drop: %+v", got)
	}
	data.Sites[1].Views, data.Sites[1].Visitors = 0, 0
	if got := run(t, "umami.traffic_drop", data, todayEnv(nil)); len(got) != 1 || got[0].Message != "umami.no_data" {
		t.Fatalf("no data: %+v", got)
	}
}

func TestFreshRSSRules(t *testing.T) {
	data := sources.DemoFreshRSS(time.Now())
	got := run(t, "freshrss.backlog", data, todayEnv(nil))
	if len(got) != 1 || got[0].Params["feeds"] != "heise online (540), Selfhosted Weekly (260), Go Blog (12)" {
		t.Fatalf("backlog: %+v", got)
	}
	if got := run(t, "freshrss.stale_feed", data, todayEnv(nil)); len(got) != 1 || got[0].Params["feed"] != "Altes Projektblog" {
		t.Fatalf("stale: %+v", got)
	}
}

func TestGiteaRules(t *testing.T) {
	data := sources.DemoGitea(time.Now())
	env := todayEnv(nil)
	if got := run(t, "gitea.review_waiting", data, env); len(got) != 1 || got[0].Params["repo"] != "team/infra" {
		t.Fatalf("review: %+v", got)
	}
	if got := run(t, "gitea.due", data, env); len(got) != 1 || got[0].Severity != enums.SeverityCritical {
		t.Fatalf("due: %+v", got)
	}
	if got := run(t, "gitea.stale_pr", data, env); len(got) != 1 || got[0].Params["number"] != int64(4) {
		t.Fatalf("stale pr: %+v", got)
	}
	if got := run(t, "gitea.actions_failed", data, env); len(got) != 1 {
		t.Fatalf("actions: %+v", got)
	}
	if got := run(t, "gitea.mirror_stale", data, env); len(got) != 1 {
		t.Fatalf("mirror: %+v", got)
	}
}

func TestBorgRules(t *testing.T) {
	data := sources.DemoBorg(time.Now())
	env := todayEnv(nil)
	if got := run(t, "borg.client_offline", data, env); len(got) != 1 || got[0].Params["client"] != "laptop" {
		t.Fatalf("offline: %+v", got)
	}
	if got := run(t, "borg.jobs_failed", data, env); len(got) != 1 {
		t.Fatalf("failed: %+v", got)
	}
	if got := run(t, "borg.backup_old", data, env); len(got) != 0 {
		t.Fatalf("7 h old backup reported: %+v", got)
	}
	data.LastBackup = time.Now().Add(-80 * time.Hour)
	if got := run(t, "borg.backup_old", data, env); len(got) != 1 || got[0].Severity != enums.SeverityCritical {
		t.Fatalf("old: %+v", got)
	}
	if got := run(t, "borg.storage", data, env); len(got) != 0 {
		t.Fatalf("78 %% storage reported: %+v", got)
	}
}

func TestHassRules(t *testing.T) {
	data := sources.DemoHass(time.Now())
	env := todayEnv(nil)
	if got := run(t, "hass.battery_low", data, env); len(got) != 1 || got[0].Severity != enums.SeverityCritical {
		t.Fatalf("battery: %+v", got)
	}
	if got := run(t, "hass.unavailable", data, env); len(got) != 1 || got[0].Params["names"] != "Steckdose Leistung" {
		t.Fatalf("unavailable: %+v", got)
	}
	if got := run(t, "hass.updates", data, env); len(got) != 1 {
		t.Fatalf("updates: %+v", got)
	}
	if got := run(t, "hass.alarm", data, env); len(got) != 0 {
		t.Fatalf("dry cellar alarmed: %+v", got)
	}
	data.Entities[0].State = sources.HassOn
	if got := run(t, "hass.alarm", data, env); len(got) != 1 || got[0].Params["kind"] != "moisture" {
		t.Fatalf("alarm: %+v", got)
	}
	ignored := todayEnv(map[string]any{"rules": map[string]any{"hass.unavailable": map[string]any{"ignore": []any{"sensor.zigbee_"}}}})
	if got := run(t, "hass.unavailable", data, ignored); len(got) != 0 {
		t.Fatalf("ignored prefix reported: %+v", got)
	}
}

func TestSureRules(t *testing.T) {
	data := sources.DemoSure(time.Now())
	env := todayEnv(nil)
	if got := run(t, "sure.recurring_missed", data, env); len(got) != 1 || got[0].Params["name"] != "Krankenversicherung" {
		t.Fatalf("missed: %+v", got)
	}
	if got := run(t, "sure.low_balance", data, env); len(got) != 1 || got[0].Params["account"] != "Tagesgeld" {
		t.Fatalf("low: %+v", got)
	}
	if got := run(t, "sure.unusual_expense", data, env); len(got) != 1 || got[0].Params["name"] != "Amazon" {
		t.Fatalf("unusual: %+v", got)
	}
}

func TestSureNinjaCross(t *testing.T) {
	sure := sources.DemoSure(time.Now())
	today := todayEnv(nil).Today
	ninja := &sources.NinjaDataset{URL: "https://in.demo", Currency: "EUR",
		Clients: []sources.NinjaClient{{ID: 1, Name: "Muster GmbH"}},
		Invoices: []sources.NinjaInvoice{
			{ID: 17, Number: "RE-2026-017", ClientID: 1, Status: "sent", Date: today.AddDate(0, 0, -20).Format("2006-01-02"), Amount: 2380, Balance: 2380},
			{ID: 18, Number: "RE-2026-018", ClientID: 1, Status: "sent", Date: today.AddDate(0, 0, -2).Format("2006-01-02"), Amount: 500, Balance: 500},
		},
		Expenses: []sources.NinjaExpense{{Date: today.AddDate(0, 0, -5).Format("2006-01-02"), Amount: 41.65}},
	}
	env := todayEnv(nil)
	env.Datasets = map[string]any{"sure": sure, "invoiceninja": ninja}

	if got := run(t, "cross.invoice_paid", nil, env); len(got) != 1 || got[0].Params["number"] != "RE-2026-017" {
		t.Fatalf("paid: %+v", got)
	}
	if got := run(t, "cross.expense_unrecorded", nil, env); len(got) != 0 {
		t.Fatalf("reported without business accounts: %+v", got)
	}
	env.Settings = map[string]any{"rules": map[string]any{"cross.expense_unrecorded": map[string]any{"accounts": []any{"Geschäftskonto"}}}}
	got := run(t, "cross.expense_unrecorded", nil, env)
	if len(got) != 2 {
		t.Fatalf("unrecorded: %+v", got)
	}
	for _, f := range got {
		if f.Params["name"] == "Hetzner Online" && f.Params["day"] != nil && f.Fingerprint == "expense:t2" {
			t.Fatalf("recorded Hetzner expense reported: %+v", f)
		}
	}
}

func TestPGBackWebRules(t *testing.T) {
	data := sources.DemoPGBack(time.Now())
	env := todayEnv(nil)
	if got := run(t, "pgbackweb.failed", data, env); len(got) != 1 || got[0].Params["backup"] != "invoiceninja" {
		t.Fatalf("failed: %+v", got)
	}
	if got := run(t, "pgbackweb.stale", data, env); len(got) != 1 || got[0].Params["backup"] != "immich" {
		t.Fatalf("stale: %+v", got)
	}
	if got := run(t, "pgbackweb.silent", data, env); len(got) != 0 {
		t.Fatalf("silent despite events: %+v", got)
	}
	if got := run(t, "pgbackweb.silent", &sources.PGBackDataset{}, env); len(got) != 1 {
		t.Fatalf("silent: %+v", got)
	}
}
