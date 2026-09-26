package web

import (
	"errors"
	"net/http"
	"strconv"

	"andon/internal/enums"
	"andon/internal/services/shares"
)

// RegisterShareRoutes wires the "who has access?" dialog: view a
// resource's shares, grant one, revoke one.
func (d Deps) RegisterShareRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /shares/{kind}/{id}", d.handleSharesPage)
	mux.HandleFunc("POST /shares/{kind}/{id}", d.handleShareGrant)
	mux.HandleFunc("POST /shares/{kind}/{id}/{shareID}/revoke", d.handleShareRevoke)
}

var errBadKind = errors.New("shares: unknown resource kind")

func parseKind(raw string) (enums.ResourceKind, error) {
	switch enums.ResourceKind(raw) {
	case enums.ResourceBoard, enums.ResourceWidget, enums.ResourceConnection, enums.ResourceTheme:
		return enums.ResourceKind(raw), nil
	default:
		return "", errBadKind
	}
}

func parseRight(raw string) (enums.Right, error) {
	switch raw {
	case "view":
		return enums.RightView, nil
	case "use":
		return enums.RightUse, nil
	case "edit":
		return enums.RightEdit, nil
	case "manage":
		return enums.RightManage, nil
	default:
		return enums.RightNone, shares.ErrRight
	}
}

func (d Deps) sharesPage(w http.ResponseWriter, ctx Ctx, kind enums.ResourceKind, resourceID int64, status int, extra map[string]any) {
	info, err := shares.Info(d.DB, ctx.Who, kind, resourceID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	values := map[string]any{"Info": info, "Kind": kind, "ResourceID": resourceID}
	for k, v := range extra {
		values[k] = v
	}
	_ = d.Page(w, ctx, "shares", status, values)
}

func (d Deps) handleSharesPage(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	kind, err := parseKind(r.PathValue("kind"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	resourceID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	d.sharesPage(w, ctx, kind, resourceID, http.StatusOK, nil)
}

func (d Deps) handleShareGrant(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	kind, err := parseKind(r.PathValue("kind"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	resourceID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	granteeID, err := strconv.ParseInt(r.FormValue("grantee_id"), 10, 64)
	if err != nil {
		d.sharesPage(w, ctx, kind, resourceID, http.StatusBadRequest, map[string]any{"Error": errKey(err)})
		return
	}
	right, err := parseRight(r.FormValue("right"))
	if err != nil {
		d.sharesPage(w, ctx, kind, resourceID, http.StatusBadRequest, map[string]any{"Error": errKey(err)})
		return
	}
	granteeKind := enums.GranteeKind(r.FormValue("grantee_kind"))

	if err := shares.Grant(d.DB, ctx.Who, kind, resourceID, granteeKind, granteeID, right); err != nil {
		d.sharesPage(w, ctx, kind, resourceID, http.StatusBadRequest, map[string]any{"Error": errKey(err)})
		return
	}
	http.Redirect(w, r, "/shares/"+string(kind)+"/"+r.PathValue("id"), http.StatusSeeOther)
}

func (d Deps) handleShareRevoke(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	kind, err := parseKind(r.PathValue("kind"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	resourceID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	shareID, err := strconv.ParseInt(r.PathValue("shareID"), 10, 64)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := shares.Revoke(d.DB, ctx.Who, shareID); err != nil {
		d.sharesPage(w, ctx, kind, resourceID, http.StatusBadRequest, map[string]any{"Error": errKey(err)})
		return
	}
	http.Redirect(w, r, "/shares/"+string(kind)+"/"+r.PathValue("id"), http.StatusSeeOther)
}
