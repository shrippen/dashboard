package sources

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"time"

	"dashboard/internal/drivers/services"
	"dashboard/internal/enums"
)

const (
	dataTTL          = 10 * time.Minute
	testTTL          = time.Second
	demoScheme       = "demo://"
	visitDays        = 120
	secondsPerMinute = 60
)

func isDemo(sctx Ctx) bool {
	return len(sctx.URL) >= len(demoScheme) && sctx.URL[:len(demoScheme)] == demoScheme
}

func needSecret(sctx Ctx) (string, error) {
	if sctx.Secret == "" {
		return "", newSourceError("credential.missing")
	}
	return sctx.Secret, nil
}

// windowStart is January 1st of last year: enough for year-over-year
// comparisons.
func windowStart(today time.Time) time.Time {
	return time.Date(today.Year()-1, 1, 1, 0, 0, 0, 0, time.UTC)
}

func kimaiAPI(sctx Ctx) (services.KimaiApi, error) {
	secret, err := needSecret(sctx)
	if err != nil {
		return services.KimaiApi{}, err
	}
	return services.KimaiApi{URL: sctx.URL, Token: secret, Verify: sctx.VerifyTLS}, nil
}

func kimaiSheet(raw any) KimaiSheet {
	m := asMap(raw)
	project := asMap(m["project"])
	customer := asMap(project["customer"])
	activity := asMap(m["activity"])
	customerID := asInt64(customer["id"])
	if customerID == 0 {
		customerID = asInt64(project["customer"])
	}
	return KimaiSheet{
		ID: asInt64(m["id"]), Begin: asStr(m["begin"]), End: asStr(m["end"]),
		Minutes: int(round(asFloat(m["duration"]) / secondsPerMinute)),
		Rate:    asFloat(m["rate"]), Billable: boolOr(m["billable"], true), Exported: asBool(m["exported"]),
		ProjectID: refID(m["project"]), CustomerID: customerID, Activity: asStr(activity["name"]),
		UserID: refID(m["user"]),
	}
}

func boolOr(v any, def bool) bool {
	if v == nil {
		return def
	}
	return asBool(v)
}

func round(f float64) float64 {
	if f < 0 {
		return float64(int64(f - 0.5))
	}
	return float64(int64(f + 0.5))
}

func kimaiProject(raw any) KimaiProject {
	m := asMap(raw)
	return KimaiProject{
		ID: asInt64(m["id"]), Name: asStr(m["name"]), CustomerID: refID(m["customer"]),
		Budget: asFloat(m["budget"]), TimeBudgetMin: int(round(asFloat(m["timeBudget"]) / secondsPerMinute)),
		BudgetType: asStr(m["budgetType"]), End: asStr(m["end"]),
	}
}

// KimaiData is the "kimai.data" source: timesheets, projects, customers and
// (if the holiday-bundle plugin is installed) absences/public holidays.
type KimaiData struct{}

func (KimaiData) Key() string                { return "kimai.data" }
func (KimaiData) TTL() time.Duration         { return dataTTL }
func (KimaiData) Service() enums.ServiceType { return enums.ServiceKimai }

func (KimaiData) Fetch(ctx context.Context, sctx Ctx) (any, error) {
	if isDemo(sctx) {
		return nil, newSourceError("demo data not yet ported")
	}
	api, err := kimaiAPI(sctx)
	if err != nil {
		return nil, err
	}
	data, err := loadKimai(ctx, api, sctx)
	if err != nil {
		var apiErr services.ApiError
		if isApiError(err, &apiErr) {
			return nil, newSourceError("%s", apiErr.Error())
		}
		return nil, err
	}
	return data, nil
}

func isApiError(err error, target *services.ApiError) bool {
	e, ok := err.(services.ApiError)
	if ok {
		*target = e
	}
	return ok
}

