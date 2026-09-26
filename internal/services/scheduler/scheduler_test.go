package scheduler_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"andon/internal/services/scheduler"
)

func TestJobRunsOnInterval(t *testing.T) {
	var calls int32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	scheduler.Start(ctx, []scheduler.Job{{
		Name: "tick", Interval: 10 * time.Millisecond,
		Run: func(context.Context) error { atomic.AddInt32(&calls, 1); return nil },
	}})

	time.Sleep(55 * time.Millisecond)
	if n := atomic.LoadInt32(&calls); n < 3 {
		t.Fatalf("expected at least 3 ticks in 55ms at 10ms interval, got %d", n)
	}
}

func TestFailingJobDoesNotStopOthers(t *testing.T) {
	var okCalls int32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	scheduler.Start(ctx, []scheduler.Job{
		{Name: "bad", Interval: 10 * time.Millisecond, Run: func(context.Context) error { return errors.New("boom") }},
		{Name: "ok", Interval: 10 * time.Millisecond, Run: func(context.Context) error { atomic.AddInt32(&okCalls, 1); return nil }},
	})

	time.Sleep(35 * time.Millisecond)
	if n := atomic.LoadInt32(&okCalls); n < 2 {
		t.Fatalf("expected the healthy job to keep running, got %d calls", n)
	}
}

func TestPanickingJobDoesNotStopOthers(t *testing.T) {
	var okCalls int32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	scheduler.Start(ctx, []scheduler.Job{
		{Name: "bad", Interval: 10 * time.Millisecond, Run: func(context.Context) error { panic("boom") }},
		{Name: "ok", Interval: 10 * time.Millisecond, Run: func(context.Context) error { atomic.AddInt32(&okCalls, 1); return nil }},
	})

	time.Sleep(35 * time.Millisecond)
	if n := atomic.LoadInt32(&okCalls); n < 2 {
		t.Fatalf("expected the healthy job to keep running despite the other panicking, got %d calls", n)
	}
}

func TestAtStartRunsImmediatelyAndRecordsRun(t *testing.T) {
	var calls int32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	scheduler.Start(ctx, []scheduler.Job{{
		Name: "early", Interval: time.Hour, Start: scheduler.AtStart,
		Run: func(context.Context) error { atomic.AddInt32(&calls, 1); return nil },
	}})

	time.Sleep(30 * time.Millisecond)
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("expected one run at start, got %d", calls)
	}
	if run, ok := scheduler.LastRun("early"); !ok || run.At.IsZero() || run.Err != "" {
		t.Fatalf("last run: %+v %v", run, ok)
	}
}

func TestTriggerRunsJobNow(t *testing.T) {
	var calls int32
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	scheduler.Start(ctx, []scheduler.Job{{Name: "manual", Interval: time.Hour,
		Run: func(context.Context) error { atomic.AddInt32(&calls, 1); return nil }}})
	time.Sleep(10 * time.Millisecond)

	if !scheduler.Trigger("manual") {
		t.Fatal("trigger refused")
	}
	time.Sleep(30 * time.Millisecond)
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("expected one triggered run, got %d", calls)
	}
	if scheduler.Trigger("unknown") {
		t.Fatal("unknown job triggered")
	}
}
