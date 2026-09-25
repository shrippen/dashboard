package rules_test

import (
	"testing"
	"time"

	"dashboard/internal/enums"
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
