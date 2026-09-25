package rules_test

import (
	"testing"
	"time"

	"dashboard/internal/enums"
	"dashboard/internal/rules"
	"dashboard/internal/sources"
)

// Demo datasets are built relative to now; rules see today's date.
func todayEnv(settings map[string]any) rules.Env {
	now := time.Now().UTC()
	return rules.Env{Today: time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC), Settings: settings}
}

func TestKumaRules(t *testing.T) {
	data := sources.DemoKuma()
	down := run(t, "kuma.monitor_down", data, todayEnv(nil))
	if len(down) != 1 || down[0].Params["monitor"] != "NAS" || down[0].Severity != enums.SeverityCritical {
		t.Fatalf("down: %+v", down)
	}
	// Shop: 9 days left → warn; Kimai: 54 days → nothing.
	certs := run(t, "kuma.cert_expiring", data, todayEnv(nil))
	if len(certs) != 1 || certs[0].Params["monitor"] != "Shop" || certs[0].Severity != enums.SeverityWarn {
		t.Fatalf("certs: %+v", certs)
	}
}

func TestProxmoxRules(t *testing.T) {
	data := sources.DemoProxmox(time.Now())
	env := todayEnv(nil)

	if got := run(t, "proxmox.updates", data, env); len(got) != 1 || got[0].Params["count"] != 12 {
		t.Fatalf("updates: %+v", got)
	}
	// local-lvm 430/480 = 89.6 % → warn; backup 27 % → nothing.
	if got := run(t, "proxmox.storage_full", data, env); len(got) != 1 || got[0].Severity != enums.SeverityWarn {
		t.Fatalf("storage: %+v", got)
	}
	// homeassistant (101) is 9 days old; template 9000 is skipped.
	if got := run(t, "proxmox.backup_old", data, env); len(got) != 1 || got[0].Params["vmid"] != "101" {
		t.Fatalf("backups: %+v", got)
	}
	ignored := todayEnv(map[string]any{"rules": map[string]any{"proxmox.backup_old": map[string]any{"ignore": []any{101.0}}}})
	if got := run(t, "proxmox.backup_old", data, ignored); len(got) != 0 {
		t.Fatalf("ignored guest reported: %+v", got)
	}
	if got := run(t, "proxmox.node_offline", data, env); len(got) != 0 {
		t.Fatalf("offline: %+v", got)
	}
}

func TestPaperlessInbox(t *testing.T) {
	got := run(t, "paperless.inbox", sources.DemoPaperless(time.Now()), todayEnv(nil))
	if len(got) != 1 || got[0].Severity != enums.SeverityWarn || got[0].Params["days"] != 23 {
		t.Fatalf("inbox: %+v", got)
	}
}

func TestCertsExpiring(t *testing.T) {
	data := sources.DemoCerts(time.Now())
	data.Certs = append(data.Certs, sources.Cert{Host: "down.demo:443", Error: "connect failed"})

	got := run(t, "certs.expiring", data, todayEnv(nil))
	if len(got) != 2 {
		t.Fatalf("certs: %+v", got)
	}
	if got[0].Params["host"] != "shop.demo:443" || got[0].Severity != enums.SeverityWarn || got[1].Message != "certs.unreachable" {
		t.Fatalf("certs: %+v", got)
	}
}
