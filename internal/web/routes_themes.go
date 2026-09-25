package web

import (
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"dashboard/internal/enums"
	"dashboard/internal/services/access"
	"dashboard/internal/services/themes"
)

const (
	cssType       = "text/css; charset=utf-8"
	cacheForever  = "public, max-age=31536000, immutable"
	maxUpload     = 10 << 20
	modeDark      = "dark"
	modeLight     = "light"
	formFileField = "file"
)

// tokenGroups orders the editor's token table (display only).
var tokenGroups = []struct {
	Key   string
	Names []string
}{
	{"background", []string{"--bg-void", "--bg-hard", "--bg0", "--bg-panel", "--bg1", "--bg2", "--nav-bg"}},
	{"text", []string{"--fg0", "--fg1", "--fg2", "--fg3", "--accent"}},
	{"semantic", []string{"--blue", "--blue-hover", "--aqua", "--green", "--yellow", "--orange", "--red", "--purple"}},
	{"roles", []string{"--field", "--score", "--hl", "--scrim", "--shadow"}},
	{"shape", []string{"--radius", "--chamfer", "--gutter", "--max-w", "--max-w-wide"}},
	{"fonts", []string{"--font-heading", "--font-sans", "--font-mono"}},
}

var hexColor = regexp.MustCompile(`^#([0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)

// RegisterThemeRoutes wires stylesheets, fonts, the theme list and editor,
// ZIP import/export and the styleguide.
func (d Deps) RegisterThemeRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /theme/{idcss}", d.handleThemeCSS)
	mux.HandleFunc("GET /theme-fonts/{id}/{name}", d.handleThemeFont)
	mux.HandleFunc("GET /themes", d.handleThemeList)
	mux.HandleFunc("POST /themes/duplicate", d.handleThemeDuplicate)
	mux.HandleFunc("POST /themes/import", d.handleThemeImport)
	mux.HandleFunc("POST /themes/preset", d.handleThemePreset)
	mux.HandleFunc("GET /themes/{id}", d.handleThemeEdit)
	mux.HandleFunc("POST /themes/{id}", d.handleThemeSave)
	mux.HandleFunc("POST /themes/{id}/delete", d.handleThemeDelete)
	mux.HandleFunc("GET /themes/{id}/export", d.handleThemeExport)
	mux.HandleFunc("POST /themes/{id}/fonts", d.handleFontUpload)
	mux.HandleFunc("POST /themes/{id}/fonts/{name}/delete", d.handleFontDelete)
	mux.HandleFunc("GET /styleguide", d.handleStyleguide)
}

// handleThemeCSS serves one theme's rendered stylesheet. No auth: it's a
// <link> target, cached hard by the version in its own query string.
func (d Deps) handleThemeCSS(w http.ResponseWriter, r *http.Request) {
	idStr := strings.TrimSuffix(r.PathValue("idcss"), ".css")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	css, _, err := themes.Stylesheet(d.DB, id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", cssType)
	w.Header().Set("Cache-Control", cacheForever)
	w.Write([]byte(css))
}

// handleThemeFont serves an uploaded theme font (public, like the CSS).
func (d Deps) handleThemeFont(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	data, contentType, err := themes.Font(d.DB, id, r.PathValue("name"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", cacheForever)
	w.Write(data)
}

func (d Deps) themeListPage(w http.ResponseWriter, ctx Ctx, status int, extra map[string]any) {
	items, err := themes.Listing(d.DB, ctx.Who)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	values := map[string]any{"Items": items, "Spaces": access.EditableSpaces(ctx.Who), "Presets": themes.Presets()}
	for k, v := range extra {
		values[k] = v
	}
	_ = d.Page(w, ctx, "themes", status, values)
}

func (d Deps) handleThemeList(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	d.themeListPage(w, ctx, http.StatusOK, nil)
}

func themeID(r *http.Request) (int64, error) { return strconv.ParseInt(r.PathValue("id"), 10, 64) }

func themeURL(id int64) string { return "/themes/" + strconv.FormatInt(id, 10) }

func (d Deps) handleThemeDuplicate(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	source, err1 := strconv.ParseInt(r.FormValue("theme_id"), 10, 64)
	space, err2 := strconv.ParseInt(r.FormValue("space_id"), 10, 64)
	if err1 != nil || err2 != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	newID, err := themes.Duplicate(d.DB, ctx.Who, source, space, r.FormValue("name"))
	if err != nil {
		d.themeListPage(w, ctx, http.StatusBadRequest, map[string]any{"Error": errKey(err)})
		return
	}
	http.Redirect(w, r, themeURL(newID), http.StatusSeeOther)
}

// handleThemePreset creates a theme from a Dashy preset.
func (d Deps) handleThemePreset(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	space, err := strconv.ParseInt(r.FormValue("space_id"), 10, 64)
	if err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	newID, _, err := themes.FromPreset(d.DB, ctx.Who, space, r.FormValue("preset"))
	if err != nil {
		d.themeListPage(w, ctx, http.StatusBadRequest, map[string]any{"Error": errKey(err)})
		return
	}
	http.Redirect(w, r, themeURL(newID), http.StatusSeeOther)
}

func (d Deps) handleThemeImport(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload)
	if err := r.ParseMultipartForm(maxUpload); err != nil {
		http.Error(w, "upload too large", http.StatusRequestEntityTooLarge)
		return
	}
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	space, err := strconv.ParseInt(r.FormValue("space_id"), 10, 64)
	if err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	blob, err := uploaded(r)
	if err != nil {
		http.Error(w, "missing file", http.StatusBadRequest)
		return
	}
	newID, err := themes.ImportZip(d.DB, ctx.Who, space, blob)
	if err != nil {
		d.themeListPage(w, ctx, http.StatusBadRequest, map[string]any{"Error": errKey(err)})
		return
	}
	http.Redirect(w, r, themeURL(newID), http.StatusSeeOther)
}

// uploaded reads the multipart file field.
func uploaded(r *http.Request) ([]byte, error) {
	f, _, err := r.FormFile(formFileField)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

// tokenRow is one token line in the editor: value per mode, hex for pickers.
type tokenRow struct {
	Name, Dark, Light     string
	DarkColor, LightColor string
	DarkIsHex, LightIsHex bool
}

// hex6 expands "#abc" to "#aabbcc" (the colour input needs six digits).
func hex6(v string) string {
	if len(v) != len("#abc") {
		return v
	}
	return "#" + string([]byte{v[1], v[1], v[2], v[2], v[3], v[3]})
}

func (d Deps) themeEditor(w http.ResponseWriter, ctx Ctx, id int64, status int, extra map[string]any) {
	theme, granted, err := themes.Get(d.DB, ctx.Who, id)
	if err != nil {
		d.handleThemeError(w, err)
		return
	}
	baseDark, baseLight := themes.Contract()
	dark := merged(baseDark, theme.Dark)
	light := merged(merged(baseDark, toAny(baseLight)), theme.Light)

	type group struct {
		Key  string
		Rows []tokenRow
	}
	groups := make([]group, 0, len(tokenGroups))
	for _, g := range tokenGroups {
		rows := make([]tokenRow, 0, len(g.Names))
		for _, name := range g.Names {
			row := tokenRow{Name: name, Dark: dark[name], Light: light[name]}
			row.DarkIsHex, row.LightIsHex = hexColor.MatchString(row.Dark), hexColor.MatchString(row.Light)
			row.DarkColor, row.LightColor = hex6(row.Dark), hex6(row.Light)
			rows = append(rows, row)
		}
		groups = append(groups, group{Key: g.Key, Rows: rows})
	}

	values := map[string]any{
		"Theme": theme, "Editable": granted >= enums.RightEdit && !theme.Builtin,
		"Groups": groups, "Issues": themes.ContrastIssues(stringsOf(theme.Dark), stringsOf(theme.Light)),
	}
	for k, v := range extra {
		values[k] = v
	}
	_ = d.Page(w, ctx, "theme_edit", status, values)
}

func merged(base map[string]string, extra map[string]any) map[string]string {
	out := make(map[string]string, len(base))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range extra {
		if s, ok := v.(string); ok {
			out[k] = s
		}
	}
	return out
}

func toAny(m map[string]string) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func stringsOf(m map[string]any) map[string]string { return merged(nil, m) }

func (d Deps) handleThemeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, themes.ErrNotFound):
		http.Error(w, "not found", http.StatusNotFound)
	case errors.Is(err, themes.ErrDenied):
		http.Error(w, "forbidden", http.StatusForbidden)
	default:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (d Deps) handleThemeEdit(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	id, err := themeID(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	d.themeEditor(w, ctx, id, http.StatusOK, nil)
}

// formTokens keeps only values that differ from the base, so the theme
// follows future contract changes for everything it didn't touch.
func formTokens(r *http.Request, mode string, base map[string]string) map[string]any {
	out := map[string]any{}
	for name := range base {
		value := strings.TrimSpace(r.FormValue(mode + name))
		if value != "" && value != base[name] {
			out[name] = value
		}
	}
	return out
}

func (d Deps) handleThemeSave(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	id, err := themeID(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	baseDark, baseLight := themes.Contract()
	lightBase := merged(baseDark, toAny(baseLight))

	var css *string
	if _, sent := r.PostForm["custom_css"]; sent && ctx.Who.IsAdmin() {
		value := r.PostForm.Get("custom_css")
		css = &value
	}
	issues, err := themes.Update(d.DB, ctx.Who, id, r.FormValue("name"),
		formTokens(r, modeDark, baseDark), formTokens(r, modeLight, lightBase), css)
	if err != nil {
		d.themeEditor(w, ctx, id, http.StatusBadRequest, map[string]any{"Error": errKey(err)})
		return
	}
	if len(issues) > 0 {
		d.themeEditor(w, ctx, id, http.StatusOK, map[string]any{"Saved": true})
		return
	}
	http.Redirect(w, r, themeURL(id), http.StatusSeeOther)
}

func (d Deps) handleThemeDelete(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	id, err := themeID(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := themes.Delete(d.DB, ctx.Who, id); err != nil {
		d.handleThemeError(w, err)
		return
	}
	http.Redirect(w, r, "/themes", http.StatusSeeOther)
}

func (d Deps) handleThemeExport(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	id, err := themeID(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	name, blob, err := themes.ExportZip(d.DB, ctx.Who, id)
	if err != nil {
		d.handleThemeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Write(blob)
}

func (d Deps) handleFontUpload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxUpload)
	if err := r.ParseMultipartForm(maxUpload); err != nil {
		http.Error(w, "upload too large", http.StatusRequestEntityTooLarge)
		return
	}
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	id, err := themeID(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	f, header, err := r.FormFile(formFileField)
	if err != nil {
		http.Error(w, "missing file", http.StatusBadRequest)
		return
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		http.Error(w, "read failed", http.StatusBadRequest)
		return
	}
	if err := themes.AddFont(d.DB, ctx.Who, id, header.Filename, data); err != nil {
		d.themeEditor(w, ctx, id, http.StatusBadRequest, map[string]any{"Error": errKey(err)})
		return
	}
	http.Redirect(w, r, themeURL(id), http.StatusSeeOther)
}

func (d Deps) handleFontDelete(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	id, err := themeID(r)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := themes.RemoveFont(d.DB, ctx.Who, id, r.PathValue("name")); err != nil {
		d.themeEditor(w, ctx, id, http.StatusBadRequest, map[string]any{"Error": errKey(err)})
		return
	}
	http.Redirect(w, r, themeURL(id), http.StatusSeeOther)
}

func (d Deps) handleStyleguide(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	_ = d.Page(w, ctx, "styleguide", http.StatusOK, nil)
}
