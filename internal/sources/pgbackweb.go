package sources

// PG Back Web has no read API; it calls webhooks. Its events arrive in
// Ctx.Events and are folded into the latest state per backup:
//
//	execution_success / execution_failed        → per backup
//	database_(un)healthy, destination_(un)healthy → per target

import (
	"context"
	"sort"
	"strings"
	"time"

	"dashboard/internal/enums"
)

const (
	pgbackWindow    = 30 * 24 * time.Hour
	EventSuccess    = "execution_success"
	EventFailed     = "execution_failed"
	healthySuffix   = "_healthy"
	unhealthySuffix = "_unhealthy"
)

// PGBackup is one backup's latest outcomes.
type PGBackup struct {
	Name                     string
	LastSuccess, LastFailure time.Time
}

// Failing reports whether the newest outcome is a failure.
func (b PGBackup) Failing() bool { return b.LastFailure.After(b.LastSuccess) }

// PGHealth is a database or destination currently reported unhealthy.
type PGHealth struct {
	Kind, Name string // Kind: database or destination
	Since      time.Time
}

type PGBackDataset struct {
	URL       string
	Backups   []PGBackup
	Unhealthy []PGHealth
	LastEvent time.Time // zero: no webhook received in the window
}

type PGBackData struct{}

func (PGBackData) Key() string                { return "pgbackweb.data" }
func (PGBackData) TTL() time.Duration         { return time.Minute }
func (PGBackData) Service() enums.ServiceType { return enums.ServicePGBackWeb }
func (PGBackData) PushWindow() time.Duration  { return pgbackWindow }

func (PGBackData) Fetch(_ context.Context, sctx Ctx) (any, error) {
	if isDemo(sctx) {
		return DemoPGBack(time.Now()), nil
	}
	return foldPGBack(sctx.URL, sctx.Events), nil
}

func foldPGBack(base string, events []Pushed) *PGBackDataset {
	data := &PGBackDataset{URL: base}
	backups := map[string]*PGBackup{}
	health := map[[2]string]*PGHealth{}
	for _, e := range events {
		data.LastEvent = e.At
		switch {
		case e.Event == EventSuccess || e.Event == EventFailed:
			b, ok := backups[e.Subject]
			if !ok {
				b = &PGBackup{Name: e.Subject}
				backups[e.Subject] = b
			}
			if e.Event == EventSuccess {
				b.LastSuccess = e.At
			} else {
				b.LastFailure = e.At
			}
		case strings.HasSuffix(e.Event, unhealthySuffix):
			key := [2]string{strings.TrimSuffix(e.Event, unhealthySuffix), e.Subject}
			if _, open := health[key]; !open {
				health[key] = &PGHealth{Kind: key[0], Name: e.Subject, Since: e.At}
			}
		case strings.HasSuffix(e.Event, healthySuffix):
			delete(health, [2]string{strings.TrimSuffix(e.Event, healthySuffix), e.Subject})
		}
	}
	for _, b := range backups {
		data.Backups = append(data.Backups, *b)
	}
	for _, h := range health {
		data.Unhealthy = append(data.Unhealthy, *h)
	}
	sort.Slice(data.Backups, func(i, j int) bool { return data.Backups[i].Name < data.Backups[j].Name })
	sort.Slice(data.Unhealthy, func(i, j int) bool { return data.Unhealthy[i].Name < data.Unhealthy[j].Name })
	return data
}

func init() {
	Register(PGBackData{})
	Register(testOf{PGBackData{}, func(d any) map[string]any { return map[string]any{"backups": len(d.(*PGBackDataset).Backups)} }})
}
