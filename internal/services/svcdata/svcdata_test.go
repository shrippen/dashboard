package svcdata_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"dashboard/internal/db"
	"dashboard/internal/enums"
	"dashboard/internal/services/svcdata"
	"dashboard/internal/sources"
)

type countingSource struct{ calls *int }

func (countingSource) Key() string                { return "test.counting" }
func (countingSource) TTL() time.Duration         { return time.Minute }
func (countingSource) Service() enums.ServiceType { return "" }

func (s countingSource) Fetch(context.Context, sources.Ctx) (any, error) {
	*s.calls++
	return map[string]any{"n": *s.calls}, nil
}

// TestGetHonorsTTL: a cached read within the source's TTL does not reach
// the service again; Force does.
func TestGetHonorsTTL(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	calls := 0
	sources.Register(countingSource{&calls})
	params := map[string]any{"q": "x"}

	for range 2 {
		if _, err := svcdata.Get(context.Background(), d, "test.counting", params, nil, nil, svcdata.Cached); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatalf("cached reads fetched %d times", calls)
	}
	if _, err := svcdata.Get(context.Background(), d, "test.counting", params, nil, nil, svcdata.Force); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("forced read did not fetch: %d", calls)
	}
}
