// Package widgetlib is the widget library: create, change, delete widgets.
// Loading a widget's live data (running its queries against a source) is a
// separate, not-yet-ported concern (app/services/widgets.py's other half);
// this package only manages the widget rows themselves.
//
//	placement ──► widget ──► type.Queries(config) ──► (not yet: svcdata.Get)
//	    │            │
//	 board right   widget right (view)
package widgetlib

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"dashboard/internal/db"
	"dashboard/internal/enums"
	"dashboard/internal/model"
	"dashboard/internal/repos/content"
	"dashboard/internal/services/access"
	"dashboard/internal/services/util"
	"dashboard/internal/widgets"
)

var (
	ErrNotFound         = util.ErrNotFound
	ErrConflict         = util.ErrConflict
	ErrDenied           = access.ErrDenied
	ErrUnknownType      = errors.New("widgetlib: unknown widget type")
	ErrConnRequired     = errors.New("widgetlib: connection required")
	ErrConnMissing      = errors.New("widgetlib: connection not found")
	ErrConnWrongService = errors.New("widgetlib: connection is for a different service")
)

// Ref is a widget library entry.
type Ref struct {
	ID           int64
	Key          string
	Type         string
	Title        string
	Space        access.SpaceRef
	ConnectionID *int64
	Uses         int
	CanEdit      bool
}

func widgetRight(q db.Queryer, who *access.Principal, w *model.Widget) (enums.Right, error) {
	space, err := access.SpaceOf(q, who, w.SpaceID)
	if err != nil {
		return enums.RightNone, err
	}
	return access.Right(who, enums.ResourceWidget, w.ID, space, w.MinTeamRole), nil
}

// Library lists the widgets who may at least USE: their own spaces plus
// explicitly shared ones.
func Library(d *sql.DB, who *access.Principal) ([]Ref, error) {
	var out []Ref
	err := db.WithTx(d, func(tx *sql.Tx) error {
		spaceIDs := make([]int64, 0, len(who.Spaces))
		for id := range who.Spaces {
			spaceIDs = append(spaceIDs, id)
		}
		found, err := content.Widgets(tx, spaceIDs)
		if err != nil {
			return err
		}
		for _, id := range access.GrantedResourceIDs(who, enums.ResourceWidget) {
			w, err := content.Widget(tx, id)
			if err != nil {
				return err
			}
			if w != nil {
				if _, already := who.Spaces[w.SpaceID]; !already {
					found = append(found, w)
				}
			}
		}

		for _, w := range found {
			granted, err := widgetRight(tx, who, w)
			if err != nil {
				return err
			}
			if granted < enums.RightUse {
				continue
			}
			space, err := access.SpaceOf(tx, who, w.SpaceID)
			if err != nil {
				return err
			}
			uses, err := content.WidgetUses(tx, w.ID)
			if err != nil {
				return err
			}
			ref := Ref{ID: w.ID, Key: w.Key, Type: w.Type, Title: w.Title, ConnectionID: w.ConnectionID,
				Uses: uses, CanEdit: granted >= enums.RightEdit}
			if space != nil {
				ref.Space = *space
			}
			out = append(out, ref)
		}
		return nil
	})
	return out, err
}

func checkConnection(q db.Queryer, who *access.Principal, connID *int64, typeKey string) error {
	kind, ok := widgets.Get(typeKey)
	if !ok {
		return ErrUnknownType
	}
	if connID == nil {
		if kind.Service != "" {
			return ErrConnRequired
		}
		return nil
	}
	conn, err := content.Connection(q, *connID)
	if err != nil {
		return err
	}
	if conn == nil {
		return ErrConnMissing
	}
	space, err := access.SpaceOf(q, who, conn.SpaceID)
	if err != nil {
		return err
	}
	if err := access.Need(access.Right(who, enums.ResourceConnection, conn.ID, space, nil), enums.RightUse); err != nil {
		return err
	}
	if kind.Service != "" && enums.ServiceType(conn.Service) != kind.Service {
		return ErrConnWrongService
	}
	return nil
}

