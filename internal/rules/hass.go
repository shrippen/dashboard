package rules

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"dashboard/internal/enums"
	"dashboard/internal/sources"
)

const listShown = 5

// hassAlarms are binary sensors whose "on" means damage now.
var hassAlarms = map[string]bool{"moisture": true, "smoke": true, "gas": true, "carbon_monoxide": true, "safety": true, "problem": true}

// hassSilent are domains where "unavailable" is normal.
var hassSilent = map[string]bool{"update": true, "button": true, "scene": true, "event": true}

// shortList joins names, "A, B, C +2".
func shortList(names []string) string {
	sort.Strings(names)
	if len(names) <= listShown {
		return strings.Join(names, ", ")
	}
	return strings.Join(names[:listShown], ", ") + " +" + strconv.Itoa(len(names)-listShown)
}

func init() {
	svc := string(enums.ServiceHomeAssistant)
	entityURL := func(data *sources.HassDataset) string {
		return strings.TrimRight(data.URL, "/") + "/config/entities"
	}

	Register("hass.alarm", svc, nil, func(raw any, cfg map[string]any, env Env) []Finding {
		data, _ := raw.(*sources.HassDataset)
		var found []Finding
		for _, e := range data.Entities {
			if e.Domain != "binary_sensor" || !hassAlarms[e.DeviceClass] || e.State != sources.HassOn {
				continue
			}
			found = append(found, svcFinding(svc, "hass.alarm", "alarm:"+e.ID, "hass.alarm", enums.SeverityCritical,
				data.URL, map[string]any{"entity": e.Name, "kind": e.DeviceClass}))
		}
		return found
	})

	Register("hass.battery_low", svc, map[string]any{"warn": 20.0, "critical": 10.0}, func(raw any, cfg map[string]any, env Env) []Finding {
		data, _ := raw.(*sources.HassDataset)
		var found []Finding
		for _, e := range data.Entities {
			level, err := strconv.ParseFloat(e.State, 64)
			if e.DeviceClass != "battery" || e.Unit != "%" || err != nil || level >= cfgFloat(cfg, "warn") {
				continue
			}
			sev := enums.SeverityWarn
			if level < cfgFloat(cfg, "critical") {
				sev = enums.SeverityCritical
			}
			found = append(found, svcFinding(svc, "hass.battery_low", "battery:"+e.ID, "hass.battery", sev,
				entityURL(data), map[string]any{"entity": e.Name, "percent": int(level)}))
		}
		return found
	})

	// One hint for all long-unavailable entities: a dead Zigbee stick
	// takes dozens with it, and that should read as one problem.
	Register("hass.unavailable", svc, map[string]any{"hours": 6.0, "ignore": []any{}}, func(raw any, cfg map[string]any, env Env) []Finding {
		data, _ := raw.(*sources.HassDataset)
		ignore := stringsSlice(cfg["ignore"])
		var names []string
		for _, e := range data.Entities {
			if (e.State != sources.HassUnavailable && e.State != sources.HassUnknown) || hassSilent[e.Domain] {
				continue
			}
			if e.Changed.IsZero() || time.Now().UTC().Sub(e.Changed).Hours() < cfgFloat(cfg, "hours") || hasPrefix(e.ID, ignore) {
				continue
			}
			names = append(names, e.Name)
		}
		if len(names) == 0 {
			return nil
		}
		return []Finding{svcFinding(svc, "hass.unavailable", "unavailable", "hass.unavailable", enums.SeverityWarn,
			entityURL(data), map[string]any{"count": len(names), "names": shortList(names)})}
	})

	Register("hass.updates", svc, nil, func(raw any, cfg map[string]any, env Env) []Finding {
		data, _ := raw.(*sources.HassDataset)
		var names []string
		for _, e := range data.Entities {
			if e.Domain == "update" && e.State == sources.HassOn {
				names = append(names, e.Name)
			}
		}
		if len(names) == 0 {
			return nil
		}
		return []Finding{svcFinding(svc, "hass.updates", "updates", "hass.updates", enums.SeverityInfo,
			strings.TrimRight(data.URL, "/")+"/config/updates", map[string]any{"count": len(names), "names": shortList(names)})}
	})
}

func hasPrefix(id string, prefixes []string) bool {
	for _, p := range prefixes {
		if p != "" && strings.HasPrefix(strings.ToLower(id), p) {
			return true
		}
	}
	return false
}
