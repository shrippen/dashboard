package sources

// Operations sources: Uptime Kuma, Proxmox VE, Paperless-ngx and TLS
// certificate expiry. Each has "<service>.data" (one cached dataset for
// the analysis) and "<service>.test" (connection check).

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"dashboard/internal/drivers/services"
	"dashboard/internal/enums"
)

const (
	opsTTL      = 5 * time.Minute
	certTTL     = 6 * time.Hour
	httpsPort   = "443"
	vzdumpLimit = 200
	certWorkers = 4
)

// fetchError turns a driver error into a message for the user.
func fetchError(err error) error {
	var apiErr services.ApiError
	if isApiError(err, &apiErr) {
		return newSourceError("%s", apiErr.Error())
	}
	return err
}

// ── Uptime Kuma ──

// Kuma monitor states as exported in monitor_status.
const (
	KumaDown        = 0
	KumaUp          = 1
	KumaPending     = 2
	KumaMaintenance = 3
)

// KumaMonitor is one monitor; CertDays is -1 when it has no certificate.
type KumaMonitor struct {
	Name     string
	Type     string
	Target   string
	Status   int
	CertDays int
}

// KumaDataset is every monitor of one Uptime Kuma instance.
type KumaDataset struct {
	URL      string
	Monitors []KumaMonitor
}

// metricLine matches `name{labels} value`, e.g.
// monitor_status{monitor_name="NAS",monitor_type="http"} 1
var metricLine = regexp.MustCompile(`^(\w+)\{(.*)\}\s+(\S+)$`)
var metricLabel = regexp.MustCompile(`(\w+)="((?:[^"\\]|\\.)*)"`)

// parseKuma reads monitor_status and monitor_cert_days_remaining.
func parseKuma(text string) []KumaMonitor {
	byName := map[string]*KumaMonitor{}
	var order []string
	for _, line := range strings.Split(text, "\n") {
		m := metricLine.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil || (m[1] != "monitor_status" && m[1] != "monitor_cert_days_remaining") {
			continue
		}
		labels := map[string]string{}
		for _, l := range metricLabel.FindAllStringSubmatch(m[2], -1) {
			labels[l[1]] = l[2]
		}
		name := labels["monitor_name"]
		mon, ok := byName[name]
		if !ok {
			mon = &KumaMonitor{Name: name, Type: labels["monitor_type"], Target: labels["monitor_url"], CertDays: -1}
			byName[name] = mon
			order = append(order, name)
		}
		value, _ := strconv.ParseFloat(m[3], 64)
		if m[1] == "monitor_status" {
			mon.Status = int(value)
		} else {
			mon.CertDays = int(value)
		}
	}
	out := make([]KumaMonitor, 0, len(order))
	for _, name := range order {
		out = append(out, *byName[name])
	}
	return out
}

func kumaAPI(sctx Ctx) (services.KumaApi, error) {
	secret, err := needSecret(sctx)
	if err != nil {
		return services.KumaApi{}, err
	}
	return services.KumaApi{URL: sctx.URL, Key: secret, Verify: sctx.VerifyTLS}, nil
}

type KumaData struct{}

func (KumaData) Key() string                { return "uptimekuma.data" }
func (KumaData) TTL() time.Duration         { return time.Minute }
func (KumaData) Service() enums.ServiceType { return enums.ServiceUptimeKuma }

func (KumaData) Fetch(ctx context.Context, sctx Ctx) (any, error) {
	if isDemo(sctx) {
		return DemoKuma(), nil
	}
	api, err := kumaAPI(sctx)
	if err != nil {
		return nil, err
	}
	text, err := api.Metrics(ctx)
	if err != nil {
		return nil, fetchError(err)
	}
	return &KumaDataset{URL: sctx.URL, Monitors: parseKuma(text)}, nil
}

type KumaTest struct{}

