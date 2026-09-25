package web

import (
	"net/http"
	"strconv"

	"dashboard/internal/enums"
	"dashboard/internal/repos/users"
	"dashboard/internal/services/teams"
)

// RegisterTeamRoutes wires /teams: overview, create, rename, member
// set/remove, delete. Admins manage every team; owners manage their own.
func (d Deps) RegisterTeamRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /teams", d.handleTeamsPage)
	mux.HandleFunc("POST /teams", d.handleTeamCreate)
	mux.HandleFunc("POST /teams/{id}/rename", d.handleTeamRename)
	mux.HandleFunc("POST /teams/{id}/members", d.handleTeamMemberSet)
	mux.HandleFunc("POST /teams/{id}/members/{userID}/remove", d.handleTeamMemberRemove)
	mux.HandleFunc("POST /teams/{id}/delete", d.handleTeamDelete)
}

func (d Deps) teamsPage(w http.ResponseWriter, ctx Ctx, status int, extra map[string]any) {
	overview, err := teams.Overview(d.DB, ctx.Who)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	allUsers, err := users.All(d.DB)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	values := map[string]any{"Teams": overview, "Users": allUsers}
	for k, v := range extra {
		values[k] = v
	}
	_ = d.Page(w, ctx, "teams", status, values)
}

func (d Deps) handleTeamsPage(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	d.teamsPage(w, ctx, http.StatusOK, nil)
}

func (d Deps) handleTeamCreate(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if _, err := teams.Create(d.DB, ctx.Who, r.FormValue("name"), ClientIP(r)); err != nil {
		d.teamsPage(w, ctx, http.StatusBadRequest, map[string]any{"Error": err.Error()})
		return
	}
	http.Redirect(w, r, "/teams", http.StatusSeeOther)
}

func teamID(r *http.Request) (int64, error) {
	return strconv.ParseInt(r.PathValue("id"), 10, 64)
}

func (d Deps) handleTeamRename(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	id, err := teamID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := teams.Rename(d.DB, ctx.Who, id, r.FormValue("name")); err != nil {
		d.teamsPage(w, ctx, http.StatusBadRequest, map[string]any{"Error": err.Error()})
		return
	}
	http.Redirect(w, r, "/teams", http.StatusSeeOther)
}

func (d Deps) handleTeamMemberSet(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	id, err := teamID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	userID, err := strconv.ParseInt(r.FormValue("user_id"), 10, 64)
	if err != nil {
		d.teamsPage(w, ctx, http.StatusBadRequest, map[string]any{"Error": err.Error()})
		return
	}
	role := enums.TeamRole(r.FormValue("role"))
	if err := teams.SetMember(d.DB, ctx.Who, id, userID, role, ClientIP(r)); err != nil {
		d.teamsPage(w, ctx, http.StatusBadRequest, map[string]any{"Error": err.Error()})
		return
	}
	http.Redirect(w, r, "/teams", http.StatusSeeOther)
}

func (d Deps) handleTeamMemberRemove(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	id, err := teamID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	userID, err := strconv.ParseInt(r.PathValue("userID"), 10, 64)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := teams.RemoveMember(d.DB, ctx.Who, id, userID, ClientIP(r)); err != nil {
		d.teamsPage(w, ctx, http.StatusBadRequest, map[string]any{"Error": err.Error()})
		return
	}
	http.Redirect(w, r, "/teams", http.StatusSeeOther)
}

func (d Deps) handleTeamDelete(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	id, err := teamID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := teams.Delete(d.DB, ctx.Who, id, ClientIP(r)); err != nil {
		d.teamsPage(w, ctx, http.StatusBadRequest, map[string]any{"Error": err.Error()})
		return
	}
	http.Redirect(w, r, "/teams", http.StatusSeeOther)
}