// Create adds a new widget to a space. Requires EDIT on the space.
func Create(d *sql.DB, who *access.Principal, spaceID int64, typeKey, title string, config map[string]any,
	connID *int64, minRole *enums.TeamRole) (int64, error) {
	if _, ok := widgets.Get(typeKey); !ok {
		return 0, ErrUnknownType
	}
	var id int64
	err := db.WithTx(d, func(tx *sql.Tx) error {
		space, err := access.SpaceOf(tx, who, spaceID)
		if err != nil {
			return err
		}
		if err := access.Need(access.SpaceRight(who, space), enums.RightEdit); err != nil {
			return err
		}
		if err := checkConnection(tx, who, connID, typeKey); err != nil {
			return err
		}

		existing, err := content.Widgets(tx, []int64{spaceID})
		if err != nil {
			return err
		}
		taken := map[string]bool{}
		for _, w := range existing {
			taken[w.Key] = true
		}
		label := strings.TrimSpace(title)

		widget := &model.Widget{
			SpaceID: spaceID, Key: util.Unique(util.Slug(firstNonEmpty(label, typeKey), typeKey), taken),
			Type: typeKey, Title: label, Config: config, ConnectionID: connID, MinTeamRole: minRole,
			Version: 1, UpdatedAt: time.Now().UTC(),
		}
		if err := content.AddWidget(tx, widget); err != nil {
			return err
		}
		id = widget.ID
		return snapshot(tx, who, widget)
	})
	return id, err
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// Detail returns a widget and the caller's right on it. Requires VIEW.
func Detail(d *sql.DB, who *access.Principal, widgetID int64) (*model.Widget, enums.Right, error) {
	var w *model.Widget
	var granted enums.Right
	err := db.WithTx(d, func(tx *sql.Tx) error {
		item, err := content.Widget(tx, widgetID)
		if err != nil {
			return err
		}
		if item == nil {
			return ErrNotFound
		}
		g, err := widgetRight(tx, who, item)
		if err != nil {
			return err
		}
		if err := access.Need(g, enums.RightView); err != nil {
			return err
		}
		w, granted = item, g
		return nil
	})
	return w, granted, err
}

// Update changes a widget's title/config/connection/min-role. Requires EDIT.
func Update(d *sql.DB, who *access.Principal, widgetID int64, version int, title string, config map[string]any,
	connID *int64, minRole *enums.TeamRole) error {
	return db.WithTx(d, func(tx *sql.Tx) error {
		widget, err := content.Widget(tx, widgetID)
		if err != nil {
			return err
		}
		if widget == nil {
			return ErrNotFound
		}
		g, err := widgetRight(tx, who, widget)
		if err != nil {
			return err
		}
		if err := access.Need(g, enums.RightEdit); err != nil {
			return err
		}
		if widget.Version != version {
			return ErrConflict
		}
		if err := checkConnection(tx, who, connID, widget.Type); err != nil {
			return err
		}

		widget.Title = strings.TrimSpace(title)
		widget.Config = config
		widget.ConnectionID = connID
		widget.MinTeamRole = minRole
		widget.Version++
		widget.UpdatedAt = time.Now().UTC()
		if err := content.UpdateWidget(tx, widget); err != nil {
			return err
		}
		return snapshot(tx, who, widget)
	})
}

// Copy creates an independent copy of a widget in spaceID ("keep as copy").
func Copy(d *sql.DB, who *access.Principal, widgetID, spaceID int64) (int64, error) {
	w, _, err := Detail(d, who, widgetID)
	if err != nil {
		return 0, err
	}
	return Create(d, who, spaceID, w.Type, w.Title, w.Config, w.ConnectionID, nil)
}

// Delete removes a widget. Requires MANAGE.
func Delete(d *sql.DB, who *access.Principal, widgetID int64) error {
	return db.WithTx(d, func(tx *sql.Tx) error {
		widget, err := content.Widget(tx, widgetID)
		if err != nil || widget == nil {
			return err
		}
		g, err := widgetRight(tx, who, widget)
		if err != nil {
			return err
		}
		if err := access.Need(g, enums.RightManage); err != nil {
			return err
		}
		return content.RemoveWidget(tx, widget.ID)
	})
}

// snapshot stores a revision of a widget's own fields. Simplification vs.
// app/services/porting.py: this is the widget's own JSON, not the
// cross-space YAML shape porting.widget_dict produces (see the same note
// on services/boards.snapshot).
func snapshot(q db.Queryer, who *access.Principal, w *model.Widget) error {
	raw, err := json.Marshal(map[string]any{"title": w.Title, "type": w.Type, "config": w.Config})
	if err != nil {
		return err
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return err
	}
	rev := &model.Revision{
		Kind: enums.RevisionWidget, EntityID: w.ID, SpaceID: w.SpaceID, UserID: &who.UserID,
		Version: w.Version, Data: data, CreatedAt: time.Now().UTC(),
	}
	if err := content.AddRevision(q, rev); err != nil {
		return err
	}
	return content.PruneRevisions(q, enums.RevisionWidget, w.ID)
}
