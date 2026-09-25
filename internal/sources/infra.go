package sources

// TrueNAS (storage), Komodo (containers), Pangolin (tunnels) and
// authentik (identity: usage statistics).

import (
	"context"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"dashboard/internal/drivers/services"
	"dashboard/internal/enums"
)

const (
	pangolinPage   = "100"
	authentikPage  = 100
	authentikPages = 5
	authentikDays  = "7"
)

// ── TrueNAS ──

type Pool struct {
	Name, Status    string
	Healthy         bool
	Size, Allocated float64
}

type TNAlert struct {
	ID, Level, Text string
}

type TNApp struct {
	Name, State string
	Update      bool
}

type TrueNASDataset struct {
	URL, Host, Version string
	Pools              []Pool
	Alerts             []TNAlert // not dismissed
	Apps               []TNApp
}

type TrueNASData struct{}

func (TrueNASData) Key() string                { return "truenas.data" }
func (TrueNASData) TTL() time.Duration         { return opsTTL }
func (TrueNASData) Service() enums.ServiceType { return enums.ServiceTrueNAS }

func (TrueNASData) Fetch(ctx context.Context, sctx Ctx) (any, error) {
	if isDemo(sctx) {
		return DemoTrueNAS(), nil
	}
	secret, err := needSecret(sctx)
	if err != nil {
		return nil, err
	}
	session, err := services.TrueNASApi{URL: sctx.URL, Key: secret, Verify: sctx.VerifyTLS}.Open(ctx)
	if err != nil {
		return nil, fetchError(err)
	}
	defer session.Close()

	data := &TrueNASDataset{URL: sctx.URL}
	info, err := session.Call(ctx, "system.info")
	if err != nil {
		return nil, fetchError(err)
	}
	data.Host, data.Version = asStr(asMap(info)["hostname"]), asStr(asMap(info)["version"])
	pools, err := session.Call(ctx, "pool.query")
	if err != nil {
		return nil, fetchError(err)
	}
	for _, raw := range asList(pools) {
		p := asMap(raw)
		data.Pools = append(data.Pools, Pool{Name: asStr(p["name"]), Status: asStr(p["status"]), Healthy: asBool(p["healthy"]),
			Size: asFloat(p["size"]), Allocated: asFloat(p["allocated"])})
	}
	if alerts, err := session.Call(ctx, "alert.list"); err == nil {
		for _, raw := range asList(alerts) {
			a := asMap(raw)
			if asBool(a["dismissed"]) {
				continue
			}
			data.Alerts = append(data.Alerts, TNAlert{ID: asStr(a["uuid"]), Level: asStr(a["level"]), Text: strings.TrimSpace(asStr(a["formatted"]))})
		}
	}
	// Apps exist on SCALE only.
	if apps, err := session.Call(ctx, "app.query"); err == nil {
		for _, raw := range asList(apps) {
			a := asMap(raw)
			data.Apps = append(data.Apps, TNApp{Name: asStr(a["name"]), State: asStr(a["state"]), Update: asBool(a["upgrade_available"])})
		}
	}
	return data, nil
}

// ── Komodo ──

type KStack struct {
	Name, State string
	Updates     []string // services with a newer image
}

type KAlert struct {
	Level, Kind, Name string
	At                time.Time
}

type KomodoDataset struct {
	URL                                          string
	ServersTotal, ServersHealthy, ServersProblem int
	Stacks                                       []KStack
	Alerts                                       []KAlert // open
}

type KomodoData struct{}

func (KomodoData) Key() string                { return "komodo.data" }
func (KomodoData) TTL() time.Duration         { return opsTTL }
func (KomodoData) Service() enums.ServiceType { return enums.ServiceKomodo }

