// Package metrics: Dawarich — client visits, travel distance, time away
// from home.
package metrics

import (
	"math"
	"sort"
	"time"

	"andon/internal/sources"
)

const (
	earthKM    = 6371.0
	roadFactor = 1.3
)

// DistanceKM is the great-circle distance (haversine) between two
// (lat, lon) points.
func DistanceKM(latA, lonA, latB, lonB float64) float64 {
	rad := math.Pi / 180
	lat1, lon1, lat2, lon2 := latA*rad, lonA*rad, latB*rad, lonB*rad
	h := math.Pow(math.Sin((lat2-lat1)/2), 2) +
		math.Cos(lat1)*math.Cos(lat2)*math.Pow(math.Sin((lon2-lon1)/2), 2)
	return 2 * earthKM * math.Asin(math.Sqrt(h))
}

// AreaMapping maps a Dawarich area's name to what it represents: a Kimai
// customer ("Muster GmbH Büro" -> {"customer_id": 1}) or home ({"home": true}).
type AreaMapping struct {
	CustomerID int64
	Home       bool
}

// ParseAreaMapping decodes a Dawarich connection's "areas" option
// (name -> {"customer_id": N} | {"home": true}).
func ParseAreaMapping(options map[string]any) map[string]AreaMapping {
	raw, _ := options["areas"].(map[string]any)
	out := map[string]AreaMapping{}
	for name, v := range raw {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		am := AreaMapping{}
		if home, ok := m["home"].(bool); ok {
			am.Home = home
		}
		switch cid := m["customer_id"].(type) {
		case float64:
			am.CustomerID = int64(cid)
		case int64:
			am.CustomerID = cid
		}
		out[name] = am
	}
	return out
}

func areaOf(visit sources.DawarichVisit, areas []sources.DawarichArea) *sources.DawarichArea {
	if visit.AreaID != 0 {
		for i := range areas {
			if areas[i].ID == visit.AreaID {
				return &areas[i]
			}
		}
	}
	if visit.Lat == nil || visit.Lon == nil {
		return nil
	}
	for i := range areas {
		a := areas[i]
		if DistanceKM(*visit.Lat, *visit.Lon, a.Lat, a.Lon)*1000 <= a.Radius+50 {
			return &a
		}
	}
	return nil
}

// ClientVisit is one Dawarich visit resolved to a Kimai customer.
type ClientVisit struct {
	Day        time.Time
	CustomerID int64
	Area       sources.DawarichArea
	Minutes    int
	Start, End *time.Time
}

// ClientVisits returns visits in areas mapped to a Kimai customer.
func ClientVisits(data *sources.DawarichDataset, mapping map[string]AreaMapping) []ClientVisit {
	var out []ClientVisit
	for _, v := range data.Visits {
		area := areaOf(v, data.Areas)
		if area == nil {
			continue
		}
		target, ok := mapping[area.Name]
		if !ok || target.CustomerID == 0 {
			continue
		}
		day, hasDay := ParseDay(v.Start)
		if !hasDay {
			continue
		}
		start, hasStart := ParseTime(v.Start)
		end, hasEnd := ParseTime(v.End)
		minutes := v.Minutes
		if minutes == 0 && hasStart && hasEnd {
			minutes = int(end.Sub(start).Minutes())
		}
		cv := ClientVisit{Day: day, CustomerID: target.CustomerID, Area: *area, Minutes: minutes}
		if hasStart {
			cv.Start = &start
		}
		if hasEnd {
			cv.End = &end
		}
		out = append(out, cv)
	}
	return out
}

// Home returns the area mapped as home, if any.
func Home(data *sources.DawarichDataset, mapping map[string]AreaMapping) *sources.DawarichArea {
	for i := range data.Areas {
		if mapping[data.Areas[i].Name].Home {
			return &data.Areas[i]
		}
	}
	return nil
}

// Trip is one round trip to a client on one day.
type Trip struct {
	Day        string
	CustomerID int64
	KM         float64
	AwayMin    int
	Area       string
}

// Trips returns one round trip per client day in [start, end]: straight
// line x road factor, there and back.
func Trips(data *sources.DawarichDataset, mapping map[string]AreaMapping, start, end time.Time) []Trip {
	base := Home(data, mapping)
	perDay := map[time.Time][]ClientVisit{}
	var days []time.Time
	for _, v := range ClientVisits(data, mapping) {
		if v.Day.Before(start) || v.Day.After(end) {
			continue
		}
		if _, ok := perDay[v.Day]; !ok {
			days = append(days, v.Day)
		}
		perDay[v.Day] = append(perDay[v.Day], v)
	}
	sort.Slice(days, func(i, j int) bool { return days[i].Before(days[j]) })

	out := make([]Trip, 0, len(days))
	for _, day := range days {
		visits := perDay[day]
		area := visits[0].Area
		var km float64
		if base != nil {
			km = 2 * DistanceKM(base.Lat, base.Lon, area.Lat, area.Lon) * roadFactor
		}

		var first, last *time.Time
		for _, v := range visits {
			if v.Start != nil && (first == nil || v.Start.Before(*first)) {
				first = v.Start
			}
			if v.End != nil && (last == nil || v.End.After(*last)) {
				last = v.End
			}
		}
		away := 0
		if first != nil && last != nil {
			away = int(last.Sub(*first).Minutes())
		} else {
			for _, v := range visits {
				away += v.Minutes
			}
		}

		out = append(out, Trip{
			Day: day.Format("2006-01-02"), CustomerID: visits[0].CustomerID, KM: round1(km),
			AwayMin: away, Area: area.Name,
		})
	}
	return out
}

func round1(f float64) float64 { return round(f*10) / 10 }
