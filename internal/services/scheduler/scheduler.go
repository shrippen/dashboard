// Package scheduler runs the background jobs (one process, one scheduler):
//
//	every 5 min   fetch integrations, rules → hints (analysis; also at start,
//	              interval ANALYSIS_MINUTES)
//	every 1 min   push notifications
//	every 5 min   digest mails
//	hourly        housekeeping (sessions, cache, hints, audit)
//	daily         retry icons that failed to download
package scheduler

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// StartMode says whether a job also runs right at startup.
type StartMode int

const (
	AfterInterval StartMode = iota // first run after one interval
	AtStart                        // first run immediately
)

// Job is one background task, run on its own interval.
type Job struct {
	Name     string
	Interval time.Duration
	Start    StartMode
	Run      func(context.Context) error
}

// Run is the outcome of a job's latest run.
type Run struct {
	At       time.Time
	Duration time.Duration
	Err      string
	Every    time.Duration
}

var (
	runsMu   sync.Mutex
	runs     = map[string]Run{}
	triggers = map[string]chan struct{}{}
)

// Trigger asks a job to run now (queued behind a running one); false if
// no such job runs.
func Trigger(name string) bool {
	runsMu.Lock()
	ch, ok := triggers[name]
	runsMu.Unlock()
	if !ok {
		return false
	}
	select {
	case ch <- struct{}{}:
	default: // one run is already queued
	}
	return true
}

// LastRun returns a job's latest run, false if it has not run yet.
func LastRun(name string) (Run, bool) {
	runsMu.Lock()
	defer runsMu.Unlock()
	r, ok := runs[name]
	return r, ok
}

// Start runs every job on its own ticker until ctx is cancelled. Each job
// gets its own goroutine and runs strictly one tick at a time (a slow run
// coalesces any ticks queued behind it — time.Ticker only buffers one); a
// panicking or erroring job is logged and never stops the others.
func Start(ctx context.Context, jobs []Job) {
	for _, job := range jobs {
		kick := make(chan struct{}, 1)
		runsMu.Lock()
		triggers[job.Name] = kick
		runsMu.Unlock()
		go runJob(ctx, job, kick)
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

func runJob(ctx context.Context, job Job, kick <-chan struct{}) {
	ticker := time.NewTicker(job.Interval)
	defer ticker.Stop()
	if job.Start == AtStart {
		safeRun(ctx, job)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			safeRun(ctx, job)
		case <-kick:
			safeRun(ctx, job)
		}
	}
}

func safeRun(ctx context.Context, job Job) {
	started := time.Now()
	run := Run{At: started.UTC(), Every: job.Interval}
	defer func() {
		if r := recover(); r != nil {
			slog.Error("scheduler: job panicked", "job", job.Name, "recover", r)
			run.Err = "panic"
		}
		run.Duration = time.Since(started)
		runsMu.Lock()
		runs[job.Name] = run
		runsMu.Unlock()
	}()
	if err := job.Run(ctx); err != nil {
		slog.Error("scheduler: job failed", "job", job.Name, "err", err)
		run.Err = err.Error()
	}
}
