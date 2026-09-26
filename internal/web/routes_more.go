package web

import (
	"net/http"
	"net/url"
	"strconv"

	"andon/internal/enums"
	"andon/internal/services/access"
	"andon/internal/services/accounts"
	"andon/internal/services/auth"
	"andon/internal/services/boards"
	"andon/internal/services/connections"
	"andon/internal/services/porting"
	"andon/internal/services/spaces"
	"andon/internal/services/widgetlib"
)

// RegisterMoreRoutes wires the smaller personal and editor actions:
// new board, personal credentials, connection options, ending other
// sessions, team space settings, widget copy and the language switch.
func (d Deps) RegisterMoreRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /boards/new", d.handleBoardNewForm)
	mux.HandleFunc("POST /boards/new", d.handleBoardCreate)
	mux.HandleFunc("GET /me/credentials", d.handleCredentials)
	mux.HandleFunc("POST /me/credentials/{id}", d.handleCredentialSave)
	mux.HandleFunc("POST /me/credentials/{id}/delete", d.handleCredentialDelete)
	mux.HandleFunc("POST /connections/{id}/options", d.handleConnectionOptions)
	mux.HandleFunc("POST /me/security/sessions/others/end", d.handleEndOthers)
	mux.HandleFunc("POST /spaces/{id}/team-settings", d.handleTeamSpaceSettings)
	mux.HandleFunc("POST /widgets/{id}/copy", d.handleWidgetCopy)
	mux.HandleFunc("POST /me/locale", d.handleLocale)
}

func (d Deps) handleBoardNewForm(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	_ = d.Page(w, ctx, "board_new", http.StatusOK, map[string]any{"Spaces": access.EditableSpaces(ctx.Who), "Templates": porting.Templates()})
}

func (d Deps) handleBoardCreate(w http.ResponseWriter, r *http.Request) {
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
	if tpl := r.FormValue("template"); tpl != "" {
		if _, err := porting.ApplyTemplate(d.DB, ctx.Who, space, tpl); err != nil {
			d.handleBoardError(w, r, err)
			return
		}
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	id, err := boards.Create(d.DB, ctx.Who, space, r.FormValue("name"))
	if err != nil {
		d.handleBoardError(w, r, err)
		return
	}
	http.Redirect(w, r, boardPath(id)+"?edit", http.StatusSeeOther)
}

func (d Deps) handleCredentials(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	items, err := connections.PersonalNeeded(d.DB, ctx.Who)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = d.Page(w, ctx, "credentials", http.StatusOK, map[string]any{"Items": items})
}

// credentialAction runs a personal-credential change and returns to the list.
func (d Deps) credentialAction(w http.ResponseWriter, r *http.Request, run func(Ctx, int64) error) {
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
	if err := run(ctx, id); err != nil {
		d.handleBoardError(w, r, err)
		return
	}
	http.Redirect(w, r, "/me/credentials", http.StatusSeeOther)
}

func (d Deps) handleCredentialSave(w http.ResponseWriter, r *http.Request) {
	d.credentialAction(w, r, func(ctx Ctx, id int64) error {
		conn, err := connections.Get(d.DB, ctx.Who, id)
		if err != nil {
			return err
		}
		return connections.SetMine(d.DB, ctx.Who, id, formSecret(r, conn.Service))
	})
}

func (d Deps) handleCredentialDelete(w http.ResponseWriter, r *http.Request) {
	d.credentialAction(w, r, func(ctx Ctx, id int64) error {
		return connections.DropMine(d.DB, ctx.Who, id)
	})
}

// handleConnectionOptions stores a connection's options, edited as YAML.
func (d Deps) handleConnectionOptions(w http.ResponseWriter, r *http.Request) {
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
	options, err := porting.Load(r.FormValue("options"))
	if err == nil {
		err = connections.SetOptions(d.DB, ctx.Who, id, options)
	}
	if err != nil {
		http.Redirect(w, r, "/connections/"+strconv.FormatInt(id, 10)+"/edit?error="+url.QueryEscape(errKey(err)), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/connections/"+strconv.FormatInt(id, 10)+"/edit", http.StatusSeeOther)
}

func (d Deps) handleEndOthers(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	if err := auth.EndOtherSessions(d.DB, ctx.Who); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/me/security", http.StatusSeeOther)
}

// handleTeamSpaceSettings stores a team space's hint handling and theme.
func (d Deps) handleTeamSpaceSettings(w http.ResponseWriter, r *http.Request) {
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
	mode := enums.AckPerUser
	if enums.HintAckMode(r.FormValue("hint_ack")) == enums.AckTeam {
		mode = enums.AckTeam
	}
	var theme any
	if n, err := strconv.ParseInt(r.FormValue("theme_id"), 10, 64); err == nil {
		theme = float64(n)
	}
	if err := spaces.Update(d.DB, ctx.Who, id, map[string]any{"hint_ack": string(mode), "theme_id": theme}, ClientIP(r)); err != nil {
		d.handleBoardError(w, r, err)
		return
	}
	http.Redirect(w, r, "/teams", http.StatusSeeOther)
}

func (d Deps) handleWidgetCopy(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	id, err1 := pathID(r, "id")
	space, err2 := strconv.ParseInt(r.FormValue("space_id"), 10, 64)
	if err1 != nil || err2 != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	newID, err := widgetlib.Copy(d.DB, ctx.Who, id, space)
	if err != nil {
		d.handleBoardError(w, r, err)
		return
	}

	// From the gallery: place the copy, then adjust it on its edit form.
	edit := "/widgets/" + strconv.FormatInt(newID, 10) + "/edit"
	target := targetOf(r.FormValue)
	if target.Place {
		if err := d.placeNew(ctx, target, newID, ""); err != nil {
			d.handleBoardError(w, r, err)
			return
		}
		edit += "?board_id=" + strconv.FormatInt(target.BoardID, 10)
	}
	http.Redirect(w, r, edit, http.StatusSeeOther)
}

// handleLocale switches the language and returns to the page it came from.
func (d Deps) handleLocale(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	locale := enums.Locale(r.FormValue("locale"))
	if err := accounts.UpdateProfile(d.DB, ctx.Who, accounts.ProfileChanges{Locale: &locale}); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	back := "/"
	if ref, err := url.Parse(r.Referer()); err == nil {
		back = safeNext(ref.Path)
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}
