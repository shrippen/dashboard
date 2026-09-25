package widgets

// Freelance widgets on the Kimai connection:
//
//	kimai_timer   running timers (stop), recent pairs (start), hours today

import (
	"fmt"
	"time"

	"dashboard/internal/enums"
	"dashboard/internal/sources"
)

const minutesPerHour = 60

// TimerRow is one running or startable timer.
type TimerRow struct {
	ID, ProjectID, ActivityID   int64
	Project, Activity, Customer string
	Elapsed                     string // "1:07" for running ones
}

func timerRow(t sources.KimaiTimer) TimerRow {
	return TimerRow{ID: t.ID, ProjectID: t.ProjectID, ActivityID: t.ActivityID, Project: t.Project, Activity: t.Activity, Customer: t.Customer}
}

// clockMinutes: 67 → "1:07".
func clockMinutes(m int) string {
	return fmt.Sprintf("%d:%02d", m/minutesPerHour, m%minutesPerHour)
}

func timerView(_ any, results map[string]any, _ ViewCtx) map[string]any {
	data, ok := results["live"].(*sources.KimaiLive)
	if !ok {
		return map[string]any{}
	}
	running := map[[2]int64]bool{}
	var active, recent []TimerRow
	for _, t := range data.Active {
		row := timerRow(t)
		row.Elapsed = clockMinutes(int(time.Since(t.Begin).Minutes()))
		active = append(active, row)
		running[[2]int64{t.ProjectID, t.ActivityID}] = true
	}
	for _, t := range data.Recent {
		if !running[[2]int64{t.ProjectID, t.ActivityID}] {
			recent = append(recent, timerRow(t))
		}
	}
	return map[string]any{"Active": active, "Recent": recent, "Today": clockMinutes(data.TodayMin)}
}

func init() {
	Register(WidgetType{Key: "kimai_timer", Decode: decodeEmpty, Template: "widgets/kimai_timer", Category: CategoryInsight,
		Service: enums.ServiceKimai, RefreshS: 60, Live: true, View: timerView,
		Queries: func(any) []Query { return []Query{{Name: "live", Source: "kimai.live", Conn: ConnWidget}} }})
}
