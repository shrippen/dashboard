package widgets

// The data tiles of the catalog (ROADMAP 9.2) on one service each:
//
//	kimai_week      hours per weekday against a daily target
//	kimai_split     this week's hours per day, stacked by customer
//	unbilled_age    billable, unexported Kimai time stacked by age
//	disks           Scrutiny: every disk's state, temperature, hours
//	komodo_stacks   Komodo: servers and stacks, pending image updates
//	truenas_pools   TrueNAS: pool fill and state, alerts, app updates
//	dns_filter      Pi-hole / AdGuard: blocked share today
//	vpn             Gluetun: tunnel state, exit country, leak
//	gateway         OPNsense, pfSense, UniFi: WAN links, devices, updates
//	expiry          certificates (and domains): days left as bars
//	speed_history   Speedtest: the last days' download against the contract
//	sabnzbd         SABnzbd: speed, queue, free space, failures
//	paperless_inbox Paperless: inbox size and its oldest document
//	mail_invoices   invoices found in the mailbox
//	freshrss_feeds  FreshRSS: unread per feed
//	linkwarden      Linkwarden: links per collection
//	gitea_reviews   Gitea: reviews waiting, assigned issues
//	dawarich_day    Dawarich: today's places on an hour bar
//	authentik_logins authentik: logins and failures, latest logins
//	vaultwarden_2fa Vaultwarden: accounts with and without two-factor
//	kintsugi        Kintsugi: open acquisition suggestions, take-up, gaps

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"andon/internal/enums"
	"andon/internal/metrics"
	"andon/internal/sources"
)

const (
	listShown     = 5
	barsShown     = 6
	weekDays      = 7
	workDays      = 5
	defaultWeekH  = 40
	expiryHorizon = 90 // days a full expiry bar stands for
	expiryWarn    = 30
	expiryHigh    = 14
	tempWarn      = 45
	tempHigh      = 55
	hoursPerDay   = 24
	speedDays     = 7
	speedWarn     = 0.8 // share of the contract below which a day is slow
	splitShown    = 4   // customers with their own colour, the rest is "other"
)

// splitTiers colours customers in the kimai_split stack.
var splitTiers = []string{"blue", "purple", "aqua", "orange", "grey"}

// HBar is one labelled horizontal bar (feeds, collections, expiry).
type HBar struct {
	Label string
	Value string
	W     int // width in percent
	Tier  string
}

// Seg is one part of a stacked column, height in percent of the column.
type Seg struct {
	H    int
	Tier string
}

// DayCol is one day of a weekday chart.
type DayCol struct {
	I     int // 0 = Monday
	H     int // total height in percent
	Hours string
	Tier  string
	Segs  []Seg
}

func pctOf(v, full float64) int {
	if full <= 0 {
		return 0
	}
	return int(min(max(v/full, 0), 1)*pctFull + 0.5)
}

// weekStart is Monday 00:00 of today's week.
func weekStart(today time.Time) time.Time {
	d := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, today.Location())
	return d.AddDate(0, 0, -((int(d.Weekday()) + 6) % 7))
}

func todayOf(ctx ViewCtx) time.Time {
	t, err := time.Parse(time.DateOnly, ctx.Today)
	if err != nil {
		return time.Now()
	}
	return t
}

// weekMinutes sums this week's Kimai minutes per weekday, by customer.
func weekMinutes(data *sources.KimaiDataset, today time.Time) ([weekDays]int, [weekDays]map[int64]int) {
	var total [weekDays]int
	var byCustomer [weekDays]map[int64]int
	start := weekStart(today)
	for _, s := range data.Timesheets {
		day, ok := metrics.ParseDay(s.Begin)
		if !ok {
			continue
		}
		i := int(day.Sub(start).Hours() / hoursPerDay)
		if i < 0 || i >= weekDays {
			continue
		}
		total[i] += s.Minutes
		if byCustomer[i] == nil {
			byCustomer[i] = map[int64]int{}
		}
		byCustomer[i][s.CustomerID] += s.Minutes
	}
	return total, byCustomer
}

