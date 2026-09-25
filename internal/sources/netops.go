package sources

// Homelab network and download services:
//
//	pihole     Pi-hole v6 (v5 fallback)   queries, blocked share, blocking on/off
//	adguard    AdGuard Home                same shape as pihole
//	nextcloud  serverinfo                  users, free space, app updates
//	sabnzbd    queue + history             speed, failed downloads, disk space
//	gluetun    control server              VPN state, exit IP and country
//	domains    RDAP (rdap.org)             registration expiry
//	blacklist  DNSBL zones                 own IPs on spam blocklists

import (
	"context"
	"net"
	"net/url"
	"sort"
	"strings"
	"time"

	"golang.org/x/net/publicsuffix"

	"dashboard/internal/drivers/httpclient"
	"dashboard/internal/drivers/services"
	"dashboard/internal/enums"
)

const (
	domainsTTL     = 12 * time.Hour
	blacklistTTL   = 6 * time.Hour
	sabHistory     = "30"
	gigabyte       = 1 << 30
	rdapExpiration = "expiration"
	gluetunRunning = "running"
	pihole5Enabled = "enabled"
	secondsPerDay  = 24 * 60 * 60
	percentOf      = 100
	defaultDNSBL   = "zen.spamhaus.org,bl.spamcop.net,b.barracudacentral.org"
	mebibytePerSec = 1024
)

var (
	rdapBase = "https://rdap.org"
	ownIPURL = publicIPURL
)

// ── DNS filters: pihole, adguard ──

// DNSFilterDataset is the common view of Pi-hole and AdGuard Home.
type DNSFilterDataset struct {
	URL              string
	Queries, Blocked int
	Percent          float64 // blocked share, 0–100
	Enabled          bool
	ListsUpdated     time.Time // Pi-hole gravity; zero if unknown
	Clients          int
	TopClients       []DNSClient // busiest clients (Pi-hole v6, AdGuard)
}

// DNSClient is one device's queries of the last 24 hours.
type DNSClient struct {
	IP, Name         string
	Queries, Blocked int
}

// topClientCount is how many busy clients are read.
const topClientCount = "25"

type PiholeData struct{}

func (PiholeData) Key() string                { return "pihole.data" }
func (PiholeData) TTL() time.Duration         { return opsTTL }
func (PiholeData) Service() enums.ServiceType { return enums.ServicePihole }

func (PiholeData) Fetch(ctx context.Context, sctx Ctx) (any, error) {
	if isDemo(sctx) {
		return DemoPihole(time.Now()), nil
	}
	session, err := services.PiholeApi{URL: sctx.URL, Password: sctx.Secret, Verify: sctx.VerifyTLS}.Open(ctx)
	if err != nil {
		return nil, fetchError(err)
	}
	defer session.Close(context.WithoutCancel(ctx))

	if session.Legacy() {
		body, err := session.Summary(ctx)
		if err != nil {
			return nil, fetchError(err)
		}
		m := asMap(body)
		return &DNSFilterDataset{URL: sctx.URL, Queries: int(asFloat(m["dns_queries_today"])), Blocked: int(asFloat(m["ads_blocked_today"])),
			Percent: asFloat(m["ads_percentage_today"]), Enabled: asStr(m["status"]) == pihole5Enabled,
			ListsUpdated: time.Unix(asInt64(asMap(m["gravity_last_updated"])["absolute"]), 0).UTC(), Clients: int(asFloat(m["unique_clients"]))}, nil
	}

	summary, err := session.Get(ctx, "stats/summary")
	if err != nil {
		return nil, fetchError(err)
	}
	blocking, err := session.Get(ctx, "dns/blocking")
	if err != nil {
		return nil, fetchError(err)
	}
	q, g := asMap(asMap(summary)["queries"]), asMap(asMap(summary)["gravity"])
	data := &DNSFilterDataset{URL: sctx.URL, Queries: int(asFloat(q["total"])), Blocked: int(asFloat(q["blocked"])),
		Percent: asFloat(q["percent_blocked"]), Enabled: asStr(asMap(blocking)["blocking"]) == pihole5Enabled,
		ListsUpdated: time.Unix(asInt64(g["last_update"]), 0).UTC(), Clients: int(asFloat(asMap(asMap(summary)["clients"])["active"]))}
	data.TopClients = piholeClients(ctx, session)
	return data, nil
}

type AdGuardData struct{}

