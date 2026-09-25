// Package scheduler runs the background jobs (one process, one scheduler):
//
//	every 5 min   rules → hints           (analysis)
//	every 1 min   push notifications
//	every 5 min   digest mails
//	hourly        housekeeping (sessions, cache, hints, audit)
//	daily         retry icons that failed to download
//
// Ports app/services/scheduler.py + app/services/jobs.py.
package scheduler

import (
	"context"
	"log/slog"
	"time"
)

// Job is one background task, run on its own interval.
type Job struct {
	Name     string
	Interval time.Duration
	Run      func(context.Context) error
}

// Start runs every job on its own ticker until ctx is cancelled. Each job
// gets its own goroutine and runs strictly one tick at a time (a slow run
// coalesces any ticks queued behind it — time.Ticker only buffers one); a
// panicking or erroring job is logged and never stops the others.
func Start(ctx context.Context, jobs []Job) {
	for _, job := range jobs {
		go runJob(ctx, job)
	}
	slog.Info("scheduler started", "jobs", jobNames(jobs))
}

func jobNames(jobs []Job) []string {
	out := make([]string, len(jobs))
	for i, j := range jobs {
		out[i] = j.Name
	}
	return out
}

func runJob(ctx context.Context, job Job) {
	ticker := time.NewTicker(job.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			safeRun(ctx, job)
		}
	}
}

func safeRun(ctx context.Context, job Job) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("scheduler: job panicked", "job", job.Name, "recover", r)
		}
	}()
	if err := job.Run(ctx); err != nil {
		slog.Error("scheduler: job failed", "job", job.Name, "err", err)
	}
}
