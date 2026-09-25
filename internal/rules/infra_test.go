package rules_test

import (
	"testing"
	"time"

	"dashboard/internal/enums"
	"dashboard/internal/rules"
	"dashboard/internal/sources"
)

func TestTrueNASRules(t *testing.T) {
	data := sources.DemoTrueNAS()
	if got := run(t, "truenas.pool_unhealthy", data, todayEnv(nil)); len(got) != 1 || got[0].Params["pool"] != "fast" {
		t.Fatalf("unhealthy: %+v", got)
	}
	// tank at 88 % → warn.
	if got := run(t, "truenas.pool_full", data, todayEnv(nil)); len(got) != 1 || got[0].Severity != enums.SeverityWarn || got[0].Params["percent"] != 88 {
		t.Fatalf("full: %+v", got)
	}
	if got := run(t, "truenas.alerts", data, todayEnv(nil)); len(got) != 1 || got[0].Severity != enums.SeverityWarn {
		t.Fatalf("alerts: %+v", got)
	}
	if got := run(t, "truenas.app_updates", data, todayEnv(nil)); len(got) != 1 || got[0].Params["apps"] != "jellyfin" {
		t.Fatalf("apps: %+v", got)
	}
}

func TestKomodoRules(t *testing.T) {
	data := sources.DemoKomodo(time.Now())
	if got := run(t, "komodo.alerts", data, todayEnv(nil)); len(got) != 1 || got[0].Severity != enums.SeverityCritical {
		t.Fatalf("alerts: %+v", got)
	}
	if got := run(t, "komodo.stack_down", data, todayEnv(nil)); len(got) != 1 || got[0].Params["stack"] != "paperless" {
		t.Fatalf("down: %+v", got)
	}
	if got := run(t, "komodo.updates", data, todayEnv(nil)); len(got) != 1 || got[0].Params["stacks"] != "immich" {
		t.Fatalf("updates: %+v", got)
	}
}

func TestPangolinRules(t *testing.T) {
	data := sources.DemoPangolin()
	if got := run(t, "pangolin.site_offline", data, todayEnv(nil)); len(got) != 1 || got[0].Params["site"] != "eltern" {
		t.Fatalf("offline: %+v", got)
	}
	if got := run(t, "pangolin.unhealthy", data, todayEnv(nil)); len(got) != 1 || got[0].Params["name"] != "Vaultwarden" {
		t.Fatalf("unhealthy: %+v", got)
	}

	// Local sites report no online state.
	data.Sites[1].Online = nil
	if got := run(t, "pangolin.site_offline", data, todayEnv(nil)); len(got) != 0 {
		t.Fatalf("local site reported: %+v", got)
	}
}

func TestAuthentikRules(t *testing.T) {
	data := sources.DemoAuthentik(time.Now())
	if got := run(t, "authentik.update", data, todayEnv(nil)); len(got) != 1 || got[0].Params["version"] != "2025.8.1" {
		t.Fatalf("update: %+v", got)
	}
	if got := run(t, "authentik.failed_logins", data, todayEnv(nil)); len(got) != 1 || got[0].Severity != enums.SeverityWarn {
		t.Fatalf("failed: %+v", got)
	}
	if got := run(t, "authentik.stale_users", data, todayEnv(nil)); len(got) != 1 || got[0].Params["users"] != "kim, test" {
		t.Fatalf("stale: %+v", got)
	}
}

func TestBackupRules(t *testing.T) {
	nas := sources.DemoTrueNAS()
	if got := run(t, "truenas.snapshot_failed", nas, todayEnv(nil)); len(got) != 1 || got[0].Params["dataset"] != "fast/vms" {
		t.Fatalf("snapshot: %+v", got)
	}

	// Komodo stacks immich, paperless, gitea; apps jellyfin, syncthing; only
	// the snapshot dataset tank/photos and Borg clients nas/laptop exist.
	env := todayEnv(nil)
	env.Datasets = map[string]any{"komodo": sources.DemoKomodo(time.Now()), "truenas": nas, "borgbackup": sources.DemoBorg(time.Now())}
	got := run(t, "backups.gap", nil, env)
	if len(got) != 1 || got[0].Params["count"] != 5 {
		t.Fatalf("gap: %+v", got)
	}

	// Without any backup tool the rule stays silent.
	env.Datasets = map[string]any{"komodo": sources.DemoKomodo(time.Now())}
	if got := run(t, "backups.gap", nil, env); len(got) != 0 {
		t.Fatalf("no tool: %+v", got)
	}
}

func TestOutageSuppressesMonitors(t *testing.T) {
	kuma := &sources.KumaDataset{Monitors: []sources.KumaMonitor{
		{Name: "NAS", Target: "https://nas.lan", Status: sources.KumaDown},
		{Name: "Web", Target: "https://web.lan", Status: sources.KumaDown},
	}}
	env := todayEnv(nil)
	env.Datasets = map[string]any{"uptimekuma": kuma, rules.FailedDataset: []rules.Failed{{Service: "truenas", Name: "TrueNAS", Host: "nas.lan"}}}

	outages := rules.Outages(env)
	if len(outages) != 1 || len(outages["nas.lan"]) != 2 {
		t.Fatalf("outages: %v", outages)
	}
	down := run(t, "kuma.monitor_down", kuma, env)
	if len(down) != 2 || !rules.Suppressed(down[0], env, outages) || rules.Suppressed(down[1], env, outages) {
		t.Fatalf("suppression: %+v", down)
	}
	if got := run(t, "system.outage", nil, env); len(got) != 1 || got[0].Params["host"] != "nas.lan" {
		t.Fatalf("outage: %+v", got)
	}
}

func TestDiscoveryNoTile(t *testing.T) {
	env := todayEnv(nil)
	env.Datasets = map[string]any{
		rules.LinksDataset: []rules.Link{{Title: "Immich", URL: "https://photos.example.org"}, {Title: "Git", URL: "https://git.lan"}},
		"komodo":           sources.DemoKomodo(time.Now()), // immich, paperless, gitea
		"pangolin":         sources.DemoPangolin(),         // photos.example.org, vault.example.org
	}
	got := run(t, "discovery.no_tile", nil, env)
	if len(got) != 2 || got[0].Params["names"] != "gitea, paperless" || got[1].Params["names"] != "vault.example.org" {
		t.Fatalf("discovery: %+v", got)
	}
}

func TestCustomRules(t *testing.T) {
	env := todayEnv(map[string]any{rules.CustomKey: []any{
		map[string]any{"id": "q", "title": "Queue", "service": "sabnzbd", "path": "Slots", "op": ">", "value": 2.0},
		map[string]any{"id": "f", "title": "Failed", "service": "sabnzbd", "path": "Failures#", "op": ">=", "value": 1.0, "severity": 30.0},
		map[string]any{"id": "x", "title": "Bad path", "service": "sabnzbd", "path": "Nope.x", "op": ">", "value": 0.0},
	}})
	env.Datasets = map[string]any{"sabnzbd": sources.DemoSabnzbd(time.Now())}
	got := run(t, "custom.rules", nil, env)
	if len(got) != 2 || got[1].Severity != enums.SeverityCritical || got[0].Params["title"] != "Queue" {
		t.Fatalf("custom: %+v", got)
	}
}

func TestContractNotice(t *testing.T) {
	// Deadline in 20 days → warn (critical from 14 days).
	got := run(t, "paperless.contract_notice", sources.DemoPaperless(time.Now()), todayEnv(nil))
	if len(got) != 1 || got[0].Severity != enums.SeverityWarn || got[0].Due == "" {
		t.Fatalf("contract: %+v", got)
	}
}
