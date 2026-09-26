// Package metrics: the info line of a link tile — two or three short facts
// per service. Params use the same typed shape as hint params
// ({"$num": ...}, {"$money": ...}), formatted per reader locale by i18n.Typed.
package metrics

import (
	"time"

	"andon/internal/sources"
)

// InfoPart is one fact: key -> catalog "info.<key>", with typed params.
type InfoPart struct {
	Key    string
	Params map[string]any
}

func part(key string, params map[string]any) InfoPart {
	if params == nil {
		params = map[string]any{}
	}
	return InfoPart{Key: key, Params: params}
}

// KimaiInfo builds the info line for a Kimai link tile.
func KimaiInfo(data *sources.KimaiDataset, today time.Time) []InfoPart {
	stats := KimaiSummaryOf(data, today, 0)
	found := []InfoPart{part("kimai.today", map[string]any{
		"hours": map[string]any{"$num": float64(stats.TodayMin) / minutesPerHour, "digits": 1},
	})}
	if len(stats.Running) > 0 {
		found = append(found, part("kimai.running", nil))
	}
	return found
}

// NinjaInfo builds the info line for an Invoice Ninja link tile.
func NinjaInfo(data *sources.NinjaDataset, today time.Time) []InfoPart {
	stats := NinjaSummaryOf(data, today, "", "")
	var found []InfoPart
	if len(stats.Overdue) > 0 {
		found = append(found, part("in.overdue", map[string]any{"count": len(stats.Overdue)}))
	}
	found = append(found, part("in.open", map[string]any{
		"amount": map[string]any{"$money": stats.OpenAmount, "currency": stats.Currency},
	}))
	return found
}

// SnipeInfo builds the info line for a Snipe-IT link tile.
func SnipeInfo(data *sources.SnipeDataset, today time.Time) []InfoPart {
	stats := SnipeSummaryOf(data, today)
	return []InfoPart{
		part("snipe.assets", map[string]any{"count": stats.Assets}),
		part("snipe.upcoming", map[string]any{"count": len(stats.Upcoming)}),
	}
}

// DawarichInfo builds the info line for a Dawarich link tile.
func DawarichInfo(lastPoint string) []InfoPart {
	last, ok := ParseTime(lastPoint)
	if !ok {
		return nil
	}
	hours := int(time.Now().UTC().Sub(last).Hours())
	return []InfoPart{part("dawarich.last", map[string]any{"hours": hours})}
}

// GlancesInfo builds the info line for a Glances link tile.
func GlancesInfo(cpuPercent float64) []InfoPart {
	return []InfoPart{part("glances.cpu", map[string]any{"percent": round(cpuPercent)})}
}
