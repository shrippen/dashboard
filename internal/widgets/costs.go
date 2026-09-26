package widgets

// "homelab_cost": what the homelab costs per month (power, hardware
// write-off, domains, hosting), the power split by Proxmox guests, the
// share used for customers, and a cloud comparison. Settings: space
// settings → Homelab costs.

import (
	"time"

	"andon/internal/enums"
	"andon/internal/metrics"
	"andon/internal/sources"
)

// costPeers are the services the bill is built from.
var costPeers = []enums.ServiceType{
	enums.ServiceHomeAssistant, enums.ServiceTibber, enums.ServiceSnipeIT, enums.ServiceSure, enums.ServiceDomains,
	enums.ServiceKimai, enums.ServiceKomodo, enums.ServiceGitea, enums.ServiceGitHub, enums.ServiceProxmox,
}

func homelabCostView(_ any, results map[string]any, ctx ViewCtx) map[string]any {
	ds := peerDatasets(results, costPeers)
	s := metrics.HomelabSettingsOf(ctx.Settings)
	in := metrics.CostInputs{}
	in.Hass, _ = ds[string(enums.ServiceHomeAssistant)].(*sources.HassDataset)
	in.Tibber, _ = ds[string(enums.ServiceTibber)].(*sources.TibberDataset)
	in.Snipe, _ = ds[string(enums.ServiceSnipeIT)].(*sources.SnipeDataset)
	in.Sure, _ = ds[string(enums.ServiceSure)].(*sources.SureDataset)
	in.Domains, _ = ds[string(enums.ServiceDomains)].(*sources.DomainsDataset)
	bill := metrics.HomelabCost(in, s, time.Now().UTC())

	out := map[string]any{"Bill": bill, "Settings": s}
	kimai, _ := ds[string(enums.ServiceKimai)].(*sources.KimaiDataset)
	if share, business, all := metrics.BusinessShare(kimai, metrics.WorkNames(ds)); all > 0 && kimai != nil {
		out["Share"], out["SharePct"], out["ShareOf"], out["ShareAll"] = share, share*100, business, all
		out["BusinessYearly"] = bill.Total * share * monthsPerYearF
	}
	if w, ok := metrics.PowerWatts(in.Hass, s.PowerEntity); ok {
		monthly := metrics.MonthlyPowerCost(w, metrics.PowerPrice(in.Tibber, s.PowerPrice))
		proxmox, _ := ds[string(enums.ServiceProxmox)].(*sources.ProxmoxDataset)
		out["Watts"], out["PowerMonthly"], out["Services"] = w, monthly, metrics.PowerPerService(proxmox, monthly)
	}
	return out
}

// monthsPerYearF turns monthly into yearly amounts.
const monthsPerYearF = 12.0

// StorySlot names the week's story among a widget's results.
const StorySlot = "story"

func storyView(_ any, results map[string]any, _ ViewCtx) map[string]any {
	return map[string]any{"Lines": results[StorySlot]}
}

func init() {
	Register(WidgetType{Key: "week_story", Decode: decodeEmpty, Template: "widgets/week_story", Category: CategoryInsight,
		RefreshS: 3600, View: storyView, Extra: ExtraStory})
	Register(WidgetType{Key: "homelab_cost", Decode: decodeEmpty, Template: "widgets/homelab_cost", Category: CategoryInsight,
		RefreshS: 3600, View: homelabCostView, Queries: func(any) []Query { return peersOf(costPeers) }})
}
