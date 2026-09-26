package boards

// Bulk edit of several tiles at once (edit mode):
//
//	move    placements → end of another section
//	color   link tiles → theme color (widget config)
//	remove  placements off the board (widgets stay in the library)

import (
	"database/sql"
	"errors"
	"time"

	"dashboard/internal/db"
	"dashboard/internal/enums"
	"dashboard/internal/model"
	"dashboard/internal/repos/content"
	"dashboard/internal/services/access"
)

// BulkAction is what happens to the chosen tiles.
type BulkAction string

const (
	BulkMove   BulkAction = "move"
	BulkColor  BulkAction = "color"
	BulkRemove BulkAction = "remove"
)

// ErrBulk means an unknown action or an empty selection.
var ErrBulk = errors.New("board.bulk_invalid")

// BulkChange carries the action's target.
type BulkChange struct {
	Action    BulkAction
	SectionID int64  // move
	Color     string // color, "" clears
}

// Bulk applies one action to several placements of a board.
func Bulk(d *sql.DB, who *access.Principal, boardID int64, version int, placements []int64, change BulkChange) error {
	if len(placements) == 0 {
		return ErrBulk
	}
	return db.WithTx(d, func(tx *sql.Tx) error {
		board, err := load(tx, who, boardID, enums.RightEdit)
		if err != nil {
			return err
		}
		known := map[int64]model.Placement{}
		sections := map[int64]int{}
		for _, sec := range board.Sections {
			sections[sec.ID] = len(sec.Placements)
			for _, p := range sec.Placements {
				known[p.ID] = p
			}
		}
		for _, id := range placements {
			if _, ok := known[id]; !ok {
				return ErrNotFound
			}
		}
		if err := bump(board, version); err != nil {
			return err
		}

		switch change.Action {
		case BulkRemove:
			for _, id := range placements {
				if err := content.RemovePlacement(tx, id); err != nil {
					return err
				}
			}
		case BulkMove:
			next, ok := sections[change.SectionID]
			if !ok {
				return ErrNotFound
			}
			for _, id := range placements {
				if err := content.UpdatePlacementPosition(tx, id, change.SectionID, next); err != nil {
					return err
				}
				next++
			}
		case BulkColor:
			if err := recolor(tx, who, board, known, placements, sectionColor(change.Color)); err != nil {
				return err
			}
		default:
			return ErrBulk
		}

		board.UpdatedAt = time.Now().UTC()
		if err := content.UpdateBoard(tx, board); err != nil {
			return err
		}
		return snapshot(tx, who, board)
	})
}

// recolor sets the color of the chosen link tiles the caller may edit.
func recolor(tx *sql.Tx, who *access.Principal, board *model.Board, known map[int64]model.Placement, placements []int64, color string) error {
	for _, id := range placements {
		w := known[id].Widget
		if w == nil || w.Type != linkType {
			continue
		}
		granted, err := seenRight(tx, who, w, board)
		if err != nil {
			return err
		}
		if granted < enums.RightEdit {
			continue
		}
		config := map[string]any{}
		for k, v := range w.Config {
			config[k] = v
		}
		if color == "" {
			delete(config, "color")
		} else {
			config["color"] = color
		}
		w.Config = config
		w.Version++
		w.UpdatedAt = time.Now().UTC()
		if err := content.UpdateWidget(tx, w); err != nil {
			return err
		}
	}
	return nil
}

// Duplicate copies a board (sections and placements, same widgets) into
// its own space under a new name and returns the new board's id.
func Duplicate(d *sql.DB, who *access.Principal, boardID int64, name string) (int64, error) {
	src, err := func() (*model.Board, error) {
		var b *model.Board
		err := db.WithTx(d, func(tx *sql.Tx) error {
			var err error
			b, err = load(tx, who, boardID, enums.RightView)
			return err
		})
		return b, err
	}()
	if err != nil {
		return 0, err
	}
	id, err := Create(d, who, src.SpaceID, name)
	if err != nil {
		return 0, err
	}
	err = db.WithTx(d, func(tx *sql.Tx) error {
		copyBoard, err := load(tx, who, id, enums.RightEdit)
		if err != nil {
			return err
		}
		for _, empty := range copyBoard.Sections {
			if err := content.RemoveSection(tx, empty.ID); err != nil {
				return err
			}
		}
		for _, sec := range src.Sections {
			clone := sec
			clone.ID, clone.BoardID, clone.Placements = 0, id, nil
			if err := content.AddSection(tx, &clone); err != nil {
				return err
			}
			for _, p := range sec.Placements {
				if err := content.AddPlacement(tx, &model.Placement{SectionID: clone.ID, WidgetID: p.WidgetID, Position: p.Position, Rows: p.Rows}); err != nil {
					return err
				}
			}
		}
		fresh, err := load(tx, who, id, enums.RightEdit)
		if err != nil {
			return err
		}
		return snapshot(tx, who, fresh)
	})
	return id, err
}
