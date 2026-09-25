// Package boards is what a user sees, and how editors change layouts.
//
//	Board ─ Section ─ Placement → Widget          (shared structure)
//	                     ▲
//	Overlay(user, board): order, hidden, collapsed, size   (personal layer)
//
// Editors (EDIT on the board) change the board itself. Everybody else
// dragging tiles around changes only their overlay.
package boards

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"dashboard/internal/db"
	"dashboard/internal/enums"
	"dashboard/internal/model"
	"dashboard/internal/repos/content"
	"dashboard/internal/repos/misc"
	"dashboard/internal/services/access"
	"dashboard/internal/services/icons"
	"dashboard/internal/services/spaces"
	"dashboard/internal/services/svcdata"
	"dashboard/internal/services/util"
	"dashboard/internal/services/widgetlib"
	"dashboard/internal/widgets"
)

var areas = []string{"main", "side"}

const startSlug = "start"

// LayoutTarget says where Arrange stored a drag-and-drop result.
type LayoutTarget string

const (
	LayoutBoard   LayoutTarget = "board"
	LayoutOverlay LayoutTarget = "overlay"
)

var (
	ErrNotFound = util.ErrNotFound
	ErrConflict = util.ErrConflict
	ErrDenied   = access.ErrDenied
)

// Tile is one placed, viewable widget.
type Tile struct {
	PlacementID int64
	WidgetID    int64
	Type        string
	Title       string
	Template    string
	Category    widgets.Category
	Inline      bool
	RefreshS    int
	Config      any
	Hidden      bool
	IconURL     string // link tiles: cached icon, "" = monogram
	IconEmoji   string // link tiles: emoji instead of an image
	IconGlyph   bool   // single-color icon, inverted on dark themes
	Items       []TileItem
}

// TileItem is a link tile's sub-link with its resolved icon.
type TileItem struct {
	Title, URL, IconURL string
}

// SectionView is one section with its visible tiles.
type SectionView struct {
	ID        int64
	Title     string
	Cols      *int
	Size      enums.TileSize
	Sort      enums.SortOrder
	Collapsed bool
	Area      string
	Span      int
	Rows      int
	Color     string
	Mobile    enums.MobileMode
	Tiles     []Tile
}

// BoardView is a full board as rendered for one viewer.
type BoardView struct {
	ID          int64
	Slug        string
	Name        string
	Space       access.SpaceRef
	Version     int
	ThemeID     *int64
	MinTeamRole *enums.TeamRole
	CanEdit     bool
	HasOverlay  bool
	Sections    []SectionView
	Page        spaces.PageInfo
	Frequent    []Tile // the viewer's most clicked links
}

// BoardRef is a lightweight board reference for listings.
type BoardRef struct {
	ID      int64
	Slug    string
	Name    string
	Space   access.SpaceRef
	CanEdit bool
}

// ── Rights ──

func boardRight(q db.Queryer, who *access.Principal, board *model.Board) (enums.Right, error) {
	if who.TokenBoards != nil && !containsID(who.TokenBoards, board.ID) {
		return enums.RightNone, nil
	}
	space, err := access.SpaceOf(q, who, board.SpaceID)
	if err != nil {
		return enums.RightNone, err
	}
	return access.Right(who, enums.ResourceBoard, board.ID, space, board.MinTeamRole), nil
}

func containsID(ids []int64, id int64) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}

func widgetRight(q db.Queryer, who *access.Principal, w *model.Widget) (enums.Right, error) {
	space, err := access.SpaceOf(q, who, w.SpaceID)
	if err != nil {
		return enums.RightNone, err
	}
	return access.Right(who, enums.ResourceWidget, w.ID, space, w.MinTeamRole), nil
}

// seenRight is the viewing right on a placed widget: a board shared with
// somebody shows its own space's widgets to them, but not widgets from
// other spaces even if the board happens to reference one.
func seenRight(q db.Queryer, who *access.Principal, w *model.Widget, board *model.Board) (enums.Right, error) {
	granted, err := widgetRight(q, who, w)
	if err != nil {
		return enums.RightNone, err
	}
	if w.SpaceID == board.SpaceID && w.MinTeamRole == nil {
		br, err := boardRight(q, who, board)
		if err != nil {
			return enums.RightNone, err
		}
		if br > enums.RightView {
			br = enums.RightView
		}
		if br > granted {
			granted = br
		}
	}
	return granted, nil
}

