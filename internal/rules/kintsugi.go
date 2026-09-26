package rules

//	kintsugi.run_failed   the last daily suggestion run failed
//	kintsugi.budget       the monthly research budget is used up
//	kintsugi.stale        open suggestions wait longer than "days"

import (
	"time"

	"andon/internal/enums"
	"andon/internal/sources"
)

func init() {
	registerKintsugi()
}

func registerKintsugi() {
	svc := string(enums.ServiceKintsugi)

	Register("kintsugi.run_failed", svc, nil, func(raw any, _ map[string]any, _ Env) []Finding {
		data, _ := raw.(*sources.KintsugiDataset)
		run := data.LastRun
		if run == nil || run.Status != sources.KintsugiRunFailed {
			return nil
		}
		return []Finding{svcFinding(svc, "kintsugi.run_failed", "run:"+run.At.UTC().Format(time.RFC3339), "kintsugi.run_failed",
			enums.SeverityWarn, data.URL+"/vorschlaege", map[string]any{"detail": run.Detail})}
	})

	// Research stops for the month once the budget is spent; the
	// suggestions then come from the profile alone.
	Register("kintsugi.budget", svc, nil, func(raw any, _ map[string]any, env Env) []Finding {
		data, _ := raw.(*sources.KintsugiDataset)
		if !data.Research || data.BudgetUSD <= 0 || data.UsedUSD < data.BudgetUSD {
			return nil
		}
		return []Finding{svcFinding(svc, "kintsugi.budget", "budget:"+env.Today.Format("2006-01"), "kintsugi.budget",
			enums.SeverityInfo, data.URL+"/einstellungen", map[string]any{"budget": data.BudgetUSD})}
	})

	Register("kintsugi.stale", svc, map[string]any{"days": 7.0}, func(raw any, cfg map[string]any, env Env) []Finding {
		data, _ := raw.(*sources.KintsugiDataset)
		oldest, ok := data.Oldest()
		if !ok {
			return nil
		}
		days := int(env.Today.Sub(oldest).Hours() / hoursPerDay)
		if days < cfgInt(cfg, "days") {
			return nil
		}
		return []Finding{svcFinding(svc, "kintsugi.stale", "stale:"+oldest.UTC().Format(time.RFC3339), "kintsugi.stale",
			enums.SeverityInfo, data.URL+"/vorschlaege", map[string]any{"count": data.New, "days": days})}
	})
}
