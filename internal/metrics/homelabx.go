package metrics

// Homelab analyses across services and over time (phase 13):
//
//	StorageForecasts   used-share series → days until full
//	LastBackup         newest successful backup of any tool
//	UnsavedSince       items added since that backup (photos, documents, files)
//	Slowdowns          response time after an update against before
//	PendingUpdates     every update a service reports
//	UpdateWindow       whether now is a good time to update, and why not
//	Exposure           public resources: login, certificate, updates
//	LoginAnomalies     logins from new countries or far from where you are
//	DeviceSpikes       DNS clients far above their usual volume, new devices
//	SpeedDays          days below the booked speed, WAN outages
//	StormWarnings      warnings that call for preparation
//	DomainChains       what depends on each domain

import (
	"sort"
	"strings"
	"time"

	"dashboard/internal/sources"
)

const (
	forecastDays    = 30 // trend window
	forecastMin     = 7  // points needed for a trend
	fullShare       = 1.0
	regressionDays  = 7
	regressionAfter = 2 // days of data after an update before judging
	minSlowdownMS   = 100
	farKM           = 500.0
	nearHours       = 3
	usualDays       = 14
	usualMin        = 5
	defaultStormH   = 12
)

// ── Storage ──

// StorageForecast is one storage's trend.
type StorageForecast struct {
	Key    string
	Label  string
	Used   float64 // share now
	FullIn int     // days until full at the recent pace, -1 = not filling
}

// usedSuffix marks share-of-capacity series.
const usedSuffix = ".used"

// StorageForecasts projects every "*.used" series of the last 30 days.
func StorageForecasts(h *History, now time.Time) []StorageForecast {
	var out []StorageForecast
	since := now.AddDate(0, 0, -forecastDays)
	for _, k := range h.Keys("") {
		if !strings.HasSuffix(k, usedSuffix) {
			continue
		}
		var recent []Point
		for _, p := range h.SeriesOf(k) {
			if !p.Day.Before(since) {
				recent = append(recent, p)
			}
		}
		slope, last, ok := Trend(recent, forecastMin)
		if !ok {
			continue
		}
		f := StorageForecast{Key: k, Label: storageLabel(k), Used: last, FullIn: -1}
		if slope > 0 {
			f.FullIn = int((fullShare - last) / slope)
		}
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Used > out[j].Used })
	return out
}

// storageLabel turns "truenas.pool.tank.used" into "truenas tank".
func storageLabel(k string) string {
	parts := strings.Split(strings.TrimSuffix(k, usedSuffix), ".")
	if len(parts) >= 3 {
		return parts[0] + " " + strings.Join(parts[2:], " ")
	}
	return strings.Join(parts, " ")
}

// ── Backups ──

// LastBackup is the newest successful backup of any tool; zero if none.
func LastBackup(datasets map[string]any) (time.Time, string) {
	var last time.Time
	var tool string
	take := func(t time.Time, name string) {
		if t.After(last) {
			last, tool = t, name
		}
	}
	for _, raw := range datasets {
		switch d := raw.(type) {
		case *sources.BorgDataset:
			take(d.LastBackup, "Borg")
			for _, c := range d.Clients {
				take(c.LastBackup, "Borg")
			}
		case *sources.TrueNASDataset:
			for _, s := range d.Snapshots {
				if s.State == "FINISHED" {
					take(s.Last, "TrueNAS")
				}
			}
		case *sources.ProxmoxDataset:
			for _, t := range d.Backups {
				take(t, "Proxmox")
			}
		}
	}
	return last, tool
}

// Unsaved is how many items a service added since the last backup.
type Unsaved struct {
	What  string // "immich", "paperless", "nextcloud"
	Count int
}

// countSeries are the item counts compared with the backup day.
var countSeries = map[string]string{"immich.items": "immich", "paperless.docs": "paperless", "nextcloud.files": "nextcloud"}

// UnsavedSince compares today's counts with the counts on the backup day.
func UnsavedSince(h *History, backup time.Time) []Unsaved {
	var out []Unsaved
	for k, what := range countSeries {
		series := h.SeriesOf(k)
		if len(series) == 0 {
			continue
		}
		then, ok := ValueOn(series, Today(backup))
		if !ok {
			continue
		}
		if n := int(series[len(series)-1].Value - then); n > 0 {
			out = append(out, Unsaved{What: what, Count: n})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].What < out[j].What })
	return out
}

