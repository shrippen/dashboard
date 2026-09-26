package rules_test

import (
	"testing"
	"time"

	"andon/internal/sources"
)

func TestKintsugiRules(t *testing.T) {
	data := sources.DemoKintsugi(time.Now())
	if got := run(t, "kintsugi.run_failed", data, todayEnv(nil)); len(got) != 1 || got[0].Params["detail"] != "LLM nicht erreichbar" {
		t.Fatalf("run_failed: %+v", got)
	}
	// The oldest demo suggestion is 8 days old, over the 7-day default.
	if got := run(t, "kintsugi.stale", data, todayEnv(nil)); len(got) != 1 || got[0].Params["count"] != 3 {
		t.Fatalf("stale: %+v", got)
	}
	if got := run(t, "kintsugi.budget", data, todayEnv(nil)); len(got) != 0 {
		t.Fatalf("budget left but reported: %+v", got)
	}

	data.UsedUSD = data.BudgetUSD
	data.LastRun.Status = "ok"
	data.Open = data.Open[:2]
	if got := run(t, "kintsugi.budget", data, todayEnv(nil)); len(got) != 1 {
		t.Fatalf("budget: %+v", got)
	}
	if got := run(t, "kintsugi.run_failed", data, todayEnv(nil)); len(got) != 0 {
		t.Fatalf("ok run reported: %+v", got)
	}
	if got := run(t, "kintsugi.stale", data, todayEnv(nil)); len(got) != 0 {
		t.Fatalf("fresh suggestions reported: %+v", got)
	}
}
