package web

import (
	"dashboard/internal/model"
	"net/http"
	"strconv"

	"dashboard/internal/enums"
	"dashboard/internal/services/access"
	"dashboard/internal/services/boards"
	"dashboard/internal/services/connections"
	"dashboard/internal/services/themes"
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
	mux.HandleFunc("POST /widget-preview", d.handleWidgetPreview)
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
	themeList, err := themes.Listing(d.DB, ctx.Who)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = d.Page(w, ctx, "board_settings", http.StatusOK, map[string]any{
		"Board": view, "Themes": themeList,
		"TeamRoles": []enums.TeamRole{enums.TeamViewer, enums.TeamEditor, enums.TeamOwner},
	})
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
	var themeID *int64
	if n, err := strconv.ParseInt(r.FormValue("theme_id"), 10, 64); err == nil {
		themeID = &n
	}
	if err := boards.Rename(d.DB, ctx.Who, id, version, r.FormValue("name"), themeID, minRole(r)); err != nil {
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
	http.Redirect(w, r, "/boards/"+r.PathValue("id")+"?edit", http.StatusSeeOther)
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
	collapsed := r.FormValue("collapsed") != ""
	var cols *int
	if n, err := strconv.Atoi(r.FormValue("cols")); err == nil && n > 0 {
		cols = &n
	}
	span, _ := strconv.Atoi(r.FormValue("span"))
	rows, _ := strconv.Atoi(r.FormValue("rows"))
	color := r.FormValue("color")
	mobile := enums.MobileMode(r.FormValue("mobile"))
	changes := boards.SectionChanges{Title: &title, Size: &size, Sort: &sortOrder, Area: &area,
		Collapsed: &collapsed, Cols: &cols, Span: &span, Rows: &rows, Color: &color, Mobile: &mobile}

	boardID := r.FormValue("board_id")
	if err := boards.EditSection(d.DB, ctx.Who, id, version, changes); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/boards/"+boardID+"?edit", http.StatusSeeOther)
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
	http.Redirect(w, r, "/boards/"+boardID+"?edit&undo", http.StatusSeeOther)
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
	http.Redirect(w, r, "/boards/"+strconv.FormatInt(boardID, 10)+"?edit", http.StatusSeeOther)
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
	http.Redirect(w, r, "/boards/"+boardID+"?edit&undo", http.StatusSeeOther)
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
	_ = d.Page(w, ctx, "widgets", http.StatusOK, map[string]any{"Widgets": lib, "Spaces": access.EditableSpaces(ctx.Who)})
}

// widgetTarget is where a new widget goes after saving: a board section
// (placed right away) or just the library.
type widgetTarget struct {
	SpaceID, SectionID, BoardID int64
	Version                     int
	Place                       bool
}

func targetOf(get func(string) string) widgetTarget {
	num := func(k string) int64 { n, _ := strconv.ParseInt(get(k), 10, 64); return n }
	version, err := strconv.Atoi(get("version"))
	t := widgetTarget{SpaceID: num("space"), SectionID: num("section"), BoardID: num("board"), Version: version}
	if t.SpaceID == 0 {
		t.SpaceID = num("space_id")
	}
	if t.SectionID == 0 {
		t.SectionID = num("section_id")
	}
	if t.BoardID == 0 {
		t.BoardID = num("board_id")
	}
	t.Place = t.SectionID > 0 && t.BoardID > 0 && err == nil
	return t
}

func (t widgetTarget) Back() string {
	if t.BoardID > 0 {
		return "/boards/" + strconv.FormatInt(t.BoardID, 10) + "?edit"
	}
	return "/widgets"
}

const linkType = "link"

type widgetForm struct {
	Kind    widgets.WidgetType
	Title   string
	Config  map[string]any
	ConnID  *int64
	MinRole string
	Widget  *model.Widget
	Target  widgetTarget
	Error   string
}

func (d Deps) widgetFormPage(w http.ResponseWriter, ctx Ctx, status int, f widgetForm) {
	conns, err := connections.Listing(d.DB, ctx.Who, enums.RightUse)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var matching []connections.View
	for _, c := range conns {
		if f.Kind.Service == "" || c.Service == f.Kind.Service {
			matching = append(matching, c)
		}
	}
	_ = d.Page(w, ctx, "widget_form", status, map[string]any{
		"Kind": f.Kind, "Title": f.Title, "Fields": widgets.FormValues(f.Kind.Key, f.Config),
		"Conns": matching, "AllConns": conns, "ConnID": f.ConnID, "MinRole": f.MinRole,
		"Widget": f.Widget, "Target": f.Target, "Error": f.Error,
		"NeedsConn":  f.Kind.Service != "" || f.Kind.Category == widgets.CategoryInsight,
		"TeamRoles":  []enums.TeamRole{enums.TeamViewer, enums.TeamEditor, enums.TeamOwner},
		"Categories": []widgets.Category{widgets.CategoryStart, widgets.CategoryInsight},
	})
}

func (d Deps) handleWidgetNewForm(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	target := targetOf(r.URL.Query().Get)
	spaces := access.EditableSpaces(ctx.Who)
	if target.SpaceID == 0 && len(spaces) > 0 {
		target.SpaceID = spaces[0].ID
	}
	kind, ok := widgets.Get(r.URL.Query().Get("type"))
	if !ok {
		_ = d.Page(w, ctx, "widget_types", http.StatusOK, map[string]any{
			"Types": widgets.AllTypes(), "Target": target, "Spaces": spaces,
			"Categories": []widgets.Category{widgets.CategoryStart, widgets.CategoryInsight},
		})
		return
	}
	d.widgetFormPage(w, ctx, http.StatusOK, widgetForm{Kind: kind, Target: target})
}

// connectionID parses the widget form's optional "connection_id" field
// ("" means no connection).
func connectionID(r *http.Request) *int64 {
	raw := r.FormValue("connection_id")
	if raw == "" {
		return nil
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return nil
	}
	return &id
}

func minRole(r *http.Request) *enums.TeamRole {
	raw := r.FormValue("min_role")
	if raw == "" {
		return nil
	}
	role := enums.TeamRole(raw)
	return &role
}

func (d Deps) handleWidgetCreate(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	kind, ok := widgets.Get(r.FormValue("type"))
	if !ok {
		http.Error(w, "widget.unknown_type", http.StatusBadRequest)
		return
	}
	target := targetOf(r.FormValue)
	config := widgets.ParseForm(kind.Key, r.FormValue)
	title := r.FormValue("title")

	id, err := widgetlib.Create(d.DB, ctx.Who, target.SpaceID, kind.Key, title, config, connectionID(r), minRole(r))
	if err != nil {
		d.widgetFormPage(w, ctx, http.StatusBadRequest, widgetForm{Kind: kind, Title: title, Config: config,
			ConnID: connectionID(r), MinRole: r.FormValue("min_role"), Target: target, Error: errKey(err)})
		return
	}
	if target.Place {
		if _, err := boards.Place(d.DB, ctx.Who, target.SectionID, id, target.Version); err != nil {
			d.handleBoardError(w, r, err)
			return
		}
	}
	http.Redirect(w, r, target.Back(), http.StatusSeeOther)
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
	widget, granted, err := widgetlib.Detail(d.DB, ctx.Who, id)
	if err != nil {
		d.handleBoardError(w, r, err)
		return
	}
	if granted < enums.RightEdit {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	kind, _ := widgets.Get(widget.Type)
	role := ""
	if widget.MinTeamRole != nil {
		role = string(*widget.MinTeamRole)
	}
	target := targetOf(r.URL.Query().Get)
	target.SpaceID = widget.SpaceID
	d.widgetFormPage(w, ctx, http.StatusOK, widgetForm{Kind: kind, Title: widget.Title, Config: widget.Config,
		ConnID: widget.ConnectionID, MinRole: role, Widget: widget, Target: target})
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
	widget, _, err := widgetlib.Detail(d.DB, ctx.Who, id)
	if err != nil {
		d.handleBoardError(w, r, err)
		return
	}
	kind, _ := widgets.Get(widget.Type)
	config := widgets.ParseForm(kind.Key, r.FormValue)
	version, _ := strconv.Atoi(r.FormValue("widget_version"))
	title := r.FormValue("title")
	target := targetOf(r.FormValue)

	if err := widgetlib.Update(d.DB, ctx.Who, id, version, title, config, connectionID(r), minRole(r)); err != nil {
		d.widgetFormPage(w, ctx, http.StatusBadRequest, widgetForm{Kind: kind, Title: title, Config: config,
			ConnID: connectionID(r), MinRole: r.FormValue("min_role"), Widget: widget, Target: target, Error: errKey(err)})
		return
	}
	http.Redirect(w, r, target.Back(), http.StatusSeeOther)
}

// handleWidgetPreview renders the form's current state (htmx, unsaved).
func (d Deps) handleWidgetPreview(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	kind, ok := widgets.Get(r.FormValue("type"))
	if !ok {
		http.Error(w, "widget.unknown_type", http.StatusBadRequest)
		return
	}
	space, _ := strconv.ParseInt(r.FormValue("space_id"), 10, 64)
	config := widgets.ParseForm(kind.Key, r.FormValue)
	frag, err := widgetlib.Preview(r.Context(), d.DB, ctx.Who, space, kind.Key, r.FormValue("title"), config, connectionID(r))
	if err != nil {
		_ = d.Page(w, ctx, "widget_preview_error", http.StatusOK, map[string]any{"Error": errKey(err), "ThemeURL": ""})
		return
	}
	name := kind.Template
	if kind.Key == linkType {
		name = "widget_preview"
	}
	_ = d.Page(w, ctx, name, http.StatusOK, map[string]any{"Frag": frag, "Kind": kind, "ThemeURL": ""})
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
