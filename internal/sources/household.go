package sources

// Everyday services:
//
//	vaultwarden  admin token          users, two-factor, version
//	speedtest    Speedtest Tracker    latest down/up/ping against expectations
//	grocy        GROCY-API-KEY        expired, due and missing products, chores
//	dwd          Bright Sky (DWD)     weather warnings for a place
//	github       api.github.com       repos: issues, PRs, CI, latest release
//	tibber       GraphQL              electricity prices and daily cost

import (
	"context"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"dashboard/internal/drivers/httpclient"
	"dashboard/internal/drivers/services"
	"dashboard/internal/enums"
)

const (
	bitsPerMbit       = 1e6
	grocyDueDays      = "5"
	githubPerPage     = "100"
	tibberDays        = 30
	dwdTTL            = 15 * time.Minute
	priceTTL          = 15 * time.Minute
	vaultwardenLayout = "2006-01-02 15:04:05 MST"
)

// ── Vaultwarden ──

type VaultUser struct {
	Email              string
	TwoFactor, Enabled bool
	LastActive         time.Time
}

type VaultwardenDataset struct {
	URL     string
	Version string
	Users   []VaultUser
}

type VaultwardenData struct{}

func (VaultwardenData) Key() string                { return "vaultwarden.data" }
func (VaultwardenData) TTL() time.Duration         { return opsTTL }
func (VaultwardenData) Service() enums.ServiceType { return enums.ServiceVaultwarden }

func (VaultwardenData) Fetch(ctx context.Context, sctx Ctx) (any, error) {
	if isDemo(sctx) {
		return DemoVaultwarden(time.Now().UTC()), nil
	}
	secret, err := needSecret(sctx)
	if err != nil {
		return nil, err
	}
	api := services.VaultwardenApi{URL: sctx.URL, Token: secret, Verify: sctx.VerifyTLS}
	users, err := api.Users(ctx)
	if err != nil {
		return nil, fetchError(err)
	}
	data := &VaultwardenDataset{URL: sctx.URL, Version: api.Version(ctx)}
	for _, raw := range asList(users) {
		u := asMap(raw)
		data.Users = append(data.Users, VaultUser{Email: asStr(u["email"]), TwoFactor: asBool(u["twoFactorEnabled"]),
			Enabled: asBool(u["userEnabled"]), LastActive: vaultTime(u["lastActive"])})
	}
	return data, nil
}

// vaultTime reads "2026-09-25 10:00:00 UTC" or RFC 3339.
func vaultTime(v any) time.Time {
	if t := parseTime(v); !t.IsZero() {
		return t
	}
	t, _ := time.Parse(vaultwardenLayout, asStr(v))
	return t.UTC()
}

// ── Speedtest Tracker ──

type SpeedtestDataset struct {
	URL                  string
	Down, Up, Ping       float64 // Mbit/s, ms
	At                   time.Time
	ExpectDown, ExpectUp float64 // from the options, 0 = none
}

type SpeedtestData struct{}

func (SpeedtestData) Key() string                { return "speedtest.data" }
func (SpeedtestData) TTL() time.Duration         { return opsTTL }
func (SpeedtestData) Service() enums.ServiceType { return enums.ServiceSpeedtest }

func (SpeedtestData) Fetch(ctx context.Context, sctx Ctx) (any, error) {
	data := &SpeedtestDataset{URL: sctx.URL, ExpectDown: asFloat(sctx.Options["expect_down"]), ExpectUp: asFloat(sctx.Options["expect_up"])}
	if isDemo(sctx) {
		data.Down, data.Up, data.Ping, data.At = 243, 41, 12, time.Now().UTC().Add(-20*time.Minute)
		return data, nil
	}

	// v1 API with token; the old open endpoint reports Mbit/s directly.
	api := services.BearerApi(sctx.URL, sctx.Secret, sctx.VerifyTLS)
	if sctx.Secret != "" {
		body, err := api.Get(ctx, "api/v1/results/latest", nil)
		if err != nil {
			return nil, fetchError(err)
		}
		r := asMap(asMap(body)["data"])
		data.Down, data.Up = asFloat(r["download_bits"])/bitsPerMbit, asFloat(r["upload_bits"])/bitsPerMbit
		data.Ping, data.At = asFloat(r["ping"]), parseTime(r["created_at"])
		return data, nil
	}
	body, err := api.Get(ctx, "api/speedtest/latest", nil)
	if err != nil {
		return nil, fetchError(err)
	}
	r := asMap(asMap(body)["data"])
	data.Down, data.Up, data.Ping, data.At = asFloat(r["download"]), asFloat(r["upload"]), asFloat(r["ping"]), parseTime(r["created_at"])
	return data, nil
}