func loadKimai(ctx context.Context, api services.KimaiApi, sctx Ctx) (*KimaiDataset, error) {
	today := time.Now().UTC()
	scope := url.Values{}
	if allUsers, _ := sctx.Options["all_users"].(bool); allUsers {
		scope.Set("user", "all")
	}
	scope.Set("begin", windowStart(today).Format("2006-01-02")+"T00:00:00")
	scope.Set("full", "true")

	sheetsRaw, err := api.Pages(ctx, "timesheets", scope)
	if err != nil {
		return nil, err
	}
	projectsRaw, err := api.Get(ctx, "projects", url.Values{"visible": {"3"}})
	if err != nil {
		return nil, err
	}

	projects := make([]KimaiProject, 0, len(asList(projectsRaw)))
	for _, p := range asList(projectsRaw) {
		pm := asMap(p)
		full, err := api.Get(ctx, fmt.Sprintf("projects/%d", asInt64(pm["id"])), nil)
		if err != nil {
			return nil, err
		}
		project := kimaiProject(full)
		if project.Budget != 0 || project.TimeBudgetMin != 0 {
			used := url.Values{}
			for k, v := range scope {
				used[k] = v
			}
			used.Set("projects[]", strconv.FormatInt(project.ID, 10))
			usedSheets, err := api.Pages(ctx, "timesheets", used)
			if err != nil {
				return nil, err
			}
			var money float64
			var minutes float64
			for _, s := range usedSheets {
				sm := asMap(s)
				money += asFloat(sm["rate"])
				minutes += asFloat(sm["duration"])
			}
			project.UsedMoney = money
			project.UsedMinutes = int(round(minutes / secondsPerMinute))
		}
		projects = append(projects, project)
	}

	customersRaw, err := api.Get(ctx, "customers", url.Values{"visible": {"3"}})
	if err != nil {
		return nil, err
	}
	customers := make([]KimaiCustomer, 0, len(asList(customersRaw)))
	for _, c := range asList(customersRaw) {
		cm := asMap(c)
		customers = append(customers, KimaiCustomer{ID: asInt64(cm["id"]), Name: asStr(cm["name"])})
	}

	activeRaw, err := api.Get(ctx, "timesheets/active", nil)
	if err != nil {
		return nil, err
	}

	sheets := make([]KimaiSheet, 0, len(sheetsRaw))
	for _, s := range sheetsRaw {
		sheets = append(sheets, kimaiSheet(s))
	}
	active := make([]KimaiSheet, 0, len(asList(activeRaw)))
	for _, s := range asList(activeRaw) {
		active = append(active, kimaiSheet(s))
	}

	data := &KimaiDataset{
		URL: sctx.URL, Timesheets: sheets, Active: active, Projects: projects, Customers: customers,
	}
	loadKimaiHolidays(ctx, api, today, data)
	return data, nil
}

// loadKimaiHolidays reads the kimai-holiday-bundle: absences and public
// holidays. A missing plugin is fine (data.HolidayBundle stays false).
func loadKimaiHolidays(ctx context.Context, api services.KimaiApi, today time.Time, data *KimaiDataset) {
	for _, year := range []int{today.Year() - 1, today.Year()} {
		absencesRaw, err := api.Get(ctx, "holiday/absences", url.Values{"year": {strconv.Itoa(year)}})
		if err != nil {
			if _, ok := err.(services.ApiMissing); ok {
				return
			}
			return
		}
		for _, a := range asList(absencesRaw) {
			am := asMap(a)
			data.Absences = append(data.Absences, KimaiAbsence{
				Start: asStr(am["startDate"]), End: asStr(am["endDate"]), Type: asStr(am["type"]),
				Status: asStr(am["status"]), HalfDay: asBool(am["halfDay"]),
			})
		}

		holidaysRaw, err := api.Get(ctx, "holiday/public-holidays", url.Values{"year": {strconv.Itoa(year)}})
		if err != nil {
			return
		}
		for _, h := range asList(holidaysRaw) {
			hm := asMap(h)
			data.Holidays = append(data.Holidays, KimaiHoliday{
				Date: asStr(hm["date"]), Name: asStr(hm["name"]), HalfDay: asBool(hm["halfDay"]),
			})
		}
	}
	data.HolidayBundle = true
}

// KimaiTest is the "kimai.test" source: a lightweight connection check.
type KimaiTest struct{}

func (KimaiTest) Key() string                { return "kimai.test" }
func (KimaiTest) TTL() time.Duration         { return testTTL }
func (KimaiTest) Service() enums.ServiceType { return enums.ServiceKimai }

func (KimaiTest) Fetch(ctx context.Context, sctx Ctx) (any, error) {
	if isDemo(sctx) {
		return map[string]any{"version": "demo"}, nil
	}
	api, err := kimaiAPI(sctx)
	if err != nil {
		return nil, err
	}
	body, err := api.Get(ctx, "version", nil)
	if err != nil {
		var apiErr services.ApiError
		if isApiError(err, &apiErr) {
			return nil, newSourceError("%s", apiErr.Error())
		}
		return nil, err
	}
	return map[string]any{"version": asStr(asMap(body)["version"])}, nil
}
