// Package timer starts and stops Kimai timers from a board.
//
//	tile ──POST──► Start/Stop: widget is a Kimai timer? USE on the
//	connection? ──► outbound.KimaiStart/KimaiStop ──► cache dropped
package timer

import (
	"context"
	"database/sql"
	"errors"
	"strconv"

	"dashboard/internal/model"
	"dashboard/internal/outbound"
	"dashboard/internal/services/access"
	auditsvc "dashboard/internal/services/audit"
	"dashboard/internal/services/boards"
	"dashboard/internal/services/connections"
	"dashboard/internal/services/svcdata"
)

// WidgetType is the widget key the timer actions belong to.
const WidgetType = "kimai_timer"

// ErrNotTimer means the placement is not a Kimai timer tile.
var ErrNotTimer = errors.New("timer.not_timer")

// Action is what a tile button asks for.
type Action string

const (
	ActionStart Action = "start"
	ActionStop  Action = "stop"
)

// Run starts (project, activity) or stops (sheet) a timer behind a tile.
func Run(ctx context.Context, d *sql.DB, who *access.Principal, placementID int64, action Action, project, activity, sheet int64, ip string) error {
	conn, secret, err := target(d, who, placementID)
	if err != nil {
		return err
	}
	switch action {
	case ActionStart:
		if project <= 0 || activity <= 0 {
			return ErrNotTimer
		}
		err = outbound.KimaiStart(ctx, conn.URL, secret, conn.VerifyTLS, project, activity)
	case ActionStop:
		if sheet <= 0 {
			return ErrNotTimer
		}
		err = outbound.KimaiStop(ctx, conn.URL, secret, conn.VerifyTLS, sheet)
	default:
		return ErrNotTimer
	}
	if err != nil {
		return err
	}
	svcdata.Forget(conn.ID)
	return auditsvc.Log(d, &who.UserID, "kimai."+string(action), strconv.FormatInt(max(project, sheet), 10), ip, nil)
}

// target resolves the tile's connection and the viewer's secret for it.
func target(d *sql.DB, who *access.Principal, placementID int64) (*model.Connection, string, error) {
	w, err := boards.PlacedWidget(d, who, placementID)
	if err != nil {
		return nil, "", err
	}
	if w.Type != WidgetType || w.ConnectionID == nil {
		return nil, "", ErrNotTimer
	}

	// Seeing a board is not enough to act: the viewer must use the connection.
	if _, err := connections.Get(d, who, *w.ConnectionID); err != nil {
		return nil, "", err
	}
	conn, err := connections.ByID(d, *w.ConnectionID)
	if err != nil {
		return nil, "", err
	}
	secret, err := svcdata.Secret(d, conn, who.UserID)
	return conn, secret, err
}