func load(q db.Queryer, who *access.Principal, boardID int64, required enums.Right) (*model.Board, error) {
	board, err := content.Board(q, boardID)
	if err != nil {
		return nil, err
	}
	if board == nil {
		return nil, ErrNotFound
	}
	granted, err := boardRight(q, who, board)
	if err != nil {
		return nil, err
	}
	if err := access.Need(granted, required); err != nil {
		return nil, err
	}
	return board, nil
}

// ── Listing ──

// Visible lists the boards who may at least VIEW, personal spaces first.
func Visible(d *sql.DB, who *access.Principal) ([]BoardRef, error) {
	var out []BoardRef
	err := db.WithTx(d, func(tx *sql.Tx) error {
		spaceIDs := make([]int64, 0, len(who.Spaces))
		for id := range who.Spaces {
			spaceIDs = append(spaceIDs, id)
		}
		found, err := content.Boards(tx, spaceIDs)
		if err != nil {
			return err
		}
		for _, id := range access.GrantedResourceIDs(who, enums.ResourceBoard) {
			if _, inOwn := who.Spaces[id]; inOwn {
				continue
			}
			b, err := content.Board(tx, id)
			if err != nil {
				return err
			}
			if b != nil {
				if _, already := who.Spaces[b.SpaceID]; !already {
					found = append(found, b)
				}
			}
		}

		order := map[enums.SpaceKind]int{enums.SpacePersonal: 0, enums.SpaceTeam: 1, enums.SpaceInstance: 2}
		for _, board := range found {
			if board.IsTemplate {
				continue
			}
			granted, err := boardRight(tx, who, board)
			if err != nil {
				return err
			}
			if granted < enums.RightView {
				continue
			}
			space, err := access.SpaceOf(tx, who, board.SpaceID)
			if err != nil {
				return err
			}
			ref := BoardRef{ID: board.ID, Slug: board.Slug, Name: board.Name, CanEdit: granted >= enums.RightEdit}
			if space != nil {
				ref.Space = *space
			}
			out = append(out, ref)
		}
		sort.SliceStable(out, func(i, j int) bool { return order[out[i].Space.Kind] < order[out[j].Space.Kind] })
		return nil
	})
	return out, err
}

// StartBoard returns the user's start board id, creating an empty personal
// one on first visit.
func StartBoard(d *sql.DB, who *access.Principal, preferred *int64) (int64, error) {
	listed, err := Visible(d, who)
	if err != nil {
		return 0, err
	}
	if preferred != nil {
		for _, b := range listed {
			if b.ID == *preferred {
				return *preferred, nil
			}
		}
	}
	if len(listed) > 0 {
		return listed[0].ID, nil
	}

	personal := access.Personal(who)
	if personal == nil {
		return 0, ErrNotFound
	}
	return Create(d, who, personal.ID, "Start")
}

// ── View ──

// View renders a board for who, applying their personal overlay.
func View(d *sql.DB, who *access.Principal, boardID int64) (*BoardView, error) {
	var out *BoardView
	err := db.WithTx(d, func(tx *sql.Tx) error {
		board, err := load(tx, who, boardID, enums.RightView)
		if err != nil {
			return err
		}
		granted, err := boardRight(tx, who, board)
		if err != nil {
			return err
		}
		overlay, err := content.Overlay(tx, who.UserID, board.ID)
		if err != nil {
			return err
		}
		layer := map[string]any{}
		if overlay != nil {
			layer = overlay.Data
		}
		space, err := access.SpaceOf(tx, who, board.SpaceID)
		if err != nil {
			return err
		}
		view := &BoardView{
			ID: board.ID, Slug: board.Slug, Name: board.Name, Version: board.Version, ThemeID: board.ThemeID,
			CanEdit: granted >= enums.RightEdit, HasOverlay: len(layer) > 0,
		}
		if space != nil {
			view.Space = *space
		}
		if sp, err := content.Space(tx, board.SpaceID); err == nil && sp != nil {
			view.Page = spaces.PageOf(sp.Settings)
		}
		for _, section := range board.Sections {
			sv, err := viewSection(tx, who, section, board, layer)
			if err != nil {
				return err
			}
			view.Sections = append(view.Sections, sv)
		}
		if view.Frequent, err = frequent(tx, who, view.Sections); err != nil {
			return err
		}
		out = view
		return nil
	})
	return out, err
}

