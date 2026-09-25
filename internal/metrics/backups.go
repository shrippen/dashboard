package metrics

// Backup overview across tools:
//
//	borg clients   ─┐
//	pgbackweb      ─┼─► []BackupRow{Tool, Item, Last, State}  (oldest first)
//	truenas snaps  ─┘

import (
	"sort"
	"time"

	"dashboard/internal/sources"
)

// BackupState is a row's outcome.
type BackupState string

const (
	BackupOK      BackupState = "ok"
	BackupOld     BackupState = "old"
	BackupFailed  BackupState = "failed"
	BackupUnknown BackupState = "unknown"
)

// BackupRow is one backed-up item.
type BackupRow struct {
	Tool, Item string
	Last       time.Time // zero: never / unknown
	State      BackupState
}

// Backups builds the overview; maxAge marks older successes as old.
func Backups(borg *sources.BorgDataset, pg *sources.PGBackDataset, nas *sources.TrueNASDataset, now time.Time, maxAge time.Duration) []BackupRow {
	var rows []BackupRow
	age := func(last time.Time) BackupState {
		switch {
		case last.IsZero():
			return BackupUnknown
		case now.Sub(last) > maxAge:
			return BackupOld
		}
		return BackupOK
	}

	if borg != nil {
		for _, c := range borg.Clients {
			rows = append(rows, BackupRow{Tool: "borgbackup", Item: c.Name, Last: c.LastBackup, State: age(c.LastBackup)})
		}
	}
	if pg != nil {
		for _, b := range pg.Backups {
			row := BackupRow{Tool: "pgbackweb", Item: b.Name, Last: b.LastSuccess, State: age(b.LastSuccess)}
			if b.Failing() {
				row.State = BackupFailed
			}
			rows = append(rows, row)
		}
	}
	if nas != nil {
		for _, s := range nas.Snapshots {
			if !s.Enabled {
				continue
			}
			row := BackupRow{Tool: "truenas", Item: s.Dataset, Last: s.Last, State: age(s.Last)}
			if s.State == "ERROR" {
				row.State = BackupFailed
			}
			rows = append(rows, row)
		}
	}

	// Worst first: failed, old, unknown, ok; then oldest.
	rank := map[BackupState]int{BackupFailed: 0, BackupOld: 1, BackupUnknown: 2, BackupOK: 3}
	sort.SliceStable(rows, func(i, j int) bool {
		if rank[rows[i].State] != rank[rows[j].State] {
			return rank[rows[i].State] < rank[rows[j].State]
		}
		return rows[i].Last.Before(rows[j].Last)
	})
	return rows
}