func (KomodoData) Fetch(ctx context.Context, sctx Ctx) (any, error) {
	if isDemo(sctx) {
		return DemoKomodo(time.Now()), nil
	}
	secret, err := needSecret(sctx)
	if err != nil {
		return nil, err
	}
	api := services.KomodoApi{URL: sctx.URL, Secret: secret, Verify: sctx.VerifyTLS}
	servers, err := api.Read(ctx, "GetServersSummary", nil)
	if err != nil {
		return nil, fetchError(err)
	}
	s := asMap(servers)
	data := &KomodoDataset{URL: sctx.URL, ServersTotal: int(asFloat(s["total"])), ServersHealthy: int(asFloat(s["healthy"])),
		ServersProblem: int(asFloat(s["warning"]) + asFloat(s["unhealthy"]))}

	stacks, err := api.Read(ctx, "ListStacks", nil)
	if err != nil {
		return nil, fetchError(err)
	}
	for _, raw := range asList(stacks) {
		st := asMap(raw)
		info := asMap(st["info"])
		stack := KStack{Name: asStr(st["name"]), State: strings.ToLower(asStr(info["state"]))}
		for _, svc := range asList(info["services"]) {
			if sv := asMap(svc); asBool(sv["update_available"]) {
				stack.Updates = append(stack.Updates, asStr(sv["service"]))
			}
		}
		data.Stacks = append(data.Stacks, stack)
	}

	alerts, err := api.Read(ctx, "ListAlerts", map[string]any{"query": map[string]any{"resolved": false}})
	if err == nil {
		for _, raw := range asList(asMap(alerts)["alerts"]) {
			a := asMap(raw)
			body := asMap(a["data"])
			data.Alerts = append(data.Alerts, KAlert{Level: strings.ToUpper(asStr(a["level"])), Kind: asStr(body["type"]),
				Name: asStr(asMap(body["data"])["name"]), At: time.UnixMilli(asInt64(a["ts"])).UTC()})
		}
	}
	return data, nil
}

// ── Pangolin ──

type PSite struct {
	Name, Type  string
	Online      *bool // nil for local sites
	MBIn, MBOut float64
	Update      bool
}

type PResource struct {
	Name, Domain, Health string
	Enabled              bool
}

type PangolinDataset struct {
	URL, Org  string
	Sites     []PSite
	Resources []PResource
}

type PangolinData struct{}

func (PangolinData) Key() string                { return "pangolin.data" }
func (PangolinData) TTL() time.Duration         { return opsTTL }
func (PangolinData) Service() enums.ServiceType { return enums.ServicePangolin }

// Fetch needs options.org, the organisation id.
func (PangolinData) Fetch(ctx context.Context, sctx Ctx) (any, error) {
	if isDemo(sctx) {
		return DemoPangolin(), nil
	}
	secret, err := needSecret(sctx)
	if err != nil {
		return nil, err
	}
	org := asStr(sctx.Options["org"])
	if org == "" {
		return nil, newSourceError("options: org missing")
	}
	api := services.PangolinApi{URL: sctx.URL, Key: secret, Verify: sctx.VerifyTLS}
	base := "org/" + url.PathEscape(org) + "/"
	page := url.Values{"pageSize": {pangolinPage}}

	sites, err := api.Get(ctx, base+"sites", page)
	if err != nil {
		return nil, fetchError(err)
	}
	data := &PangolinDataset{URL: sctx.URL, Org: org}
	for _, raw := range asList(asMap(sites)["sites"]) {
		s := asMap(raw)
		site := PSite{Name: asStr(s["name"]), Type: asStr(s["type"]), MBIn: asFloat(s["megabytesIn"]), MBOut: asFloat(s["megabytesOut"]),
			Update: asBool(s["newtUpdateAvailable"])}
		if online, ok := s["online"].(bool); ok {
			site.Online = &online
		}
		data.Sites = append(data.Sites, site)
	}
	resources, err := api.Get(ctx, base+"resources", page)
	if err != nil {
		return nil, fetchError(err)
	}
	for _, raw := range asList(asMap(resources)["resources"]) {
		r := asMap(raw)
		health := asStr(r["healthStatus"])
		if health == "" {
			health = asStr(r["health"])
		}
		data.Resources = append(data.Resources, PResource{Name: asStr(r["name"]), Domain: asStr(r["fullDomain"]),
			Enabled: asBool(r["enabled"]), Health: health})
	}
	return data, nil
}

// ── authentik ──

// AKApp is one application's authorizations in the last 7 days.
type AKApp struct {
	Name          string
	Events, Users int
}

type AKUser struct {
	Name      string
	LastLogin time.Time // zero: never
}

type AuthentikDataset struct {
	URL, Version, Latest string
	Outdated, Outposts   bool // update for server / outposts
	Logins7d, Failed7d   int
	Failed24h            int
	Apps                 []AKApp
	Users                []AKUser // active human accounts
}