// ── kimai_week ──

// KimaiWeekConfig is the "kimai_week" widget's config.
type KimaiWeekConfig struct{ WeekHours float64 }

func decodeKimaiWeek(raw map[string]any) any {
	h := asFloat(raw["week_hours"])
	if h <= 0 {
		h = defaultWeekH
	}
	return KimaiWeekConfig{WeekHours: h}
}

// kimaiWeekView draws Mon–Sun against the daily target (week / 5):
//
//	target 8 h, Mo 6.5 h Di 9 h  →  Mo yellow below the line, Di over it
func kimaiWeekView(cfgAny any, results map[string]any, ctx ViewCtx) map[string]any {
	cfg := cfgAny.(KimaiWeekConfig)
	data, ok := results["data"].(*sources.KimaiDataset)
	if !ok {
		return map[string]any{}
	}
	total, _ := weekMinutes(data, todayOf(ctx))

	target := cfg.WeekHours / workDays * minutesPerHour
	top := target
	sum := 0
	for _, m := range total {
		top = max(top, float64(m))
		sum += m
	}
	cols := make([]DayCol, weekDays)
	for i, m := range total {
		tier := ""
		if i < workDays && float64(m) < target {
			tier = "yellow"
		}
		cols[i] = DayCol{I: i, H: max(pctOf(float64(m), top), 2), Hours: clockMinutes(m), Tier: tier}
	}
	left := int(cfg.WeekHours*minutesPerHour) - sum
	return map[string]any{"Days": cols, "TargetPct": pctOf(target, top), "Total": clockMinutes(sum),
		"Left": clockMinutes(max(left, -left)), "Over": left < 0, "WeekHours": cfg.WeekHours}
}

// ── kimai_split ──

// SplitLegend is one customer of the stacked week.
type SplitLegend struct {
	Name, Hours, Tier string
}

// kimaiSplitView stacks each day by customer; the busiest customers keep
// their own colour, the rest share "other".
func kimaiSplitView(_ any, results map[string]any, ctx ViewCtx) map[string]any {
	data, ok := results["data"].(*sources.KimaiDataset)
	if !ok {
		return map[string]any{}
	}
	total, byCustomer := weekMinutes(data, todayOf(ctx))
	names := metrics.KimaiCustomerNames(data)

	sums := map[int64]int{}
	top := 1
	for i := range weekDays {
		top = max(top, total[i])
		for c, m := range byCustomer[i] {
			sums[c] += m
		}
	}
	order := make([]int64, 0, len(sums))
	for c := range sums {
		order = append(order, c)
	}
	sort.Slice(order, func(a, b int) bool { return sums[order[a]] > sums[order[b]] })

	tierOf := map[int64]string{}
	other := len(splitTiers) - 1
	var legend []SplitLegend
	otherMin := 0
	for i, c := range order {
		if i < splitShown && i < other {
			tierOf[c] = splitTiers[i]
			name := names[c]
			if name == "" {
				name = "?"
			}
			legend = append(legend, SplitLegend{Name: name, Hours: clockMinutes(sums[c]), Tier: splitTiers[i]})
			continue
		}
		tierOf[c] = splitTiers[other]
		otherMin += sums[c]
	}
	if otherMin > 0 {
		legend = append(legend, SplitLegend{Name: "", Hours: clockMinutes(otherMin), Tier: splitTiers[other]})
	}

	cols := make([]DayCol, weekDays)
	for i := range weekDays {
		col := DayCol{I: i, H: pctOf(float64(total[i]), float64(top)), Hours: clockMinutes(total[i])}
		byTier := map[string]int{}
		for c, m := range byCustomer[i] {
			byTier[tierOf[c]] += m
		}
		for _, tier := range splitTiers {
			if m := byTier[tier]; m > 0 {
				col.Segs = append(col.Segs, Seg{H: pctOf(float64(m), float64(max(total[i], 1))), Tier: tier})
			}
		}
		cols[i] = col
	}
	return map[string]any{"Days": cols, "Legend": legend}
}

