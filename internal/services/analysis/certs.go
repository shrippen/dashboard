package analysis

// Automatic certificate targets: a certs connection with options.auto
// also checks every https link tile and every Pangolin resource.
//
//	options {auto: true, hosts: [mail.example.org:993]}
//	  + links  https://cloud.example.org/…  → cloud.example.org
//	  + pangolin resource vault.example.org → vault.example.org

import (
	"net/url"
	"sort"
	"strings"

	"dashboard/internal/enums"
	"dashboard/internal/model"
	"dashboard/internal/rules"
	"dashboard/internal/sources"
)

const (
	autoKey      = "auto"
	hostsKey     = "hosts"
	maxAutoHosts = 50
	httpsScheme  = "https"
)

// withAutoHosts returns conn with the found hosts added to its options;
// conn itself stays untouched.
func withAutoHosts(conn *model.Connection, links []rules.Link, datasets map[string]any) *model.Connection {
	if auto, _ := conn.Options[autoKey].(bool); !auto {
		return conn
	}
	found := map[string]bool{}
	for _, l := range links {
		if u, err := url.Parse(l.URL); err == nil && u.Scheme == httpsScheme && u.Hostname() != "" {
			found[strings.ToLower(u.Host)] = true
		}
	}
	if p, ok := datasets[string(enums.ServicePangolin)].(*sources.PangolinDataset); ok {
		for _, r := range p.Resources {
			if r.Enabled && r.Domain != "" {
				found[strings.ToLower(r.Domain)] = true
			}
		}
	}

	hosts := []any{}
	existing, _ := conn.Options[hostsKey].([]any)
	for _, h := range existing {
		hosts = append(hosts, h)
		delete(found, strings.ToLower(asString(h)))
	}
	extra := make([]string, 0, len(found))
	for h := range found {
		extra = append(extra, h)
	}
	sort.Strings(extra)
	for _, h := range extra {
		if len(hosts) >= maxAutoHosts {
			break
		}
		hosts = append(hosts, h)
	}

	copied := *conn
	copied.Options = map[string]any{}
	for k, v := range conn.Options {
		copied.Options[k] = v
	}
	copied.Options[hostsKey] = hosts
	return &copied
}

func asString(v any) string {
	s, _ := v.(string)
	return s
}
