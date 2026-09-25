package web

import (
	"net/http"
	"strconv"

	"dashboard/internal/enums"
	"dashboard/internal/services/admin"
	"dashboard/internal/services/audit"
)

// RegisterAdminRoutes wires the admin-only user list (/admin/users) and
// audit log (/admin/audit). Instance settings (network, OIDC, ...) are not
// ported yet.
func (d Deps) RegisterAdminRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/users", d.handleAdminUsers)
	mux.HandleFunc("POST /admin/users/{id}/role", d.handleAdminUserRole)
	mux.HandleFunc("POST /admin/users/{id}/active", d.handleAdminUserActive)
	mux.HandleFunc("POST /admin/users/{id}/delete", d.handleAdminUserDelete)
	mux.HandleFunc("GET /admin/audit", d.handleAdminAudit)
}

func (d Deps) adminUsersPage(w http.ResponseWriter, ctx Ctx, status int, extra map[string]any) {
	list, err := admin.Users(d.DB, ctx.Who)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	values := map[string]any{"Users": list}
	for k, v := range extra {
		values[k] = v
	}
	_ = d.Page(w, ctx, "admin_users", status, values)
}

func (d Deps) handleAdminUsers(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	d.adminUsersPage(w, ctx, http.StatusOK, nil)
}

func adminUserID(r *http.Request) (int64, error) {
	return strconv.ParseInt(r.PathValue("id"), 10, 64)
}

func (d Deps) handleAdminUserRole(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	id, err := adminUserID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	role := enums.InstanceRole(r.FormValue("role"))
	if err := admin.SetRole(d.DB, ctx.Who, id, role, ClientIP(r)); err != nil {
		d.adminUsersPage(w, ctx, http.StatusBadRequest, map[string]any{"Error": err.Error()})
		return
	}
	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

func (d Deps) handleAdminUserActive(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	id, err := adminUserID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := admin.SetActive(d.DB, ctx.Who, id, r.FormValue("active") == "1", ClientIP(r)); err != nil {
		d.adminUsersPage(w, ctx, http.StatusBadRequest, map[string]any{"Error": err.Error()})
		return
	}
	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

func (d Deps) handleAdminUserDelete(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	id, err := adminUserID(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := admin.Delete(d.DB, ctx.Who, id, ClientIP(r)); err != nil {
		d.adminUsersPage(w, ctx, http.StatusBadRequest, map[string]any{"Error": err.Error()})
		return
	}
	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

func (d Deps) handleAdminAudit(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	entries, err := audit.Entries(d.DB, ctx.Who)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = d.Page(w, ctx, "admin_audit", http.StatusOK, map[string]any{"Entries": entries})
}