// ── unbilled_age ──

// UnbilledRow is one customer's unbilled money by age.
type UnbilledRow struct {
	Customer           string
	Fresh, Mid, Old    float64
	Total              float64
	FreshW, MidW, OldW int
}

func unbilledAgeView(_ any, results map[string]any, ctx ViewCtx) map[string]any {
	data, ok := results["data"].(*sources.KimaiDataset)
	if !ok {
		return map[string]any{}
	}
	var rows []UnbilledRow
	var fresh, mid, old float64
	top := 0.0
	for _, r := range metrics.UnbilledAging(data, todayOf(ctx)) {
		row := UnbilledRow{Customer: r.Customer, Fresh: r.Fresh, Mid: r.Mid, Old: r.Old, Total: r.Fresh + r.Mid + r.Old}
		fresh, mid, old = fresh+r.Fresh, mid+r.Mid, old+r.Old
		top = max(top, row.Total)
		rows = append(rows, row)
	}
	sort.Slice(rows, func(a, b int) bool { return rows[a].Total > rows[b].Total })
	if len(rows) > listShown {
		rows = rows[:listShown]
	}
	for i := range rows {
		rows[i].FreshW, rows[i].MidW, rows[i].OldW = pctOf(rows[i].Fresh, top), pctOf(rows[i].Mid, top), pctOf(rows[i].Old, top)
	}
	total := fresh + mid + old
	return map[string]any{"Total": total, "Rows": rows,
		"Bands": []AgingBand{
			{Key: "fresh", Tier: "green", Amount: fresh, Pct: pctOf(fresh, total)},
			{Key: "mid", Tier: "yellow", Amount: mid, Pct: pctOf(mid, total)},
			{Key: "old", Tier: "red", Amount: old, Pct: pctOf(old, total)},
		}}
}

// ── disks ──

// DiskRow is one disk as drawn.
type DiskRow struct {
	Name, Model string
	OK          bool
	Temp        float64
	TempTier    string
	Years       float64 // power-on time
}

func disksView(_ any, results map[string]any, _ ViewCtx) map[string]any {
	data, ok := results["data"].(*sources.ScrutinyDataset)
	if !ok {
		return map[string]any{}
	}
	healthy := 0
	var rows []DiskRow
	for _, d := range data.Disks {
		row := DiskRow{Name: d.Name, Model: d.Model, OK: d.Status == sources.ScrutinyPassed, Temp: d.Temp,
			Years: float64(d.Hours) / hoursPerDay / 365}
		switch {
		case d.Temp >= tempHigh:
			row.TempTier = "red"
		case d.Temp >= tempWarn:
			row.TempTier = "yellow"
		}
		if row.OK {
			healthy++
		}
		rows = append(rows, row)
	}
	sort.SliceStable(rows, func(a, b int) bool { return !rows[a].OK && rows[b].OK })
	return map[string]any{"Healthy": healthy, "Total": len(rows), "Rows": rows}
}

// ── komodo_stacks ──

func stackState(state string) string {
	switch strings.ToLower(state) {
	case "running", "healthy":
		return "ok"
	case "down", "unhealthy", "dead", "restarting":
		return "bad"
	default:
		return "mid"
	}
}

func komodoView(_ any, results map[string]any, _ ViewCtx) map[string]any {
	data, ok := results["data"].(*sources.KomodoDataset)
	if !ok {
		return map[string]any{}
	}
	var cells []StripCell
	var trouble []string
	updates, running := 0, 0
	for _, s := range data.Stacks {
		state := stackState(s.State)
		cells = append(cells, StripCell{State: state, Title: s.Name + " · " + s.State})
		if state == "ok" {
			running++
		} else if len(trouble) < listShown {
			trouble = append(trouble, s.Name+" · "+s.State)
		}
		if len(s.Updates) > 0 {
			updates++
		}
	}
	return map[string]any{"Servers": data.ServersHealthy, "ServersTotal": data.ServersTotal, "Running": running,
		"Stacks": len(data.Stacks), "Cells": cells, "Trouble": trouble, "Updates": updates, "Alerts": len(data.Alerts)}
}

