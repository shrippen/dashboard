package sources

// Light Kimai source for live widgets: running timers, recent project and
// activity pairs, and today's total – three small calls instead of the
// full dataset.
//
//	GET /api/timesheets/active
//	GET /api/timesheets/recent?size=5
//	GET /api/timesheets?begin=<today>T00:00:00

import (
	"context"
	"net/url"
	"strconv"
	"time"

	"dashboard/internal/enums"
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
	Begin                 time.Time
}

// KimaiLive is the live view of one Kimai user.
type KimaiLive struct {
	URL      string
	Active   []KimaiTimer
	Recent   []KimaiTimer
	TodayMin int
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
	today, err := api.Pages(ctx, "timesheets", url.Values{"begin": {midnight.Format(kimaiDateTime)}})
	if err != nil {
		return nil, fetchError(err)
	}
	for _, raw := range today {
		out.TodayMin += int(asFloat(asMap(raw)["duration"])) / secondsPerMin
	}
	return out, nil
}

// kimaiTimer reads a timesheet whose project/activity may be expanded
// objects or plain ids.
func kimaiTimer(raw any) KimaiTimer {
	m := asMap(raw)
	project, activity := asMap(m["project"]), asMap(m["activity"])
	t := KimaiTimer{ID: asInt64(m["id"]), ProjectID: refID(m["project"]), ActivityID: refID(m["activity"]),
		Project: asStr(project["name"]), Activity: asStr(activity["name"]), Customer: asStr(asMap(project["customer"])["name"])}
	t.Begin, _ = time.Parse(time.RFC3339, asStr(m["begin"]))
	return t
}

func init() {
	Register(KimaiLiveSource{})
}
