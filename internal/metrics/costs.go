package metrics

// Cost and use of the homelab (phase 13):
//
//	HomelabSettings    the space's "homelab" settings
//	PowerWatts         current power draw from a Home Assistant sensor
//	PowerPrice         Tibber's average price, else the configured one
//	PowerPerService    monthly power cost split by Proxmox guests' CPU use
//	JobPrices          when backups run against the cheapest hours
//	HomelabCost        power, hardware write-off, domains, hosting per month
//	BusinessShare      share of stacks and repos named after Kimai customers
//	Replacements       old devices where a frugal one pays back
//	UnusedServices     stacks, guests and apps nobody opens
//	WeatherAdjusted    last week's energy use against the weather-based expectation

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"andon/internal/sources"
)

const (
	hoursPerMonth = 730.0
	wattsPerKW    = 1000.0
	monthsPerYear = 12.0
	heatingBaseC  = 15.0 // Heizgrenze for heating degree days
	weekLen       = 7
	minFitDays    = 10
	defaultYears  = 5.0
	powerDevice   = "power"
)

// HomelabSettings are the space's cost settings.
type HomelabSettings struct {
	PowerEntity  string
	PowerPrice   float64 // €/kWh when Tibber is missing
	HardwareYrs  float64
	DomainYearly float64
	CloudMonthly float64
	HostingWords []string
}

// HomelabSettingsOf reads settings["homelab"].
func HomelabSettingsOf(settings map[string]any) HomelabSettings {
	raw, _ := settings["homelab"].(map[string]any)
	f := func(k string) float64 { v, _ := raw[k].(float64); return v }
	s := HomelabSettings{PowerPrice: f("power_price"), HardwareYrs: f("hardware_years"), DomainYearly: f("domain_yearly"), CloudMonthly: f("cloud_monthly")}
	s.PowerEntity, _ = raw["power_entity"].(string)
	words, _ := raw["hosting_words"].(string)
	for _, w := range strings.Split(words, ",") {
		if w = strings.TrimSpace(strings.ToLower(w)); w != "" {
			s.HostingWords = append(s.HostingWords, w)
		}
	}
	if s.HardwareYrs <= 0 {
		s.HardwareYrs = defaultYears
	}
	return s
}

// PowerWatts reads a power sensor in W or kW.
func PowerWatts(hass *sources.HassDataset, entity string) (float64, bool) {
	if hass == nil || entity == "" {
		return 0, false
	}
	e, ok := hass.Find(entity)
	if !ok {
		return 0, false
	}
	v, err := strconv.ParseFloat(e.State, 64)
	if err != nil {
		return 0, false
	}
	if strings.EqualFold(e.Unit, "kW") {
		v *= wattsPerKW
	}
	return v, true
}

// PowerPrice: today's and tomorrow's Tibber mean, else the setting.
func PowerPrice(tibber *sources.TibberDataset, fallback float64) float64 {
	if tibber == nil || len(tibber.Prices) == 0 {
		return fallback
	}
	sum := 0.0
	for _, p := range tibber.Prices {
		sum += p.Total
	}
	return sum / float64(len(tibber.Prices))
}

// MonthlyPowerCost converts a steady draw to € per month.
func MonthlyPowerCost(watts, price float64) float64 {
	return watts / wattsPerKW * hoursPerMonth * price
}

// ServiceCost is one guest's share of the power bill.
type ServiceCost struct {
	Name    string
	Share   float64
	Monthly float64
}