// ── truenas_pools ──

func truenasView(_ any, results map[string]any, _ ViewCtx) map[string]any {
	data, ok := results["data"].(*sources.TrueNASDataset)
	if !ok {
		return map[string]any{}
	}
	var pools []HBar
	for _, p := range data.Pools {
		used := 0.0
		if p.Size > 0 {
			used = p.Allocated / p.Size
		}
		tier := loadTier(used * pctFull)
		if !p.Healthy {
			tier = "red"
		}
		pools = append(pools, HBar{Label: p.Name + " · " + p.Status, W: pctOf(used, 1), Tier: tier})
	}
	updates := 0
	for _, a := range data.Apps {
		if a.Update {
			updates++
		}
	}
	return map[string]any{"Pools": pools, "Alerts": len(data.Alerts), "Updates": updates, "Version": data.Version}
}

// ── dns_filter ──

func dnsFilterView(_ any, results map[string]any, _ ViewCtx) map[string]any {
	data, ok := results["data"].(*sources.DNSFilterDataset)
	if !ok {
		return map[string]any{}
	}
	return map[string]any{"Percent": data.Percent, "W": pctOf(data.Percent, pctFull), "Queries": data.Queries,
		"Blocked": data.Blocked, "Enabled": data.Enabled}
}

// ── vpn ──

func vpnView(_ any, results map[string]any, _ ViewCtx) map[string]any {
	data, ok := results["data"].(*sources.GluetunDataset)
	if !ok {
		return map[string]any{}
	}
	up := data.Status == "running"
	leak := data.ExitIP != "" && data.ExitIP == data.OwnIP
	wrongCountry := data.ExpectedCountry != "" && !strings.EqualFold(data.ExpectedCountry, data.Country)
	state := "ok"
	switch {
	case !up || leak:
		state = "fail"
	case wrongCountry:
		state = "warn"
	}
	return map[string]any{"Data": data, "Up": up, "Leak": leak, "WrongCountry": wrongCountry, "State": state}
}

// ── gateway ──

func gatewayView(_ any, results map[string]any, _ ViewCtx) map[string]any {
	data, ok := results["data"].(*sources.GatewayDataset)
	if !ok {
		return map[string]any{}
	}
	online, pending := 0, 0
	var offline []string
	for _, dev := range data.Devices {
		if dev.Online {
			online++
		} else if len(offline) < listShown {
			offline = append(offline, dev.Name)
		}
		if dev.Update {
			pending++
		}
	}
	return map[string]any{"Links": data.Gateways, "Online": online, "Devices": len(data.Devices), "Offline": offline,
		"Updates": data.Updates + pending, "Kind": data.Kind, "Version": data.Version}
}

// ── expiry ──

const peerDomains = "domains"

func expiryBar(label string, left int, err string) HBar {
	if err != "" {
		return HBar{Label: label, Value: err, Tier: "red"}
	}
	tier := ""
	switch {
	case left < expiryHigh:
		tier = "red"
	case left < expiryWarn:
		tier = "yellow"
	}
	return HBar{Label: label, Value: strconv.Itoa(left), W: max(pctOf(float64(left), expiryHorizon), 2), Tier: tier}
}