type AuthentikData struct{}

func (AuthentikData) Key() string                { return "authentik.data" }
func (AuthentikData) TTL() time.Duration         { return opsTTL }
func (AuthentikData) Service() enums.ServiceType { return enums.ServiceAuthentik }

func (AuthentikData) Fetch(ctx context.Context, sctx Ctx) (any, error) {
	if isDemo(sctx) {
		return DemoAuthentik(time.Now()), nil
	}
	secret, err := needSecret(sctx)
	if err != nil {
		return nil, err
	}
	api := services.AuthentikApi{URL: sctx.URL, Token: secret, Verify: sctx.VerifyTLS}
	data, err := loadAuthentik(ctx, api, sctx.URL, time.Now().UTC())
	if err != nil {
		return nil, fetchError(err)
	}
	return data, nil
}

func loadAuthentik(ctx context.Context, api services.AuthentikApi, base string, now time.Time) (*AuthentikDataset, error) {
	version, err := api.Get(ctx, "admin/version/", nil)
	if err != nil {
		return nil, err
	}
	v := asMap(version)
	data := &AuthentikDataset{URL: base, Version: asStr(v["version_current"]), Latest: asStr(v["version_latest"]),
		Outdated: asBool(v["outdated"]), Outposts: asBool(v["outpost_outdated"])}

	// Event volume in 6-hour buckets over 7 days.
	volume, err := api.Get(ctx, "events/events/volume/", url.Values{"history_days": {authentikDays}})
	if err != nil {
		return nil, err
	}
	for _, raw := range asList(volume) {
		b := asMap(raw)
		n := int(asFloat(b["count"]))
		at, _ := time.Parse(time.RFC3339, asStr(b["time"]))
		switch asStr(b["action"]) {
		case "login":
			data.Logins7d += n
		case "login_failed":
			data.Failed7d += n
			if now.Sub(at) <= 24*time.Hour {
				data.Failed24h += n
			}
		}
	}

	if top, err := api.Get(ctx, "events/events/top_per_user/", url.Values{"action": {"authorize_application"}, "top_n": {"10"}}); err == nil {
		for _, raw := range asList(top) {
			t := asMap(raw)
			data.Apps = append(data.Apps, AKApp{Name: asStr(asMap(t["application"])["name"]),
				Events: int(asFloat(t["counted_events"])), Users: int(asFloat(t["unique_users"]))})
		}
	}

	for page := 1; page <= authentikPages; page++ {
		users, err := api.Get(ctx, "core/users/", url.Values{"is_active": {"true"}, "page_size": {strconv.Itoa(authentikPage)}, "page": {strconv.Itoa(page)}})
		if err != nil {
			return nil, err
		}
		body := asMap(users)
		for _, raw := range asList(body["results"]) {
			u := asMap(raw)
			if strings.Contains(asStr(u["type"]), "service_account") {
				continue
			}
			name := asStr(u["username"])
			last, _ := time.Parse(time.RFC3339, asStr(u["last_login"]))
			data.Users = append(data.Users, AKUser{Name: name, LastLogin: last.UTC()})
		}
		if asStr(asMap(body["pagination"])["next"]) == "" && asFloat(asMap(body["pagination"])["next"]) == 0 {
			break
		}
	}
	sort.Slice(data.Apps, func(i, j int) bool { return data.Apps[i].Events > data.Apps[j].Events })
	return data, nil
}

func init() {
	Register(TrueNASData{})
	Register(testOf{TrueNASData{}, func(d any) map[string]any {
		t := d.(*TrueNASDataset)
		return map[string]any{"version": t.Version, "pools": len(t.Pools)}
	}})
	Register(KomodoData{})
	Register(testOf{KomodoData{}, func(d any) map[string]any { return map[string]any{"stacks": len(d.(*KomodoDataset).Stacks)} }})
	Register(PangolinData{})
	Register(testOf{PangolinData{}, func(d any) map[string]any { return map[string]any{"sites": len(d.(*PangolinDataset).Sites)} }})
	Register(AuthentikData{})
	Register(testOf{AuthentikData{}, func(d any) map[string]any { return map[string]any{"version": d.(*AuthentikDataset).Version} }})
}
