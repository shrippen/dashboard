package web

import (
	"net/http"
	"strconv"

	"dashboard/internal/services/access"
	"dashboard/internal/services/porting"
)

const (
	yamlType      = "application/yaml; charset=utf-8"
	importDashy   = "dashy"
	maxImportSize = 2 << 20
)

// RegisterPortingRoutes wires YAML import/export: the import page (Dashy or
// our own format), a space's code view and the downloads.
func (d Deps) RegisterPortingRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /import", d.handleImportForm)
	mux.HandleFunc("POST /import", d.handleImportRun)
	mux.HandleFunc("GET /spaces/{id}/code", d.handleSpaceCode)
	mux.HandleFunc("POST /spaces/{id}/code", d.handleSpaceCodeSave)
	mux.HandleFunc("GET /spaces/{id}/export", d.handleSpaceExport)
	mux.HandleFunc("GET /boards/{id}/export", d.handleBoardExport)
}

func (d Deps) importPage(w http.ResponseWriter, ctx Ctx, status int, extra map[string]any) {
	values := map[string]any{"Spaces": access.EditableSpaces(ctx.Who)}
	for k, v := range extra {
		values[k] = v
	}
	_ = d.Page(w, ctx, "import", status, values)
}

func (d Deps) handleImportForm(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	d.importPage(w, ctx, http.StatusOK, nil)
}

func (d Deps) handleImportRun(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxImportSize+maxIconForm)
	if err := r.ParseMultipartForm(maxImportSize); err != nil {
		http.Error(w, "import.too_large", http.StatusRequestEntityTooLarge)
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

	var report *porting.Report
	if r.FormValue("kind") == importDashy {
		report, err = porting.ImportDashy(d.DB, ctx.Who, space, string(blob))
	} else {
		report, err = porting.ImportSpace(d.DB, ctx.Who, space, string(blob), porting.Merge)
	}
	if err != nil {
		d.importPage(w, ctx, http.StatusBadRequest, map[string]any{"Error": errKey(err)})
		return
	}
	d.importPage(w, ctx, http.StatusOK, map[string]any{"Report": report})
}

func (d Deps) codePage(w http.ResponseWriter, ctx Ctx, space int64, status int, extra map[string]any) {
	values := map[string]any{"SpaceID": space}
	if _, ok := extra["Text"]; !ok {
		text, err := porting.ExportSpace(d.DB, ctx.Who, space)
		if err != nil {
			d.handleBoardError(w, nil, err)
			return
		}
		values["Text"] = text
	}
	for k, v := range extra {
		values[k] = v
	}
	_ = d.Page(w, ctx, "space_code", status, values)
}

func (d Deps) handleSpaceCode(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	space, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	d.codePage(w, ctx, space, http.StatusOK, nil)
}

func (d Deps) handleSpaceCodeSave(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	space, err := pathID(r, "id")
	if err != nil {
		http.NotFound(w, r)
		return
	}
	text := r.FormValue("text")
	mode := porting.Mode(r.FormValue("mode"))
	if mode != porting.Merge {
		mode = porting.Replace
	}
	report, err := porting.ImportSpace(d.DB, ctx.Who, space, text, mode)
	if err != nil {
		d.codePage(w, ctx, space, http.StatusBadRequest, map[string]any{"Text": text, "Error": errKey(err)})
		return
	}
	d.codePage(w, ctx, space, http.StatusOK, map[string]any{"Report": report})
}

func (d Deps) sendYAML(w http.ResponseWriter, r *http.Request, name string, export func(Ctx, int64) (string, error)) {
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
	text, err := export(ctx, id)
	if err != nil {
		d.handleBoardError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", yamlType)
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+"-"+strconv.FormatInt(id, 10)+`.yml"`)
	w.Write([]byte(text))
}

func (d Deps) handleSpaceExport(w http.ResponseWriter, r *http.Request) {
	d.sendYAML(w, r, "space", func(ctx Ctx, id int64) (string, error) { return porting.ExportSpace(d.DB, ctx.Who, id) })
}

func (d Deps) handleBoardExport(w http.ResponseWriter, r *http.Request) {
	d.sendYAML(w, r, "board", func(ctx Ctx, id int64) (string, error) { return porting.ExportBoard(d.DB, ctx.Who, id) })
}
