package analysis

// Each run stores the scope's key figures and version changes, then
// hands the stored history to the rules:
//
//	datasets → metrics.Samples  → samples (one value per key and day)
//	datasets → metrics.Versions → versions; a change → events ("update")
//	samples (historyDays) + events (eventDays) → Datasets["history"]

import (
	"database/sql"
	"time"

	"dashboard/internal/db"
	"dashboard/internal/metrics"
	data "dashboard/internal/repos/data"
)

const (
	historyDays = 400 // a year back for seasonal comparisons
	eventDays   = 30
)

func ownerID(owner *int64) int64 {
	if owner == nil {
		return 0
	}
	return *owner
}

// recordHistory stores today's figures and version changes of a scope
// and returns its history for the rules.
func recordHistory(d *sql.DB, sc *scope, now time.Time) (*metrics.History, error) {
	owner := ownerID(sc.owner)
	day := now.Format(time.DateOnly)
	err := db.WithTx(d, func(tx *sql.Tx) error {
		if err := data.PutSamples(tx, sc.spaceID, owner, day, metrics.Samples(sc.datasets)); err != nil {
			return err
		}
		known, err := data.Versions(tx, sc.spaceID, owner)
		if err != nil {
			return err
		}
		for subject, version := range metrics.Versions(sc.datasets) {
			if e, ok := metrics.VersionEvent(subject, known[subject], version, now); ok {
				if err := data.AddEvent(tx, sc.spaceID, data.Event{At: e.At, Kind: e.Kind, Subject: e.Subject, Detail: e.Detail, Owner: owner}); err != nil {
					return err
				}
			}
			if known[subject] != version {
				if err := data.SetVersion(tx, sc.spaceID, owner, subject, version, now); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return loadHistory(d, sc.spaceID, owner, now)
}

func loadHistory(d *sql.DB, spaceID, owner int64, now time.Time) (*metrics.History, error) {
	raw, err := data.SamplesSince(d, spaceID, owner, now.AddDate(0, 0, -historyDays).Format(time.DateOnly))
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
	events, err := data.EventsSince(d, []int64{spaceID}, owner, now.AddDate(0, 0, -eventDays), eventLimit)
	if err != nil {
		return nil, err
	}
	for _, e := range events {
		h.Events = append(h.Events, metrics.Event{At: e.At, Kind: e.Kind, Subject: e.Subject, Detail: e.Detail})
	}
	return h, nil
}

// eventLimit caps the events handed to rules.
const eventLimit = 500

// PruneHistory drops samples older than historyDays and events older
// than a year.
func PruneHistory(d *sql.DB, now time.Time) error {
	return db.WithTx(d, func(tx *sql.Tx) error {
		return data.PruneHistory(tx, now.AddDate(0, 0, -historyDays).Format(time.DateOnly), now.AddDate(-1, 0, 0))
	})
}
