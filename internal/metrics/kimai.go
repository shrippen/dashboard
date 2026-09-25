// Package metrics: Kimai — hours, utilization, unbilled work, budgets.
package metrics

import (
	"sort"
	"time"

	"dashboard/internal/sources"
)

const (
	minutesPerHour     = 60
	defaultHoursPerDay = 8
)

// Hours selects which timesheet entries count.
type Hours int

const (
	HoursAll Hours = iota
	HoursBillable
)

func sheetDay(s sources.KimaiSheet) (time.Time, bool) { return ParseDay(s.Begin) }

// KimaiFreeDays returns public holidays and approved absences
// (kimai-holiday-bundle), as full (non-half) days off.
func KimaiFreeDays(data *sources.KimaiDataset) map[time.Time]bool {
	days := map[time.Time]bool{}
	for _, h := range data.Holidays {
		if h.HalfDay || h.Date == "" {
			continue
		}
		if d, ok := ParseDay(h.Date); ok {
			days[d] = true
		}
	}
	for _, a := range data.Absences {
		if a.HalfDay || (a.Status != "" && a.Status != "approved") {
			continue
		}
		for d := range Expand(a.Start, a.End) {
			days[d] = true
		}
	}
	return days
}

// KimaiMinutesBetween sums timesheet minutes in [start, end], optionally
// billable-only.
func KimaiMinutesBetween(data *sources.KimaiDataset, start, end time.Time, kind Hours) int {
	total := 0
	for _, s := range data.Timesheets {
		d, ok := sheetDay(s)
		if !ok || d.Before(start) || d.After(end) {
			continue
		}
		if kind == HoursBillable && !s.Billable {
			continue
		}
		total += s.Minutes
	}
	return total
}

// KimaiValueBetween sums the billable rate of timesheets in [start, end].
func KimaiValueBetween(data *sources.KimaiDataset, start, end time.Time) float64 {
	var total float64
	for _, s := range data.Timesheets {
		d, ok := sheetDay(s)
		if !ok || d.Before(start) || d.After(end) || !s.Billable {
			continue
		}
		total += s.Rate
	}
	return total
}

// KimaiRunning is a running timer plus its elapsed minutes.
type KimaiRunning struct {
	Sheet      sources.KimaiSheet
	RunningMin int
}

// KimaiRunningNow returns every currently active timer with elapsed minutes.
func KimaiRunningNow(data *sources.KimaiDataset, now time.Time) []KimaiRunning {
	var out []KimaiRunning
	for _, s := range data.Active {
		begin, ok := ParseTime(s.Begin)
		if !ok {
			continue
		}
		out = append(out, KimaiRunning{Sheet: s, RunningMin: int(now.Sub(begin).Minutes())})
	}
	return out
}

// KimaiCustomerNames maps customer id to name.
func KimaiCustomerNames(data *sources.KimaiDataset) map[int64]string {
	out := make(map[int64]string, len(data.Customers))
	for _, c := range data.Customers {
		out[c.ID] = c.Name
	}
	return out
}

// KimaiUnbilledGroup is one customer's unbilled work.
type KimaiUnbilledGroup struct {
	CustomerID int64
	Customer   string
	Minutes    int
	Amount     float64
	Oldest     string // "" if none
	AgeDays    int
}

// KimaiUnbilled groups billable, finished, not-yet-exported entries per
// customer (like the abrechnung bundle), oldest-first.
func KimaiUnbilled(data *sources.KimaiDataset, today time.Time) []KimaiUnbilledGroup {
	type agg struct {
		minutes int
		amount  float64
		oldest  *time.Time
	}
	groups := map[int64]*agg{}
	for _, s := range data.Timesheets {
		if !s.Billable || s.Exported || s.End == "" {
			continue
		}
		g, ok := groups[s.CustomerID]
		if !ok {
			g = &agg{}
			groups[s.CustomerID] = g
		}
		g.minutes += s.Minutes
		g.amount += s.Rate
		if d, ok := sheetDay(s); ok && (g.oldest == nil || d.Before(*g.oldest)) {
			g.oldest = &d
		}
	}

	names := KimaiCustomerNames(data)
	out := make([]KimaiUnbilledGroup, 0, len(groups))
	for cid, g := range groups {
		name := names[cid]
		if name == "" {
			name = "?"
		}
		row := KimaiUnbilledGroup{CustomerID: cid, Customer: name, Minutes: g.minutes, Amount: round2(g.amount)}
		if g.oldest != nil {
			row.Oldest = g.oldest.Format("2006-01-02")
			row.AgeDays = int(today.Sub(*g.oldest).Hours() / 24)
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AgeDays > out[j].AgeDays })
	return out
}

// KimaiTargetMinutes is the workday target for [start, end] at hoursPerDay.
func KimaiTargetMinutes(data *sources.KimaiDataset, start, end time.Time, hoursPerDay float64) int {
	days := Workdays(start, end, KimaiFreeDays(data))
	return int(round(float64(len(days)) * hoursPerDay * minutesPerHour))
}

// KimaiSummary is the Kimai dashboard's headline numbers.
type KimaiSummary struct {
	TodayMin         int
	WeekMin          int
	MonthMin         int
	BillableMonthMin int
	TargetMonthMin   int
	Utilization      *float64 // nil if there is no target yet
	MonthValue       float64
	Running          []KimaiRunning
	Unbilled         []KimaiUnbilledGroup
}

// KimaiSummaryOf computes KimaiSummary for today.
func KimaiSummaryOf(data *sources.KimaiDataset, today time.Time, hoursPerDay float64) KimaiSummary {
	if hoursPerDay == 0 {
		hoursPerDay = defaultHoursPerDay
	}
	month := MonthStart(today)
	monthMin := KimaiMinutesBetween(data, month, today, HoursAll)
	billableMonth := KimaiMinutesBetween(data, month, today, HoursBillable)
	target := KimaiTargetMinutes(data, month, today, hoursPerDay)

	var utilization *float64
	if target > 0 {
		u := float64(billableMonth) / float64(target)
		utilization = &u
	}

	return KimaiSummary{
		TodayMin: KimaiMinutesBetween(data, today, today, HoursAll),
		WeekMin:  KimaiMinutesBetween(data, WeekStart(today), today, HoursAll),
		MonthMin: monthMin, BillableMonthMin: billableMonth, TargetMonthMin: target,
		Utilization: utilization, MonthValue: round2(KimaiValueBetween(data, month, today)),
		Running: KimaiRunningNow(data, time.Now().UTC()), Unbilled: KimaiUnbilled(data, today),
	}
}

// KimaiLast12MonthsBillable sums billable minutes over the trailing year.
func KimaiLast12MonthsBillable(data *sources.KimaiDataset, today time.Time) int {
	return KimaiMinutesBetween(data, today.AddDate(0, 0, -365), today, HoursBillable)
}

func round(f float64) float64 {
	if f < 0 {
		return float64(int64(f - 0.5))
	}
	return float64(int64(f + 0.5))
}

func round2(f float64) float64 {
	return round(f*100) / 100
}
