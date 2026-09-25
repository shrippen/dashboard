package rules

import (
	"fmt"
	"strings"
	"time"

	"dashboard/internal/enums"
	"dashboard/internal/metrics"
	"dashboard/internal/sources"
)

const (
	kimaiSource        = "kimai"
	kimaiOpen          = "open_in_kimai"
	monthCloseDays     = 5
	utilizationFromDay = 10
)

func kimaiURL(data *sources.KimaiDataset, path string) string {
	return strings.TrimRight(data.URL, "/") + "/" + path
}

func hoursParam(minutes float64) map[string]any {
	return Num(minutes/60, 1)
}

func kimaiData(data any) *sources.KimaiDataset {
	d, _ := data.(*sources.KimaiDataset)
	return d
}

func init() {
	Register("kimai.timer_running_long", string(enums.ServiceKimai), map[string]any{"hours": 10.0},
		func(raw any, cfg map[string]any, env Env) []Finding {
			data := kimaiData(raw)
			var found []Finding
			for _, r := range metrics.KimaiRunningNow(data, time.Now().UTC()) {
				if float64(r.RunningMin) < cfgFloat(cfg, "hours")*60 {
					continue
				}
				found = append(found, Finding{
					Fingerprint: fmt.Sprintf("timer:%d", r.Sheet.ID), Rule: "kimai.timer_running_long",
					Severity: enums.SeverityWarn, Message: "kimai.timer_long",
					Params:    map[string]any{"hours": hoursParam(float64(r.RunningMin))},
					ActionURL: kimaiURL(data, "timesheet/"), ActionLabel: kimaiOpen, Sources: []string{kimaiSource},
				})
			}
			return found
		})

	Register("kimai.missing_day", string(enums.ServiceKimai), map[string]any{"lookback_days": 10.0},
		func(raw any, cfg map[string]any, env Env) []Finding {
			data := kimaiData(raw)
			booked := map[time.Time]bool{}
			for _, s := range data.Timesheets {
				if d, ok := metrics.ParseDay(s.Begin); ok {
					booked[d] = true
				}
			}
			start := env.Today.AddDate(0, 0, -cfgInt(cfg, "lookback_days"))
			days := metrics.Workdays(start, env.Today.AddDate(0, 0, -1), metrics.KimaiFreeDays(data))

			var found []Finding
			for _, d := range days {
				if booked[d] {
					continue
				}
				found = append(found, Finding{
					Fingerprint: "missing:" + d.Format("2006-01-02"), Rule: "kimai.missing_day",
					Severity: enums.SeverityInfo, Message: "kimai.missing_day",
					Params: map[string]any{"day": Day(d)}, ActionURL: kimaiURL(data, "timesheet/"),
					ActionLabel: kimaiOpen, Sources: []string{kimaiSource},
				})
			}
			return found
		})

	Register("kimai.unbilled_hours", string(enums.ServiceKimai),
		map[string]any{"warn_days": 30.0, "critical_days": 60.0},
		func(raw any, cfg map[string]any, env Env) []Finding {
			data := kimaiData(raw)
			var found []Finding
			for _, g := range metrics.KimaiUnbilled(data, env.Today) {
				if g.AgeDays < cfgInt(cfg, "warn_days") {
					continue
				}
				level := enums.SeverityWarn
				if g.AgeDays >= cfgInt(cfg, "critical_days") {
					level = enums.SeverityCritical
				}
				found = append(found, Finding{
					Fingerprint: fmt.Sprintf("unbilled:%d", g.CustomerID), Rule: "kimai.unbilled_hours",
					Severity: level, Message: "kimai.unbilled",
					Params: map[string]any{
						"hours": hoursParam(float64(g.Minutes)), "customer": g.Customer,
						"amount": Money(g.Amount, ""), "oldest": DayStr(g.Oldest), "days": cfgFloat(cfg, "warn_days"),
					},
					ActionURL: kimaiURL(data, "abrechnung"), ActionLabel: kimaiOpen,
					Sources: []string{kimaiSource, string(enums.ServiceInvoiceNinja)},
				})
			}
			return found
		})

	Register("kimai.budget_burn", string(enums.ServiceKimai), map[string]any{"warn": 0.8, "critical": 1.0},
		func(raw any, cfg map[string]any, env Env) []Finding {
			data := kimaiData(raw)
			var found []Finding
			for _, p := range data.Projects {
				money, minutes := budgetUsed(p, data, env)
				var ratios []float64
				if p.Budget != 0 {
					ratios = append(ratios, money/p.Budget)
				}
				if p.TimeBudgetMin != 0 {
					ratios = append(ratios, float64(minutes)/float64(p.TimeBudgetMin))
				}
				if len(ratios) == 0 {
					continue
				}
				ratio := maxOf(ratios)
				if ratio < cfgFloat(cfg, "warn") {
					continue
				}
				level := enums.SeverityWarn
				if ratio >= cfgFloat(cfg, "critical") {
					level = enums.SeverityCritical
				}
				found = append(found, Finding{
					Fingerprint: fmt.Sprintf("budget:%d", p.ID), Rule: "kimai.budget_burn", Severity: level,
					Message:     "kimai.budget",
					Params:      map[string]any{"project": p.Name, "percent": Num(ratio*100, 0)},
					ActionURL:   kimaiURL(data, fmt.Sprintf("admin/project/%d/details", p.ID)),
					ActionLabel: kimaiOpen, Sources: []string{kimaiSource},
				})
			}
			return found
		})

	Register("kimai.budget_pace", string(enums.ServiceKimai), nil,
		func(raw any, cfg map[string]any, env Env) []Finding {
			data := kimaiData(raw)
			var found []Finding
			for _, p := range data.Projects {
				end, ok := metrics.ParseDay(p.End)
				if p.Budget == 0 || !ok || !end.After(env.Today) || p.BudgetType != "" {
					continue
				}
				since := env.Today.AddDate(0, 0, -30)
				var recentMoney float64
				for _, s := range data.Timesheets {
					d, ok := metrics.ParseDay(s.Begin)
					if s.ProjectID == p.ID && ok && !d.Before(since) {
						recentMoney += s.Rate
					}
				}
				perDay := recentMoney / 30
				forecast := p.UsedMoney + perDay*float64(int(end.Sub(env.Today).Hours()/24))
				if forecast <= p.Budget {
					continue
				}
				found = append(found, Finding{
					Fingerprint: fmt.Sprintf("pace:%d", p.ID), Rule: "kimai.budget_pace",
					Severity: enums.SeverityWarn, Message: "kimai.pace",
					Params: map[string]any{
						"project": p.Name, "forecast": Money(forecast, ""), "budget": Money(p.Budget, ""),
						"end": Day(end),
					},
					ActionURL:   kimaiURL(data, fmt.Sprintf("admin/project/%d/details", p.ID)),
					ActionLabel: kimaiOpen, Sources: []string{kimaiSource},
				})
			}
			return found
		})

	Register("kimai.utilization_low", string(enums.ServiceKimai), map[string]any{"goal": 0.7, "hours_per_day": 8.0},
		func(raw any, cfg map[string]any, env Env) []Finding {
			if env.Today.Day() < utilizationFromDay {
				return nil
			}
			data := kimaiData(raw)
			stats := metrics.KimaiSummaryOf(data, env.Today, cfgFloat(cfg, "hours_per_day"))
			if stats.Utilization == nil || *stats.Utilization >= cfgFloat(cfg, "goal") {
				return nil
			}
			return []Finding{{
				Fingerprint: "utilization:" + env.Today.Format("2006-01"), Rule: "kimai.utilization_low",
				Severity: enums.SeverityInfo, Message: "kimai.utilization",
				Params:  map[string]any{"percent": Num(*stats.Utilization*100, 0), "goal": Num(cfgFloat(cfg, "goal")*100, 0)},
				Sources: []string{kimaiSource},
			}}
		})

	Register("kimai.overtime", string(enums.ServiceKimai), map[string]any{"max_week_hours": 45.0, "weeks": 2.0},
		func(raw any, cfg map[string]any, env Env) []Finding {
			data := kimaiData(raw)
			perWeek := map[time.Time]int{}
			for _, s := range data.Timesheets {
				if d, ok := metrics.ParseDay(s.Begin); ok {
					perWeek[metrics.WeekStart(d)] += s.Minutes
				}
			}

			weeksBack := cfgInt(cfg, "weeks")
			current := metrics.WeekStart(env.Today)
			weeks := make([]time.Time, weeksBack)
			for i := 0; i < weeksBack; i++ {
				weeks[i] = current.AddDate(0, 0, -7*(i+1))
			}
			limit := cfgFloat(cfg, "max_week_hours") * 60
			for _, w := range weeks {
				if float64(perWeek[w]) <= limit {
					return nil
				}
			}
			var sum int
			for _, w := range weeks {
				sum += perWeek[w]
			}
			average := float64(sum) / float64(len(weeks))
			return []Finding{{
				Fingerprint: "overtime:" + weeks[0].Format("2006-01-02"), Rule: "kimai.overtime",
				Severity: enums.SeverityInfo, Message: "kimai.overtime",
				Params: map[string]any{
					"hours": hoursParam(average), "weeks": weeksBack, "limit": cfgFloat(cfg, "max_week_hours"),
				},
				Sources: []string{kimaiSource},
			}}
		})

	Register("kimai.monthly_close", string(enums.ServiceKimai), nil,
		func(raw any, cfg map[string]any, env Env) []Finding {
			if env.Today.Day() > monthCloseDays {
				return nil
			}
			data := kimaiData(raw)
			start := metrics.AddMonths(env.Today, -1)
			end := metrics.MonthStart(env.Today).AddDate(0, 0, -1)
			var openMin int
			for _, s := range data.Timesheets {
				d, ok := metrics.ParseDay(s.Begin)
				if s.Billable && !s.Exported && ok && !d.Before(start) && !d.After(end) {
					openMin += s.Minutes
				}
			}
			if openMin == 0 {
				return nil
			}
			return []Finding{{
				Fingerprint: "close:" + start.Format("2006-01"), Rule: "kimai.monthly_close",
				Severity: enums.SeverityWarn, Message: "kimai.monthly_close",
				Params:    map[string]any{"month": start.Format("01/2006"), "hours": hoursParam(float64(openMin))},
				ActionURL: kimaiURL(data, "abrechnung"), ActionLabel: kimaiOpen, Sources: []string{kimaiSource},
			}}
		})
}

// budgetUsed returns (money, minutes) used against a project's budget;
// monthly budgets count only the current month.
func budgetUsed(p sources.KimaiProject, data *sources.KimaiDataset, env Env) (float64, int) {
	if p.BudgetType != "month" {
		return p.UsedMoney, p.UsedMinutes
	}
	start := metrics.MonthStart(env.Today)
	var money float64
	var minutes int
	for _, s := range data.Timesheets {
		d, ok := metrics.ParseDay(s.Begin)
		if s.ProjectID == p.ID && ok && !d.Before(start) {
			money += s.Rate
			minutes += s.Minutes
		}
	}
	return money, minutes
}

func maxOf(vs []float64) float64 {
	m := vs[0]
	for _, v := range vs[1:] {
		if v > m {
			m = v
		}
	}
	return m
}