// ── Grocy ──

// Product is one stock entry; Due is its best-before day.
type Product struct {
	Name    string
	Due     string
	Missing float64 // amount below the minimum stock
}

// Chore is one household chore with its next due time.
type Chore struct {
	Name string
	Due  time.Time
}

type GrocyDataset struct {
	URL                    string
	Expired, Overdue, Soon []Product
	Missing                []Product
	Chores                 []Chore
}

type GrocyData struct{}

func (GrocyData) Key() string                { return "grocy.data" }
func (GrocyData) TTL() time.Duration         { return opsTTL }
func (GrocyData) Service() enums.ServiceType { return enums.ServiceGrocy }

func (GrocyData) Fetch(ctx context.Context, sctx Ctx) (any, error) {
	if isDemo(sctx) {
		return DemoGrocy(time.Now().UTC()), nil
	}
	secret, err := needSecret(sctx)
	if err != nil {
		return nil, err
	}
	api := services.HeaderApi(sctx.URL, "GROCY-API-KEY", secret, sctx.VerifyTLS)
	stock, err := api.Get(ctx, "api/stock/volatile", url.Values{"due_soon_days": {grocyDueDays}})
	if err != nil {
		return nil, fetchError(err)
	}
	chores, err := api.Get(ctx, "api/chores", nil)
	if err != nil {
		return nil, fetchError(err)
	}
	s := asMap(stock)
	products := func(key string) []Product {
		var out []Product
		for _, raw := range asList(s[key]) {
			m := asMap(raw)
			out = append(out, Product{Name: firstStr(asStr(asMap(m["product"])["name"]), asStr(m["name"])),
				Due: day(m["best_before_date"]), Missing: asFloat(m["amount_missing"])})
		}
		return out
	}
	data := &GrocyDataset{URL: sctx.URL, Expired: products("expired_products"), Overdue: products("overdue_products"),
		Soon: products("due_products"), Missing: products("missing_products")}
	for _, raw := range asList(chores) {
		m := asMap(raw)
		due, _ := time.ParseInLocation(time.DateTime, asStr(m["next_estimated_execution_time"]), time.Local)
		if due.IsZero() {
			continue
		}
		data.Chores = append(data.Chores, Chore{Name: asStr(m["chore_name"]), Due: due.UTC()})
	}
	sort.Slice(data.Chores, func(i, j int) bool { return data.Chores[i].Due.Before(data.Chores[j].Due) })
	return data, nil
}

// ── DWD warnings via Bright Sky ──

// Warning severities as Bright Sky reports them.
const (
	WarnMinor    = "minor"
	WarnModerate = "moderate"
	WarnSevere   = "severe"
	WarnExtreme  = "extreme"
)

type WeatherWarning struct {
	ID            string
	Event         string
	Headline      string
	Severity      string
	Onset, Expire time.Time
}

type DWDDataset struct {
	URL      string
	Place    string
	Warnings []WeatherWarning
}

type DWDData struct{}

func (DWDData) Key() string                { return "dwd.data" }
func (DWDData) TTL() time.Duration         { return dwdTTL }
func (DWDData) Service() enums.ServiceType { return enums.ServiceDWD }

