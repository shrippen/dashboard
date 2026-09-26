package web

import (
	"cmp"
	"database/sql"
	"errors"
	"net/http"
	"strconv"

	"dashboard/internal/enums"
	"dashboard/internal/model"
	"dashboard/internal/services/access"
	"dashboard/internal/services/admin"
	"dashboard/internal/services/audit"
	"dashboard/internal/services/invites"
	"dashboard/internal/services/teams"
)

// RegisterAdminRoutes wires the admin-only account pages: user list with
// role/active/break-glass/reset/delete, invitations, and the audit log.
func (d Deps) RegisterAdminRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /admin/users", d.handleAdminUsers)
	mux.HandleFunc("POST /admin/users/{id}/role", d.handleAdminUserRole)
	mux.HandleFunc("POST /admin/users/{id}/active", d.handleAdminSwitch(admin.SetActive))
	mux.HandleFunc("POST /admin/users/{id}/breakglass", d.handleAdminSwitch(admin.SetBreakglass))
	mux.HandleFunc("POST /admin/users/{id}/reset", d.handleAdminUserReset)
	mux.HandleFunc("POST /admin/users/{id}/delete", d.handleAdminUserDelete)
	mux.HandleFunc("POST /admin/invite", d.handleAdminInvite)
	mux.HandleFunc("POST /admin/invites/{id}/delete", d.handleAdminInviteDelete)
	mux.HandleFunc("GET /admin/audit", d.handleAdminAudit)
}

func (d Deps) adminUsersPage(w http.ResponseWriter, ctx Ctx, status int, extra map[string]any) {
	rows, err := admin.Users(d.DB, ctx.Who)
	if err != nil {
		d.pageError(w, ctx, err)
		return
	}
	pending, err := invites.Pending(d.DB, ctx.Who)
	if err != nil {
		d.pageError(w, ctx, err)
		return
	}
	teamList, err := teams.Overview(d.DB, ctx.Who)
	if err != nil {
		d.pageError(w, ctx, err)
		return
	}

	values := map[string]any{"Users": rows, "Invites": pending, "TeamList": teamList}
	for k, v := range extra {
		values[k] = v
	}
	_ = d.Page(w, ctx, "admin_users", status, values)
}

// pageError answers a failed admin read: 403 for denial, 500 otherwise.
func (d Deps) pageError(w http.ResponseWriter, ctx Ctx, err error) {
	if errors.Is(err, admin.ErrDenied) || errors.Is(err, invites.ErrDenied) || errors.Is(err, audit.ErrDenied) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	http.Error(w, err.Error(), http.StatusInternalServerError)
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

// adminAction is the common shape of an admin form post: auth, id, run, back to list.
func (d Deps) adminAction(w http.ResponseWriter, r *http.Request, run func(Ctx, int64) error) {
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
	if err := run(ctx, id); err != nil {
		d.adminUsersPage(w, ctx, http.StatusBadRequest, map[string]any{"Error": errKey(err)})
		return
	}
	http.Redirect(w, r, "/admin/users", http.StatusSeeOther)
}

func (d Deps) handleAdminUserRole(w http.ResponseWriter, r *http.Request) {
	d.adminAction(w, r, func(ctx Ctx, id int64) error {
		return admin.SetRole(d.DB, ctx.Who, id, enums.InstanceRole(r.FormValue("role")), ClientIP(r))
	})
}

type switchFunc func(d *sql.DB, who *access.Principal, userID int64, state admin.Switch, ip string) error

func (d Deps) handleAdminSwitch(set switchFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		d.adminAction(w, r, func(ctx Ctx, id int64) error {
			return set(d.DB, ctx.Who, id, admin.Switch(r.FormValue("state")), ClientIP(r))
		})
	}
}

func (d Deps) handleAdminUserDelete(w http.ResponseWriter, r *http.Request) {
	d.adminAction(w, r, func(ctx Ctx, id int64) error {
		return admin.Delete(d.DB, ctx.Who, id, ClientIP(r))
	})
}

func (d Deps) handleAdminUserReset(w http.ResponseWriter, r *http.Request) {
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
	link, err := invites.AdminResetLink(d.DB, ctx.Who, id)
	if err != nil {
		d.adminUsersPage(w, ctx, http.StatusBadRequest, map[string]any{"Error": errKey(err)})
		return
	}
	d.adminUsersPage(w, ctx, http.StatusOK, map[string]any{"ResetLink": link})
}

func (d Deps) handleAdminInvite(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	teamRole := enums.TeamRole(r.FormValue("team_role"))
	var joins []model.InviteTeam
	for _, name := range r.Form["teams"] {
		if name != "" {
			joins = append(joins, model.InviteTeam{Team: name, Role: teamRole})
		}
	}

	link, err := invites.Create(d.DB, ctx.Who, r.FormValue("email"), enums.InstanceRole(r.FormValue("role")),
		joins, enums.Locale(r.FormValue("locale")))
	if err != nil {
		d.adminUsersPage(w, ctx, http.StatusBadRequest, map[string]any{"Error": errKey(err)})
		return
	}
	d.adminUsersPage(w, ctx, http.StatusOK, map[string]any{"InviteLink": link})
}

func (d Deps) handleAdminInviteDelete(w http.ResponseWriter, r *http.Request) {
	d.adminAction(w, r, func(ctx Ctx, id int64) error {
		return invites.Revoke(d.DB, ctx.Who, id)
	})
}

// auditRow is an audit entry with its user's name ("" = system).
type auditRow struct {
	*model.AuditEntry
	Who string
}

func (d Deps) handleAdminAudit(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	entries, err := audit.Entries(d.DB, ctx.Who)
	if err != nil {
		d.pageError(w, ctx, err)
		return
	}
	users, err := admin.Users(d.DB, ctx.Who)
	if err != nil {
		d.pageError(w, ctx, err)
		return
	}
	names := map[int64]string{}
	for _, u := range users {
		names[u.ID] = u.Name
	}
	rows := make([]auditRow, 0, len(entries))
	for _, e := range entries {
		row := auditRow{AuditEntry: e}
		if e.UserID != nil {
			row.Who = cmp.Or(names[*e.UserID], "#"+strconv.FormatInt(*e.UserID, 10))
		}
		rows = append(rows, row)
	}
	_ = d.Page(w, ctx, "admin_audit", http.StatusOK, map[string]any{"Entries": rows})
}
