package rules

// Outages: several signals on one host become one hint.
//
//	connection nas.lan failed ─┐
//	kuma monitor "NAS" down    ├─ host nas.lan ─► system.outage (critical)
//	kuma monitor "SMB" down   ─┘                  single hints suppressed

import (
	"net/url"
	"sort"
	"strings"

	"andon/internal/enums"
	"andon/internal/sources"
)

// FailedDataset is the Env.Datasets key of connections that failed to load.
const FailedDataset = "failed"

// Failed is one connection whose fetch failed.
type Failed struct {
	Service, Name, Host string
}

const (
	outageRule    = "system.outage"
	minOutageHits = 2
	kumaDownRule  = "kuma.monitor_down"
)

// HostOf returns the lower-case host of a URL or bare host name.
func HostOf(raw string) string {
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "://") {
		raw = "//" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

// Outages maps each host with at least two failure signals to the names
// of what failed there.
func Outages(env Env) map[string][]string {
	signals := map[string][]string{}
	if failed, ok := env.Datasets[FailedDataset].([]Failed); ok {
		for _, f := range failed {
			if f.Host != "" {
				signals[f.Host] = append(signals[f.Host], f.Name)
			}
		}
	}
	if kuma, ok := env.Datasets[string(enums.ServiceUptimeKuma)].(*sources.KumaDataset); ok {
		for _, m := range kuma.Monitors {
			if host := HostOf(m.Target); m.Status == sources.KumaDown && host != "" {
				signals[host] = append(signals[host], m.Name)
			}
		}
	}
	out := map[string][]string{}
	for host, names := range signals {
		if len(names) >= minOutageHits {
			out[host] = names
		}
	}
	return out
}

// Suppressed reports whether a finding is covered by an outage hint.
func Suppressed(f Finding, env Env, outages map[string][]string) bool {
	if f.Rule != kumaDownRule || len(outages) == 0 {
		return false
	}
	kuma, ok := env.Datasets[string(enums.ServiceUptimeKuma)].(*sources.KumaDataset)
	if !ok {
		return false
	}
	for _, m := range kuma.Monitors {
		if f.Params["monitor"] == m.Name {
			_, down := outages[HostOf(m.Target)]
			return down
		}
	}
	return false
}

func init() {
	Register(outageRule, Cross, nil, func(_ any, cfg map[string]any, env Env) []Finding {
		outages := Outages(env)
		hosts := make([]string, 0, len(outages))
		for h := range outages {
			hosts = append(hosts, h)
		}
		sort.Strings(hosts)

		var found []Finding
		for _, host := range hosts {
			found = append(found, Finding{Fingerprint: "outage:" + host, Rule: outageRule, Severity: enums.SeverityCritical,
				Message: "system.outage", Params: map[string]any{"host": host, "count": len(outages[host]), "names": shortList(outages[host])},
				Sources: []string{"system"}})
		}
		return found
	})
}