func (AdGuardData) Key() string                { return "adguard.data" }
func (AdGuardData) TTL() time.Duration         { return opsTTL }
func (AdGuardData) Service() enums.ServiceType { return enums.ServiceAdGuard }

func (AdGuardData) Fetch(ctx context.Context, sctx Ctx) (any, error) {
	if isDemo(sctx) {
		return DemoAdGuard(), nil
	}
	api := services.AdGuardApi{URL: sctx.URL, Secret: sctx.Secret, Verify: sctx.VerifyTLS}
	status, err := api.Get(ctx, "status")
	if err != nil {
		return nil, fetchError(err)
	}
	stats, err := api.Get(ctx, "stats")
	if err != nil {
		return nil, fetchError(err)
	}
	s := asMap(stats)
	data := &DNSFilterDataset{URL: sctx.URL, Queries: int(asFloat(s["num_dns_queries"])), Blocked: int(asFloat(s["num_blocked_filtering"])),
		Enabled: asBool(asMap(status)["protection_enabled"])}
	if data.Queries > 0 {
		data.Percent = float64(data.Blocked) / float64(data.Queries) * percentOf
	}
	// top_clients: [{"192.168.1.5": 1234}, …]
	for _, raw := range asList(s["top_clients"]) {
		for ip, n := range asMap(raw) {
			data.TopClients = append(data.TopClients, DNSClient{IP: ip, Queries: int(asFloat(n))})
		}
	}
	return data, nil
}

// piholeClients reads the busiest clients and their blocked counts;
// nil when the API does not offer them.
func piholeClients(ctx context.Context, session *services.PiholeSession) []DNSClient {
	top, err := session.Get(ctx, "stats/top_clients?count="+topClientCount)
	if err != nil {
		return nil
	}
	var out []DNSClient
	index := map[string]int{}
	for _, raw := range asList(asMap(top)["clients"]) {
		c := asMap(raw)
		index[asStr(c["ip"])] = len(out)
		out = append(out, DNSClient{IP: asStr(c["ip"]), Name: asStr(c["name"]), Queries: int(asFloat(c["count"]))})
	}
	if blocked, err := session.Get(ctx, "stats/top_clients?blocked=true&count="+topClientCount); err == nil {
		for _, raw := range asList(asMap(blocked)["clients"]) {
			c := asMap(raw)
			if i, ok := index[asStr(c["ip"])]; ok {
				out[i].Blocked = int(asFloat(c["count"]))
			}
		}
	}
	return out
}

// ── nextcloud ──

type NextcloudDataset struct {
	URL, Version    string
	FreeBytes       float64 // -1 when unknown
	Users, Active24 int
	Files           int
	AppUpdates      int
}

type NextcloudData struct{}

func (NextcloudData) Key() string                { return "nextcloud.data" }
func (NextcloudData) TTL() time.Duration         { return opsTTL }
func (NextcloudData) Service() enums.ServiceType { return enums.ServiceNextcloud }

func (NextcloudData) Fetch(ctx context.Context, sctx Ctx) (any, error) {
	if isDemo(sctx) {
		return DemoNextcloud(), nil
	}
	secret, err := needSecret(sctx)
	if err != nil {
		return nil, err
	}
	body, err := services.NextcloudApi{URL: sctx.URL, Secret: secret, Verify: sctx.VerifyTLS}.ServerInfo(ctx)
	if err != nil {
		return nil, fetchError(err)
	}
	m := asMap(body)
	system := asMap(asMap(m["nextcloud"])["system"])
	storage := asMap(asMap(m["nextcloud"])["storage"])
	free := -1.0
	if v, ok := system["freespace"]; ok {
		free = asFloat(v)
	}
	return &NextcloudDataset{URL: sctx.URL, Version: asStr(system["version"]), FreeBytes: free,
		Users: int(asFloat(storage["num_users"])), Files: int(asFloat(storage["num_files"])),
		Active24: int(asFloat(asMap(m["activeUsers"])["last24hours"])), AppUpdates: int(asFloat(asMap(system["apps"])["num_updates_available"]))}, nil
}

// ── sabnzbd ──

// SabFailure is one failed download.
type SabFailure struct {
	Name, Reason string
	At           time.Time
}

type SabnzbdDataset struct {
	URL      string
	Paused   bool
	Slots    int
	SpeedKB  float64
	FreeGB   float64 // download disk
	Failures []SabFailure
}

type SabnzbdData struct{}

func (SabnzbdData) Key() string                { return "sabnzbd.data" }
func (SabnzbdData) TTL() time.Duration         { return opsTTL }
func (SabnzbdData) Service() enums.ServiceType { return enums.ServiceSabnzbd }

