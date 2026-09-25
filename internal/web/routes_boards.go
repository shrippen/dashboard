package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"dashboard/internal/enums"
	"dashboard/internal/services/accounts"
	"dashboard/internal/services/boards"
	"dashboard/internal/services/hass"
	"dashboard/internal/services/svcdata"
	"dashboard/internal/services/util"
	"dashboard/internal/services/widgetlib"
	"dashboard/internal/widgets"
)

const compactView = "compact"

// RegisterBoardRoutes wires the home page, board view, widget fragments
// and the personal layout changes (fold, hide, size, order).
func (d Deps) RegisterBoardRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", d.handleHome)
	mux.HandleFunc("GET /boards/{id}", d.handleBoardView)
	mux.HandleFunc("GET /widget-fragments/{id}", d.handleWidgetFragment)
	mux.HandleFunc("POST /widget-fragments/{id}/toggle", d.handleHassToggle)
	mux.HandleFunc("POST /widget-fragments/{id}/kimai", d.handleKimaiTimer)
	mux.HandleFunc("POST /boards/{id}/arrange", d.handleArrange)
	mux.HandleFunc("POST /boards/{id}/fold/{sectionID}", d.handleFold)
	mux.HandleFunc("POST /boards/{id}/show/{placementID}", d.handleShow)
	mux.HandleFunc("POST /boards/{id}/size/{sectionID}", d.handleSize)
	mux.HandleFunc("POST /boards/{id}/overlay/reset", d.handleOverlayReset)
	mux.HandleFunc("GET /boards/{id}/history", d.handleHistory)
	mux.HandleFunc("POST /boards/{id}/restore/{revisionID}", d.handleRestore)
	mux.HandleFunc("GET /sections/{id}/pick", d.handlePick)
}

func (d Deps) handleHome(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}

	id, err := boards.StartBoard(d.DB, ctx.Who, nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/boards/"+strconv.FormatInt(id, 10), http.StatusSeeOther)
}

func (d Deps) handleBoardView(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	d.renderBoard(w, r, ctx, "")
}

// boardMode reads ?edit, ?layout and ?view=compact.
type boardMode struct{ edit, layer, compact bool }

func modeOf(r *http.Request, canEdit bool) boardMode {
	q := r.URL.Query()
	return boardMode{edit: q.Has("edit") && canEdit, layer: q.Has("layout"), compact: q.Get("view") == compactView}
}

// renderBoard shows board {id}. With an embed token the page drops the
// app nav and edit controls, and fragment URLs carry the token along.
func (d Deps) renderBoard(w http.ResponseWriter, r *http.Request, ctx Ctx, embedToken string) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	view, err := boards.View(d.DB, ctx.Who, id)
	if err != nil {
		d.handleBoardError(w, r, err)
		return
	}
	embed := embedToken != ""
	if embed {
		view.CanEdit = false
	}
	mode := modeOf(r, view.CanEdit)
	searchEngine := ""
	if !embed {
		if profile, err := accounts.GetProfile(d.DB, ctx.Who); err == nil {
			searchEngine = profile.SearchEngine
		}
	}
	navBoards, err := boards.Visible(d.DB, ctx.Who)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	themeURL, err := d.themeURL(ctx.Who, view.ThemeID, &view.Space.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	_ = d.Page(w, ctx, "board", http.StatusOK, map[string]any{
		"Board": view, "NavBoards": navBoards, "ThemeURL": themeURL,
		"Embed": embed, "EmbedToken": embedToken, "SearchEngine": searchEngine,
		"Edit": mode.edit, "LayerEdit": mode.layer && !embed, "Compact": mode.compact,
		"Sizes": []enums.TileSize{enums.TileSmall, enums.TileMedium, enums.TileLarge},
		"Sorts": []enums.SortOrder{enums.SortManual, enums.SortAlphabetical},
		"Areas": []string{"main", "side"},
		"Spans": []int{0, 1, 2, 3}, "RowSpans": []int{1, 2, 3, 4}, "Colors": widgets.TileColors,
	})
}

// handleWidgetFragment renders one placed widget's live data (lazy-loaded
// by the board page via htmx), so a slow source never blocks the page.
func (d Deps) handleWidgetFragment(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Viewer(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}

	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	fresh := svcdata.Cached
	if r.URL.Query().Has("refresh") {
		fresh = svcdata.Force
	}
	d.renderFragment(w, r, ctx, id, fresh)
}

func (d Deps) renderFragment(w http.ResponseWriter, r *http.Request, ctx Ctx, placementID int64, fresh svcdata.Freshness) {
	frag, err := boards.Fragment(r.Context(), d.DB, ctx.Who, placementID, fresh)
	if err != nil {
		d.handleBoardError(w, r, err)
		return
	}

	kind, ok := widgets.Get(frag.Type)
	if !ok {
		http.NotFound(w, r)
		return
	}
	// ThemeURL is irrelevant to a fragment (no <head> here) and would
	// otherwise cost a DB round trip on every htmx refresh.
	_ = d.Page(w, ctx, kind.Template, http.StatusOK, map[string]any{"Frag": frag, "ThemeURL": "", "PlacementID": placementID})
}

// handleHassToggle switches a Home Assistant entity and answers with the
// refreshed tile body (htmx swaps it in place).
func (d Deps) handleHassToggle(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := hass.Toggle(r.Context(), d.DB, ctx.Who, id, r.FormValue("entity"), ClientIP(r)); err != nil {
		if errors.Is(err, hass.ErrNotSwitchable) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		d.handleBoardError(w, r, err)
		return
	}
	d.renderFragment(w, r, ctx, id, svcdata.Force)
}

