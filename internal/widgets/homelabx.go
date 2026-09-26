package widgets

// Homelab widgets across services (phase 13):
//
//	update_window     pending updates against backup age, streams, timers,
//	                  appointments and the power price
//	storage_forecast  every storage's share and days until full (history)
//	table exposure    Pangolin's public resources: login, certificate, updates
//	table domain_chain what depends on each domain

import (
	"strconv"
	"strings"
	"time"

	"andon/internal/enums"
	"andon/internal/metrics"
	"andon/internal/sources"
)

const (
	// HistorySlot names the recorded history among a widget's results.
	HistorySlot = "history"

	TableExposure    TableKind = "exposure"
	TableDomainChain TableKind = "domain_chain"

	windowBackupAge = 24 * time.Hour
	windowRefreshS  = 300
)

// windowPeers are the services the update window weighs.
var windowPeers = []enums.ServiceType{
	enums.ServiceKimai, enums.ServiceMediaServer, enums.ServiceCalendar, enums.ServiceTibber,
	enums.ServiceBorgBackup, enums.ServiceTrueNAS, enums.ServiceProxmox, enums.ServiceImmich, enums.ServiceAuthentik,
	enums.ServiceKomodo, enums.ServiceNextcloud, enums.ServiceGateway, enums.ServiceHomeAssistant,
}

// updatePeers report pending updates for the exposure table.
var updatePeers = []enums.ServiceType{enums.ServiceKomodo, enums.ServiceTrueNAS, enums.ServiceImmich, enums.ServiceAuthentik, enums.ServiceNextcloud}

func peersOf(services []enums.ServiceType) []Query {
	out := make([]Query, 0, len(services))
	for _, s := range services {
		out = append(out, peer(string(s), s))
	}
	return out
}

// peerDatasets collects the results of peer queries by service.
func peerDatasets(results map[string]any, services []enums.ServiceType) map[string]any {
	out := map[string]any{}
	for _, s := range services {
		if d, ok := results[string(s)]; ok {
			out[string(s)] = d
		}
	}
	return out
}

func updateWindowView(_ any, results map[string]any, ctx ViewCtx) map[string]any {
	w := metrics.UpdateWindow(peerDatasets(results, windowPeers), time.Now().UTC(), windowBackupAge)
	return map[string]any{"Window": w, "Good": len(w.Blockers) == 0 && len(w.Updates) > 0,
		"BackupHours": int(w.BackupAge.Hours())}
}

func storageView(_ any, results map[string]any, _ ViewCtx) map[string]any {
	h, _ := results[HistorySlot].(*metrics.History)
	if h == nil {
		return map[string]any{}
	}
	return map[string]any{"Rows": metrics.StorageForecasts(h, time.Now().UTC())}
}

// homelabQueries are the peers of the homelab tables.
func homelabQueries(kind TableKind) []Query {
	switch kind {
	case TableExposure:
		return append(peersOf(updatePeers), peer(string(enums.ServiceCerts), enums.ServiceCerts))
	case TableDomainChain:
		return peersOf([]enums.ServiceType{enums.ServiceCerts, enums.ServicePangolin, enums.ServiceUptimeKuma})
	}
	return nil
}

func homelabCols(kind TableKind) []Col {
	switch kind {
	case TableExposure:
		return []Col{{"name", "text"}, {"domain", "text"}, {"login", "yesno"}, {"cert_days", "text"}, {"updates", "text"}, {"risk", "risk"}}
	case TableDomainChain:
		return []Col{{"domain", "text"}, {"expires", "day"}, {"cert_days", "text"}, {"resources", "text"}, {"monitors", "text"}}
	}
	return nil
}

func homelabRows(kind TableKind, data any, results map[string]any, ctx ViewCtx) ([]Row, bool) {
	today := parseToday(ctx.Today)
	var rows []Row
	switch d := data.(type) {
	case *sources.PangolinDataset:
		if kind != TableExposure {
			return nil, false
		}
		certs, _ := results[string(enums.ServiceCerts)].(*sources.CertDataset)
		updates := metrics.PendingUpdates(peerDatasets(results, updatePeers))
		for _, r := range metrics.Exposure(d, certs, updates, today) {
			rows = append(rows, Row{[]any{r.Name, r.Domain, r.Login, certText(r.CertDays), strings.Join(r.Updates, ", "), r.Risk}})
		}
	case *sources.DomainsDataset:
		if kind != TableDomainChain {
			return nil, false
		}
		certs, _ := results[string(enums.ServiceCerts)].(*sources.CertDataset)
		pangolin, _ := results[string(enums.ServicePangolin)].(*sources.PangolinDataset)
		kuma, _ := results[string(enums.ServiceUptimeKuma)].(*sources.KumaDataset)
		for _, c := range metrics.DomainChains(d, certs, pangolin, kuma, nil, today) {
			rows = append(rows, Row{[]any{c.Domain, dayOrEmpty(c.Expires), certText(c.CertDays), strings.Join(c.Resources, ", "), strings.Join(c.Monitors, ", ")}})
		}
	default:
		return nil, false
	}
	return rows, true
}

// certText shows remaining certificate days, "" when unknown.
func certText(days int) string {
	if days < 0 {
		return ""
	}
	return strconv.Itoa(days)
}

func init() {
	Register(WidgetType{Key: "update_window", Decode: decodeEmpty, Template: "widgets/update_window", Category: CategoryInsight,
		RefreshS: windowRefreshS, View: updateWindowView, Queries: func(any) []Query { return peersOf(windowPeers) }})
	Register(WidgetType{Key: "storage_forecast", Decode: decodeEmpty, Template: "widgets/storage_forecast", Category: CategoryInsight,
		RefreshS: 3600, View: storageView, Extra: ExtraHistory})
}