func viewSection(q db.Queryer, who *access.Principal, section model.Section, board *model.Board, layer map[string]any) (SectionView, error) {
	key := strconv.FormatInt(section.ID, 10)
	size := section.Size
	if sizes, ok := layer["size"].(map[string]any); ok {
		if v, ok := sizes[key].(string); ok {
			size = enums.TileSize(v)
		}
	}
	collapsed := section.Collapsed
	if collapsedMap, ok := layer["collapsed"].(map[string]any); ok {
		if v, ok := collapsedMap[key].(bool); ok {
			collapsed = v
		}
	}
	area := section.Area
	if !containsStr(areas, area) {
		area = areas[0]
	}

	view := SectionView{ID: section.ID, Title: section.Title, Cols: section.Cols, Size: size, Sort: section.Sort,
		Collapsed: collapsed, Area: area, Span: section.Span, Rows: section.Rows, Color: section.Color, Mobile: section.Mobile}

	hidden := map[int64]bool{}
	if hiddenList, ok := layer["hidden"].([]any); ok {
		for _, v := range hiddenList {
			hidden[int64FromAny(v)] = true
		}
	}

	placements := append([]model.Placement(nil), section.Placements...)
	if orderMap, ok := layer["order"].(map[string]any); ok {
		if order, ok := orderMap[key].([]any); ok {
			rank := map[int64]int{}
			for i, v := range order {
				rank[int64FromAny(v)] = i
			}
			sort.SliceStable(placements, func(i, j int) bool {
				ri, iok := rank[placements[i].ID]
				rj, jok := rank[placements[j].ID]
				if !iok {
					ri = len(rank) + placements[i].Position
				}
				if !jok {
					rj = len(rank) + placements[j].Position
				}
				return ri < rj
			})
		}
	}

	for _, placement := range placements {
		w := placement.Widget
		if w == nil {
			continue
		}
		kind, ok := widgets.Get(w.Type)
		if !ok {
			continue
		}
		granted, err := seenRight(q, who, w, board)
		if err != nil {
			return SectionView{}, err
		}
		if granted < enums.RightView {
			continue
		}
		cfg, _ := widgets.Decode(w.Type, w.Config)
		tile := Tile{
			PlacementID: placement.ID, WidgetID: w.ID, Type: w.Type, Title: w.Title, Template: kind.Template,
			Category: kind.Category, Inline: kind.Inline, RefreshS: kind.RefreshS, Config: cfg, Hidden: hidden[placement.ID],
		}
		if link, ok := cfg.(widgets.LinkConfig); ok {
			tile.IconEmoji = icons.Emoji(link.Icon)
			tile.IconGlyph = icons.Glyph(link.Icon)
			if tile.IconEmoji == "" {
				tile.IconURL = icons.URL(link.Icon, link.URL)
			}
			for _, item := range link.Items {
				tile.Items = append(tile.Items, TileItem{Title: item.Title, URL: item.URL, IconURL: icons.URL(item.Icon, item.URL)})
			}
		}
		view.Tiles = append(view.Tiles, tile)
	}

	if view.Sort == enums.SortAlphabetical {
		sort.SliceStable(view.Tiles, func(i, j int) bool {
			return strings.ToLower(view.Tiles[i].Title) < strings.ToLower(view.Tiles[j].Title)
		})
	}
	return view, nil
}

func containsStr(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func int64FromAny(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case string:
		i, _ := strconv.ParseInt(n, 10, 64)
		return i
	default:
		return 0
	}
}

// PlacedWidget returns the widget behind a placement, checking view rights.
func PlacedWidget(d *sql.DB, who *access.Principal, placementID int64) (*model.Widget, error) {
	var w *model.Widget
	err := db.WithTx(d, func(tx *sql.Tx) error {
		placement, err := content.Placement(tx, placementID)
		if err != nil {
			return err
		}
		if placement == nil {
			return ErrNotFound
		}
		section, err := content.Section(tx, placement.SectionID)
		if err != nil || section == nil {
			return orNotFound(err)
		}
		board, err := load(tx, who, section.BoardID, enums.RightView)
		if err != nil {
			return err
		}
		granted, err := seenRight(tx, who, placement.Widget, board)
		if err != nil {
			return err
		}
		if err := access.Need(granted, enums.RightView); err != nil {
			return err
		}
		w = placement.Widget
		return nil
	})
	return w, err
}

