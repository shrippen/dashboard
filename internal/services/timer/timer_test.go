package timer_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"andon/internal/enums"
	"andon/internal/services/timer"
	"andon/internal/testkit"
)

// A timer tile writes to Kimai; bad ranges and other tiles are refused
// before anything is sent.
func TestRunWritesOnlyForTimers(t *testing.T) {
	d := testkit.DB(t)
	who, space := testkit.User(t, d, "a@b.c", enums.RoleUser)

	var mu sync.Mutex
	var writes []string
	kimai := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		writes = append(writes, r.Method+" "+r.URL.Path)
		mu.Unlock()
		w.Write([]byte(`{}`))
	}))
	defer kimai.Close()
	conn := testkit.Conn(t, d, who, space, enums.ServiceKimai, kimai.URL)
	tile := testkit.Place(t, d, who, space, timer.WidgetType, nil, &conn)
	ctx := context.Background()

	if err := timer.Run(ctx, d, who, tile, timer.Request{Action: timer.ActionStart, Project: 3, Activity: 7}, ""); err != nil {
		t.Fatalf("start: %v", err)
	}
	bad := timer.Request{Action: timer.ActionCreate, Project: 3, Activity: 7, Begin: "2026-09-26T10:00", End: "2026-09-26T09:00"}
	if err := timer.Run(ctx, d, who, tile, bad, ""); !errors.Is(err, timer.ErrBadRange) {
		t.Fatalf("bad range: %v", err)
	}
	note := testkit.Place(t, d, who, space, "note", nil, nil)
	if err := timer.Run(ctx, d, who, note, timer.Request{Action: timer.ActionStart, Project: 3, Activity: 7}, ""); !errors.Is(err, timer.ErrNotTimer) {
		t.Fatalf("note tile: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(writes) != 1 || writes[0] != "POST /api/timesheets" {
		t.Fatalf("writes: %v", writes)
	}
}
