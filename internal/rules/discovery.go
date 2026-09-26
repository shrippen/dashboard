package rules

// Services without a tile: what Komodo runs, Pangolin publishes or Kuma
// watches, but no link tile of the space points to.
//
//	komodo stack "immich"          ↔ tile title/URL contains "immich"
//	pangolin resource photos.x.org ↔ tile host photos.x.org
//	kuma monitor https://nas.lan   ↔ tile host nas.lan

import (
	"strings"

	"andon/internal/enums"
	"andon/internal/sources"
)

const discoveryRule = "discovery.no_tile"

// tileIndex holds what the tiles cover: hosts and lower-case text.
type tileIndex struct {
	hosts map[string]bool
	text  []string
}

func indexTiles(links []Link) tileIndex {
	idx := tileIndex{hosts: map[string]bool{}}
	for _, l := range links {
		idx.hosts[HostOf(l.URL)] = true
		idx.text = append(idx.text, strings.ToLower(l.Title+" "+l.URL))
	}
	return idx
}

func (idx tileIndex) mentions(name string) bool {
	name = strings.ToLower(name)
	for _, t := range idx.text {
		if strings.Contains(t, name) {
			return true
		}
	}
	return false
}

func init() {
	Register(discoveryRule, Cross, nil, func(_ any, cfg map[string]any, env Env) []Finding {
		links, ok := boardLinks(env)
		if !ok {
			return nil
		}
		idx := indexTiles(links)
		missing := map[string][]string{}

		if k, ok := env.Datasets[string(enums.ServiceKomodo)].(*sources.KomodoDataset); ok {
			for _, s := range k.Stacks {
				if !idx.mentions(s.Name) {
					missing[string(enums.ServiceKomodo)] = append(missing[string(enums.ServiceKomodo)], s.Name)
				}
			}
		}
		if p, ok := env.Datasets[string(enums.ServicePangolin)].(*sources.PangolinDataset); ok {
			for _, r := range p.Resources {
				if r.Enabled && !idx.hosts[strings.ToLower(r.Domain)] {
					missing[string(enums.ServicePangolin)] = append(missing[string(enums.ServicePangolin)], r.Domain)
				}
			}
		}
		if k, ok := env.Datasets[string(enums.ServiceUptimeKuma)].(*sources.KumaDataset); ok {
			for _, m := range k.Monitors {
				if host := HostOf(m.Target); host != "" && !idx.hosts[host] && !idx.mentions(m.Name) {
					missing[string(enums.ServiceUptimeKuma)] = append(missing[string(enums.ServiceUptimeKuma)], m.Name)
				}
			}
		}

		var found []Finding
		for _, svc := range sortedKeysOf(missing) {
			found = append(found, Finding{Fingerprint: "no_tile:" + svc, Rule: discoveryRule, Severity: enums.SeverityInfo,
				Message: "discovery.no_tile", Params: map[string]any{"service": svc, "count": len(missing[svc]), "names": shortList(missing[svc])},
				ActionURL: "/widgets/new?type=link", Sources: []string{svc}})
		}
		return found
	})
}