// Fragment loads one placed widget's live data for lazy tile rendering
// (the board page's own render only shows title/type; the tile then
// hx-gets this to fill in).
func Fragment(ctx context.Context, d *sql.DB, who *access.Principal, placementID int64, fresh svcdata.Freshness) (*widgetlib.Fragment, error) {
	w, err := PlacedWidget(d, who, placementID)
	if err != nil {
		return nil, err
	}
	return widgetlib.Load(ctx, d, who, w, fresh)
}

func orNotFound(err error) error {
	if err != nil {
		return err
	}
	return ErrNotFound
}

// ── Board changes (EDIT) ──

// Create adds a board (with one empty section) to a space. Requires EDIT.
func Create(d *sql.DB, who *access.Principal, spaceID int64, name string) (int64, error) {
	var id int64
	err := db.WithTx(d, func(tx *sql.Tx) error {
		space, err := access.SpaceOf(tx, who, spaceID)
		if err != nil {
			return err
		}
		if err := access.Need(access.SpaceRight(who, space), enums.RightEdit); err != nil {
			return err
		}
		existing, err := content.Boards(tx, []int64{spaceID})
		if err != nil {
			return err
		}
		taken := map[string]bool{}
		for _, b := range existing {
			taken[b.Slug] = true
		}
		label := strings.TrimSpace(name)
		if label == "" {
			label = "Board"
		}
		board := &model.Board{
			SpaceID: spaceID, Slug: util.Unique(util.Slug(name, startSlug), taken), Name: label,
			Position: len(taken), Version: 1, UpdatedAt: time.Now().UTC(),
		}
		if err := content.AddBoard(tx, board); err != nil {
			return err
		}
		if err := content.AddSection(tx, &model.Section{
			BoardID: board.ID, Size: enums.TileMedium, Sort: enums.SortManual, Area: areas[0],
		}); err != nil {
			return err
		}
		id = board.ID
		return snapshot(tx, who, board)
	})
	return id, err
}

// Rename updates a board's name/theme/team restriction.
func Rename(d *sql.DB, who *access.Principal, boardID int64, version int, name string, themeID *int64, minRole *enums.TeamRole) error {
	return db.WithTx(d, func(tx *sql.Tx) error {
		board, err := load(tx, who, boardID, enums.RightEdit)
		if err != nil {
			return err
		}
		if err := bump(board, version); err != nil {
			return err
		}
		if n := strings.TrimSpace(name); n != "" {
			board.Name = n
		}
		board.ThemeID = themeID
		board.MinTeamRole = minRole
		board.UpdatedAt = time.Now().UTC()
		if err := content.UpdateBoard(tx, board); err != nil {
			return err
		}
		return snapshot(tx, who, board)
	})
}

// Delete removes a board and its shares. Requires MANAGE.
func Delete(d *sql.DB, who *access.Principal, boardID int64) error {
	return db.WithTx(d, func(tx *sql.Tx) error {
		board, err := load(tx, who, boardID, enums.RightManage)
		if err != nil {
			return err
		}
		if err := misc.DropShares(tx, enums.ResourceBoard, board.ID); err != nil {
			return err
		}
		return content.RemoveBoard(tx, board.ID)
	})
}

func bump(board *model.Board, version int) error {
	if board.Version != version {
		return ErrConflict
	}
	board.Version++
	return nil
}

// AddSection appends a new section to a board.
func AddSection(d *sql.DB, who *access.Principal, boardID int64, version int, title string) (int64, error) {
	var id int64
	err := db.WithTx(d, func(tx *sql.Tx) error {
		board, err := load(tx, who, boardID, enums.RightEdit)
		if err != nil {
			return err
		}
		if err := bump(board, version); err != nil {
			return err
		}
		section := &model.Section{
			BoardID: board.ID, Title: strings.TrimSpace(title), Position: len(board.Sections),
			Size: enums.TileMedium, Sort: enums.SortManual, Area: areas[0],
		}
		if err := content.AddSection(tx, section); err != nil {
			return err
		}
		id = section.ID
		board.UpdatedAt = time.Now().UTC()
		if err := content.UpdateBoard(tx, board); err != nil {
			return err
		}
		return snapshot(tx, who, board)
	})
	return id, err
}

