// Package weekly tells the caller's week in connected numbers: work,
// money, storage and power from the stored data, plus how many hints
// came and went. Everything is computed locally.
package weekly

import (
	"context"
	"database/sql"
	"time"

	"andon/internal/enums"
	"andon/internal/metrics"
	data "andon/internal/repos/data"
	"andon/internal/services/access"
	"andon/internal/services/connections"
	"andon/internal/services/history"
	"andon/internal/services/svcdata"
	"andon/internal/sources"
)

// storyServices are the services a week story reads.
var storyServices = map[enums.ServiceType]bool{
	enums.ServiceKimai: true, enums.ServiceInvoiceNinja: true, enums.ServiceTibber: true,
}

const (
	weekDays   = 7
	eventLimit = 1000
)

// Story returns the week's lines across the caller's spaces.
func Story(ctx context.Context, d *sql.DB, who *access.Principal, now time.Time) ([]metrics.StoryLine, error) {
	views, err := connections.Listing(d, who, enums.RightUse)
	if err != nil {
		return nil, err
	}
	datasets := map[string]any{}
	spaces := map[int64]bool{}
	for _, v := range views {
		spaces[v.SpaceID] = true
		if !storyServices[v.Service] {
			continue
		}
		conn, err := connections.ByID(d, v.ID)
		if err != nil || conn == nil {
			continue
		}
		uid := who.UserID
		res, err := svcdata.Get(ctx, d, sources.DataKey(v.Service), nil, conn, &uid, svcdata.Stored)
		if err == nil && res.Data != nil {
			datasets[string(v.Service)] = res.Data
		}
	}

	var lines []metrics.StoryLine
	merged := &metrics.History{Series: map[string][]metrics.Point{}}
	for id := range spaces {
		h, err := history.Load(d, id, 0, now)
		if err != nil {
			return nil, err
		}
		for k, v := range h.Series {
			merged.Series[k] = v
		}
	}
	lines = append(lines, metrics.WeekStory(datasets, merged, now)...)

	ids := make([]int64, 0, len(who.Spaces))
	for id := range who.Spaces {
		ids = append(ids, id)
	}
	events, err := data.HintEventsSince(d, ids, []string{string(enums.EventOpened), string(enums.EventResolved)}, now.AddDate(0, 0, -weekDays), eventLimit)
	if err != nil {
		return nil, err
	}
	opened, resolved := 0, 0
	for _, e := range events {
		if e.Owner != 0 && e.Owner != who.UserID {
			continue
		}
		if e.Kind == string(enums.EventOpened) {
			opened++
		} else {
			resolved++
		}
	}
	if opened+resolved > 0 {
		lines = append(lines, metrics.StoryLine{Key: "hints", Params: map[string]any{"opened": opened, "resolved": resolved}})
	}
	return lines, nil
}
