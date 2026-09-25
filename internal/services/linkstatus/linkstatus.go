// Package linkstatus checks every link tile in the background and keeps a
// daily tally, for uptime bars and the dead-link hint.
//
//	every 10 min   link tiles with status "http" ──http_status──► link_status(widget, day, ok, fail, ms)
//	tile view      Bars(widget) → last 30 days, worst state per day
//	analysis       DownDays(widget) → days in a row without a single success
package linkstatus

import (
	"context"
	"database/sql"
	"sync"
	"time"

	"dashboard/internal/db"
	"dashboard/internal/repos/content"
	"dashboard/internal/repos/data"
	"dashboard/internal/services/svcdata"
	"dashboard/internal/widgets"
)

const (
	// JobName is the scheduler job running Check.
	JobName    = "linkstatus"
	Interval   = 10 * time.Minute
	barDays    = 30
	keepDays   = 60
	workers    = 8
	isoDay     = "2006-01-02"
	linkWidget = "link"
	statusKey  = "http_status"
)

// check is one tile to probe.
type check struct {
	widgetID int64
	params   map[string]any
}

// Check probes every link tile once and records the results.
func Check(ctx context.Context, d *sql.DB) error {
	var checks []check
	err := db.WithTx(d, func(tx *sql.Tx) error {
		spaces, err := content.AllSpaces(tx)
		if err != nil {
			return err
		}
		ids := make([]int64, len(spaces))
		for i, s := range spaces {
			ids[i] = s.ID
		}
		all, err := content.Widgets(tx, ids)
		if err != nil {
			return err
		}
		for _, w := range all {
			if w.Type != linkWidget {
				continue
			}
			kind, _ := widgets.Get(linkWidget)
			cfg, _ := widgets.Decode(w.Type, w.Config)
			for _, q := range kind.Queries(cfg) {
				if q.Source == statusKey {
					checks = append(checks, check{widgetID: w.ID, params: q.Params})
				}
			}
		}
		return nil
	})
	if err != nil {
		return err
	}

	today := time.Now().UTC().Format(isoDay)
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var firstErr error
	for _, c := range checks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			// Cached: the same result the tiles show, no extra traffic.
			res, err := svcdata.Get(ctx, d, statusKey, c.params, nil, nil, svcdata.Cached)
			status, ok := res.Data.(interface{ Outcome() (bool, int) })
			if err != nil || !ok {
				return
			}
			up, ms := status.Outcome()
			if err := data.RecordStatus(d, c.widgetID, today, up, ms); err != nil {
				mu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if firstErr != nil {
		return firstErr
	}
	return data.PruneStatus(d, time.Now().UTC().AddDate(0, 0, -keepDays).Format(isoDay))
}

// BarState is one day's summary.
type BarState string

const (
	BarUp      BarState = "up"
	BarPartial BarState = "partial"
	BarDown    BarState = "down"
	BarNone    BarState = "none"
)

// Bar is one day of the uptime strip.
type Bar struct {
	Day   string
	State BarState
	Share float64 // successful checks, 0–1
}

// Uptime is a tile's last 30 days.
type Uptime struct {
	Bars  []Bar
	Share float64 // over all checks
	AvgMs int
}

// Bars summarises a tile's last 30 days, oldest first; ok=false when no
// check ran yet.
func Bars(q db.Queryer, widgetID int64, today time.Time) (Uptime, bool) {
	start := today.AddDate(0, 0, -(barDays - 1))
	rows, err := data.StatusSince(q, widgetID, start.Format(isoDay))
	if err != nil || len(rows) == 0 {
		return Uptime{}, false
	}
	byDay := map[string]data.DayStatus{}
	total, good, msSum := 0, 0, 0
	for _, r := range rows {
		byDay[r.Day] = r
		total += r.OK + r.Fail
		good += r.OK
		msSum += r.MsSum
	}
	out := Uptime{}
	for d := start; !d.After(today); d = d.AddDate(0, 0, 1) {
		key := d.Format(isoDay)
		r, ok := byDay[key]
		bar := Bar{Day: key, State: BarNone}
		if ok && r.OK+r.Fail > 0 {
			bar.Share = float64(r.OK) / float64(r.OK+r.Fail)
			switch {
			case r.Fail == 0:
				bar.State = BarUp
			case r.OK == 0:
				bar.State = BarDown
			default:
				bar.State = BarPartial
			}
		}
		out.Bars = append(out.Bars, bar)
	}
	if total > 0 {
		out.Share = float64(good) / float64(total)
	}
	if good > 0 {
		out.AvgMs = msSum / good
	}
	return out, true
}

// DownDays counts the days up to today in which a tile never answered.
func DownDays(q db.Queryer, widgetID int64, today time.Time) int {
	rows, err := data.StatusSince(q, widgetID, today.AddDate(0, 0, -keepDays).Format(isoDay))
	if err != nil {
		return 0
	}
	days := 0
	for i := len(rows) - 1; i >= 0; i-- {
		if rows[i].OK > 0 || rows[i].Fail == 0 {
			break
		}
		days++
	}
	return days
}