func (KumaTest) Key() string                { return "uptimekuma.test" }
func (KumaTest) TTL() time.Duration         { return testTTL }
func (KumaTest) Service() enums.ServiceType { return enums.ServiceUptimeKuma }

func (KumaTest) Fetch(ctx context.Context, sctx Ctx) (any, error) {
	data, err := KumaData{}.Fetch(ctx, sctx)
	if err != nil {
		return nil, err
	}
	return map[string]any{"version": nil, "monitors": len(data.(*KumaDataset).Monitors)}, nil
}

// ── Proxmox VE ──

type ProxmoxStorage struct {
	Name        string
	Used, Total float64
}

type ProxmoxNode struct {
	Name     string
	Online   bool
	Updates  int // -1: token may not read apt
	Storages []ProxmoxStorage
}

type ProxmoxGuest struct {
	VMID     int64
	Name     string
	Node     string
	Template bool
}

// ProxmoxDataset: Backups is the newest successful vzdump per VMID.
type ProxmoxDataset struct {
	URL     string
	Nodes   []ProxmoxNode
	Guests  []ProxmoxGuest
	Backups map[int64]time.Time
}

func proxmoxAPI(sctx Ctx) (services.ProxmoxApi, error) {
	secret, err := needSecret(sctx)
	if err != nil {
		return services.ProxmoxApi{}, err
	}
	return services.ProxmoxApi{URL: sctx.URL, Token: secret, Verify: sctx.VerifyTLS}, nil
}

type ProxmoxData struct{}

func (ProxmoxData) Key() string                { return "proxmox.data" }
func (ProxmoxData) TTL() time.Duration         { return opsTTL }
func (ProxmoxData) Service() enums.ServiceType { return enums.ServiceProxmox }

func (ProxmoxData) Fetch(ctx context.Context, sctx Ctx) (any, error) {
	if isDemo(sctx) {
		return DemoProxmox(time.Now()), nil
	}
	api, err := proxmoxAPI(sctx)
	if err != nil {
		return nil, err
	}
	data, err := loadProxmox(ctx, api, sctx)
	if err != nil {
		return nil, fetchError(err)
	}
	return data, nil
}

func loadProxmox(ctx context.Context, api services.ProxmoxApi, sctx Ctx) (*ProxmoxDataset, error) {
	nodesRaw, err := api.Get(ctx, "nodes", nil)
	if err != nil {
		return nil, err
	}
	data := &ProxmoxDataset{URL: sctx.URL, Backups: map[int64]time.Time{}}
	for _, raw := range asList(nodesRaw) {
		n := asMap(raw)
		node := ProxmoxNode{Name: asStr(n["node"]), Online: asStr(n["status"]) == "online", Updates: -1}
		if !node.Online {
			data.Nodes = append(data.Nodes, node)
			continue
		}
		if err := loadProxmoxNode(ctx, api, &node, data); err != nil {
			return nil, err
		}
		data.Nodes = append(data.Nodes, node)
	}
	return data, nil
}

// loadProxmoxNode reads one online node: storage, updates, guests, backups.
func loadProxmoxNode(ctx context.Context, api services.ProxmoxApi, node *ProxmoxNode, data *ProxmoxDataset) error {
	base := "nodes/" + url.PathEscape(node.Name) + "/"

	storages, err := api.Get(ctx, base+"storage", url.Values{"enabled": {"1"}})
	if err != nil {
		return err
	}
	for _, raw := range asList(storages) {
		s := asMap(raw)
		if asFloat(s["total"]) > 0 {
			node.Storages = append(node.Storages, ProxmoxStorage{Name: asStr(s["storage"]), Used: asFloat(s["used"]), Total: asFloat(s["total"])})
		}
	}

	// Reading apt needs Sys.Audit; without it the count stays unknown.
	if updates, err := api.Get(ctx, base+"apt/update", nil); err == nil {
		node.Updates = len(asList(updates))
	}

	for _, kind := range []string{"qemu", "lxc"} {
		guests, err := api.Get(ctx, base+kind, nil)
		if err != nil {
			return err
		}
		for _, raw := range asList(guests) {
			g := asMap(raw)
			data.Guests = append(data.Guests, ProxmoxGuest{VMID: asInt64(g["vmid"]), Name: asStr(g["name"]),
				Node: node.Name, Template: asFloat(g["template"]) == 1})
		}
	}

	tasks, err := api.Get(ctx, base+"tasks", url.Values{"typefilter": {"vzdump"}, "limit": {strconv.Itoa(vzdumpLimit)}})
	if err != nil {
		return err
	}
	for _, raw := range asList(tasks) {
		t := asMap(raw)
		vmid := asInt64(t["id"])
		if asStr(t["status"]) != "OK" || vmid == 0 {
			continue
		}
		end := time.Unix(asInt64(t["endtime"]), 0).UTC()
		if end.After(data.Backups[vmid]) {
			data.Backups[vmid] = end
		}
	}
	return nil
}

