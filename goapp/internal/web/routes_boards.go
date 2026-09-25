package web

import (
	"errors"
	"net/http"
	"strconv"

	"dashboard/internal/services/boards"
	"dashboard/internal/services/util"
)

// RegisterBoardRoutes wires the home page and board view.
func (d Deps) RegisterBoardRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", d.handleHome)
	mux.HandleFunc("GET /boards/{id}", d.handleBoardView)
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

	_ = Page(w, ctx, "board", http.StatusOK, map[string]any{"Board": view, "NavBoards": navBoards})
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