// ── Before and after updates ──

// Slowdown is a monitor that answers slower since an update.
type Slowdown struct {
	Subject, Monitor string
	At               time.Time
	BeforeMS, NowMS  float64
}

// Slowdowns compares each monitor matching an updated service in the week
// before the update with the days since.
func Slowdowns(h *History, now time.Time, factor float64) []Slowdown {
	var out []Slowdown
	for _, e := range h.Events {
		if e.Kind != EventUpdate || now.Sub(e.At).Hours() < regressionAfter*hoursPerDay {
			continue
		}
		tokens := nameTokens(e.Subject)
		if len(tokens) == 0 {
			continue
		}
		for _, k := range h.Keys("kuma.ms.") {
			monitor := strings.TrimPrefix(k, "kuma.ms.")
			if !strings.Contains(monitor, tokens[0]) {
				continue
			}
			day := Today(e.At)
			before, n1 := Mean(h.SeriesOf(k), day.AddDate(0, 0, -regressionDays), day)
			after, n2 := Mean(h.SeriesOf(k), day.AddDate(0, 0, 1), now.AddDate(0, 0, 1))
			if n1 < usualMin || n2 < regressionAfter || after < before*factor || after-before < minSlowdownMS {
				continue
			}
			out = append(out, Slowdown{Subject: e.Subject, Monitor: monitor, At: e.At, BeforeMS: before, NowMS: after})
		}
	}
	return out
}

// ── Updates ──

// PendingUpdate is one update a service offers.
type PendingUpdate struct {
	Service, What string
}