func (DWDData) Fetch(ctx context.Context, sctx Ctx) (any, error) {
	if isDemo(sctx) {
		return DemoDWD(time.Now().UTC()), nil
	}
	lat, lon := asFloat(sctx.Options["lat"]), asFloat(sctx.Options["lon"])
	if lat == 0 && lon == 0 {
		return nil, newSourceError("options lat/lon missing")
	}
	params := url.Values{"lat": {fmtCoord(lat)}, "lon": {fmtCoord(lon)}}
	body, err := services.BearerApi(sctx.URL, "", sctx.VerifyTLS).Get(ctx, "alerts", params)
	if err != nil {
		return nil, fetchError(err)
	}
	b := asMap(body)
	data := &DWDDataset{URL: sctx.URL, Place: asStr(asMap(b["location"])["name"])}
	for _, raw := range asList(b["alerts"]) {
		a := asMap(raw)
		data.Warnings = append(data.Warnings, WeatherWarning{ID: asStr(a["alert_id"]), Event: firstStr(asStr(a["event_de"]), asStr(a["event_en"])),
			Headline: firstStr(asStr(a["headline_de"]), asStr(a["headline_en"])), Severity: strings.ToLower(asStr(a["severity"])),
			Onset: parseTime(a["onset"]), Expire: parseTime(a["expires"])})
	}
	return data, nil
}