// SectionChanges lists the mutable fields EditSection may set.
type SectionChanges struct {
	Title     *string
	Cols      **int
	Size      *enums.TileSize
	Sort      *enums.SortOrder
	Collapsed *bool
	Area      *string
	Span      *int
	Rows      *int
	Color     *string
	Mobile    *enums.MobileMode
}

// EditSection applies changes to one section.
func EditSection(d *sql.DB, who *access.Principal, sectionID int64, version int, changes SectionChanges) error {
	return db.WithTx(d, func(tx *sql.Tx) error {
		section, err := content.Section(tx, sectionID)
		if err != nil {
			return err
		}
		if section == nil {
			return ErrNotFound
		}
		board, err := load(tx, who, section.BoardID, enums.RightEdit)
		if err != nil {
			return err
		}
		if err := bump(board, version); err != nil {
			return err
		}
		if changes.Title != nil {
			section.Title = *changes.Title
		}
		if changes.Cols != nil {
			section.Cols = *changes.Cols
		}
		if changes.Size != nil {
			section.Size = *changes.Size
		}
		if changes.Sort != nil {
			section.Sort = *changes.Sort
		}
		if changes.Collapsed != nil {
			section.Collapsed = *changes.Collapsed
		}
		if changes.Area != nil {
			section.Area = *changes.Area
		}
		if changes.Span != nil {
			section.Span = clampLayout(*changes.Span, MaxSpan)
		}
		if changes.Rows != nil {
			section.Rows = clampLayout(*changes.Rows, MaxRows)
		}
		if changes.Color != nil {
			section.Color = sectionColor(*changes.Color)
		}
		if changes.Mobile != nil {
			section.Mobile = mobileMode(*changes.Mobile)
		}
		if err := content.UpdateSection(tx, section); err != nil {
			return err
		}
		board.UpdatedAt = time.Now().UTC()
		if err := content.UpdateBoard(tx, board); err != nil {
			return err
		}
		return snapshot(tx, who, board)
	})
}

// DeleteSection removes a section and its placements.
func DeleteSection(d *sql.DB, who *access.Principal, sectionID int64, version int) error {
	return db.WithTx(d, func(tx *sql.Tx) error {
		section, err := content.Section(tx, sectionID)
		if err != nil || section == nil {
			return err
		}
		board, err := load(tx, who, section.BoardID, enums.RightEdit)
		if err != nil {
			return err
		}
		if err := bump(board, version); err != nil {
			return err
		}
		if err := content.RemoveSection(tx, section.ID); err != nil {
			return err
		}
		board.UpdatedAt = time.Now().UTC()
		if err := content.UpdateBoard(tx, board); err != nil {
			return err
		}
		return snapshot(tx, who, board)
	})
}

// Place puts a library widget on a board (needs USE on the widget).
func Place(d *sql.DB, who *access.Principal, sectionID, widgetID int64, version int) (int64, error) {
	var id int64
	err := db.WithTx(d, func(tx *sql.Tx) error {
		section, err := content.Section(tx, sectionID)
		if err != nil {
			return err
		}
		if section == nil {
			return ErrNotFound
		}
		board, err := load(tx, who, section.BoardID, enums.RightEdit)
		if err != nil {
			return err
		}
		widget, err := content.Widget(tx, widgetID)
		if err != nil {
			return err
		}
		if widget == nil {
			return ErrNotFound
		}
		granted, err := widgetRight(tx, who, widget)
		if err != nil {
			return err
		}
		if err := access.Need(granted, enums.RightUse); err != nil {
			return err
		}
		if err := bump(board, version); err != nil {
			return err
		}
		placement := &model.Placement{SectionID: section.ID, WidgetID: widget.ID, Position: len(section.Placements)}
		if err := content.AddPlacement(tx, placement); err != nil {
			return err
		}
		id = placement.ID
		board.UpdatedAt = time.Now().UTC()
		if err := content.UpdateBoard(tx, board); err != nil {
			return err
		}
		return snapshot(tx, who, board)
	})
	return id, err
}

