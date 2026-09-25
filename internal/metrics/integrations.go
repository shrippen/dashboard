package metrics

// Info lines and views of the network, media and everyday integrations.

import (
	"time"

	"dashboard/internal/sources"
)

// CheapHours is the default window length for flexible loads.
const CheapHours = 3

// TailscaleInfo: "5/7 online".
func TailscaleInfo(data *sources.TailscaleDataset) []InfoPart {
	online := 0
	for _, d := range data.Devices {
		if d.Online {
			online++
		}
	}
	return []InfoPart{part("tailscale.online", map[string]any{"online": online, "count": len(data.Devices)})}
}

// GatewayInfo: "WAN ok" or "WAN down", plus pending updates.
func GatewayInfo(data *sources.GatewayDataset) []InfoPart {
	var found []InfoPart
	if len(data.Gateways) > 0 {
		key := "gateway.wan_ok"
		for _, g := range data.Gateways {
			if !g.Up {
				key = "gateway.wan_down"
			}
		}
		found = append(found, part(key, nil))
	}
	if len(data.Devices) > 0 {
		found = append(found, part("gateway.devices", map[string]any{"count": len(data.Devices)}))
	}
	if data.Updates > 0 {
		found = append(found, part("gateway.updates", map[string]any{"count": data.Updates}))
	}
	return found
}

// MediaServerInfo: "2 streams · 1204 movies".
func MediaServerInfo(data *sources.MediaServerDataset) []InfoPart {
	return []InfoPart{
		part("mediaserver.streams", map[string]any{"count": len(data.Streams)}),
		part("mediaserver.movies", map[string]any{"count": data.Movies}),
	}
}

// ArrInfo: "3 in queue · 12 missing".
func ArrInfo(data *sources.ArrDataset) []InfoPart {
	return []InfoPart{
		part("arr.queue", map[string]any{"count": data.Queue}),
		part("arr.missing", map[string]any{"count": data.Missing}),
	}
}

// VaultwardenInfo: "4 users · 1 without 2FA".
func VaultwardenInfo(data *sources.VaultwardenDataset) []InfoPart {
	without := 0
	for _, u := range data.Users {
		if u.Enabled && !u.TwoFactor {
			without++
		}
	}
	found := []InfoPart{part("vaultwarden.users", map[string]any{"count": len(data.Users)})}
	if without > 0 {
		found = append(found, part("vaultwarden.no_2fa", map[string]any{"count": without}))
	}
	return found
}

// SpeedtestInfo: "↓ 243 ↑ 41 Mbit/s · 12 ms".
func SpeedtestInfo(data *sources.SpeedtestDataset) []InfoPart {
	return []InfoPart{part("speedtest.result", map[string]any{"down": int(data.Down + 0.5), "up": int(data.Up + 0.5), "ping": int(data.Ping + 0.5)})}
}

// GrocyInfo: "1 expired · 2 due soon · 1 missing".
func GrocyInfo(data *sources.GrocyDataset) []InfoPart {
	var found []InfoPart
	if n := len(data.Expired) + len(data.Overdue); n > 0 {
		found = append(found, part("grocy.expired", map[string]any{"count": n}))
	}
	found = append(found, part("grocy.soon", map[string]any{"count": len(data.Soon)}))
	if len(data.Missing) > 0 {
		found = append(found, part("grocy.missing", map[string]any{"count": len(data.Missing)}))
	}
	return found
}

// DWDInfo: "no warnings" or "2 warnings".
func DWDInfo(data *sources.DWDDataset) []InfoPart {
	if len(data.Warnings) == 0 {
		return []InfoPart{part("dwd.none", nil)}
	}
	return []InfoPart{part("dwd.count", map[string]any{"count": len(data.Warnings)})}
}

// GitHubInfo: "3 PRs · 1 CI failed".
func GitHubInfo(data *sources.GitHubDataset) []InfoPart {
	prs, failed := 0, 0
	for _, r := range data.Repos {
		prs += r.PRs
		if r.CI == "failure" {
			failed++
		}
	}
	found := []InfoPart{part("github.prs", map[string]any{"count": prs})}
	if failed > 0 {
		found = append(found, part("github.ci_failed", map[string]any{"count": failed}))
	}
	return found
}

// TibberInfo: "0,31 € / kWh".
func TibberInfo(data *sources.TibberDataset) []InfoPart {
	return []InfoPart{part("tibber.price", map[string]any{"price": map[string]any{"$money": data.Current, "currency": data.Currency}})}
}

// CheapWindow finds the cheapest run of `hours` consecutive prices from
// now on; ok is false with too few prices left.
func CheapWindow(prices []sources.PricePoint, now time.Time, hours int) (start time.Time, avg float64, ok bool) {
	var ahead []sources.PricePoint
	for _, p := range prices {
		if !p.At.Add(time.Hour).Before(now) {
			ahead = append(ahead, p)
		}
	}
	best := -1.0
	for i := 0; i+hours <= len(ahead); i++ {
		sum := 0.0
		for _, p := range ahead[i : i+hours] {
			sum += p.Total
		}
		if best < 0 || sum < best {
			best, start = sum, ahead[i].At
		}
	}
	if best < 0 {
		return time.Time{}, 0, false
	}
	return start, best / float64(hours), true
}
