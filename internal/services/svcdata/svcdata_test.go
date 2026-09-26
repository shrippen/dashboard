package svcdata_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"andon/internal/db"
	"andon/internal/db/dbtest"
	"andon/internal/enums"
	"andon/internal/services/svcdata"
	"andon/internal/sources"
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
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"), dbtest.Key)
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

type slowSource struct{ calls *int }

func (slowSource) Key() string                { return "test.stored" }
func (slowSource) TTL() time.Duration         { return time.Minute }
func (slowSource) Service() enums.ServiceType { return "" }

func (s slowSource) Fetch(context.Context, sources.Ctx) (any, error) {
	*s.calls++
	return "ok", nil
}

// TestStoredNeverFetchesInRequest: a Stored read answers Pending at once
// and fills the value in the background; later reads get it.
func TestStoredNeverFetchesInRequest(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"), dbtest.Key)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	calls := 0
	sources.Register(slowSource{&calls})

	first, _ := svcdata.Get(context.Background(), d, "test.stored", nil, nil, nil, svcdata.Stored)
	if !first.Pending || first.Data != nil {
		t.Fatalf("first read: %+v", first)
	}
	var got svcdata.Result
	for range 50 {
		got, _ = svcdata.Get(context.Background(), d, "test.stored", nil, nil, nil, svcdata.Stored)
		if !got.Pending {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if got.Data != "ok" || calls != 1 {
		t.Fatalf("stored read: %+v calls=%d", got, calls)
	}
}

// TestCacheRowHoldsNoData: the persisted row records the outcome only;
// the dataset never reaches the disk.
func TestCacheRowHoldsNoData(t *testing.T) {
	d, err := db.Open(filepath.Join(t.TempDir(), "test.db"), dbtest.Key)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	calls := 0
	sources.Register(countingSource{&calls})
	res, err := svcdata.Get(context.Background(), d, "test.counting", map[string]any{"q": "row"}, nil, nil, svcdata.Force)
	if err != nil || !res.Ok() {
		t.Fatalf("fetch: %+v %v", res, err)
	}

	var rows, withData int
	if err := d.QueryRow("SELECT COUNT(*), COUNT(data) FROM cache WHERE source = ?", "test.counting").Scan(&rows, &withData); err != nil {
		t.Fatal(err)
	}
	if rows == 0 || withData != 0 {
		t.Fatalf("cache rows=%d with data=%d", rows, withData)
	}
}