// Unplace removes a widget from a board.
func Unplace(d *sql.DB, who *access.Principal, placementID int64, version int) error {
	return db.WithTx(d, func(tx *sql.Tx) error {
		placement, err := content.Placement(tx, placementID)
		if err != nil || placement == nil {
			return err
		}
		section, err := content.Section(tx, placement.SectionID)
		if err != nil || section == nil {
			return orNotFound(err)
		}
		board, err := load(tx, who, section.BoardID, enums.RightEdit)
		if err != nil {
			return err
		}
		if err := bump(board, version); err != nil {
			return err
		}
		if err := content.RemovePlacement(tx, placement.ID); err != nil {
			return err
		}
		board.UpdatedAt = time.Now().UTC()
		if err := content.UpdateBoard(tx, board); err != nil {
			return err
		}
		return snapshot(tx, who, board)
	})
}

// Arrange applies a drag-and-drop result {section_id: [placement ids]}.
// Editors reorder the board itself (tiles may move between sections);
// everybody else stores the order in their own overlay (within a section).
func Arrange(d *sql.DB, who *access.Principal, boardID int64, version int, layout map[int64][]int64) (LayoutTarget, error) {
	var target LayoutTarget
	err := db.WithTx(d, func(tx *sql.Tx) error {
		board, err := load(tx, who, boardID, enums.RightView)
		if err != nil {
			return err
		}
		known := map[int64]model.Placement{}
		sections := map[int64]bool{}
		for _, sec := range board.Sections {
			sections[sec.ID] = true
			for _, p := range sec.Placements {
				known[p.ID] = p
			}
		}
		for sid, pids := range layout {
			if !sections[sid] {
				return ErrNotFound
			}
			for _, pid := range pids {
				if _, ok := known[pid]; !ok {
					return ErrNotFound
				}
			}
		}

		granted, err := boardRight(tx, who, board)
		if err != nil {
			return err
		}
		if granted >= enums.RightEdit {
			if err := bump(board, version); err != nil {
				return err
			}
			for sectionID, pids := range layout {
				for index, pid := range pids {
					if err := content.UpdatePlacementPosition(tx, pid, sectionID, index); err != nil {
						return err
					}
				}
			}
			board.UpdatedAt = time.Now().UTC()
			if err := content.UpdateBoard(tx, board); err != nil {
				return err
			}
			target = LayoutBoard
			return snapshot(tx, who, board)
		}

		layer, err := overlayOf(tx, who, board.ID)
		if err != nil {
			return err
		}
		data := layer.Data
		orderRaw, _ := data["order"].(map[string]any)
		if orderRaw == nil {
			orderRaw = map[string]any{}
		}
		for sectionID, pids := range layout {
			var kept []any
			for _, pid := range pids {
				if known[pid].SectionID == sectionID {
					kept = append(kept, pid)
				}
			}
			orderRaw[strconv.FormatInt(sectionID, 10)] = kept
		}
		data["order"] = orderRaw
		target = LayoutOverlay
		return content.SetOverlay(tx, who.UserID, board.ID, data)
	})
	return target, err
}

// ── Personal overlay ──

func overlayOf(q db.Queryer, who *access.Principal, boardID int64) (*model.Overlay, error) {
	found, err := content.Overlay(q, who.UserID, boardID)
	if err != nil {
		return nil, err
	}
	if found != nil {
		return found, nil
	}
	if err := content.SetOverlay(q, who.UserID, boardID, map[string]any{}); err != nil {
		return nil, err
	}
	return &model.Overlay{UserID: who.UserID, BoardID: boardID, Data: map[string]any{}}, nil
}

// Fold is the collapsed state a section's overlay may hold.
type Fold string

const (
	FoldOpen   Fold = "open"
	FoldClosed Fold = "closed"
)

// Visibility is the hidden state a placement's overlay may hold.
type Visibility string

const (
	VisShown  Visibility = "shown"
	VisHidden Visibility = "hidden"
)

func setLayer(d *sql.DB, who *access.Principal, boardID int64, key string, id int64, value any) error {
	return db.WithTx(d, func(tx *sql.Tx) error {
		if _, err := load(tx, who, boardID, enums.RightView); err != nil {
			return err
		}
		overlay, err := content.Overlay(tx, who.UserID, boardID)
		if err != nil {
			return err
		}
		data := map[string]any{}
		if overlay != nil {
			data = overlay.Data
		}

		if key == "hidden" {
			hidden := map[int64]bool{}
			if list, ok := data["hidden"].([]any); ok {
				for _, v := range list {
					hidden[int64FromAny(v)] = true
				}
			}
			if b, _ := value.(bool); b {
				hidden[id] = true
			} else {
				delete(hidden, id)
			}
			ids := make([]int64, 0, len(hidden))
			for k := range hidden {
				ids = append(ids, k)
			}
			sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
			list := make([]any, len(ids))
			for i, v := range ids {
				list[i] = v
			}
			data["hidden"] = list
		} else {
			entry, _ := data[key].(map[string]any)
			if entry == nil {
				entry = map[string]any{}
			}
			entry[strconv.FormatInt(id, 10)] = value
			data[key] = entry
		}
		return content.SetOverlay(tx, who.UserID, boardID, data)
	})
}