// expiryView lists certificates and domains soonest first:
//
//	shop.example 12 d (red) · nas.lan 40 d · example.org (domain) 200 d
func expiryView(_ any, results map[string]any, ctx ViewCtx) map[string]any {
	today := todayOf(ctx)
	daysTo := func(t time.Time) int { return int(t.Sub(today).Hours() / hoursPerDay) }

	type item struct {
		bar  HBar
		left int
	}
	var items []item
	if certs, ok := results["data"].(*sources.CertDataset); ok {
		for _, c := range certs.Certs {
			left := daysTo(c.NotAfter)
			items = append(items, item{expiryBar(c.Host, left, c.Error), left})
		}
	}
	if doms, ok := results[peerDomains].(*sources.DomainsDataset); ok {
		for _, dm := range doms.Domains {
			left := daysTo(dm.Expires)
			items = append(items, item{expiryBar(dm.Name, left, dm.Error), left})
		}
	}
	sort.SliceStable(items, func(a, b int) bool { return items[a].left < items[b].left })

	var bars []HBar
	for _, it := range items {
		if len(bars) < barsShown {
			bars = append(bars, it.bar)
		}
	}
	return map[string]any{"Bars": bars, "Total": len(items)}
}

// ── speed_history ──

func speedHistoryView(_ any, results map[string]any, _ ViewCtx) map[string]any {
	h, _ := results[HistorySlot].(*metrics.History)
	data, _ := results["data"].(*sources.SpeedtestDataset)
	if h == nil {
		return map[string]any{}
	}
	points := h.SeriesOf(metrics.SampleKey("speedtest", "down"))
	if len(points) > speedDays {
		points = points[len(points)-speedDays:]
	}
	expect := 0.0
	if data != nil {
		expect = data.ExpectDown
	}
	top := expect
	for _, p := range points {
		top = max(top, p.Value)
	}
	var bars []LoadBar
	slow := 0
	for _, p := range points {
		tier := ""
		if expect > 0 && p.Value < expect*speedWarn {
			tier = "yellow"
			slow++
		}
		bars = append(bars, LoadBar{H: max(pctOf(p.Value, top), 2), Tier: tier, Title: p.Day.Format(time.DateOnly)})
	}
	out := map[string]any{"Bars": bars, "Slow": slow, "Expect": expect, "ExpectPct": pctOf(expect, top)}
	if data != nil {
		out["Data"] = data
	}
	return out
}

// ── sabnzbd ──

func sabnzbdView(_ any, results map[string]any, _ ViewCtx) map[string]any {
	data, ok := results["data"].(*sources.SabnzbdDataset)
	if !ok {
		return map[string]any{}
	}
	failures := data.Failures
	if len(failures) > listShown {
		failures = failures[:listShown]
	}
	return map[string]any{"Data": data, "SpeedMB": data.SpeedKB / 1024, "Failures": failures}
}

// ── paperless_inbox ──

func paperlessInboxView(_ any, results map[string]any, ctx ViewCtx) map[string]any {
	data, ok := results["data"].(*sources.PaperlessDataset)
	if !ok {
		return map[string]any{}
	}
	out := map[string]any{"Data": data}
	if added, ok := metrics.ParseDay(data.OldestAdded); ok {
		out["OldestDays"] = int(todayOf(ctx).Sub(added).Hours() / hoursPerDay)
	}
	return out
}

// ── mail_invoices ──

func mailInvoicesView(_ any, results map[string]any, _ ViewCtx) map[string]any {
	data, ok := results["data"].(*sources.MailDataset)
	if !ok {
		return map[string]any{}
	}
	sum := 0.0
	for _, inv := range data.Invoices {
		sum += inv.Amount
	}
	shown := append([]sources.MailInvoice(nil), data.Invoices...)
	sort.Slice(shown, func(a, b int) bool { return shown[a].Date.After(shown[b].Date) })
	if len(shown) > listShown {
		shown = shown[:listShown]
	}
	return map[string]any{"Count": len(data.Invoices), "Sum": sum, "Shown": shown, "Scanned": data.Scanned, "Mailbox": data.Mailbox}
}

// ── freshrss_feeds ──