func (SabnzbdData) Fetch(ctx context.Context, sctx Ctx) (any, error) {
	if isDemo(sctx) {
		return DemoSabnzbd(time.Now()), nil
	}
	secret, err := needSecret(sctx)
	if err != nil {
		return nil, err
	}
	api := services.SabnzbdApi{URL: sctx.URL, Key: secret, Verify: sctx.VerifyTLS}
	queue, err := api.Mode(ctx, "queue", nil)
	if err != nil {
		return nil, fetchError(err)
	}
	q := asMap(asMap(queue)["queue"])
	data := &SabnzbdDataset{URL: sctx.URL, Paused: asBool(q["paused"]), Slots: int(asFloat(q["noofslots"])),
		SpeedKB: asFloat(q["kbpersec"]), FreeGB: asFloat(q["diskspace1"])}

	history, err := api.Mode(ctx, "history", url.Values{"limit": {sabHistory}})
	if err != nil {
		return nil, fetchError(err)
	}
	for _, raw := range asList(asMap(asMap(history)["history"])["slots"]) {
		h := asMap(raw)
		if asStr(h["status"]) != "Failed" {
			continue
		}
		data.Failures = append(data.Failures, SabFailure{Name: asStr(h["name"]), Reason: asStr(h["fail_message"]),
			At: time.Unix(asInt64(h["completed"]), 0).UTC()})
	}
	return data, nil
}

// ── gluetun ──

type GluetunDataset struct {
	URL, Status     string
	ExitIP, Country string
	OwnIP           string // the dashboard's own public IP; equal to ExitIP = leak
	ExpectedCountry string
}

type GluetunData struct{}

func (GluetunData) Key() string                { return "gluetun.data" }
func (GluetunData) TTL() time.Duration         { return opsTTL }
func (GluetunData) Service() enums.ServiceType { return enums.ServiceGluetun }

// Fetch reads VPN state and exit IP; options.country names the expected
// exit country (e.g. "Sweden").
func (GluetunData) Fetch(ctx context.Context, sctx Ctx) (any, error) {
	if isDemo(sctx) {
		return DemoGluetun(), nil
	}
	api := services.GluetunApi{URL: sctx.URL, Key: sctx.Secret, Verify: sctx.VerifyTLS}
	data := &GluetunDataset{URL: sctx.URL, ExpectedCountry: asStr(sctx.Options["country"])}

	// Gluetun ≥ 3.40 reports /vpn/status, older versions /openvpn/status.
	status, err := api.Get(ctx, "vpn/status")
	if err != nil {
		status, err = api.Get(ctx, "openvpn/status")
	}
	if err != nil {
		return nil, fetchError(err)
	}
	data.Status = asStr(asMap(status)["status"])

	ip, err := api.Get(ctx, "publicip/ip")
	if err != nil {
		return nil, fetchError(err)
	}
	data.ExitIP, data.Country = asStr(asMap(ip)["public_ip"]), asStr(asMap(ip)["country"])

	// Our own address, to tell a tunnel from a leak.
	if own, _, err := httpclient.GetJSON(ctx, ownIPURL, httpclient.Options{Params: url.Values{"format": {"json"}}}); err == nil {
		data.OwnIP = asStr(asMap(own)["ip"])
	}
	return data, nil
}

// ── domains ──

// DomainInfo is one registered domain; Expires is zero when the registry
// publishes no date (DENIC for .de).
type DomainInfo struct {
	Name    string
	Expires time.Time
	Error   string
}

type DomainsDataset struct{ Domains []DomainInfo }

type DomainsData struct{}

func (DomainsData) Key() string                { return "domains.data" }
func (DomainsData) TTL() time.Duration         { return domainsTTL }
func (DomainsData) Service() enums.ServiceType { return enums.ServiceDomains }

// Fetch checks the connection URL's domain plus options.domains.
func (DomainsData) Fetch(ctx context.Context, sctx Ctx) (any, error) {
	if isDemo(sctx) {
		return DemoDomains(time.Now()), nil
	}
	data := &DomainsDataset{}
	for _, name := range registeredDomains(sctx) {
		info := DomainInfo{Name: name}
		body, _, err := httpclient.GetJSON(ctx, rdapBase+"/domain/"+url.PathEscape(name), httpclient.Options{})
		if err != nil {
			info.Error = err.Error()
			data.Domains = append(data.Domains, info)
			continue
		}
		for _, raw := range asList(asMap(body)["events"]) {
			ev := asMap(raw)
			if asStr(ev["eventAction"]) == rdapExpiration {
				info.Expires, _ = time.Parse(time.RFC3339, asStr(ev["eventDate"]))
			}
		}
		data.Domains = append(data.Domains, info)
	}
	return data, nil
}

