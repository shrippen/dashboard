package sources

// Light Kimai source for live widgets: running timers, recent project and
// activity pairs, today's spans and the week's total – three small calls
// instead of the full dataset.
//
//	GET /api/timesheets/active
//	GET /api/timesheets/recent?size=6
//	GET /api/timesheets?begin=<monday>T00:00:00

import (
	"context"
	"net/url"
	"sort"
	"strconv"
	"time"

	"andon/internal/enums"
)

const (
	kimaiLiveTTL  = 30 * time.Second
	kimaiRecent   = 6
	kimaiDateTime = "2006-01-02T15:04:05"
)

// KimaiTimer is a running or recent timesheet with names resolved.
type KimaiTimer struct {
	ID                    int64
	ProjectID, ActivityID int64
	Project, Activity     string
	Customer              string
	Color                 string // customer colour, else the project's ("#rrggbb", "" = none)
	Begin                 time.Time
}

// KimaiSpan is one of today's timesheets; End is zero while it runs.
type KimaiSpan struct {
	Begin, End time.Time
}

// KimaiLive is the live view of one Kimai user.
type KimaiLive struct {
	URL      string
	Active   []KimaiTimer
	Recent   []KimaiTimer
	TodayMin int
	WeekMin  int
	Today    []KimaiSpan // stopped sheets of today, oldest first
}

type KimaiLiveSource struct{}

func (KimaiLiveSource) Key() string                { return "kimai.live" }
func (KimaiLiveSource) TTL() time.Duration         { return kimaiLiveTTL }
func (KimaiLiveSource) Service() enums.ServiceType { return enums.ServiceKimai }

func (KimaiLiveSource) Fetch(ctx context.Context, sctx Ctx) (any, error) {
	if isDemo(sctx) {
		return DemoKimaiLive(time.Now()), nil
	}
	api, err := kimaiAPI(sctx)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	out := &KimaiLive{URL: sctx.URL}

	active, err := api.Get(ctx, "timesheets/active", nil)
	if err != nil {
		return nil, fetchError(err)
	}
	for _, raw := range asList(active) {
		timer := kimaiTimer(raw)
		out.Active = append(out.Active, timer)
		out.TodayMin += int(now.Sub(timer.Begin).Minutes())
	}

	recent, err := api.Get(ctx, "timesheets/recent", url.Values{"size": {strconv.Itoa(kimaiRecent)}})
	if err != nil {
		return nil, fetchError(err)
	}
	for _, raw := range asList(recent) {
		out.Recent = append(out.Recent, kimaiTimer(raw))
	}

	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	monday := midnight.AddDate(0, 0, -daysSinceMonday(midnight))
	week, err := api.Pages(ctx, "timesheets", url.Values{"begin": {monday.Format(kimaiDateTime)}})
	if err != nil {
		return nil, fetchError(err)
	}
	out.WeekMin = out.TodayMin
	for _, raw := range week {
		m := asMap(raw)
		minutes := int(round(asFloat(m["duration"]) / secondsPerMin)) // as the Kimai dataset does
		out.WeekMin += minutes

		begin, end := kimaiTime(asStr(m["begin"])), kimaiTime(asStr(m["end"]))
		if begin.IsZero() || end.IsZero() || begin.Before(midnight) {
			continue
		}
		out.TodayMin += minutes
		out.Today = append(out.Today, KimaiSpan{Begin: begin, End: end})
	}
	sort.Slice(out.Today, func(a, b int) bool { return out.Today[a].Begin.Before(out.Today[b].Begin) })
	return out, nil
}

// daysSinceMonday: Monday 0 … Sunday 6.
func daysSinceMonday(t time.Time) int {
	return (int(t.Weekday()) + 6) % 7
}

// kimaiTimer reads a timesheet whose project/activity may be expanded
// objects or plain ids.
func kimaiTimer(raw any) KimaiTimer {
	m := asMap(raw)
	project, activity := asMap(m["project"]), asMap(m["activity"])
	customer := asMap(project["customer"])
	t := KimaiTimer{ID: asInt64(m["id"]), ProjectID: refID(m["project"]), ActivityID: refID(m["activity"]),
		Project: asStr(project["name"]), Activity: asStr(activity["name"]), Customer: asStr(customer["name"])}
	t.Color = asStr(customer["color"])
	if t.Color == "" {
		t.Color = asStr(project["color"])
	}
	t.Begin = kimaiTime(asStr(m["begin"]))
	return t
}

// kimaiTimeLayouts: Kimai writes "2026-09-26T13:04:00+0200" (no colon in
// the offset), which RFC3339 rejects.
var kimaiTimeLayouts = []string{"2006-01-02T15:04:05-0700", time.RFC3339}

// kimaiTime parses a Kimai timestamp; zero if it is none.
func kimaiTime(s string) time.Time {
	for _, layout := range kimaiTimeLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

func init() {
	Register(KimaiLiveSource{})
}
