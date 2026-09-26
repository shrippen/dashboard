package hass_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"andon/internal/enums"
	"andon/internal/services/hass"
	"andon/internal/testkit"
)

// Only switchable entities listed on the tile are toggled.
func TestToggleListedSwitchesOnly(t *testing.T) {
	d := testkit.DB(t)
	who, space := testkit.User(t, d, "a@b.c", enums.RoleUser)

	var called string
	ha := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = r.Method + " " + r.URL.Path
		w.Write([]byte(`[]`))
	}))
	defer ha.Close()
	conn := testkit.Conn(t, d, who, space, enums.ServiceHomeAssistant, ha.URL)
	tile := testkit.Place(t, d, who, space, "hass", map[string]any{"entities": []any{"light.desk", "sensor.temp"}}, &conn)
	ctx := context.Background()

	for _, entity := range []string{"sensor.temp", "light.kitchen"} {
		if err := hass.Toggle(ctx, d, who, tile, entity, ""); !errors.Is(err, hass.ErrNotSwitchable) {
			t.Fatalf("%s: %v", entity, err)
		}
	}
	if err := hass.Toggle(ctx, d, who, tile, "light.desk", ""); err != nil {
		t.Fatalf("toggle: %v", err)
	}
	if called != "POST /api/services/light/toggle" {
		t.Fatalf("called %q", called)
	}
}
