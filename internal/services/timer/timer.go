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
	ActionStart  Action = "start"
	ActionStop   Action = "stop"
	ActionSwitch Action = "switch"
)

// Request is what a tile button asks for: start (project, activity),
// stop (sheet, with an optional note as description) or switch (stop
// sheet, then start the pair).
type Request struct {
	Action            Action
	Project, Activity int64
	Sheet             int64
	Note              string
}

// Run starts, stops or switches a timer behind a tile.
func Run(ctx context.Context, d *sql.DB, who *access.Principal, placementID int64, req Request, ip string) error {
	conn, secret, err := target(d, who, placementID)
	if err != nil {
		return err
	}
	stop := func() error {
		if req.Sheet <= 0 {
			return ErrNotTimer
		}
		if req.Note != "" {
			if err := outbound.KimaiDescribe(ctx, conn.URL, secret, conn.VerifyTLS, req.Sheet, req.Note); err != nil {
				return err
			}
		}
		return outbound.KimaiStop(ctx, conn.URL, secret, conn.VerifyTLS, req.Sheet)
	}
	start := func() error {
		if req.Project <= 0 || req.Activity <= 0 {
			return ErrNotTimer
		}
		return outbound.KimaiStart(ctx, conn.URL, secret, conn.VerifyTLS, req.Project, req.Activity)
	}

	switch req.Action {
	case ActionStart:
		err = start()
	case ActionStop:
		err = stop()
	case ActionSwitch:
		if err = stop(); err == nil {
			err = start()
		}
	default:
		return ErrNotTimer
	}
	if err != nil {
		return err
	}
	svcdata.Forget(conn.ID)
	return auditsvc.Log(d, &who.UserID, "kimai."+string(req.Action), strconv.FormatInt(max(req.Project, req.Sheet), 10), ip, nil)
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
