package rules_test

import (
	"testing"
	"time"

	"dashboard/internal/enums"
	"dashboard/internal/sources"
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
