package web

import (
	"encoding/json"
	"net/http"
	"strconv"

	"dashboard/internal/i18n"
	"dashboard/internal/services/boards"
)

// RegisterStartPageRoutes wires the start page conveniences: undo, add a
// link by URL, click counting and the command palette's data.
func (d Deps) RegisterStartPageRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /boards/{id}/undo", d.handleUndo)
	mux.HandleFunc("POST /sections/{id}/quick-link", d.handleQuickLink)
	mux.HandleFunc("POST /clicks/{id}", d.handleClick)
	mux.HandleFunc("GET /palette.json", d.handlePalette)
}

// palettePages are the app pages the palette offers, by catalog key.
var palettePages = []struct{ key, url string }{
	{"nav.hints", "/hints"}, {"nav.billing", "/billing"}, {"nav.connections", "/connections"}, {"nav.library", "/widgets"},
	{"nav.themes", "/themes"}, {"nav.space_settings", "/spaces/settings"}, {"nav.notify", "/me/notify"}, {"nav.teams", "/teams"},
	{"nav.import", "/import"}, {"nav.security", "/me/security"},
}

func (d Deps) handleUndo(w http.ResponseWriter, r *http.Request) {
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
	if err := boards.Undo(d.DB, ctx.Who, id); err != nil {
		d.handleBoardError(w, r, err)
		return
	}
	http.Redirect(w, r, "/boards/"+r.PathValue("id")+"?edit", http.StatusSeeOther)
}

func (d Deps) handleQuickLink(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	section, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	version, _ := strconv.Atoi(r.FormValue("version"))
	if _, err := boards.QuickLink(r.Context(), d.DB, ctx.Who, section, version, r.FormValue("url")); err != nil {
		d.handleBoardError(w, r, err)
		return
	}
	http.Redirect(w, r, "/boards/"+r.FormValue("board_id")+"?edit&undo", http.StatusSeeOther)
}

// handleClick counts a tile click (sent with navigator.sendBeacon).
func (d Deps) handleClick(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err == nil {
		_ = boards.Click(d.DB, ctx.Who, id)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (d Deps) handlePalette(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	items, err := boards.Palette(d.DB, ctx.Who)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for _, p := range palettePages {
		items = append(items, boards.PaletteItem{Kind: boards.PalettePage, Title: i18n.T(p.key, ctx.Locale, nil), URL: p.url})
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(items)
}
