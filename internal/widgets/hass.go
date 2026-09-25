package widgets

import (
	"strings"

	"dashboard/internal/enums"
	"dashboard/internal/sources"
)

// hassSwitchable are domains the toggle service works on.
var hassSwitchable = map[string]bool{"switch": true, "light": true, "input_boolean": true, "fan": true, "automation": true}

// HassConfig lists the entities a "hass" widget shows, in order.
type HassConfig struct {
	Entities []string
}

func decodeHass(raw map[string]any) any { return HassConfig{Entities: asStringList(raw["entities"])} }

// HassRow is one entity line; Toggle offers a switch, On is its state.
type HassRow struct {
	ID, Name, Value string
	Toggle, On      bool
	Missing         bool
}

// HassToggleable reports whether a configured entity can be switched.
func HassToggleable(cfg any, entityID string) bool {
	c, ok := cfg.(HassConfig)
	if !ok {
		return false
	}
	domain, _, _ := strings.Cut(entityID, ".")
	for _, id := range c.Entities {
		if id == entityID {
			return hassSwitchable[domain]
		}
	}
	return false
}

func hassView(cfgAny any, results map[string]any, _ ViewCtx) map[string]any {
	cfg := cfgAny.(HassConfig)
	data, ok := results["data"].(*sources.HassDataset)
	if !ok {
		return map[string]any{}
	}
	rows := make([]HassRow, 0, len(cfg.Entities))
	for _, id := range cfg.Entities {
		e, found := data.Find(id)
		if !found {
			rows = append(rows, HassRow{ID: id, Name: id, Missing: true})
			continue
		}
		value := e.State
		if e.Unit != "" {
			value += " " + e.Unit
		}
		rows = append(rows, HassRow{ID: id, Name: e.Name, Value: value, Toggle: hassSwitchable[e.Domain], On: e.State == sources.HassOn})
	}
	return map[string]any{"Rows": rows}
}

func init() {
	Register(WidgetType{Key: "hass", Decode: decodeHass, Template: "widgets/hass", Category: CategoryStart,
		Service: enums.ServiceHomeAssistant, RefreshS: 60, Queries: dataQuery, View: hassView})
}