// PendingUpdates collects the updates the scope's services report.
func PendingUpdates(datasets map[string]any) []PendingUpdate {
	var out []PendingUpdate
	add := func(service, what string) { out = append(out, PendingUpdate{Service: service, What: what}) }
	for _, raw := range datasets {
		switch d := raw.(type) {
		case *sources.ImmichDataset:
			if d.Latest != "" && d.Latest != d.Version {
				add("Immich", d.Version+" → "+d.Latest)
			}
		case *sources.AuthentikDataset:
			if d.Outdated {
				add("authentik", d.Version+" → "+d.Latest)
			}
		case *sources.BorgDataset:
			if d.ServerUpdate {
				add("Borg", "server")
			}
		case *sources.TrueNASDataset:
			for _, a := range d.Apps {
				if a.Update {
					add("TrueNAS", a.Name)
				}
			}
		case *sources.KomodoDataset:
			for _, s := range d.Stacks {
				if len(s.Updates) > 0 {
					add("Komodo", s.Name)
				}
			}
		case *sources.NextcloudDataset:
			if d.AppUpdates > 0 {
				add("Nextcloud", "apps")
			}
		case *sources.ProxmoxDataset:
			for _, n := range d.Nodes {
				if n.Updates > 0 {
					add("Proxmox", n.Name)
				}
			}
		case *sources.GatewayDataset:
			if d.Updates > 0 {
				add(d.Kind, d.Version)
			}
		case *sources.MediaServerDataset:
			if d.Update {
				add(d.Kind, d.Version)
			}
		case *sources.HassDataset:
			for _, e := range d.Entities {
				if e.Domain == "update" && e.State == "on" {
					add("Home Assistant", e.Name)
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Service+out[i].What < out[j].Service+out[j].What })
	return out
}

// Reasons against updating now (catalog keys under window.*).
const (
	WindowNoBackup  = "no_backup"
	WindowStreaming = "streaming"
	WindowWorking   = "working"
	WindowMeeting   = "meeting"
	WindowExpensive = "expensive"
)

// Window says whether now is a good time to install updates.
type Window struct {
	Updates   []PendingUpdate
	Blockers  []string // reasons against now
	BackupAge time.Duration
}

// UpdateWindow weighs the pending updates against backup age, running
// streams and timers, the next appointment and the power price.
func UpdateWindow(datasets map[string]any, now time.Time, maxBackupAge time.Duration) Window {
	w := Window{Updates: PendingUpdates(datasets)}
	last, _ := LastBackup(datasets)
	if last.IsZero() || now.Sub(last) > maxBackupAge {
		w.Blockers = append(w.Blockers, WindowNoBackup)
	}
	if !last.IsZero() {
		w.BackupAge = now.Sub(last)
	}
	for _, raw := range datasets {
		switch d := raw.(type) {
		case *sources.MediaServerDataset:
			if len(d.Streams) > 0 {
				w.Blockers = append(w.Blockers, WindowStreaming)
			}
		case *sources.KimaiDataset:
			if len(d.Active) > 0 {
				w.Blockers = append(w.Blockers, WindowWorking)
			}
		case *sources.CalendarResult:
			for _, e := range d.Events {
				if !e.AllDay && e.Start.After(now) && e.Start.Sub(now) < time.Hour {
					w.Blockers = append(w.Blockers, WindowMeeting)
					break
				}
			}
		case *sources.TibberDataset:
			if d.Level == "EXPENSIVE" || d.Level == "VERY_EXPENSIVE" {
				w.Blockers = append(w.Blockers, WindowExpensive)
			}
		}
	}
	sort.Strings(w.Blockers)
	return w
}

// ── Exposure ──

// ExposureRow is one public resource with what protects it.
type ExposureRow struct {
	Name, Domain string
	Login        bool // behind Pangolin's login
	CertDays     int  // -1 unknown
	Updates      []string
	Risk         int // 0 low, 1 medium, 2 high
}

// Exposure rates Pangolin's public resources: no login and pending updates
// or certificate trouble make a resource risky.
func Exposure(pangolin *sources.PangolinDataset, certs *sources.CertDataset, updates []PendingUpdate, now time.Time) []ExposureRow {
	var out []ExposureRow
	for _, r := range pangolin.Resources {
		if !r.Enabled {
			continue
		}
		row := ExposureRow{Name: r.Name, Domain: r.Domain, Login: r.SSO, CertDays: -1}
		if certs != nil {
			for _, c := range certs.Certs {
				if strings.EqualFold(strings.Split(c.Host, ":")[0], r.Domain) && c.Error == "" {
					row.CertDays = int(c.NotAfter.Sub(now).Hours() / hoursPerDay)
				}
			}
		}
		for _, u := range updates {
			if sameThing(r.Name, u.Service+" "+u.What) || sameThing(u.What, r.Name) {
				row.Updates = append(row.Updates, u.Service+": "+u.What)
			}
		}
		if !row.Login {
			row.Risk++
		}
		if len(row.Updates) > 0 || (row.CertDays >= 0 && row.CertDays < 14) {
			row.Risk++
		}
		out = append(out, row)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Risk > out[j].Risk })
	return out
}

// ── Logins ──

// Anomaly reasons.
const (
	LoginNewCountry = "new_country"
	LoginFar        = "far"
)

// LoginAnomaly is a login worth a look.
type LoginAnomaly struct {
	sources.AKLogin
	Reason string
	KM     float64 // distance to where the location history put you
}

// LoginAnomalies checks the logins of the last day: a country never seen
// for that user in the history, or a place far from the Dawarich visit
// around the same time. Needs a week of history per user.
func LoginAnomalies(ak *sources.AuthentikDataset, h *History, geo *sources.DawarichDataset, now time.Time) []LoginAnomaly {
	var out []LoginAnomaly
	for _, l := range ak.Logins {
		if now.Sub(l.At) > hoursPerDay*time.Hour || l.Country == "" {
			continue
		}
		userKeys := h.Keys(key("authentik", "country", l.User) + ".")
		if len(userKeys) == 0 || !historyDays(h, userKeys, usualMin, l.At) {
			continue
		}
		if !seenBefore(h.SeriesOf(key("authentik", "country", l.User, l.Country)), l.At) {
			out = append(out, LoginAnomaly{AKLogin: l, Reason: LoginNewCountry})
			continue
		}
		if geo != nil && (l.Lat != 0 || l.Lon != 0) {
			if km, ok := nearVisitKM(geo, l); ok && km > farKM {
				out = append(out, LoginAnomaly{AKLogin: l, Reason: LoginFar, KM: km})
			}
		}
	}
	return out
}

// historyDays: the user's country series cover at least n days before t.
func historyDays(h *History, keys []string, n int, t time.Time) bool {
	days := map[time.Time]bool{}
	for _, k := range keys {
		for _, p := range h.SeriesOf(k) {
			if p.Day.Before(Today(t)) {
				days[p.Day] = true
			}
		}
	}
	return len(days) >= n
}

func seenBefore(series []Point, t time.Time) bool {
	for _, p := range series {
		if p.Day.Before(Today(t)) {
			return true
		}
	}
	return false
}

// nearVisitKM is the distance from a login to a visit within ±3 hours.
func nearVisitKM(geo *sources.DawarichDataset, l sources.AKLogin) (float64, bool) {
	for _, v := range geo.Visits {
		start, ok1 := ParseTime(v.Start)
		end, ok2 := ParseTime(v.End)
		if !ok1 || !ok2 || l.At.Before(start.Add(-nearHours*time.Hour)) || l.At.After(end.Add(nearHours*time.Hour)) {
			continue
		}
		if a := areaOf(v, geo.Areas); a != nil {
			return DistanceKM(l.Lat, l.Lon, a.Lat, a.Lon), true
		}
		if v.Lat != nil && v.Lon != nil {
			return DistanceKM(l.Lat, l.Lon, *v.Lat, *v.Lon), true
		}
	}
	return 0, false
}

// ── DNS ──

// DeviceSpike is a client far above its usual daily queries.
type DeviceSpike struct {
	sources.DNSClient
	Usual float64
}

// DeviceSpikes compares today's queries with the mean of the two weeks before.
func DeviceSpikes(dns *sources.DNSFilterDataset, h *History, now time.Time, factor float64, minQueries int) []DeviceSpike {
	var out []DeviceSpike
	for _, c := range dns.TopClients {
		usual, n := Mean(h.SeriesOf(key("dns", "q", c.IP)), Today(now).AddDate(0, 0, -usualDays), Today(now))
		if n < usualMin || c.Queries < minQueries || float64(c.Queries) < usual*factor {
			continue
		}
		out = append(out, DeviceSpike{DNSClient: c, Usual: usual})
	}
	return out
}

// NewDevices are busy clients without a name that the history has never
// seen, once there is a week of history.
func NewDevices(dns *sources.DNSFilterDataset, h *History, now time.Time) []sources.DNSClient {
	if len(h.Keys("dns.q.")) == 0 || !historyDays(h, h.Keys("dns.q."), usualMin, now) {
		return nil
	}
	var out []sources.DNSClient
	for _, c := range dns.TopClients {
		if (c.Name == "" || c.Name == c.IP) && !seenBefore(h.SeriesOf(key("dns", "q", c.IP)), now) {
			out = append(out, c)
		}
	}
	return out
}

// ── Internet speed ──

// SpeedDay is one day's measured speed.
type SpeedDay struct {
	Day      time.Time
	Down, Up float64
	Below    bool // under share of the booked download speed
}

// Outage is one WAN outage from the hint history.
type Outage struct {
	Gateway    string
	Start, End time.Time // End zero while ongoing
}

// SpeedReport is one month of measurements against the contract.
type SpeedReport struct {
	Days      []SpeedDay
	BelowDays int
	Outages   []Outage
}

// wanRule names the hint whose history holds WAN outages.
const wanRule = "gateway.wan_down"

// SpeedDays reads the stored daily speeds and WAN outages of the last days.
func SpeedDays(h *History, expectDown, share float64, now time.Time, days int) SpeedReport {
	var r SpeedReport
	since := Today(now).AddDate(0, 0, -days)
	up := h.SeriesOf(key("speedtest", "up"))
	for _, p := range h.SeriesOf(key("speedtest", "down")) {
		if p.Day.Before(since) {
			continue
		}
		u, _ := ValueOn(up, p.Day)
		d := SpeedDay{Day: p.Day, Down: p.Value, Up: u, Below: expectDown > 0 && p.Value < expectDown*share}
		if d.Below {
			r.BelowDays++
		}
		r.Days = append(r.Days, d)
	}
	open := map[string]time.Time{}
	events := append([]Event(nil), h.Events...)
	sort.Slice(events, func(i, j int) bool { return events[i].At.Before(events[j].At) })
	for _, e := range events {
		if e.Subject != wanRule || e.At.Before(since) {
			continue
		}
		name := strings.TrimPrefix(e.Detail, "wan:")
		switch e.Kind {
		case "opened", "reopened":
			open[name] = e.At
		case "resolved":
			if start, ok := open[name]; ok {
				r.Outages = append(r.Outages, Outage{Gateway: name, Start: start, End: e.At})
				delete(open, name)
			}
		}
	}
	for name, start := range open {
		r.Outages = append(r.Outages, Outage{Gateway: name, Start: start})
	}
	return r
}

// ── Weather ──

// stormWords mark warnings that can hit power or hardware.
var stormWords = []string{"gewitter", "sturm", "orkan", "unwetter", "thunder", "storm", "hurricane"}

// StormWarnings are severe warnings, or storms and thunderstorms of any
// level, starting within the next hours.
func StormWarnings(dwd *sources.DWDDataset, now time.Time, hours int) []sources.WeatherWarning {
	if hours <= 0 {
		hours = defaultStormH
	}
	var out []sources.WeatherWarning
	for _, w := range dwd.Warnings {
		if w.Onset.After(now.Add(time.Duration(hours)*time.Hour)) || (!w.Expire.IsZero() && w.Expire.Before(now)) {
			continue
		}
		text := strings.ToLower(w.Event + " " + w.Headline)
		severe := w.Severity == sources.WarnSevere || w.Severity == sources.WarnExtreme
		stormy := false
		for _, word := range stormWords {
			stormy = stormy || strings.Contains(text, word)
		}
		if severe || stormy {
			out = append(out, w)
		}
	}
	return out
}

// ── Domains ──

// DomainChain is one registered domain and what depends on it.
type DomainChain struct {
	Domain    string
	Expires   time.Time
	CertDays  int // nearest certificate expiry below the domain, -1 unknown
	Resources []string
	Monitors  []string
	Tiles     []string
}

// under reports whether host is the domain or one of its subdomains.
func under(host, domain string) bool {
	host, domain = strings.ToLower(host), strings.ToLower(domain)
	return host == domain || strings.HasSuffix(host, "."+domain)
}

// DomainChains maps certificates, Pangolin resources, Kuma monitors and
// link tiles (title → host) to their registered domains.
func DomainChains(domains *sources.DomainsDataset, certs *sources.CertDataset, pangolin *sources.PangolinDataset,
	kuma *sources.KumaDataset, tiles map[string]string, now time.Time) []DomainChain {
	var out []DomainChain
	for _, d := range domains.Domains {
		c := DomainChain{Domain: d.Name, Expires: d.Expires, CertDays: -1}
		if certs != nil {
			for _, cert := range certs.Certs {
				if cert.Error != "" || !under(strings.Split(cert.Host, ":")[0], d.Name) {
					continue
				}
				days := int(cert.NotAfter.Sub(now).Hours() / hoursPerDay)
				if c.CertDays < 0 || days < c.CertDays {
					c.CertDays = days
				}
			}
		}
		if pangolin != nil {
			for _, r := range pangolin.Resources {
				if under(r.Domain, d.Name) {
					c.Resources = append(c.Resources, r.Name)
				}
			}
		}
		if kuma != nil {
			for _, m := range kuma.Monitors {
				if host := hostOnly(m.Target); host != "" && under(host, d.Name) {
					c.Monitors = append(c.Monitors, m.Name)
				}
			}
		}
		for title, host := range tiles {
			if under(host, d.Name) {
				c.Tiles = append(c.Tiles, title)
			}
		}
		sort.Strings(c.Tiles)
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Expires.Before(out[j].Expires) })
	return out
}

// Dependents counts what hangs on a domain.
func (c DomainChain) Dependents() int { return len(c.Resources) + len(c.Monitors) + len(c.Tiles) }

// hostOnly returns the host of a URL, "" if none.
func hostOnly(raw string) string {
	raw = strings.TrimSpace(raw)
	if i := strings.Index(raw, "://"); i >= 0 {
		raw = raw[i+3:]
	}
	raw = strings.SplitN(raw, "/", 2)[0]
	return strings.SplitN(raw, ":", 2)[0]
}
