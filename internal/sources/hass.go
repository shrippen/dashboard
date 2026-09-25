package sources

import (
	"context"
	"sort"
	"strings"
	"time"

	"dashboard/internal/drivers/services"
	"dashboard/internal/enums"
)

// Home Assistant states that mean "no value".
const (
	HassUnavailable = "unavailable"
	HassUnknown     = "unknown"
	HassOn          = "on"
)

// Entity is one Home Assistant state.
type Entity struct {
	ID, Name, Domain string
	State, Unit      string
	DeviceClass      string
	Changed          time.Time
}

type HassDataset struct {
	URL      string
	Entities []Entity
}

// Find returns an entity by id.
func (d *HassDataset) Find(id string) (Entity, bool) {
	for _, e := range d.Entities {
		if e.ID == id {
			return e, true
		}
	}
	return Entity{}, false
}

type HassData struct{}

func (HassData) Key() string                { return "homeassistant.data" }
func (HassData) TTL() time.Duration         { return time.Minute }
func (HassData) Service() enums.ServiceType { return enums.ServiceHomeAssistant }

func (HassData) Fetch(ctx context.Context, sctx Ctx) (any, error) {
	if isDemo(sctx) {
		return DemoHass(time.Now()), nil
	}
	secret, err := needSecret(sctx)
	if err != nil {
		return nil, err
	}
	states, err := services.HassApi{URL: sctx.URL, Token: secret, Verify: sctx.VerifyTLS}.States(ctx)
	if err != nil {
		return nil, fetchError(err)
	}
	return parseHass(sctx.URL, states), nil
}

func parseHass(base string, states any) *HassDataset {
	data := &HassDataset{URL: base}
	for _, raw := range asList(states) {
		m := asMap(raw)
		attrs := asMap(m["attributes"])
		id := asStr(m["entity_id"])
		domain, _, _ := strings.Cut(id, ".")
		name := asStr(attrs["friendly_name"])
		if name == "" {
			name = id
		}
		data.Entities = append(data.Entities, Entity{
			ID: id, Name: name, Domain: domain, State: asStr(m["state"]),
			Unit: asStr(attrs["unit_of_measurement"]), DeviceClass: asStr(attrs["device_class"]),
			Changed: parseTime(m["last_changed"]),
		})
	}
	sort.Slice(data.Entities, func(i, j int) bool { return data.Entities[i].ID < data.Entities[j].ID })
	return data
}

func init() {
	Register(HassData{})
	Register(testOf{HassData{}, func(d any) map[string]any { return map[string]any{"entities": len(d.(*HassDataset).Entities)} }})
}
