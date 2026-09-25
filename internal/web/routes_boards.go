package web

import (
	"errors"
	"net/http"
	"strconv"

	"dashboard/internal/services/boards"
	"dashboard/internal/services/svcdata"
	"dashboard/internal/services/themes"
	"dashboard/internal/services/util"
	"dashboard/internal/services/widgetlib"
	"dashboard/internal/widgets"
)

// RegisterBoardRoutes wires the home page, board view and widget fragments.
func (d Deps) RegisterBoardRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", d.handleHome)
	mux.HandleFunc("GET /boards/{id}", d.handleBoardView)
	mux.HandleFunc("GET /widget-fragments/{id}", d.handleWidgetFragment)
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
	navBoards, err := boards.Visible(d.DB, ctx.Who)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	var boardTheme *int64
	if view.ThemeID != nil {
		boardTheme = view.ThemeID
	}
	themeID, err := themes.Active(d.DB, ctx.Who, boardTheme, &view.Space.ID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_, themeVersion, err := themes.Stylesheet(d.DB, themeID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	themeURL := "/theme/" + strconv.FormatInt(themeID, 10) + ".css?v=" + strconv.Itoa(themeVersion)

	var library []widgetlib.Ref
	if view.CanEdit {
		library, err = widgetlib.Library(d.DB, ctx.Who)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	_ = Page(w, ctx, "board", http.StatusOK, map[string]any{
		"Board": view, "NavBoards": navBoards, "ThemeURL": themeURL, "Library": library,
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
	frag, err := boards.Fragment(r.Context(), d.DB, ctx.Who, id, fresh)
	if err != nil {
		d.handleBoardError(w, r, err)
		return
	}

	kind, ok := widgets.Get(frag.Type)
	if !ok {
		http.NotFound(w, r)
		return
	}
	_ = Page(w, ctx, kind.Template, http.StatusOK, map[string]any{"Frag": frag})
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