// fmtCoord renders a coordinate for a query ("52.52").
func fmtCoord(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// ── GitHub ──

type GitRepo struct {
	Name       string
	Issues     int // without pull requests
	PRs        int
	CI         string // conclusion of the latest run on the default branch, "" = none
	CIURL      string
	Release    string
	ReleasedAt time.Time
}

type GitHubDataset struct {
	URL           string
	Repos         []GitRepo
	Notifications int
}

type GitHubData struct{}

func (GitHubData) Key() string                { return "github.data" }
func (GitHubData) TTL() time.Duration         { return opsTTL }
func (GitHubData) Service() enums.ServiceType { return enums.ServiceGitHub }

func (GitHubData) Fetch(ctx context.Context, sctx Ctx) (any, error) {
	if isDemo(sctx) {
		return DemoGitHub(time.Now().UTC()), nil
	}
	api := services.BearerApi(sctx.URL, sctx.Secret, sctx.VerifyTLS)
	data := &GitHubDataset{URL: sctx.URL}
	for _, raw := range asList(sctx.Options["repos"]) {
		repo, err := loadRepo(ctx, api, asStr(raw))
		if err != nil {
			return nil, fetchError(err)
		}
		data.Repos = append(data.Repos, repo)
	}
	if sctx.Secret != "" {
		if list, err := api.Get(ctx, "notifications", url.Values{"per_page": {githubPerPage}}); err == nil {
			data.Notifications = len(asList(list))
		}
	}
	return data, nil
}

func loadRepo(ctx context.Context, api services.KeyedApi, name string) (GitRepo, error) {
	path := "repos/" + name
	info, err := api.Get(ctx, path, nil)
	if err != nil {
		return GitRepo{}, err
	}
	pulls, err := api.Get(ctx, path+"/pulls", url.Values{"state": {"open"}, "per_page": {githubPerPage}})
	if err != nil {
		return GitRepo{}, err
	}
	i := asMap(info)
	repo := GitRepo{Name: name, PRs: len(asList(pulls))}
	// open_issues_count includes pull requests.
	repo.Issues = max(int(asFloat(i["open_issues_count"]))-repo.PRs, 0)

	runs, err := api.Get(ctx, path+"/actions/runs", url.Values{"branch": {asStr(i["default_branch"])}, "per_page": {"1"}})
	if err == nil {
		if list := asList(asMap(runs)["workflow_runs"]); len(list) > 0 {
			run := asMap(list[0])
			repo.CI, repo.CIURL = asStr(run["conclusion"]), asStr(run["html_url"])
		}
	}
	if release, err := api.Get(ctx, path+"/releases/latest", nil); err == nil && release != nil {
		r := asMap(release)
		repo.Release, repo.ReleasedAt = asStr(r["tag_name"]), parseTime(r["published_at"])
	}
	return repo, nil
}

// ── Tibber ──

// PricePoint is one hour's electricity price (total incl. taxes).
type PricePoint struct {
	At    time.Time
	Total float64
}

// EnergyDay is one day's consumption and cost.
type EnergyDay struct {
	Day       string
	KWh, Cost float64
	TempC     float64 // daily mean outside temperature
	HasTemp   bool
}

type TibberDataset struct {
	URL      string
	Home     string
	Currency string
	Current  float64
	Level    string // VERY_CHEAP … VERY_EXPENSIVE
	Prices   []PricePoint
	Days     []EnergyDay
}

type TibberData struct{}

func (TibberData) Key() string                { return "tibber.data" }
func (TibberData) TTL() time.Duration         { return priceTTL }
func (TibberData) Service() enums.ServiceType { return enums.ServiceTibber }

const tibberQuery = `{ viewer { homes { appNickname
  currentSubscription { priceInfo { current { total level currency }
    today { total startsAt } tomorrow { total startsAt } } }
  consumption(resolution: DAILY, last: 30) { nodes { from cost consumption } } } } }`

func (TibberData) Fetch(ctx context.Context, sctx Ctx) (any, error) {
	if isDemo(sctx) {
		return DemoTibber(time.Now().UTC()), nil
	}
	secret, err := needSecret(sctx)
	if err != nil {
		return nil, err
	}
	body, err := services.BearerApi(sctx.URL, secret, sctx.VerifyTLS).Post(ctx, "", map[string]any{"query": tibberQuery})
	if err != nil {
		return nil, fetchError(err)
	}
	b := asMap(body)
	if errs := asList(b["errors"]); len(errs) > 0 {
		return nil, newSourceError("%s", asStr(asMap(errs[0])["message"]))
	}
	homes := asList(asMap(asMap(b["data"])["viewer"])["homes"])
	if len(homes) == 0 {
		return nil, newSourceError("no home")
	}
	home := asMap(homes[0])
	info := asMap(asMap(home["currentSubscription"])["priceInfo"])
	current := asMap(info["current"])
	data := &TibberDataset{URL: sctx.URL, Home: asStr(home["appNickname"]), Currency: asStr(current["currency"]),
		Current: asFloat(current["total"]), Level: asStr(current["level"])}
	for _, key := range []string{"today", "tomorrow"} {
		for _, raw := range asList(info[key]) {
			p := asMap(raw)
			data.Prices = append(data.Prices, PricePoint{At: parseTime(p["startsAt"]), Total: asFloat(p["total"])})
		}
	}
	for _, raw := range asList(asMap(home["consumption"])["nodes"]) {
		n := asMap(raw)
		data.Days = append(data.Days, EnergyDay{Day: day(n["from"]), KWh: asFloat(n["consumption"]), Cost: asFloat(n["cost"])})
	}
	addTemperatures(ctx, data.Days, sctx.Options)
	return data, nil
}

// addTemperatures fills the days' mean outside temperature from Open-Meteo
// when the connection has lat/lon options; best-effort.
func addTemperatures(ctx context.Context, days []EnergyDay, options map[string]any) {
	lat, lon := asFloat(options["lat"]), asFloat(options["lon"])
	if lat == 0 && lon == 0 {
		return
	}
	params := url.Values{"latitude": {fmtCoord(lat)}, "longitude": {fmtCoord(lon)}, "daily": {"temperature_2m_mean"},
		"past_days": {strconv.Itoa(tibberDays + 1)}, "forecast_days": {"1"}, "timezone": {"auto"}}
	body, _, err := httpclient.GetJSON(ctx, openMeteoURL, httpclient.Options{Params: params})
	if err != nil {
		return
	}
	daily := asMap(asMap(body)["daily"])
	temps := map[string]float64{}
	values := asList(daily["temperature_2m_mean"])
	for i, d := range asList(daily["time"]) {
		if i < len(values) && values[i] != nil {
			temps[asStr(d)] = asFloat(values[i])
		}
	}
	for i := range days {
		if t, ok := temps[days[i].Day]; ok {
			days[i].TempC, days[i].HasTemp = t, true
		}
	}
}

// ── Demo ──

func DemoVaultwarden(now time.Time) *VaultwardenDataset {
	return &VaultwardenDataset{URL: "https://vault.demo", Version: "1.34.3", Users: []VaultUser{
		{Email: "anna@example.org", TwoFactor: true, Enabled: true, LastActive: now.AddDate(0, 0, -1)},
		{Email: "ben@example.org", Enabled: true, LastActive: now.AddDate(0, 0, -3)},
	}}
}

func DemoGrocy(now time.Time) *GrocyDataset {
	today := now.Format(time.DateOnly)
	return &GrocyDataset{URL: "https://grocy.demo",
		Expired: []Product{{Name: "Joghurt", Due: now.AddDate(0, 0, -2).Format(time.DateOnly)}},
		Soon:    []Product{{Name: "Milch", Due: today}, {Name: "Brot", Due: now.AddDate(0, 0, 2).Format(time.DateOnly)}},
		Missing: []Product{{Name: "Kaffee", Missing: 1}},
		Chores:  []Chore{{Name: "Bad putzen", Due: now.Add(-30 * time.Hour)}, {Name: "Pflanzen gießen", Due: now.Add(30 * time.Hour)}}}
}

func DemoDWD(now time.Time) *DWDDataset {
	return &DWDDataset{URL: "https://api.brightsky.dev", Place: "Berlin", Warnings: []WeatherWarning{
		{ID: "demo-1", Event: "STURMBÖEN", Headline: "Amtliche WARNUNG vor STURMBÖEN", Severity: WarnModerate, Onset: now, Expire: now.Add(8 * time.Hour)},
	}}
}

func DemoGitHub(now time.Time) *GitHubDataset {
	return &GitHubDataset{URL: "https://api.github.com", Notifications: 4, Repos: []GitRepo{
		{Name: "shrippen/dashboard", Issues: 3, PRs: 1, CI: "success", Release: "v0.12.0", ReleasedAt: now.AddDate(0, 0, -6)},
		{Name: "shrippen/dotfiles", Issues: 0, PRs: 0, CI: "failure"},
	}}
}

func DemoTibber(now time.Time) *TibberDataset {
	start := now.Truncate(24 * time.Hour)
	data := &TibberDataset{URL: "https://api.tibber.com/v1-beta/gql", Home: "Zuhause", Currency: "EUR", Level: "CHEAP"}
	for h := range 24 {
		data.Prices = append(data.Prices, PricePoint{At: start.Add(time.Duration(h) * time.Hour), Total: 0.24 + 0.08*float64((h+6)%24)/24})
	}
	data.Current = data.Prices[now.Hour()].Total
	for d := tibberDays; d > 0; d-- {
		data.Days = append(data.Days, EnergyDay{Day: now.AddDate(0, 0, -d).Format(time.DateOnly), KWh: 7 + float64(d%5), Cost: 2 + float64(d%5)*0.3,
			TempC: 18 - float64(d%5)*2, HasTemp: true})
	}
	return data
}

func init() {
	Register(VaultwardenData{})
	Register(testOf{VaultwardenData{}, func(d any) map[string]any { return map[string]any{"version": d.(*VaultwardenDataset).Version} }})
	Register(SpeedtestData{})
	Register(testOf{SpeedtestData{}, func(d any) map[string]any { return map[string]any{"down": d.(*SpeedtestDataset).Down} }})
	Register(GrocyData{})
	Register(testOf{GrocyData{}, func(d any) map[string]any { return map[string]any{"missing": len(d.(*GrocyDataset).Missing)} }})
	Register(DWDData{})
	Register(testOf{DWDData{}, func(d any) map[string]any { return map[string]any{"place": d.(*DWDDataset).Place} }})
	Register(GitHubData{})
	Register(testOf{GitHubData{}, func(d any) map[string]any { return map[string]any{"repos": len(d.(*GitHubDataset).Repos)} }})
	Register(TibberData{})
	Register(testOf{TibberData{}, func(d any) map[string]any { return map[string]any{"price": d.(*TibberDataset).Current} }})
}