// PowerPerService splits the monthly power cost by the running guests'
// CPU use; what the guests do not use is the host's base load.
func PowerPerService(proxmox *sources.ProxmoxDataset, monthly float64) []ServiceCost {
	if proxmox == nil {
		return nil
	}
	total := 0.0
	for _, g := range proxmox.Guests {
		if g.Running && !g.Template {
			total += g.CPU
		}
	}
	if total == 0 {
		return nil
	}
	var out []ServiceCost
	for _, g := range proxmox.Guests {
		if !g.Running || g.Template || g.CPU == 0 {
			continue
		}
		share := g.CPU / total
		out = append(out, ServiceCost{Name: g.Name, Share: share, Monthly: round2(monthly * share)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Monthly > out[j].Monthly })
	return out
}

// JobPrice is when a recurring job runs, against the cheapest hour.
type JobPrice struct {
	Job               string
	Hour, CheapHour   int
	Price, CheapPrice float64 // €/kWh at those hours (mean of known days)
}

// hourPrices averages Tibber's prices per hour of day.
func hourPrices(tibber *sources.TibberDataset) map[int]float64 {
	sum, n := map[int]float64{}, map[int]int{}
	for _, p := range tibber.Prices {
		h := p.At.Local().Hour()
		sum[h] += p.Total
		n[h]++
	}
	out := map[int]float64{}
	for h, s := range sum {
		out[h] = s / float64(n[h])
	}
	return out
}

// JobPrices compares the hours backups last ran with the cheapest hour.
func JobPrices(tibber *sources.TibberDataset, datasets map[string]any) []JobPrice {
	if tibber == nil || len(tibber.Prices) == 0 {
		return nil
	}
	prices := hourPrices(tibber)
	cheap, cheapPrice := -1, 0.0
	for h, p := range prices {
		if cheap < 0 || p < cheapPrice {
			cheap, cheapPrice = h, p
		}
	}
	jobs := map[string]time.Time{}
	for _, raw := range datasets {
		switch d := raw.(type) {
		case *sources.BorgDataset:
			for _, c := range d.Clients {
				if !c.LastBackup.IsZero() {
					jobs["Borg "+c.Name] = c.LastBackup
				}
			}
		case *sources.TrueNASDataset:
			for _, s := range d.Snapshots {
				if s.Enabled && !s.Last.IsZero() {
					jobs["TrueNAS "+s.Dataset] = s.Last
				}
			}
		case *sources.ProxmoxDataset:
			for id, t := range d.Backups {
				jobs["Proxmox "+strconv.FormatInt(id, 10)] = t
			}
		}
	}
	var out []JobPrice
	for name, t := range jobs {
		h := t.Local().Hour()
		p, ok := prices[h]
		if !ok {
			continue
		}
		out = append(out, JobPrice{Job: name, Hour: h, Price: p, CheapHour: cheap, CheapPrice: cheapPrice})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Price-out[i].CheapPrice > out[j].Price-out[j].CheapPrice })
	return out
}

// CostItem is one line of the homelab bill.
type CostItem struct {
	Key     string // catalog key under cost.*
	Name    string
	Monthly float64
}

// HomelabBill is the monthly cost of the homelab.
type HomelabBill struct {
	Items    []CostItem
	Total    float64
	Business float64 // share used for customers, 0..1
	Cloud    float64 // comparable cloud cost per month, 0 = not set
}

// CostInputs are the scope's datasets that carry costs.
type CostInputs struct {
	Hass    *sources.HassDataset
	Tibber  *sources.TibberDataset
	Snipe   *sources.SnipeDataset
	Sure    *sources.SureDataset
	Domains *sources.DomainsDataset
}

