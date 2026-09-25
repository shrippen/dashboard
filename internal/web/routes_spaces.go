package web

import (
	"net/http"
	"strconv"
	"strings"

	"dashboard/internal/services/access"
	"dashboard/internal/services/spaces"
)

const (
	defaultHoursPerDay   = 8.0
	defaultIncomeTaxRate = 0.3
	defaultAnnualDue     = "07-31"
)

var (
	vatMethods   = []string{"ist", "soll"}
	vatIntervals = []string{"monthly", "quarterly"}
)

// RegisterSpaceRoutes wires a space's evaluation settings: goals, tax
// values and rule thresholds.
func (d Deps) RegisterSpaceRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /spaces/settings", d.handleMySpaceSettings)
	mux.HandleFunc("GET /spaces/{id}/settings", d.handleSpaceSettings)
	mux.HandleFunc("POST /spaces/{id}/settings", d.handleSpaceSettingsSave)
}

func (d Deps) handleMySpaceSettings(w http.ResponseWriter, r *http.Request) {
	ctx, err := d.Require(r)
	if err != nil {
		d.handleAuthError(w, r, err)
		return
	}
	mine := access.Personal(ctx.Who)
	if mine == nil {
		http.NotFound(w, r)
		return
	}
	http.Redirect(w, r, "/spaces/"+strconv.FormatInt(mine.ID, 10)+"/settings", http.StatusSeeOther)
}

func (d Deps) handleSpaceSettings(w http.ResponseWriter, r *http.Request) {
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
	settings, err := spaces.Settings(d.DB, ctx.Who, id)
	if err != nil {
		d.handleBoardError(w, r, err)
		return
	}
	goals := asMap(settings["goals"])
	tax := asMap(settings["tax"])
	_ = d.Page(w, ctx, "space_settings", http.StatusOK, map[string]any{
		"SpaceID": id, "Goals": goals, "Tax": tax, "VAT": asMap(tax["vat"]), "Prepay": asMap(tax["prepayments"]),
		"Costs": asMap(settings["costs"]),
		"Rules": spaces.RuleViews(settings), "Methods": vatMethods, "Intervals": vatIntervals,
		"Saved": r.URL.Query().Has("saved"), "Page": spaces.PageOf(settings), "NavText": spaces.NavText(spaces.PageOf(settings)),
	})
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	if m == nil {
		return map[string]any{}
	}
	return m
}

func number(raw string, fallback float64) float64 {
	n, err := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(raw), ",", "."), 64)
	if err != nil {
		return fallback
	}
	return n
}

func oneOf(value string, allowed []string) string {
	for _, a := range allowed {
		if a == value {
			return value
		}
	}
	return allowed[0]
}

func (d Deps) handleSpaceSettingsSave(w http.ResponseWriter, r *http.Request) {
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
	annual := strings.TrimSpace(r.FormValue("annual_due"))
	if annual == "" {
		annual = defaultAnnualDue
	}
	changes := map[string]any{
		"goals": map[string]any{
			"revenue_year":  number(r.FormValue("revenue_year"), 0),
			"hours_per_day": number(r.FormValue("hours_per_day"), defaultHoursPerDay),
		},
		"tax": map[string]any{
			"vat": map[string]any{
				"method":          oneOf(r.FormValue("vat_method"), vatMethods),
				"return_interval": oneOf(r.FormValue("vat_interval"), vatIntervals),
				"extension":       checked(r, "vat_extension"),
			},
			"prepayments":     map[string]any{"amount": number(r.FormValue("prepayment"), 0)},
			"annual_due":      annual,
			"income_tax_rate": number(r.FormValue("income_tax_rate"), defaultIncomeTaxRate),
		},
		"costs": map[string]any{"fixed_monthly": number(r.FormValue("fixed_monthly"), 0)},
		"rules": spaces.ParseRules(r.FormValue),
	}
	for k, v := range spaces.ParsePage(r.FormValue) {
		changes[k] = v
	}
	if err := spaces.Update(d.DB, ctx.Who, id, changes, ClientIP(r)); err != nil {
		d.handleBoardError(w, r, err)
		return
	}
	http.Redirect(w, r, "/spaces/"+strconv.FormatInt(id, 10)+"/settings?saved=1", http.StatusSeeOther)
}
