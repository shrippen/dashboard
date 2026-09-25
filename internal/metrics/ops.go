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

// ScrutinyInfo: "2/3 disks ok".
func ScrutinyInfo(data *sources.ScrutinyDataset) []InfoPart {
	ok := 0
	for _, d := range data.Disks {
		if d.Status == sources.ScrutinyPassed {
			ok++
		}
	}
	return []InfoPart{part("scrutiny.disks", map[string]any{"ok": ok, "count": len(data.Disks)})}
}

// ImmichInfo: "48,213 photos · 87 % used".
func ImmichInfo(data *sources.ImmichDataset) []InfoPart {
	var found []InfoPart
	if data.Photos > 0 {
		found = append(found, part("immich.photos", map[string]any{"photos": map[string]any{"$num": float64(data.Photos)}}))
	}
	return append(found, part("immich.disk", map[string]any{"percent": int(data.DiskPercent + 0.5)}))
}

// UmamiInfo: visitors over all sites, last 7 days.
func UmamiInfo(data *sources.UmamiDataset) []InfoPart {
	visitors := 0
	for _, s := range data.Sites {
		visitors += s.Visitors
	}
	return []InfoPart{part("umami.visitors", map[string]any{"visitors": map[string]any{"$num": float64(visitors)}})}
}
