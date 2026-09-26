package metrics

// Key figure history: what the analysis stores each run and what rules
// read back as trends.
//
//	datasets ──Samples──► {"truenas.pool.tank.used": 0.81, …} ──► table samples (daily)
//	datasets ──Versions─► {"immich": "v1.132.3", …}         ──► change = event "update"
//	samples + events ──► History ──► rules (forecast, before/after)

import (
	"sort"
	"strings"
	"time"

	"andon/internal/sources"
)

// HistoryDataset is the Env.Datasets key of the scope's History.
const HistoryDataset = "history"

// Point is one daily value.
type Point struct {
	Day   time.Time
	Value float64
}

// Event is one timeline entry (e.g. an update from one version to another).
type Event struct {
	At                    time.Time
	Kind, Subject, Detail string
}

// EventUpdate marks a version change.
const EventUpdate = "update"

// History is a scope's stored series and recent events.
type History struct {
	Series map[string][]Point
	Events []Event
}

// SeriesOf returns one series, oldest first; nil when unknown.
func (h *History) SeriesOf(key string) []Point {
	if h == nil {
		return nil
	}
	return h.Series[key]
}

// Keys returns the series keys with a prefix, sorted.
func (h *History) Keys(prefix string) []string {
	if h == nil {
		return nil
	}
	var out []string
	for k := range h.Series {
		if strings.HasPrefix(k, prefix) {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// Trend fits value = a + slope·days by least squares; ok is false with
// fewer than min points.
func Trend(points []Point, min int) (slopePerDay, last float64, ok bool) {
	if len(points) < min || len(points) < 2 {
		return 0, 0, false
	}
	origin := points[0].Day
	var sx, sy, sxx, sxy float64
	n := float64(len(points))
	for _, p := range points {
		x := p.Day.Sub(origin).Hours() / hoursPerDay
		sx, sy, sxx, sxy = sx+x, sy+p.Value, sxx+x*x, sxy+x*p.Value
	}
	den := n*sxx - sx*sx
	if den == 0 {
		return 0, points[len(points)-1].Value, false
	}
	return (n*sxy - sx*sy) / den, points[len(points)-1].Value, true
}

// Mean averages the points within [from, to).
func Mean(points []Point, from, to time.Time) (float64, int) {
	sum, n := 0.0, 0
	for _, p := range points {
		if !p.Day.Before(from) && p.Day.Before(to) {
			sum += p.Value
			n++
		}
	}
	if n == 0 {
		return 0, 0
	}
	return sum / float64(n), n
}

// ValueOn returns the newest point on or before day.
func ValueOn(points []Point, day time.Time) (float64, bool) {
	found, ok := 0.0, false
	for _, p := range points {
		if p.Day.After(day) {
			break
		}
		found, ok = p.Value, true
	}
	return found, ok
}

// key joins parts of a series key, keeping dots out of names.
func key(parts ...string) string {
	for i, p := range parts {
		parts[i] = strings.ReplaceAll(strings.ToLower(strings.TrimSpace(p)), ".", "_")
	}
	return strings.Join(parts, ".")
}

// SampleKey builds a series key from parts ("kuma", "ms", "NAS.lan") → "kuma.ms.nas_lan".
func SampleKey(parts ...string) string { return key(parts...) }

// Samples extracts today's key figures from a scope's datasets.
func Samples(datasets map[string]any) map[string]float64 {
	out := map[string]float64{}
	for _, raw := range datasets {
		switch d := raw.(type) {
		case *sources.TrueNASDataset:
			for _, p := range d.Pools {
				if p.Size > 0 {
					out[key("truenas", "pool", p.Name, "used")] = p.Allocated / p.Size
				}
			}
		case *sources.ProxmoxDataset:
			for _, n := range d.Nodes {
				for _, s := range n.Storages {
					if s.Total > 0 {
						out[key("proxmox", "storage", n.Name+"/"+s.Name, "used")] = s.Used / s.Total
					}
				}
			}
		case *sources.BorgDataset:
			if d.TotalBytes > 0 {
				out[key("borg", "used")] = d.UsedBytes / d.TotalBytes
			}
		case *sources.ImmichDataset:
			out[key("immich", "items")] = float64(d.Photos + d.Videos)
			if d.DiskPercent > 0 {
				out[key("immich", "disk", "used")] = d.DiskPercent / percentScale
			}
		case *sources.NextcloudDataset:
			out[key("nextcloud", "files")] = float64(d.Files)
		case *sources.SpeedtestDataset:
			if !d.At.IsZero() {
				out[key("speedtest", "down")] = d.Down
				out[key("speedtest", "up")] = d.Up
			}
		case *sources.SureDataset:
			out[key("sure", "cash")] = SureCash(d)
		case *sources.KumaDataset:
			for _, m := range d.Monitors {
				if m.MS > 0 {
					out[key("kuma", "ms", m.Name)] = m.MS
				}
			}
		case *sources.DNSFilterDataset:
			for _, c := range d.TopClients {
				out[key("dns", "q", c.IP)] = float64(c.Queries)
			}
		case *sources.PaperlessDataset:
			if d.Total >= 0 {
				out[key("paperless", "docs")] = float64(d.Total)
			}
		case *sources.AuthentikDataset:
			// One marker per user and country a login came from.
			for _, l := range d.Logins {
				if l.Country != "" {
					out[key("authentik", "country", l.User, l.Country)] = 1
				}
			}
		}
	}
	return out
}

// Versions extracts the running versions of a scope's services.
func Versions(datasets map[string]any) map[string]string {
	out := map[string]string{}
	put := func(subject, version string) {
		if version != "" {
			out[subject] = version
		}
	}
	for _, raw := range datasets {
		switch d := raw.(type) {
		case *sources.ImmichDataset:
			put("Immich", d.Version)
		case *sources.AuthentikDataset:
			put("authentik", d.Version)
		case *sources.TrueNASDataset:
			put("TrueNAS", d.Version)
		case *sources.NextcloudDataset:
			put("Nextcloud", d.Version)
		case *sources.MediaServerDataset:
			put(d.Kind, d.Version)
		case *sources.ArrDataset:
			put(d.App, d.Version)
		case *sources.GatewayDataset:
			put(d.Kind, d.Version)
		case *sources.VaultwardenDataset:
			put("Vaultwarden", d.Version)
		case *sources.KomodoDataset:
			// A stack whose pending image updates disappear was redeployed.
			for _, s := range d.Stacks {
				out[s.Name] = "pending:" + strings.Join(s.Updates, ",")
			}
		}
	}
	return out
}

// pendingPrefix marks a Komodo stack's list of services with newer images.
const pendingPrefix = "pending:"

// VersionEvent turns a version change into a timeline event; a first
// sighting or a newly announced image is none.
func VersionEvent(subject, old, now string, at time.Time) (Event, bool) {
	if old == "" || old == now {
		return Event{}, false
	}
	if strings.HasPrefix(old, pendingPrefix) {
		if now != pendingPrefix || old == pendingPrefix {
			return Event{}, false
		}
		return Event{At: at, Kind: EventUpdate, Subject: subject, Detail: strings.TrimPrefix(old, pendingPrefix)}, true
	}
	return Event{At: at, Kind: EventUpdate, Subject: subject, Detail: old + " → " + now}, true
}
