package boards

// Suggested layout: a cold start for a board, built from what its space
// already has.
//
//	widgets of the space ─┐
//	connections without  ─┼─► one section per topic (overview first, links as
//	  a tile → starter    │   a flowing column), current order kept, new
//	no hints tile → hints ┘   tiles last ─► preview ─► apply (undo-able)

import (
	"database/sql"
	"sort"

	"andon/internal/db"
	"andon/internal/enums"
	"andon/internal/i18n"
	"andon/internal/model"
	"andon/internal/repos/content"
	"andon/internal/services/access"
	"andon/internal/services/widgetlib"
	"andon/internal/widgets"
)

const (
	hintsType = "hints"
	// sectionStride ranks a placed widget by section, then position.
	sectionStride = 1 << 16
	// unplaced sorts widgets that are not on the board after placed ones.
	unplaced = 1 << 30
)

// leading are overview tiles that open a board, in this order.
var leading = map[string]int{"greeting": 0, hintsType: 1, "clock": 2}

// SuggestedTile is one tile of a suggestion: an existing widget, or a new
// one of Type (on ConnID) created when the suggestion is applied.
type SuggestedTile struct {
	WidgetID int64
	Type     string
	Title    string
	ConnID   *int64
	ConnName string
}

// New reports whether the tile is created on apply.
func (t SuggestedTile) New() bool { return t.WidgetID == 0 }

// SuggestedSection is one section of a suggestion.
type SuggestedSection struct {
	Title string
	Links bool // link tiles, flowing in newspaper columns
	Tiles []SuggestedTile
}

// Suggestion is a proposed board layout.
type Suggestion struct {
	Version  int
	Sections []SuggestedSection
}

// ranked is a tile with its sort key.
type ranked struct {
	tile SuggestedTile
	rank int
}

// suggest groups a space's widgets (and planned new ones) into sections.
// order gives each widget's position on the current board.
func suggest(ws []*model.Widget, conns []*model.Connection, order map[int64]int, newTiles bool, locale enums.Locale) []SuggestedSection {
	byTopic := map[widgets.Topic][]ranked{}
	var links []ranked
	used := map[int64]bool{}
	hasHints := false

	for i, w := range ws {
		if _, ok := widgets.Get(w.Type); !ok {
			continue
		}
		rank, placed := order[w.ID]
		if !placed {
			rank = unplaced + i
		}
		if w.ConnectionID != nil {
			used[*w.ConnectionID] = true
		}
		hasHints = hasHints || w.Type == hintsType
		t := ranked{SuggestedTile{WidgetID: w.ID, Type: w.Type, Title: w.Title, ConnID: w.ConnectionID}, rank}
		if w.Type == linkType {
			links = append(links, t)
			continue
		}
		topic := widgets.TopicOf(w.Type)
		byTopic[topic] = append(byTopic[topic], t)
	}

	if newTiles {
		// New tiles after all existing ones, connections by name.
		next := 2 * unplaced
		sorted := append([]*model.Connection(nil), conns...)
		sort.SliceStable(sorted, func(a, b int) bool { return sorted[a].Name < sorted[b].Name })
		for _, c := range sorted {
			key, ok := widgets.Starter(enums.ServiceType(c.Service))
			if used[c.ID] || !ok {
				continue
			}
			id := c.ID
			topic := widgets.TopicOf(key)
			byTopic[topic] = append(byTopic[topic], ranked{SuggestedTile{Type: key, ConnID: &id, ConnName: c.Name}, next})
			next++
		}
		if !hasHints {
			byTopic[widgets.TopicOverview] = append(byTopic[widgets.TopicOverview], ranked{SuggestedTile{Type: hintsType}, next})
		}
	}

	var out []SuggestedSection
	for _, topic := range widgets.Topics {
		tiles := byTopic[topic]
		if len(tiles) > 0 {
			out = append(out, SuggestedSection{Title: i18n.T("topic."+string(topic), locale, nil), Tiles: sortTiles(tiles)})
		}
		if topic == widgets.TopicOverview && len(links) > 0 {
			out = append(out, SuggestedSection{Title: i18n.T("suggest.links", locale, nil), Links: true, Tiles: sortTiles(links)})
		}
	}
	return out
}

// sortTiles puts leading overview tiles first, then keeps the given order.
func sortTiles(tiles []ranked) []SuggestedTile {
	sort.SliceStable(tiles, func(a, b int) bool {
		la, aLead := leading[tiles[a].tile.Type]
		lb, bLead := leading[tiles[b].tile.Type]
		if aLead != bLead {
			return aLead
		}
		if aLead && la != lb {
			return la < lb
		}
		return tiles[a].rank < tiles[b].rank
	})
	out := make([]SuggestedTile, len(tiles))
	for i, t := range tiles {
		out[i] = t.tile
	}
	return out
}

// suggestIn reads what a suggestion is built from. New tiles need EDIT on
// the board's space (like adding them from the gallery).
func suggestIn(tx *sql.Tx, who *access.Principal, board *model.Board) ([]SuggestedSection, error) {
	ws, err := content.Widgets(tx, []int64{board.SpaceID})
	if err != nil {
		return nil, err
	}
	conns, err := content.Connections(tx, []int64{board.SpaceID})
	if err != nil {
		return nil, err
	}
	order := map[int64]int{}
	for s, sec := range board.Sections {
		for _, p := range sec.Placements {
			if _, seen := order[p.WidgetID]; !seen {
				order[p.WidgetID] = s*sectionStride + p.Position
			}
		}
	}
	space, err := access.SpaceOf(tx, who, board.SpaceID)
	if err != nil {
		return nil, err
	}
	newTiles := access.SpaceRight(who, space) >= enums.RightEdit
	return suggest(ws, conns, order, newTiles, who.Locale), nil
}

// Suggest proposes a layout for a board without changing anything.
// Requires EDIT.
func Suggest(d *sql.DB, who *access.Principal, boardID int64) (Suggestion, error) {
	var out Suggestion
	err := db.WithRead(d, func(tx *sql.Tx) error {
		board, err := load(tx, who, boardID, enums.RightEdit)
		if err != nil {
			return err
		}
		out.Version = board.Version
		out.Sections, err = suggestIn(tx, who, board)
		return err
	})
	return out, err
}

// ApplySuggestion replaces the board's layout with the suggestion, creating
// its new tiles. One revision, so Undo brings the old layout back.
func ApplySuggestion(d *sql.DB, who *access.Principal, boardID int64, version int) error {
	return db.WithTx(d, func(tx *sql.Tx) error {
		board, err := load(tx, who, boardID, enums.RightEdit)
		if err != nil {
			return err
		}
		if err := bump(board, version); err != nil {
			return err
		}
		sections, err := suggestIn(tx, who, board)
		if err != nil {
			return err
		}

		snap := snapshotBoard{Name: board.Name}
		for _, sec := range sections {
			row := snapshotSection{Title: sec.Title, Size: string(enums.TileMedium), Sort: string(enums.SortManual), Area: areas[0]}
			if sec.Links {
				row.Span = SpanFlow
			}
			for _, tile := range sec.Tiles {
				id := tile.WidgetID
				if tile.New() {
					if id, err = widgetlib.CreateTx(tx, who, board.SpaceID, tile.Type, "", nil, tile.ConnID, nil); err != nil {
						return err
					}
				}
				row.Widgets = append(row.Widgets, id)
			}
			snap.Sections = append(snap.Sections, row)
		}
		if err := rebuild(tx, board, snap); err != nil {
			return err
		}
		return snapshot(tx, who, board)
	})
}