type ProxmoxTest struct{}

func (ProxmoxTest) Key() string                { return "proxmox.test" }
func (ProxmoxTest) TTL() time.Duration         { return testTTL }
func (ProxmoxTest) Service() enums.ServiceType { return enums.ServiceProxmox }

func (ProxmoxTest) Fetch(ctx context.Context, sctx Ctx) (any, error) {
	if isDemo(sctx) {
		return map[string]any{"version": "demo"}, nil
	}
	api, err := proxmoxAPI(sctx)
	if err != nil {
		return nil, err
	}
	version, err := api.Get(ctx, "version", nil)
	if err != nil {
		return nil, fetchError(err)
	}
	return map[string]any{"version": asStr(asMap(version)["version"])}, nil
}

// ── Paperless-ngx ──

// PaperlessDataset is the inbox: documents carrying an inbox tag.
type PaperlessDataset struct {
	URL         string
	Inbox       int
	OldestTitle string
	OldestAdded string // "2026-09-01"
}

func paperlessAPI(sctx Ctx) (services.PaperlessApi, error) {
	secret, err := needSecret(sctx)
	if err != nil {
		return services.PaperlessApi{}, err
	}
	return services.PaperlessApi{URL: sctx.URL, Token: secret, Verify: sctx.VerifyTLS}, nil
}

type PaperlessData struct{}

func (PaperlessData) Key() string                { return "paperless.data" }
func (PaperlessData) TTL() time.Duration         { return opsTTL }
func (PaperlessData) Service() enums.ServiceType { return enums.ServicePaperless }

func (PaperlessData) Fetch(ctx context.Context, sctx Ctx) (any, error) {
	if isDemo(sctx) {
		return DemoPaperless(time.Now()), nil
	}
	api, err := paperlessAPI(sctx)
	if err != nil {
		return nil, err
	}
	data, err := loadPaperless(ctx, api, sctx)
	if err != nil {
		return nil, fetchError(err)
	}
	return data, nil
}

func loadPaperless(ctx context.Context, api services.PaperlessApi, sctx Ctx) (*PaperlessDataset, error) {
	tags, err := api.Get(ctx, "tags/", url.Values{"is_inbox_tag": {"true"}, "page_size": {"100"}})
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, raw := range asList(asMap(tags)["results"]) {
		ids = append(ids, strconv.FormatInt(asInt64(asMap(raw)["id"]), 10))
	}
	data := &PaperlessDataset{URL: sctx.URL}
	if len(ids) == 0 {
		return data, nil
	}

	// Oldest first, one row: count comes with it.
	docs, err := api.Get(ctx, "documents/", url.Values{
		"tags__id__in": {strings.Join(ids, ",")}, "ordering": {"added"}, "page_size": {"1"},
	})
	if err != nil {
		return nil, err
	}
	body := asMap(docs)
	data.Inbox = int(asFloat(body["count"]))
	if first := asList(body["results"]); len(first) > 0 {
		doc := asMap(first[0])
		data.OldestTitle = asStr(doc["title"])
		data.OldestAdded = day(doc["added"])
	}
	return data, nil
}

