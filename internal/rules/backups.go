package rules

// Backup rules across tools:
//
//	truenas.snapshot_failed   a periodic snapshot task ended in ERROR
//	backups.gap               Komodo stacks / TrueNAS apps whose name appears
//	                          in no backup item (Borg client, PG Back Web
//	                          backup, snapshot dataset) – a name heuristic

import (
	"strings"

	"andon/internal/enums"
	"andon/internal/sources"
)

func init() {
	nas := string(enums.ServiceTrueNAS)
	Register("truenas.snapshot_failed", nas, nil, func(raw any, cfg map[string]any, env Env) []Finding {
		data, _ := raw.(*sources.TrueNASDataset)
		var found []Finding
		for _, s := range data.Snapshots {
			if !s.Enabled || s.State != "ERROR" {
				continue
			}
			found = append(found, svcFinding(nas, "truenas.snapshot_failed", "snap:"+s.Dataset, "truenas.snapshot_failed",
				enums.SeverityWarn, strings.TrimRight(data.URL, "/")+"/ui/data-protection", map[string]any{"dataset": s.Dataset}))
		}
		return found
	})

	Register("backups.gap", Cross, nil, func(_ any, cfg map[string]any, env Env) []Finding {
		items, hasTool := backupItems(env)
		if !hasTool {
			return nil
		}
		var missing []string
		for _, name := range serviceNames(env) {
			if !coveredBy(name, items) {
				missing = append(missing, name)
			}
		}
		if len(missing) == 0 {
			return nil
		}
		return []Finding{{Fingerprint: "gap", Rule: "backups.gap", Severity: enums.SeverityInfo, Message: "backups.gap",
			Params: map[string]any{"count": len(missing), "names": shortList(missing)}, Sources: []string{"backups"}}}
	})
}

// backupItems lists lower-case names of everything a backup tool covers;
// false when the space has no backup tool at all.
func backupItems(env Env) ([]string, bool) {
	var items []string
	seen := false
	if borg, ok := env.Datasets[string(enums.ServiceBorgBackup)].(*sources.BorgDataset); ok {
		seen = true
		for _, c := range borg.Clients {
			items = append(items, strings.ToLower(c.Name))
		}
	}
	if pg, ok := env.Datasets[string(enums.ServicePGBackWeb)].(*sources.PGBackDataset); ok {
		seen = true
		for _, b := range pg.Backups {
			items = append(items, strings.ToLower(b.Name))
		}
	}
	if nas, ok := env.Datasets[string(enums.ServiceTrueNAS)].(*sources.TrueNASDataset); ok && len(nas.Snapshots) > 0 {
		seen = true
		for _, s := range nas.Snapshots {
			items = append(items, strings.ToLower(s.Dataset))
		}
	}
	return items, seen
}

// serviceNames lists what should be backed up: stacks and apps.
func serviceNames(env Env) []string {
	var names []string
	if komodo, ok := env.Datasets[string(enums.ServiceKomodo)].(*sources.KomodoDataset); ok {
		for _, s := range komodo.Stacks {
			names = append(names, s.Name)
		}
	}
	if nas, ok := env.Datasets[string(enums.ServiceTrueNAS)].(*sources.TrueNASDataset); ok {
		for _, a := range nas.Apps {
			names = append(names, a.Name)
		}
	}
	return names
}

// coveredBy: "immich" is covered by "immich-db" or "tank/immich".
func coveredBy(name string, items []string) bool {
	name = strings.ToLower(name)
	for _, item := range items {
		if strings.Contains(item, name) {
			return true
		}
	}
	return false
}