func (d Deps) handleBoardError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, util.ErrNotFound):
		http.NotFound(w, r)
	case errors.Is(err, boards.ErrDenied):
		http.Error(w, "forbidden", http.StatusForbidden)
	default:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// handleAuthError turns the sentinel errors from Require/Context into the
// right redirect or status code.
func (d Deps) handleAuthError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, ErrTOTPPending):
		http.Redirect(w, r, "/login/totp", http.StatusSeeOther)
	case errors.Is(err, ErrLoginRequired):
		http.Redirect(w, r, "/login", http.StatusSeeOther)
	case errors.Is(err, ErrCSRFFailed):
		http.Error(w, "csrf", http.StatusForbidden)
	default:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// ── Personal layout (overlay) and board order ──

func pathID(r *http.Request, name string) (int64, error) {
	return strconv.ParseInt(r.PathValue(name), 10, 64)
}

func boardPath(id int64) string { return "/boards/" + strconv.FormatInt(id, 10) }

// arrangeRequest is editor.js's payload: {"version": 3, "layout": {"12": [5, 7]}}.
type arrangeRequest struct {
	Version int                `json:"version"`
	Layout  map[string][]int64 `json:"layout"`
}

// handleArrange stores a new tile order: into the board for editors in
// edit mode, else into the caller's own overlay.
func (d Deps) handleArrange(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	id, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	var body arrangeRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxUpload)).Decode(&body); err != nil {
		http.Error(w, "bad json", http.StatusBadRequest)
		return
	}
	layout := make(map[int64][]int64, len(body.Layout))
	for key, placements := range body.Layout {
		sectionID, err := strconv.ParseInt(key, 10, 64)
		if err != nil {
			http.Error(w, "bad section", http.StatusBadRequest)
			return
		}
		layout[sectionID] = placements
	}
	target, err := boards.Arrange(d.DB, ctx.Who, id, body.Version, layout)
	if err != nil {
		d.handleBoardError(w, r, err)
		return
	}
	writeJSON(w, map[string]string{"target": string(target)})
}

// layoutAction runs one overlay change and answers with back.
func (d Deps) layoutAction(w http.ResponseWriter, r *http.Request, child string, run func(Ctx, int64, int64) error, back func(int64) string) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	id, err1 := pathID(r, "id")
	childID, err2 := pathID(r, child)
	if err1 != nil || err2 != nil {
		http.NotFound(w, r)
		return
	}
	if err := run(ctx, id, childID); err != nil {
		d.handleBoardError(w, r, err)
		return
	}
	if back == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, back(id), http.StatusSeeOther)
}

func layoutPage(id int64) string { return boardPath(id) + "?layout" }

func (d Deps) handleFold(w http.ResponseWriter, r *http.Request) {
	d.layoutAction(w, r, "sectionID", func(ctx Ctx, id, sectionID int64) error {
		return boards.FoldSection(d.DB, ctx.Who, id, sectionID, boards.Fold(r.FormValue("state")))
	}, nil)
}

func (d Deps) handleShow(w http.ResponseWriter, r *http.Request) {
	d.layoutAction(w, r, "placementID", func(ctx Ctx, id, placementID int64) error {
		return boards.ShowTile(d.DB, ctx.Who, id, placementID, boards.Visibility(r.FormValue("state")))
	}, layoutPage)
}

func (d Deps) handleSize(w http.ResponseWriter, r *http.Request) {
	d.layoutAction(w, r, "sectionID", func(ctx Ctx, id, sectionID int64) error {
		return boards.ResizeSection(d.DB, ctx.Who, id, sectionID, enums.TileSize(r.FormValue("value")))
	}, layoutPage)
}

func (d Deps) handleOverlayReset(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	id, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := boards.ResetOverlay(d.DB, ctx.Who, id); err != nil {
		d.handleBoardError(w, r, err)
		return
	}
	http.Redirect(w, r, boardPath(id), http.StatusSeeOther)
}

// ── History ──

// revisionRow summarises one revision: "Links (4), Tools (2)".
type revisionRow struct {
	ID       int64
	Version  int
	At       any
	Sections []revisionSection
}

type revisionSection struct {
	Title   string
	Widgets int
}

func (d Deps) handleHistory(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	id, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	view, err := boards.View(d.DB, ctx.Who, id)
	if err != nil {
		d.handleBoardError(w, r, err)
		return
	}
	revs, err := boards.History(d.DB, ctx.Who, id)
	if err != nil {
		d.handleBoardError(w, r, err)
		return
	}

	rows := make([]revisionRow, 0, len(revs))
	for _, rev := range revs {
		row := revisionRow{ID: rev.ID, Version: rev.Version, At: rev.At}
		list, _ := rev.Data["sections"].([]any)
		for _, item := range list {
			sec, _ := item.(map[string]any)
			title, _ := sec["title"].(string)
			placed, _ := sec["widgets"].([]any)
			row.Sections = append(row.Sections, revisionSection{Title: title, Widgets: len(placed)})
		}
		rows = append(rows, row)
	}
	_ = d.Page(w, ctx, "board_history", http.StatusOK, map[string]any{"Board": view, "Revisions": rows})
}

func (d Deps) handleRestore(w http.ResponseWriter, r *http.Request) {
	d.layoutAction(w, r, "revisionID", func(ctx Ctx, id, revisionID int64) error {
		return boards.Restore(d.DB, ctx.Who, id, revisionID)
	}, func(id int64) string { return boardPath(id) + "?edit" })
}

// handlePick lists the library to place an existing widget into a section.
func (d Deps) handlePick(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	sectionID, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	library, err := widgetlib.Library(d.DB, ctx.Who)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = d.Page(w, ctx, "board_pick", http.StatusOK, map[string]any{
		"Library": library, "SectionID": sectionID,
		"BoardID": r.URL.Query().Get("board_id"), "Version": r.URL.Query().Get("version"),
	})
}
