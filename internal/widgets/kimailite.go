package widgets

// kimai_timer ("Kimai Lite"): a small web version of the Plasmai panel
// widget on the Kimai connection.
//
//	┌──────────────────────────────────────┐
//	│ 01:28:16                      Intern │  running timer, ticks in the browser
//	│ Server/Dienste · Wartung             │
//	│ seit 13:04 · Heute 4:53 · Woche 26:30│
//	│ ▕██▁▁███▁▁▁▁▓▓▓│▁▁▁▁▁▁▏  09 … 21      │  today: booked, running, now
//	│ [Beschreibung…]              [Stopp] │
//	│ Zuletzt  ▶ Muster · Website          │  start, or switch when running
//	└──────────────────────────────────────┘

import (
	"fmt"
	"time"

	"andon/internal/enums"
	"andon/internal/sources"
)

const (
	dayBarFrom  = 9  // the day bar spans at least 09–21 …
	dayBarTo    = 21 // … and widens for earlier or later work
	dayBarTick  = 3  // hours between labels
	recentShown = 4
)

// KimaiLiteConfig is the "kimai_timer" widget's config.
type KimaiLiteConfig struct {
	WeekHours float64 // weekly target, 0 = none
}

func decodeKimaiLite(raw map[string]any) any {
	return KimaiLiteConfig{WeekHours: max(0, asFloat(raw["week_hours"]))}
}

// TimerRow is one running or startable timer.
type TimerRow struct {
	ID, ProjectID, ActivityID   int64
	Project, Activity, Customer string
	Color                       string
	Begin                       string // RFC3339, for the ticking clock
	Since                       string // "13:04"
	Elapsed                     string // "1:28:16"
}

// DaySeg is one booked or running block of the day bar, in percent.
type DaySeg struct {
	L, W float64
	Run  bool
}

// DayTick is one hour label of the day bar.
type DayTick struct {
	Hour int
	Pos  float64
}

func timerRow(t sources.KimaiTimer) TimerRow {
	return TimerRow{ID: t.ID, ProjectID: t.ProjectID, ActivityID: t.ActivityID, Project: t.Project, Activity: t.Activity,
		Customer: t.Customer, Color: t.Color}
}

// clockMinutes: 67 → "1:07".
func clockMinutes(m int) string {
	return fmt.Sprintf("%d:%02d", m/minutesPerHour, m%minutesPerHour)
}

// clockSeconds: 1h28m16s → "1:28:16".
func clockSeconds(d time.Duration) string {
	s := int(max(d, 0).Seconds())
	return fmt.Sprintf("%d:%02d:%02d", s/3600, s/60%60, s%60)
}

func timerView(cfgAny any, results map[string]any, _ ViewCtx) map[string]any {
	cfg, _ := cfgAny.(KimaiLiteConfig)
	data, ok := results["live"].(*sources.KimaiLive)
	if !ok {
		return map[string]any{}
	}
	now := time.Now()
	out := map[string]any{"URL": data.URL, "Today": clockMinutes(data.TodayMin), "Week": clockMinutes(data.WeekMin)}

	spans := data.Today
	running := map[[2]int64]bool{}
	if len(data.Active) > 0 {
		t := data.Active[0]
		row := timerRow(t)
		if !t.Begin.IsZero() {
			local := t.Begin.In(now.Location())
			row.Begin, row.Since, row.Elapsed = t.Begin.Format(time.RFC3339), local.Format("15:04"), clockSeconds(now.Sub(t.Begin))
			spans = append(spans, sources.KimaiSpan{Begin: t.Begin})
		}
		out["Running"] = row
		for _, a := range data.Active {
			running[[2]int64{a.ProjectID, a.ActivityID}] = true
		}
	}

	var recent []TimerRow
	for _, t := range data.Recent {
		if !running[[2]int64{t.ProjectID, t.ActivityID}] && len(recent) < recentShown {
			recent = append(recent, timerRow(t))
		}
	}
	out["Recent"] = recent

	if cfg.WeekHours > 0 {
		left := int(cfg.WeekHours*minutesPerHour) - data.WeekMin
		out["Target"], out["Left"], out["Over"] = cfg.WeekHours, clockMinutes(max(left, -left)), left < 0
	}

	from, to, segs, pos := dayBar(spans, now)
	out["DaySegs"], out["DayNow"], out["DayTicks"] = segs, pos, dayTicks(from, to)
	return out
}

// dayBar places today's spans on an hour axis of at least 09–21:
//
//	spans 09:05–11:40, 13:04–now(14:32)  →  axis 9–21, blocks at 0.7 % and 34 %
func dayBar(spans []sources.KimaiSpan, now time.Time) (int, int, []DaySeg, float64) {
	from, to := dayBarFrom, dayBarTo
	hourOf := func(t time.Time) float64 {
		t = t.In(now.Location())
		return float64(t.Hour()) + float64(t.Minute())/minutesPerHour
	}
	for _, s := range spans {
		end := s.End
		if end.IsZero() {
			end = now
		}
		from = min(from, int(hourOf(s.Begin)))
		to = max(to, int(hourOf(end))+1)
	}
	to = min(to, 24)

	span := float64(to - from)
	pct := func(h float64) float64 { return min(max((h-float64(from))/span*pctFull, 0), pctFull) }
	var segs []DaySeg
	for _, s := range spans {
		end, run := s.End, s.End.IsZero()
		if run {
			end = now
		}
		l := pct(hourOf(s.Begin))
		segs = append(segs, DaySeg{L: l, W: max(pct(hourOf(end))-l, 0.5), Run: run})
	}
	return from, to, segs, pct(hourOf(now))
}

// dayTicks labels the axis every dayBarTick hours from its start.
func dayTicks(from, to int) []DayTick {
	var out []DayTick
	for h := from; h <= to; h += dayBarTick {
		out = append(out, DayTick{Hour: h, Pos: float64(h-from) / float64(to-from) * pctFull})
	}
	return out
}

func init() {
	Register(WidgetType{Key: "kimai_timer", Decode: decodeKimaiLite, Template: "widgets/kimai_timer", Category: CategoryInsight,
		Service: enums.ServiceKimai, RefreshS: 60, Live: true, View: timerView,
		Queries: func(any) []Query { return []Query{{Name: "live", Source: "kimai.live", Conn: ConnWidget}} }})
}