func freshrssView(_ any, results map[string]any, _ ViewCtx) map[string]any {
	data, ok := results["data"].(*sources.FreshRSSDataset)
	if !ok {
		return map[string]any{}
	}
	feeds := append([]sources.Feed(nil), data.Feeds...)
	sort.Slice(feeds, func(a, b int) bool { return feeds[a].Unread > feeds[b].Unread })
	top := 1
	if len(feeds) > 0 {
		top = max(feeds[0].Unread, 1)
	}
	var bars []HBar
	for _, f := range feeds {
		if f.Unread == 0 || len(bars) >= barsShown {
			break
		}
		bars = append(bars, HBar{Label: f.Title, Value: strconv.Itoa(f.Unread), W: pctOf(float64(f.Unread), float64(top))})
	}
	return map[string]any{"Unread": data.Unread, "Feeds": len(data.Feeds), "Bars": bars}
}

// ── linkwarden ──

func linkwardenView(_ any, results map[string]any, _ ViewCtx) map[string]any {
	data, ok := results["data"].(*sources.LinkwardenDataset)
	if !ok {
		return map[string]any{}
	}
	counts := map[string]int{}
	for _, l := range data.Links {
		counts[l.Collection]++
	}
	names := make([]string, 0, len(counts))
	for n := range counts {
		names = append(names, n)
	}
	sort.Slice(names, func(a, b int) bool { return counts[names[a]] > counts[names[b]] })
	top := 1
	if len(names) > 0 {
		top = counts[names[0]]
	}
	var bars []HBar
	for _, n := range names {
		if len(bars) >= barsShown {
			break
		}
		bars = append(bars, HBar{Label: n, Value: strconv.Itoa(counts[n]), W: pctOf(float64(counts[n]), float64(top))})
	}
	return map[string]any{"Links": len(data.Links), "Collections": len(names), "Bars": bars}
}

// ── kintsugi ──

func kintsugiView(_ any, results map[string]any, _ ViewCtx) map[string]any {
	data, ok := results["data"].(*sources.KintsugiDataset)
	if !ok {
		return map[string]any{}
	}
	open := data.Open
	if len(open) > listShown {
		open = open[:listShown]
	}
	failed := data.LastRun != nil && data.LastRun.Status == sources.KintsugiRunFailed
	return map[string]any{"Data": data, "Open": open, "RunFailed": failed}
}

// ── gitea_reviews ──

func giteaView(_ any, results map[string]any, _ ViewCtx) map[string]any {
	data, ok := results["data"].(*sources.GiteaDataset)
	if !ok {
		return map[string]any{}
	}
	cut := func(list []sources.Issue) []sources.Issue {
		if len(list) > listShown {
			return list[:listShown]
		}
		return list
	}
	return map[string]any{"Data": data, "Reviews": cut(data.Reviews), "Assigned": cut(data.Assigned)}
}

// ── dawarich_day ──