// registeredDomains reduces hosts to registered domains, deduplicated:
// "cloud.example.co.uk" → "example.co.uk".
func registeredDomains(sctx Ctx) []string {
	raw := []string{}
	if u, err := url.Parse(sctx.URL); err == nil && u.Hostname() != "" {
		raw = append(raw, u.Hostname())
	}
	for _, d := range asList(sctx.Options["domains"]) {
		raw = append(raw, asStr(d))
	}
	seen := map[string]bool{}
	var out []string
	for _, host := range raw {
		name, err := publicsuffix.EffectiveTLDPlusOne(strings.ToLower(strings.TrimSpace(host)))
		if err != nil || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// ── blacklist ──

// Listing is one address on one blocklist.
type Listing struct {
	IP, Zone, Code string
}

type BlacklistDataset struct {
	Checked  []string // IPs
	Listings []Listing
	Refused  []string // zones that refused our resolver
}

type BlacklistData struct{}

func (BlacklistData) Key() string                { return "blacklist.data" }
func (BlacklistData) TTL() time.Duration         { return blacklistTTL }
func (BlacklistData) Service() enums.ServiceType { return enums.ServiceBlacklist }

// Fetch checks the IPv4 addresses of the connection URL's host plus
// options.ips against options.zones (default: Spamhaus ZEN, SpamCop,
// Barracuda).
func (BlacklistData) Fetch(ctx context.Context, sctx Ctx) (any, error) {
	if isDemo(sctx) {
		return DemoBlacklist(), nil
	}
	var ips []net.IP
	if u, err := url.Parse(sctx.URL); err == nil && u.Hostname() != "" {
		found, err := services.ResolveIPv4(ctx, u.Hostname())
		if err != nil {
			return nil, fetchError(err)
		}
		ips = append(ips, found...)
	}
	for _, raw := range asList(sctx.Options["ips"]) {
		if ip := net.ParseIP(strings.TrimSpace(asStr(raw))); ip != nil {
			ips = append(ips, ip)
		}
	}
	zones := strings.Split(defaultDNSBL, ",")
	if custom := asList(sctx.Options["zones"]); len(custom) > 0 {
		zones = zones[:0]
		for _, z := range custom {
			zones = append(zones, asStr(z))
		}
	}

	data := &BlacklistDataset{}
	refused := map[string]bool{}
	for _, ip := range ips {
		data.Checked = append(data.Checked, ip.String())
		for _, zone := range zones {
			listed, code, err := services.DNSBL(ctx, ip, zone)
			if err == services.ErrDNSBLRefused {
				refused[zone] = true
				continue
			}
			if err != nil || !listed {
				continue
			}
			data.Listings = append(data.Listings, Listing{IP: ip.String(), Zone: zone, Code: code})
		}
	}
	for z := range refused {
		data.Refused = append(data.Refused, z)
	}
	sort.Strings(data.Refused)
	return data, nil
}

func init() {
	Register(PiholeData{})
	Register(testOf{PiholeData{}, func(d any) map[string]any { return map[string]any{"queries": d.(*DNSFilterDataset).Queries} }})
	Register(AdGuardData{})
	Register(testOf{AdGuardData{}, func(d any) map[string]any { return map[string]any{"queries": d.(*DNSFilterDataset).Queries} }})
	Register(NextcloudData{})
	Register(testOf{NextcloudData{}, func(d any) map[string]any { return map[string]any{"version": d.(*NextcloudDataset).Version} }})
	Register(SabnzbdData{})
	Register(testOf{SabnzbdData{}, func(d any) map[string]any { return map[string]any{"queue": d.(*SabnzbdDataset).Slots} }})
	Register(GluetunData{})
	Register(testOf{GluetunData{}, func(d any) map[string]any { return map[string]any{"status": d.(*GluetunDataset).Status} }})
	Register(DomainsData{})
	Register(testOf{DomainsData{}, func(d any) map[string]any { return map[string]any{"domains": len(d.(*DomainsDataset).Domains)} }})
	Register(BlacklistData{})
	Register(testOf{BlacklistData{}, func(d any) map[string]any { return map[string]any{"checked": len(d.(*BlacklistDataset).Checked)} }})
}