// FoldSection sets a section's collapsed state in the caller's overlay.
func FoldSection(d *sql.DB, who *access.Principal, boardID, sectionID int64, state Fold) error {
	return setLayer(d, who, boardID, "collapsed", sectionID, state == FoldClosed)
}

// ShowTile sets a placement's hidden state in the caller's overlay.
func ShowTile(d *sql.DB, who *access.Principal, boardID, placementID int64, state Visibility) error {
	return setLayer(d, who, boardID, "hidden", placementID, state == VisHidden)
}

// ResizeSection sets a section's tile size in the caller's overlay.
func ResizeSection(d *sql.DB, who *access.Principal, boardID, sectionID int64, size enums.TileSize) error {
	return setLayer(d, who, boardID, "size", sectionID, string(size))
}

// ResetOverlay clears the caller's overlay for a board.
func ResetOverlay(d *sql.DB, who *access.Principal, boardID int64) error {
	return db.WithTx(d, func(tx *sql.Tx) error {
		found, err := content.Overlay(tx, who.UserID, boardID)
		if err != nil || found == nil {
			return err
		}
		return content.RemoveOverlay(tx, who.UserID, boardID)
	})
}

// ── Revisions ──
//
// Simplification vs. the Python service: snapshots store this board's own
// sections/placements as JSON directly, not the cross-space YAML shape
// app/services/porting.py's board_dict produces. Restoring a revision from
// this dashboard's own history works the same either way; only importing a
// revision's widget references from a *different* space (porting.resolve)
// is not implemented yet.

type snapshotSection struct {
	Title     string  `json:"title"`
	Cols      *int    `json:"cols,omitempty"`
	Size      string  `json:"size"`
	Sort      string  `json:"sort"`
	Collapsed bool    `json:"collapsed"`
	Area      string  `json:"area"`
	Span      int     `json:"span,omitempty"`
	Rows      int     `json:"rows,omitempty"`
	Color     string  `json:"color,omitempty"`
	Mobile    string  `json:"mobile,omitempty"`
	Widgets   []int64 `json:"widgets"`
}

type snapshotBoard struct {
	Name     string            `json:"name"`
	Sections []snapshotSection `json:"sections"`
}

func snapshot(q db.Queryer, who *access.Principal, board *model.Board) error {
	fresh, err := content.Board(q, board.ID)
	if err != nil || fresh == nil {
		return orNotFound(err)
	}
	snap := snapshotBoard{Name: fresh.Name}
	for _, sec := range fresh.Sections {
		row := snapshotSection{
			Title: sec.Title, Cols: sec.Cols, Size: string(sec.Size), Sort: string(sec.Sort),
			Collapsed: sec.Collapsed, Area: sec.Area, Span: sec.Span, Rows: sec.Rows, Color: sec.Color, Mobile: string(sec.Mobile),
		}
		for _, p := range sec.Placements {
			row.Widgets = append(row.Widgets, p.WidgetID)
		}
		snap.Sections = append(snap.Sections, row)
	}
	raw, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return err
	}

	rev := &model.Revision{
		Kind: enums.RevisionBoard, EntityID: fresh.ID, SpaceID: fresh.SpaceID, UserID: &who.UserID,
		Version: fresh.Version, Data: data, CreatedAt: time.Now().UTC(),
	}
	if err := content.AddRevision(q, rev); err != nil {
		return err
	}
	return content.PruneRevisions(q, enums.RevisionBoard, fresh.ID)
}

// RevisionView is one stored revision.
type RevisionView struct {
	ID      int64
	Version int
	At      time.Time
	UserID  *int64
	Data    map[string]any
}

