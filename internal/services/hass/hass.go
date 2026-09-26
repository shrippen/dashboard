// Package hass switches Home Assistant entities from a board.
//
//	board tile ──POST toggle──► Toggle: widget visible? entity in the
//	widget's list? USE on the connection? ──► outbound.HassToggle
package hass

import (
	"context"
	"database/sql"
	"errors"

	"andon/internal/outbound"
	"andon/internal/services/access"
	auditsvc "andon/internal/services/audit"
	"andon/internal/services/boards"
	"andon/internal/services/connections"
	"andon/internal/services/svcdata"
	"andon/internal/widgets"
)

// ErrNotSwitchable means the entity is not a switchable entry of the widget.
var ErrNotSwitchable = errors.New("hass.not_switchable")

// Toggle switches entityID on the Home Assistant behind a placed widget.
func Toggle(ctx context.Context, d *sql.DB, who *access.Principal, placementID int64, entityID, ip string) error {
	w, err := boards.PlacedWidget(d, who, placementID)
	if err != nil {
		return err
	}
	cfg, _ := widgets.Decode(w.Type, w.Config)
	if w.ConnectionID == nil || !widgets.HassToggleable(cfg, entityID) {
		return ErrNotSwitchable
	}

	// Seeing a board is not enough to act: the viewer must be allowed to
	// use the connection itself.
	if _, err := connections.Get(d, who, *w.ConnectionID); err != nil {
		return err
	}
	conn, err := connections.ByID(d, *w.ConnectionID)
	if err != nil {
		return err
	}
	secret, err := svcdata.Secret(d, conn, who.UserID)
	if err != nil {
		return err
	}
	if err := outbound.HassToggle(ctx, outbound.Target{URL: conn.URL, Token: secret, VerifyTLS: conn.VerifyTLS}, entityID); err != nil {
		return err
	}
	svcdata.Forget(conn.ID)
	return auditsvc.Log(d, &who.UserID, "hass.toggle", entityID, ip, nil)
}