type PaperlessTest struct{}

func (PaperlessTest) Key() string                { return "paperless.test" }
func (PaperlessTest) TTL() time.Duration         { return testTTL }
func (PaperlessTest) Service() enums.ServiceType { return enums.ServicePaperless }

func (PaperlessTest) Fetch(ctx context.Context, sctx Ctx) (any, error) {
	if isDemo(sctx) {
		return map[string]any{"version": "demo"}, nil
	}
	api, err := paperlessAPI(sctx)
	if err != nil {
		return nil, err
	}
	if _, err := api.Get(ctx, "tags/", url.Values{"page_size": {"1"}}); err != nil {
		return nil, fetchError(err)
	}
	return map[string]any{"version": nil}, nil
}

// ── TLS certificates ──

// Cert is one checked endpoint; Error is set when it could not be read.
type Cert struct {
	Host     string
	NotAfter time.Time
	Issuer   string
	Error    string
}

type CertDataset struct {
	Certs []Cert
}

// certHosts lists the endpoints: the connection URL's host plus
// options.hosts, each "host" or "host:port".
func certHosts(sctx Ctx) []string {
	var raw []string
	if u, err := url.Parse(sctx.URL); err == nil && u.Host != "" {
		raw = append(raw, u.Host)
	}
	for _, h := range asList(sctx.Options["hosts"]) {
		raw = append(raw, strings.TrimSpace(asStr(h)))
	}

	seen := map[string]bool{}
	var out []string
	for _, h := range raw {
		if h == "" {
			continue
		}
		if _, _, err := net.SplitHostPort(h); err != nil {
			h = net.JoinHostPort(h, httpsPort)
		}
		if !seen[h] {
			seen[h] = true
			out = append(out, h)
		}
	}
	return out
}

type CertData struct{}

func (CertData) Key() string                { return "certs.data" }
func (CertData) TTL() time.Duration         { return certTTL }
func (CertData) Service() enums.ServiceType { return enums.ServiceCerts }

func (CertData) Fetch(ctx context.Context, sctx Ctx) (any, error) {
	if isDemo(sctx) {
		return DemoCerts(time.Now()), nil
	}
	hosts := certHosts(sctx)
	certs := make([]Cert, len(hosts))

	// A few handshakes in parallel; each has its own connect timeout.
	var wg sync.WaitGroup
	sem := make(chan struct{}, certWorkers)
	for i, host := range hosts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			info, err := services.PeerCert(ctx, host)
			certs[i] = Cert{Host: host, NotAfter: info.NotAfter, Issuer: info.Issuer}
			if err != nil {
				certs[i].Error = err.Error()
			}
		}()
	}
	wg.Wait()

	sort.Slice(certs, func(i, j int) bool { return certs[i].Host < certs[j].Host })
	return &CertDataset{Certs: certs}, nil
}

type CertTest struct{}

func (CertTest) Key() string                { return "certs.test" }
func (CertTest) TTL() time.Duration         { return testTTL }
func (CertTest) Service() enums.ServiceType { return enums.ServiceCerts }

func (CertTest) Fetch(ctx context.Context, sctx Ctx) (any, error) {
	data, _ := CertData{}.Fetch(ctx, sctx)
	for _, c := range data.(*CertDataset).Certs {
		if c.Error != "" {
			return nil, newSourceError("%s: %s", c.Host, c.Error)
		}
	}
	return map[string]any{"version": nil, "hosts": fmt.Sprint(len(data.(*CertDataset).Certs))}, nil
}

func init() {
	Register(KumaData{})
	Register(KumaTest{})
	Register(ProxmoxData{})
	Register(ProxmoxTest{})
	Register(PaperlessData{})
	Register(PaperlessTest{})
	Register(CertData{})
	Register(CertTest{})
}