// History lists a board's revisions, newest first. Requires EDIT.
func History(d *sql.DB, who *access.Principal, boardID int64) ([]RevisionView, error) {
	var out []RevisionView
	err := db.WithTx(d, func(tx *sql.Tx) error {
		if _, err := load(tx, who, boardID, enums.RightEdit); err != nil {
			return err
		}
		revs, err := content.Revisions(tx, enums.RevisionBoard, boardID)
		if err != nil {
			return err
		}
		for _, r := range revs {
			out = append(out, RevisionView{ID: r.ID, Version: r.Version, At: r.CreatedAt, UserID: r.UserID, Data: r.Data})
		}
		return nil
	})
	return out, err
}

// ErrBadRevision means the given revision doesn't belong to this board.
var ErrBadRevision = errors.New("boards: revision does not match board")

// Restore rebuilds sections and placements from a stored revision. Widget
// references outside this board's own space are skipped (see the
// Revisions doc comment above).
func Restore(d *sql.DB, who *access.Principal, boardID, revisionID int64) error {
	return db.WithTx(d, func(tx *sql.Tx) error {
		board, err := load(tx, who, boardID, enums.RightEdit)
		if err != nil {
			return err
		}
		rev, err := content.RevisionByID(tx, revisionID)
		if err != nil {
			return err
		}
		if rev == nil || rev.EntityID != board.ID || rev.Kind != enums.RevisionBoard {
			return ErrBadRevision
		}

		for _, sec := range board.Sections {
			if err := content.RemoveSection(tx, sec.ID); err != nil {
				return err
			}
		}

		raw, err := json.Marshal(rev.Data)
		if err != nil {
			return err
		}
		var snap snapshotBoard
		if err := json.Unmarshal(raw, &snap); err != nil {
			return err
		}
		if snap.Name != "" {
			board.Name = snap.Name
		}
		board.Version++
		board.UpdatedAt = time.Now().UTC()

		for index, sec := range snap.Sections {
			size := enums.TileSize(sec.Size)
			if size == "" {
				size = enums.TileMedium
			}
			sortOrder := enums.SortOrder(sec.Sort)
			if sortOrder == "" {
				sortOrder = enums.SortManual
			}
			area := sec.Area
			if area == "" {
				area = areas[0]
			}
			newSection := &model.Section{
				BoardID: board.ID, Title: sec.Title, Position: index, Cols: sec.Cols,
				Size: size, Sort: sortOrder, Collapsed: sec.Collapsed, Area: area,
				Span: clampLayout(sec.Span, MaxSpan), Rows: clampLayout(sec.Rows, MaxRows), Color: sectionColor(sec.Color),
				Mobile: mobileMode(enums.MobileMode(sec.Mobile)),
			}
			if err := content.AddSection(tx, newSection); err != nil {
				return err
			}
			for pos, widgetID := range sec.Widgets {
				w, err := content.Widget(tx, widgetID)
				if err != nil {
					return err
				}
				if w == nil || w.SpaceID != board.SpaceID {
					continue // widget gone, or from a space we can't resolve here (see doc comment)
				}
				if err := content.AddPlacement(tx, &model.Placement{
					SectionID: newSection.ID, WidgetID: widgetID, Position: pos,
				}); err != nil {
					return err
				}
			}
		}

		if err := content.UpdateBoard(tx, board); err != nil {
			return err
		}
		return snapshot(tx, who, board)
	})
}

// EnsureEditable raises ErrDenied unless who has EDIT on the board.
func EnsureEditable(d *sql.DB, who *access.Principal, boardID int64) error {
	return db.WithTx(d, func(tx *sql.Tx) error {
		board, err := content.Board(tx, boardID)
		if err != nil {
			return err
		}
		if board == nil {
			return ErrNotFound
		}
		granted, err := boardRight(tx, who, board)
		if err != nil {
			return err
		}
		return access.Need(granted, enums.RightEdit)
	})
}

// Section layout limits: the main column is four quarters wide.
const (
	MaxSpan = 4
	MaxRows = 4
)

// clampLayout keeps span/rows in 0..max; 0 means the default.
func clampLayout(v, max int) int {
	if v < 0 || v > max {
		return 0
	}
	return v
}

// mobileMode accepts only known modes; anything else shows normally.
func mobileMode(raw enums.MobileMode) enums.MobileMode {
	if raw == enums.MobileFirst || raw == enums.MobileHide {
		return raw
	}
	return enums.MobileNormal
}

// sectionColor accepts only theme color names.
func sectionColor(raw string) string {
	for _, c := range widgets.TileColors {
		if string(c) == raw {
			return raw
		}
	}
	return ""
}
