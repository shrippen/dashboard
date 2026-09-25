package metrics_test

import (
	"testing"
	"time"

	"dashboard/internal/metrics"
)

func TestTrendAndVersionEvent(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	var points []metrics.Point
	for i := range 10 {
		points = append(points, metrics.Point{Day: start.AddDate(0, 0, i), Value: 0.5 + 0.01*float64(i)})
	}
	slope, last, ok := metrics.Trend(points, 5)
	if !ok || slope < 0.0099 || slope > 0.0101 || last != 0.59 {
		t.Fatalf("trend: %v %v %v", slope, last, ok)
	}
	if _, _, ok := metrics.Trend(points[:3], 5); ok {
		t.Fatal("too few points accepted")
	}

	now := time.Now()
	if _, ok := metrics.VersionEvent("Immich", "", "v1", now); ok {
		t.Fatal("first sighting is no update")
	}
	if e, ok := metrics.VersionEvent("Immich", "v1", "v2", now); !ok || e.Detail != "v1 → v2" {
		t.Fatalf("update: %+v", e)
	}
	if _, ok := metrics.VersionEvent("web", "pending:", "pending:app", now); ok {
		t.Fatal("announced image is no update")
	}
	if e, ok := metrics.VersionEvent("web", "pending:app", "pending:", now); !ok || e.Detail != "app" {
		t.Fatalf("redeploy: %+v", e)
	}
}