// HomelabCost adds up power, hardware write-off, domains and hosting.
func HomelabCost(in CostInputs, s HomelabSettings, today time.Time) HomelabBill {
	var bill HomelabBill
	add := func(key, name string, monthly float64) {
		if monthly > 0 {
			bill.Items = append(bill.Items, CostItem{Key: key, Name: name, Monthly: round2(monthly)})
			bill.Total += monthly
		}
	}
	if w, ok := PowerWatts(in.Hass, s.PowerEntity); ok {
		add("power", s.PowerEntity, MonthlyPowerCost(w, PowerPrice(in.Tibber, s.PowerPrice)))
	}
	if in.Snipe != nil {
		for _, a := range in.Snipe.Assets {
			bought, ok := ParseDay(a.PurchaseDate)
			if !ok || a.PurchaseCost <= 0 || today.Sub(bought).Hours()/hoursPerDay > s.HardwareYrs*365 {
				continue
			}
			add("hardware", a.Name, a.PurchaseCost/(s.HardwareYrs*monthsPerYear))
		}
	}
	if in.Domains != nil && s.DomainYearly > 0 {
		add("domains", strconv.Itoa(len(in.Domains.Domains)), float64(len(in.Domains.Domains))*s.DomainYearly/monthsPerYear)
	}
	if in.Sure != nil {
		for _, r := range in.Sure.Recurring {
			name := strings.ToLower(r.Name)
			for _, w := range s.HostingWords {
				if r.Expense && r.Status != "inactive" && strings.Contains(name, w) {
					add("hosting", r.Name, monthly(r))
					break
				}
			}
		}
	}
	bill.Total, bill.Cloud = round2(bill.Total), s.CloudMonthly
	return bill
}

// BusinessShare: the share of Komodo stacks and Git repos named after a
// Kimai customer or project – a basis for the business part of IT costs.
func BusinessShare(kimai *sources.KimaiDataset, names []string) (float64, int, int) {
	if kimai == nil || len(names) == 0 {
		return 0, 0, len(names)
	}
	var targets []string
	for _, c := range kimai.Customers {
		targets = append(targets, c.Name)
	}
	for _, p := range kimai.Projects {
		targets = append(targets, p.Name)
	}
	business := 0
	for _, n := range names {
		for _, t := range targets {
			if sameThing(t, n) || sameThing(n, t) {
				business++
				break
			}
		}
	}
	return float64(business) / float64(len(names)), business, len(names)
}

// WorkNames lists Komodo stack and Git repository names of a scope, for
// the business share.
func WorkNames(datasets map[string]any) []string {
	var names []string
	for _, raw := range datasets {
		switch d := raw.(type) {
		case *sources.KomodoDataset:
			for _, s := range d.Stacks {
				names = append(names, s.Name)
			}
		case *sources.GiteaDataset:
			for _, r := range d.Repos {
				names = append(names, r.Name)
			}
		case *sources.GitHubDataset:
			for _, r := range d.Repos {
				names = append(names, r.Name)
			}
		}
	}
	return names
}

// Replacement is an old device where a frugal one would pay back.
type Replacement struct {
	Asset         string
	AgeYears      float64
	Watts         float64
	YearlyCost    float64
	PaybackMonths int
}

// Replacements pairs Snipe-IT assets with Home Assistant power sensors by
// name and computes when a device of newWatts for newCost pays back.
func Replacements(snipe *sources.SnipeDataset, hass *sources.HassDataset, price, newWatts, newCost float64, today time.Time) []Replacement {
	if snipe == nil || hass == nil || price <= 0 {
		return nil
	}
	var out []Replacement
	for _, a := range snipe.Assets {
		bought, ok := ParseDay(a.PurchaseDate)
		if !ok {
			continue
		}
		for _, e := range hass.Entities {
			if e.DeviceClass != powerDevice || !(sameThing(a.Name, e.Name+" "+e.ID) || sameThing(e.Name, a.Name)) {
				continue
			}
			w, err := strconv.ParseFloat(e.State, 64)
			if err != nil || w <= newWatts {
				continue
			}
			yearly := MonthlyPowerCost(w, price) * monthsPerYear
			saving := (w - newWatts) / wattsPerKW * hoursPerMonth * price
			r := Replacement{Asset: a.Name, AgeYears: round1(today.Sub(bought).Hours() / hoursPerDay / 365), Watts: w, YearlyCost: round2(yearly)}
			r.PaybackMonths = int(newCost / saving)
			out = append(out, r)
			break
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PaybackMonths < out[j].PaybackMonths })
	return out
}

