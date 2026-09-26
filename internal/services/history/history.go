// Package history reads what the analysis recorded: key figure series,
// version changes and hint state changes.
//
//	Load      a scope's History for rules and widgets
//	Timeline  what happened in the caller's spaces, newest first
//	Before    what happened shortly before a hint appeared
package history

import (
	"database/sql"
	"sort"
	"time"

	"andon/internal/enums"
	"andon/internal/metrics"
	data "andon/internal/repos/data"
	"andon/internal/services/access"
	"andon/internal/services/hints"
)

const (
	// SeriesDays is how far back series are kept and read (a year for
	// seasonal comparisons).
	SeriesDays = 400
	eventDays  = 30
	eventLimit = 500
	// BeforeWindow is how long before a hint the timeline looks.
	BeforeWindow = 2 * time.Hour
)

// hintKinds are the hint changes that belong on a timeline.
var hintKinds = []string{string(enums.EventOpened), string(enums.EventResolved), string(enums.EventReopened)}

// Load returns a space's series and recent events for one owner (0 = shared).
func Load(d *sql.DB, spaceID, owner int64, now time.Time) (*metrics.History, error) {
	raw, err := data.SamplesSince(d, spaceID, owner, now.AddDate(0, 0, -SeriesDays).Format(time.DateOnly))
	if err != nil {
		return nil, err
	}
	h := &metrics.History{Series: map[string][]metrics.Point{}}
	for k, points := range raw {
		for _, p := range points {
			if day, err := time.Parse(time.DateOnly, p.Day); err == nil {
				h.Series[k] = append(h.Series[k], metrics.Point{Day: day, Value: p.Value})
			}
		}
	}

	since := now.AddDate(0, 0, -eventDays)
	events, err := data.EventsSince(d, []int64{spaceID}, owner, since, eventLimit)
	if err != nil {
		return nil, err
	}
	for _, e := range events {
		h.Events = append(h.Events, metrics.Event{At: e.At, Kind: e.Kind, Subject: e.Subject, Detail: e.Detail})
	}
	changes, err := data.HintEventsSince(d, []int64{spaceID}, hintKinds, since, eventLimit)
	if err != nil {
		return nil, err
	}
	for _, c := range changes {
		if c.Owner != 0 && c.Owner != owner {
			continue
		}
		h.Events = append(h.Events, metrics.Event{At: c.At, Kind: c.Kind, Subject: c.Rule, Detail: c.Fingerprint})
	}
	sort.Slice(h.Events, func(i, j int) bool { return h.Events[i].At.After(h.Events[j].At) })
	return h, nil
}

// Entry is one timeline line: an update, or a hint that came or went.
type Entry struct {
	At      time.Time
	Kind    string // "update", "opened", "resolved", "reopened"
	Subject string // service or hint title
	Detail  string // "v1 → v2"
	HintID  int64  // 0 for updates
	Space   string
}

// Timeline lists what happened in the caller's spaces within days.
func Timeline(d *sql.DB, who *access.Principal, since time.Time, limit int) ([]Entry, error) {
	spaceIDs := make([]int64, 0, len(who.Spaces))
	for id := range who.Spaces {
		spaceIDs = append(spaceIDs, id)
	}
	events, err := data.EventsSince(d, spaceIDs, who.UserID, since, limit)
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, e := range events {
		out = append(out, Entry{At: e.At, Kind: e.Kind, Subject: e.Subject, Detail: e.Detail})
	}
	changes, err := data.HintEventsSince(d, spaceIDs, hintKinds, since, limit)
	if err != nil {
		return nil, err
	}
	for _, c := range changes {
		if c.Owner != 0 && c.Owner != who.UserID {
			continue
		}
		view, err := hints.One(d, who, c.HintID)
		if err != nil {
			continue
		}
		out = append(out, Entry{At: c.At, Kind: c.Kind, Subject: view.Title, HintID: c.HintID, Space: view.SpaceName})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At.After(out[j].At) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// Before lists what happened in the window before a hint first appeared,
// without the hint itself: "4 min before: update Nextcloud 31.0.1 → 31.0.2".
func Before(d *sql.DB, who *access.Principal, hintID int64) ([]Entry, error) {
	hint, err := hints.One(d, who, hintID)
	if err != nil {
		return nil, err
	}
	entries, err := Timeline(d, who, hint.FirstSeen.Add(-BeforeWindow), eventLimit)
	if err != nil {
		return nil, err
	}
	var out []Entry
	for _, e := range entries {
		if e.HintID == hintID || e.At.After(hint.FirstSeen) {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}
