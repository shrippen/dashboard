package metrics

import (
	"time"

	"dashboard/internal/sources"
)

const hoursPerDay = 24

// KumaInfo: "3/4 online".
func KumaInfo(data *sources.KumaDataset) []InfoPart {
	up := 0
	for _, m := range data.Monitors {
		if m.Status == sources.KumaUp {
			up++
		}
	}
	return []InfoPart{part("kuma.status", map[string]any{"up": up, "count": len(data.Monitors)})}
}

// ProxmoxInfo: "5 guests · 12 updates".
func ProxmoxInfo(data *sources.ProxmoxDataset) []InfoPart {
	guests := 0
	for _, g := range data.Guests {
		if !g.Template {
			guests++
		}
	}
	found := []InfoPart{part("proxmox.guests", map[string]any{"count": guests})}

	updates := 0
	for _, n := range data.Nodes {
		updates += max(n.Updates, 0)
	}
	if updates > 0 {
		found = append(found, part("proxmox.updates", map[string]any{"count": updates}))
	}
	return found
}

// PaperlessInfo: "7 in inbox".
func PaperlessInfo(data *sources.PaperlessDataset) []InfoPart {
	return []InfoPart{part("paperless.inbox", map[string]any{"count": data.Inbox})}
}

// CertsInfo: days until the next readable certificate expires.
func CertsInfo(data *sources.CertDataset, today time.Time) []InfoPart {
	var next time.Time
	for _, c := range data.Certs {
		if c.Error == "" && (next.IsZero() || c.NotAfter.Before(next)) {
			next = c.NotAfter
		}
	}
	if next.IsZero() {
		return nil
	}
	return []InfoPart{part("certs.next", map[string]any{"days": int(next.Sub(today).Hours() / hoursPerDay)})}
}