// dawarichTime reads a visit time: RFC3339 or Kimai-style offsets.
func dawarichTime(s string) time.Time {
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05-0700", "2006-01-02T15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// PlaceRow is one visit of today.
type PlaceRow struct {
	Name, From, To, Dur string
}

func dawarichDayView(_ any, results map[string]any, ctx ViewCtx) map[string]any {
	data, ok := results["data"].(*sources.DawarichDataset)
	if !ok {
		return map[string]any{}
	}
	now := time.Now()
	var spans []sources.KimaiSpan
	var rows []PlaceRow
	for _, v := range data.Visits {
		begin := dawarichTime(v.Start)
		if begin.IsZero() || begin.In(now.Location()).Format(time.DateOnly) != ctx.Today {
			continue
		}
		end := dawarichTime(v.End)
		spans = append(spans, sources.KimaiSpan{Begin: begin, End: end})
		to := ""
		if !end.IsZero() {
			to = end.In(now.Location()).Format("15:04")
		}
		rows = append(rows, PlaceRow{Name: v.Name, From: begin.In(now.Location()).Format("15:04"), To: to, Dur: clockMinutes(v.Minutes)})
	}
	from, to, segs, pos := dayBar(spans, now)
	return map[string]any{"Rows": rows, "DaySegs": segs, "DayNow": pos, "DayTicks": dayTicks(from, to)}
}

// ── authentik_logins ──

func authentikView(_ any, results map[string]any, _ ViewCtx) map[string]any {
	data, ok := results["data"].(*sources.AuthentikDataset)
	if !ok {
		return map[string]any{}
	}
	logins := data.Logins
	if len(logins) > listShown {
		logins = logins[:listShown]
	}
	return map[string]any{"Data": data, "Logins": logins}
}

// ── vaultwarden_2fa ──

func vaultwardenView(_ any, results map[string]any, _ ViewCtx) map[string]any {
	data, ok := results["data"].(*sources.VaultwardenDataset)
	if !ok {
		return map[string]any{}
	}
	with, active := 0, 0
	var without []string
	for _, u := range data.Users {
		if !u.Enabled {
			continue
		}
		active++
		if u.TwoFactor {
			with++
		} else if len(without) < listShown {
			without = append(without, u.Email)
		}
	}
	return map[string]any{"With": with, "Active": active, "Without": without, "WithPct": pctOf(float64(with), float64(max(active, 1))),
		"Version": data.Version}
}

func init() {
	on := func(key string, service enums.ServiceType, refresh int, decode DecodeFunc, view ViewFunc, extra ...Query) {
		Register(WidgetType{Key: key, Decode: decode, Template: "widgets/" + key, Category: CategoryInsight,
			Service: service, RefreshS: refresh, View: view,
			Queries: func(any) []Query { return append(dataQuery(nil), extra...) }})
	}
	const minute, hour = 60, 3600

	on("kimai_week", enums.ServiceKimai, 10*minute, decodeKimaiWeek, kimaiWeekView)
	on("kimai_split", enums.ServiceKimai, 10*minute, decodeEmpty, kimaiSplitView)
	on("unbilled_age", enums.ServiceKimai, hour, decodeEmpty, unbilledAgeView)
	on("disks", enums.ServiceScrutiny, hour, decodeEmpty, disksView)
	on("komodo_stacks", enums.ServiceKomodo, 5*minute, decodeEmpty, komodoView)
	on("truenas_pools", enums.ServiceTrueNAS, 10*minute, decodeEmpty, truenasView)
	on("pihole", enums.ServicePihole, 5*minute, decodeEmpty, dnsFilterView)
	on("adguard", enums.ServiceAdGuard, 5*minute, decodeEmpty, dnsFilterView)
	on("vpn", enums.ServiceGluetun, 5*minute, decodeEmpty, vpnView)
	on("gateway", enums.ServiceGateway, 5*minute, decodeEmpty, gatewayView)
	on("expiry", enums.ServiceCerts, hour, decodeEmpty, expiryView,
		Query{Name: peerDomains, Source: "data", Conn: ConnPeer, Service: enums.ServiceDomains})
	on("sabnzbd", enums.ServiceSabnzbd, 5*minute, decodeEmpty, sabnzbdView)
	on("paperless_inbox", enums.ServicePaperless, 30*minute, decodeEmpty, paperlessInboxView)
	on("mail_invoices", enums.ServiceMail, hour, decodeEmpty, mailInvoicesView)
	on("freshrss_feeds", enums.ServiceFreshRSS, 30*minute, decodeEmpty, freshrssView)
	on("linkwarden", enums.ServiceLinkwarden, hour, decodeEmpty, linkwardenView)
	on("gitea_reviews", enums.ServiceGitea, 15*minute, decodeEmpty, giteaView)
	on("dawarich_day", enums.ServiceDawarich, 30*minute, decodeEmpty, dawarichDayView)
	on("authentik_logins", enums.ServiceAuthentik, 15*minute, decodeEmpty, authentikView)
	on("vaultwarden_2fa", enums.ServiceVaultwarden, hour, decodeEmpty, vaultwardenView)
	on("kintsugi", enums.ServiceKintsugi, 15*minute, decodeEmpty, kintsugiView)

	Register(WidgetType{Key: "speed_history", Decode: decodeEmpty, Template: "widgets/speed_history", Category: CategoryInsight,
		Service: enums.ServiceSpeedtest, RefreshS: hour, View: speedHistoryView, Queries: dataQuery, Extra: ExtraHistory})
}
