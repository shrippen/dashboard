package analysis

// Each run stores the scope's key figures and version changes, then
// hands the stored history to the rules:
//
//	datasets → metrics.Samples  → samples (one value per key and day)
//	datasets → metrics.Versions → versions; a change → events ("update")
//	samples (history.SeriesDays) + events (eventDays) → Datasets["history"]

import (
	"database/sql"
	"time"

	"andon/internal/db"
	"andon/internal/metrics"
	data "andon/internal/repos/data"
	"andon/internal/services/history"
	"andon/internal/widgets"
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
	return history.Load(d, sc.spaceID, owner, now)
}

// PruneHistory drops samples older than history.SeriesDays and events older
// than a year.
func PruneHistory(d *sql.DB, now time.Time) error {
	return db.WithTx(d, func(tx *sql.Tx) error {
		return data.PruneHistory(tx, now.AddDate(0, 0, -history.SeriesDays).Format(time.DateOnly), now.AddDate(-1, 0, 0))
	})
}

// PrunePoints drops daily key figures older than the longest trend span.
func PrunePoints(d *sql.DB, now time.Time) error {
	return db.WithTx(d, func(tx *sql.Tx) error {
		return data.PrunePoints(tx, now.AddDate(0, 0, -widgets.MaxTrendDays).Format(time.DateOnly))
	})
}
