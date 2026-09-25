package rules

// Board links against other services:
//
//	links <-> linkwarden   bookmarks missing on the board, tiles never saved
//	links <-> uptimekuma   tiles whose host no monitor watches

import (
	"net/url"
	"sort"
	"strings"
	"time"

	"dashboard/internal/enums"
	"dashboard/internal/sources"
)

// LinksDataset is the Env.Datasets key of the space's link tiles.
const LinksDataset = "links"

// Link is one link tile of a space.
type Link struct {
	Title, URL string
	DownDays   int       // days in a row without an answer (background checks)
	LastClick  time.Time // zero: never clicked
}

// normURL makes URLs comparable: lower-case host, no scheme, no "www.",
// no trailing slash, no query ("https://www.X.org/a/" → "x.org/a").
func normURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return strings.TrimRight(strings.ToLower(raw), "/")
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	return host + strings.TrimRight(u.EscapedPath(), "/")
}

func hostOf(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
}

func boardLinks(env Env) ([]Link, bool) {
	links, ok := env.Datasets[LinksDataset].([]Link)
	return links, ok
}

func init() {
	lw := string(enums.ServiceLinkwarden)
	kuma := string(enums.ServiceUptimeKuma)

	// One hint per collection: "4 bookmarks of Homelab are not on the
	// board", with names, instead of one hint per link.
	Register("linkwarden.not_on_board", Cross, nil, func(_ any, cfg map[string]any, env Env) []Finding {
		data, ok := env.Datasets[lw].(*sources.LinkwardenDataset)
		links, ok2 := boardLinks(env)
		if !ok || !ok2 {
			return nil
		}
		onBoard := map[string]bool{}
		for _, l := range links {
			onBoard[normURL(l.URL)] = true
		}
		missing := map[string][]string{}
		for _, b := range data.Links {
			if !onBoard[normURL(b.URL)] {
				missing[b.Collection] = append(missing[b.Collection], b.Name)
			}
		}
		var found []Finding
		for _, col := range sortedKeysOf(missing) {
			found = append(found, Finding{
				Fingerprint: "missing:" + col, Rule: "linkwarden.not_on_board", Severity: enums.SeverityInfo,
				Message: "linkwarden.missing", Params: map[string]any{"collection": col, "count": len(missing[col]), "names": shortList(missing[col])},
				ActionURL: data.URL, ActionLabel: "open_in_linkwarden", Sources: []string{lw},
			})
		}
		return found
	})

	Register("linkwarden.not_saved", Cross, nil, func(_ any, cfg map[string]any, env Env) []Finding {
		data, ok := env.Datasets[lw].(*sources.LinkwardenDataset)
		links, ok2 := boardLinks(env)
		if !ok || !ok2 || len(data.Links) == 0 {
			return nil
		}
		saved := map[string]bool{}
		for _, b := range data.Links {
			saved[normURL(b.URL)] = true
		}
		var names []string
		for _, l := range links {
			if !saved[normURL(l.URL)] {
				names = append(names, l.Title)
			}
		}
		if len(names) == 0 {
			return nil
		}
		return []Finding{{
			Fingerprint: "unsaved", Rule: "linkwarden.not_saved", Severity: enums.SeverityInfo,
			Message: "linkwarden.unsaved", Params: map[string]any{"count": len(names), "names": shortList(names)},
			ActionURL: data.URL, ActionLabel: "open_in_linkwarden", Sources: []string{lw},
		}}
	})

	// A service worth a tile is worth a monitor: tiles on hosts that no
	// Uptime Kuma monitor targets. "ignore" lists hosts that need none.
	Register("kuma.unmonitored", Cross, map[string]any{"ignore": []any{}}, func(_ any, cfg map[string]any, env Env) []Finding {
		data, ok := env.Datasets[kuma].(*sources.KumaDataset)
		links, ok2 := boardLinks(env)
		if !ok || !ok2 {
			return nil
		}
		watched := map[string]bool{}
		for _, m := range data.Monitors {
			if h := hostOf(m.Target); h != "" {
				watched[h] = true
			}
		}
		ignore := stringsSlice(cfg["ignore"])
		var names []string
		for _, l := range links {
			h := hostOf(l.URL)
			if h == "" || watched[h] || containsAny(h, ignore) {
				continue
			}
			names = append(names, l.Title)
		}
		if len(names) == 0 {
			return nil
		}
		return []Finding{{
			Fingerprint: "unmonitored", Rule: "kuma.unmonitored", Severity: enums.SeverityInfo,
			Message: "kuma.unmonitored", Params: map[string]any{"count": len(names), "names": shortList(names)},
			ActionURL: data.URL, ActionLabel: "open_in_uptimekuma", Sources: []string{kuma},
		}}
	})
}

func sortedKeysOf(m map[string][]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func init() {
	// Tiles that have not answered for days are probably dead links.
	Register("links.dead", Cross, map[string]any{"days": 7.0}, func(_ any, cfg map[string]any, env Env) []Finding {
		links, ok := boardLinks(env)
		if !ok {
			return nil
		}
		var names []string
		for _, l := range links {
			if l.DownDays >= cfgInt(cfg, "days") {
				names = append(names, l.Title)
			}
		}
		if len(names) == 0 {
			return nil
		}
		return []Finding{{Fingerprint: "dead", Rule: "links.dead", Severity: enums.SeverityInfo, Message: "links.dead",
			Params: map[string]any{"count": len(names), "names": shortList(names), "days": cfgInt(cfg, "days")}, Sources: []string{"links"}}}
	})
}
