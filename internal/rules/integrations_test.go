package rules_test

import (
	"testing"
	"time"

	"dashboard/internal/enums"
	"dashboard/internal/rules"
	"dashboard/internal/sources"
)

// TestIntegrationRules runs each new rule on its demo dataset.
func TestIntegrationRules(t *testing.T) {
	env := todayEnv(nil)
	now := time.Now().UTC()
	cases := []struct {
		rule  string
		data  any
		count int
		level enums.Severity
	}{
		{"tailscale.key_expiry", sources.DemoTailscale(now), 1, enums.SeverityWarn},
		{"tailscale.offline", sources.DemoTailscale(now), 1, enums.SeverityInfo},
		{"gateway.wan_down", sources.DemoGateway(), 1, enums.SeverityCritical},
		{"gateway.updates", sources.DemoGateway(), 1, enums.SeverityInfo},
		{"gateway.devices_offline", sources.DemoGateway(), 0, 0},
		{"mediaserver.update", sources.DemoMediaServer(), 0, 0},
		{"arr.health", sources.DemoArr(now), 1, enums.SeverityInfo},
		{"arr.stuck", sources.DemoArr(now), 1, enums.SeverityWarn},
		{"vaultwarden.no_2fa", sources.DemoVaultwarden(now), 1, enums.SeverityWarn},
		{"speedtest.slow", &sources.SpeedtestDataset{Down: 90, Up: 40, ExpectDown: 250, At: now}, 1, enums.SeverityWarn},
		{"speedtest.slow", &sources.SpeedtestDataset{Down: 200, ExpectDown: 250, At: now}, 0, 0},
		{"grocy.expired", sources.DemoGrocy(now), 1, enums.SeverityWarn},
		{"grocy.missing", sources.DemoGrocy(now), 1, enums.SeverityInfo},
		{"grocy.chores_overdue", sources.DemoGrocy(now), 1, enums.SeverityInfo},
		{"dwd.warning", sources.DemoDWD(now), 1, enums.SeverityInfo},
		{"github.ci_failed", sources.DemoGitHub(now), 1, enums.SeverityWarn},
		{"energy.cost_rising", sources.DemoTibber(now), 0, 0},
	}
	for _, c := range cases {
		got := run(t, c.rule, c.data, env)
		if len(got) != c.count || (c.count > 0 && got[0].Severity != c.level) {
			t.Errorf("%s: %+v", c.rule, got)
		}
	}

	// A week costing 50 % more than the one before.
	rising := &sources.TibberDataset{Currency: "EUR"}
	for i := range 14 {
		cost := 2.0
		if i >= 7 {
			cost = 3
		}
		rising.Days = append(rising.Days, sources.EnergyDay{Cost: cost})
	}
	if got := run(t, "energy.cost_rising", rising, env); len(got) != 1 {
		t.Errorf("cost rising: %+v", got)
	}
	if topic := rules.RulesOf(rules.TopicUpdates); topic[len(topic)-1] != "mediaserver.update" {
		t.Errorf("updates topic: %v", topic)
	}
}
