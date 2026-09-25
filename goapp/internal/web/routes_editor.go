package web

import (
	"encoding/json"
	"net/http"
	"strconv"

	"dashboard/internal/enums"
	"dashboard/internal/services/access"
	"dashboard/internal/services/boards"
	"dashboard/internal/services/widgetlib"
	"dashboard/internal/widgets"
)

// RegisterEditorRoutes wires board settings, sections, the widget library
// and placement.
func (d Deps) RegisterEditorRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /boards/{id}/settings", d.handleBoardSettingsForm)
	mux.HandleFunc("POST /boards/{id}/settings", d.handleBoardRename)
	mux.HandleFunc("POST /boards/{id}/delete", d.handleBoardDelete)
	mux.HandleFunc("POST /boards/{id}/sections", d.handleSectionAdd)
	mux.HandleFunc("POST /sections/{id}/edit", d.handleSectionEdit)
	mux.HandleFunc("POST /sections/{id}/delete", d.handleSectionDelete)
	mux.HandleFunc("POST /boards/{boardID}/sections/{sectionID}/place", d.handlePlace)
	mux.HandleFunc("POST /placements/{id}/unplace", d.handleUnplace)

	mux.HandleFunc("GET /widgets", d.handleWidgetLibrary)
	mux.HandleFunc("GET /widgets/new", d.handleWidgetNewForm)
	mux.HandleFunc("POST /widgets", d.handleWidgetCreate)
	mux.HandleFunc("GET /widgets/{id}/edit", d.handleWidgetEditForm)
	mux.HandleFunc("POST /widgets/{id}/edit", d.handleWidgetUpdate)
	mux.HandleFunc("POST /widgets/{id}/delete", d.handleWidgetDelete)
}

func (d Deps) handleBoardSettingsForm(w http.ResponseWriter, r *http.Request) {
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
	_ = Page(w, ctx, "board_settings", http.StatusOK, map[string]any{"Board": view})
}

func (d Deps) handleBoardRename(w http.ResponseWriter, r *http.Request) {
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
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	version, _ := strconv.Atoi(r.FormValue("version"))
	if err := boards.Rename(d.DB, ctx.Who, id, version, r.FormValue("name"), nil, nil); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/boards/"+r.PathValue("id"), http.StatusSeeOther)
}