// Unused is a running service without any sign of use.
type Unused struct {
	Name, Kind string
	MemBytes   float64
}

// UnusedServices lists Komodo stacks, running Proxmox guests and TrueNAS
// apps whose name matches a usage signal (tile, login app, website) that
// has not been used for days. Services without any matching signal are
// left out: no signal says nothing about use.
func UnusedServices(datasets map[string]any, usage []Usage, now time.Time, days int) []Unused {
	since := now.AddDate(0, 0, -days)
	idle := func(name string) bool {
		matched, used := false, false
		for _, u := range usage {
			if sameThing(name, u.Name) {
				matched = true
				used = used || u.Last.After(since)
			}
		}
		return matched && !used
	}
	var out []Unused
	for _, raw := range datasets {
		switch d := raw.(type) {
		case *sources.KomodoDataset:
			for _, s := range d.Stacks {
				if s.State == "running" && idle(s.Name) {
					out = append(out, Unused{Name: s.Name, Kind: "komodo"})
				}
			}
		case *sources.ProxmoxDataset:
			for _, g := range d.Guests {
				if g.Running && !g.Template && idle(g.Name) {
					out = append(out, Unused{Name: g.Name, Kind: "proxmox", MemBytes: g.MemBytes})
				}
			}
		case *sources.TrueNASDataset:
			for _, a := range d.Apps {
				if strings.EqualFold(a.State, "running") && idle(a.Name) {
					out = append(out, Unused{Name: a.Name, Kind: "truenas"})
				}
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Weather is last week's energy use against what the weather explains.
type Weather struct {
	ActualKWh, ExpectedKWh float64
	Deviation              float64 // (actual − expected) ÷ expected
}

// WeatherAdjusted fits kWh = base + k·HDD over the days before the last
// week and compares the last week with the fit; ok is false without
// temperatures or enough days.
func WeatherAdjusted(days []sources.EnergyDay) (Weather, bool) {
	var fit, week []sources.EnergyDay
	for _, d := range days {
		if d.HasTemp {
			fit = append(fit, d)
		}
	}
	if len(fit) < minFitDays+weekLen {
		return Weather{}, false
	}
	fit, week = fit[:len(fit)-weekLen], fit[len(fit)-weekLen:]
	xs, ys := make([]float64, len(fit)), make([]float64, len(fit))
	for i, d := range fit {
		xs[i], ys[i] = hdd(d.TempC), d.KWh
	}
	base, slope, ok := linearFit(xs, ys)
	if !ok {
		return Weather{}, false
	}
	var w Weather
	for _, d := range week {
		w.ActualKWh += d.KWh
		w.ExpectedKWh += base + slope*hdd(d.TempC)
	}
	if w.ExpectedKWh <= 0 {
		return Weather{}, false
	}
	w.Deviation = (w.ActualKWh - w.ExpectedKWh) / w.ExpectedKWh
	w.ActualKWh, w.ExpectedKWh = round1(w.ActualKWh), round1(w.ExpectedKWh)
	return w, true
}

// linearFit returns a, b of y = a + b·x by least squares; ok is false
// when x does not vary.
func linearFit(xs, ys []float64) (float64, float64, bool) {
	n := float64(len(xs))
	var sx, sy, sxx, sxy float64
	for i := range xs {
		sx, sy, sxx, sxy = sx+xs[i], sy+ys[i], sxx+xs[i]*xs[i], sxy+xs[i]*ys[i]
	}
	den := n*sxx - sx*sx
	if n == 0 || den == 0 {
		return sy / max(n, 1), 0, false
	}
	b := (n*sxy - sx*sy) / den
	return (sy - b*sx) / n, b, true
}

// hdd is a day's heating degree days below the heating limit.
func hdd(tempC float64) float64 { return max(heatingBaseC-tempC, 0) }
