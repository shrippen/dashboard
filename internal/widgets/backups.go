package widgets

// "backups": one table over Borg, PG Back Web and TrueNAS snapshots of the
// space, worst first. Tools without a connection are simply missing.

import (
	"time"

	"andon/internal/enums"
	"andon/internal/metrics"
	"andon/internal/sources"
)

const defaultBackupHours = 26

type BackupsConfig struct{ MaxHours int }

func decodeBackups(raw map[string]any) any {
	return BackupsConfig{MaxHours: clampInt(asInt(raw["max_hours"], defaultBackupHours), 1, 24*14)}
}

func backupsQueries(any) []Query {
	return []Query{
		{Name: string(enums.ServiceBorgBackup), Source: "data", Conn: ConnPeer, Service: enums.ServiceBorgBackup},
		{Name: string(enums.ServicePGBackWeb), Source: "data", Conn: ConnPeer, Service: enums.ServicePGBackWeb},
		{Name: string(enums.ServiceTrueNAS), Source: "data", Conn: ConnPeer, Service: enums.ServiceTrueNAS},
	}
}

func backupsView(cfgAny any, results map[string]any, _ ViewCtx) map[string]any {
	borg, _ := results[string(enums.ServiceBorgBackup)].(*sources.BorgDataset)
	pg, _ := results[string(enums.ServicePGBackWeb)].(*sources.PGBackDataset)
	nas, _ := results[string(enums.ServiceTrueNAS)].(*sources.TrueNASDataset)
	maxAge := time.Duration(cfgAny.(BackupsConfig).MaxHours) * time.Hour
	return map[string]any{"Rows": metrics.Backups(borg, pg, nas, time.Now().UTC(), maxAge)}
}

func init() {
	Register(WidgetType{Key: "backups", Decode: decodeBackups, Template: "widgets/backups", Category: CategoryInsight,
		RefreshS: 600, Queries: backupsQueries, View: backupsView})
}
