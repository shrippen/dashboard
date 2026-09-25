package main

import (
	"context"
	"database/sql"
	"time"

	"dashboard/internal/db"
	"dashboard/internal/services/analysis"
	"dashboard/internal/services/audit"
	"dashboard/internal/services/auth"
	"dashboard/internal/services/hints"
	"dashboard/internal/services/notify"
	"dashboard/internal/services/scheduler"
	"dashboard/internal/services/svcdata"
	"dashboard/internal/settings"
)

const (
	minute = time.Minute
	hour   = time.Hour
)

// backgroundJobs is the fixed job list (ports app/services/jobs.py). Not
// ported: the daily icon-retry job (icons service isn't ported) and the
// digest-mail job (needs an SMTP outbound service Go doesn't have yet).
func backgroundJobs(database *sql.DB, cfg settings.Settings) []scheduler.Job {
	return []scheduler.Job{
		{Name: "analysis", Interval: 5 * minute, Run: func(ctx context.Context) error {
			_, err := analysis.RunAll(ctx, database, time.Now().UTC())
			return err
		}},
		{Name: "notify", Interval: minute, Run: func(ctx context.Context) error {
			_, err := notify.Dispatch(ctx, database, cfg)
			return err
		}},
		{Name: "housekeeping", Interval: hour, Run: func(context.Context) error {
			return housekeeping(database)
		}},
	}
}

func housekeeping(database *sql.DB) error {
	if err := auth.Purge(database); err != nil {
		return err
	}
	if err := svcdata.Prune(database); err != nil {
		return err
	}
	if err := hints.Prune(database); err != nil {
		return err
	}
	return db.WithTx(database, func(tx *sql.Tx) error { return audit.Prune(tx) })
}
