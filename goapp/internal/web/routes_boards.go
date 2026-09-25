package web

import (
	"errors"
	"net/http"
	"sort"

	"dashboard/internal/repos/content"
)

// boardRow is the minimal board summary this placeholder page shows. Full
// widget rendering needs the boards/widgets service (a later task).
type boardRow struct {
	ID        int64
	Name      string
	SpaceName string
}

// RegisterBoardRoutes wires the (placeholder) home page.
func (d Deps) RegisterBoardRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /{$}", d.handleHome)
}

func (d Deps) handleHome(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}

	var spaceIDs []int64
	for id := range ctx.Who.Spaces {
		spaceIDs = append(spaceIDs, id)
	}

	boards, err := content.Boards(d.DB, spaceIDs)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	rows := make([]boardRow, 0, len(boards))
	for _, b := range boards {
		rows = append(rows, boardRow{ID: b.ID, Name: b.Name, SpaceName: ctx.Who.Spaces[b.SpaceID].Name})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })

	_ = Page(w, ctx, "boards", http.StatusOK, map[string]any{"Boards": rows})
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