func (d Deps) handleBoardDelete(w http.ResponseWriter, r *http.Request) {
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
	if err := boards.Delete(d.DB, ctx.Who, id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (d Deps) handleSectionAdd(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	boardID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	version, _ := strconv.Atoi(r.FormValue("version"))
	if _, err := boards.AddSection(d.DB, ctx.Who, boardID, version, r.FormValue("title")); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/boards/"+r.PathValue("id"), http.StatusSeeOther)
}

func (d Deps) handleSectionEdit(w http.ResponseWriter, r *http.Request) {
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
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	version, _ := strconv.Atoi(r.FormValue("version"))
	title := r.FormValue("title")
	size := enums.TileSize(r.FormValue("size"))
	sortOrder := enums.SortOrder(r.FormValue("sort"))
	area := r.FormValue("area")
	changes := boards.SectionChanges{Title: &title, Size: &size, Sort: &sortOrder, Area: &area}

	boardID := r.FormValue("board_id")
	if err := boards.EditSection(d.DB, ctx.Who, id, version, changes); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/boards/"+boardID, http.StatusSeeOther)
}

func (d Deps) handleSectionDelete(w http.ResponseWriter, r *http.Request) {
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
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	version, _ := strconv.Atoi(r.FormValue("version"))
	boardID := r.FormValue("board_id")
	if err := boards.DeleteSection(d.DB, ctx.Who, id, version); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/boards/"+boardID, http.StatusSeeOther)
}

func (d Deps) handlePlace(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	boardID, err := strconv.ParseInt(r.PathValue("boardID"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	sectionID, err := strconv.ParseInt(r.PathValue("sectionID"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	widgetID, _ := strconv.ParseInt(r.FormValue("widget_id"), 10, 64)
	version, _ := strconv.Atoi(r.FormValue("version"))
	if _, err := boards.Place(d.DB, ctx.Who, sectionID, widgetID, version); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/boards/"+strconv.FormatInt(boardID, 10), http.StatusSeeOther)
}

func (d Deps) handleUnplace(w http.ResponseWriter, r *http.Request) {
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
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	version, _ := strconv.Atoi(r.FormValue("version"))
	boardID := r.FormValue("board_id")
	if err := boards.Unplace(d.DB, ctx.Who, id, version); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/boards/"+boardID, http.StatusSeeOther)
}

// ── Widget library ──

func (d Deps) handleWidgetLibrary(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	lib, err := widgetlib.Library(d.DB, ctx.Who)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = Page(w, ctx, "widgets", http.StatusOK, map[string]any{"Widgets": lib})
}

func (d Deps) handleWidgetNewForm(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	_ = Page(w, ctx, "widget_form", http.StatusOK, map[string]any{
		"Spaces": access.EditableSpaces(ctx.Who), "Types": widgets.AllTypes(), "IsNew": true,
	})
}

func (d Deps) handleWidgetCreate(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	spaceID, _ := strconv.ParseInt(r.FormValue("space_id"), 10, 64)
	config, err := parseConfigJSON(r.FormValue("config"))
	if err != nil {
		_ = Page(w, ctx, "widget_form", http.StatusBadRequest, map[string]any{
			"Spaces": access.EditableSpaces(ctx.Who), "Types": widgets.AllTypes(), "IsNew": true, "Error": err.Error(),
		})
		return
	}

	id, err := widgetlib.Create(d.DB, ctx.Who, spaceID, r.FormValue("type"), r.FormValue("title"), config, nil, nil)
	if err != nil {
		_ = Page(w, ctx, "widget_form", http.StatusBadRequest, map[string]any{
			"Spaces": access.EditableSpaces(ctx.Who), "Types": widgets.AllTypes(), "IsNew": true, "Error": err.Error(),
		})
		return
	}
	http.Redirect(w, r, "/widgets/"+strconv.FormatInt(id, 10)+"/edit", http.StatusSeeOther)
}

func parseConfigJSON(raw string) (map[string]any, error) {
	if raw == "" {
		return map[string]any{}, nil
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (d Deps) handleWidgetEditForm(w http.ResponseWriter, r *http.Request) {
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
	widget, _, err := widgetlib.Detail(d.DB, ctx.Who, id)
	if err != nil {
		d.handleBoardError(w, r, err)
		return
	}
	configJSON, _ := json.MarshalIndent(widget.Config, "", "  ")
	_ = Page(w, ctx, "widget_form", http.StatusOK, map[string]any{
		"Widget": widget, "ConfigJSON": string(configJSON), "Types": widgets.AllTypes(), "IsNew": false,
	})
}

func (d Deps) handleWidgetUpdate(w http.ResponseWriter, r *http.Request) {
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
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	version, _ := strconv.Atoi(r.FormValue("version"))
	config, err := parseConfigJSON(r.FormValue("config"))
	if err == nil {
		err = widgetlib.Update(d.DB, ctx.Who, id, version, r.FormValue("title"), config, nil, nil)
	}
	if err != nil {
		widget, _, _ := widgetlib.Detail(d.DB, ctx.Who, id)
		_ = Page(w, ctx, "widget_form", http.StatusBadRequest, map[string]any{
			"Widget": widget, "ConfigJSON": r.FormValue("config"), "Types": widgets.AllTypes(),
			"IsNew": false, "Error": err.Error(),
		})
		return
	}
	http.Redirect(w, r, "/widgets", http.StatusSeeOther)
}

func (d Deps) handleWidgetDelete(w http.ResponseWriter, r *http.Request) {
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
	if err := widgetlib.Delete(d.DB, ctx.Who, id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/widgets", http.StatusSeeOther)
}
