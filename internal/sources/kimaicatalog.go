package sources

// Kimai's visible projects and activities, for the add-entry form of the
// Kimai Lite tile. Fetched only when the form opens.
//
//	GET /api/projects?visible=1
//	GET /api/activities?visible=1

import (
	"context"
	"net/url"
	"sort"
	"time"

	"andon/internal/enums"
)

const kimaiCatalogTTL = 10 * time.Minute

// KimaiPick is a project the user may book on.
type KimaiPick struct {
	ID             int64
	Name, Customer string
}

// KimaiActivityPick is an activity; ProjectID 0 means it is global.
type KimaiActivityPick struct {
	ID        int64
	Name      string
	ProjectID int64
}

// KimaiCatalog is what the add-entry form offers.
type KimaiCatalog struct {
	Projects   []KimaiPick
	Activities []KimaiActivityPick
}

type KimaiCatalogSource struct{}

func (KimaiCatalogSource) Key() string                { return "kimai.catalog" }
func (KimaiCatalogSource) TTL() time.Duration         { return kimaiCatalogTTL }
func (KimaiCatalogSource) Service() enums.ServiceType { return enums.ServiceKimai }

func (KimaiCatalogSource) Fetch(ctx context.Context, sctx Ctx) (any, error) {
	if isDemo(sctx) {
		return DemoKimaiCatalog(), nil
	}
	api, err := kimaiAPI(sctx)
	if err != nil {
		return nil, err
	}
	visible := url.Values{"visible": {"1"}}

	projects, err := api.Get(ctx, "projects", visible)
	if err != nil {
		return nil, fetchError(err)
	}
	out := &KimaiCatalog{}
	for _, raw := range asList(projects) {
		m := asMap(raw)
		// parentTitle is the customer's name in Kimai's project list.
		out.Projects = append(out.Projects, KimaiPick{ID: asInt64(m["id"]), Name: asStr(m["name"]), Customer: asStr(m["parentTitle"])})
	}
	sort.Slice(out.Projects, func(a, b int) bool {
		pa, pb := out.Projects[a], out.Projects[b]
		if pa.Customer != pb.Customer {
			return pa.Customer < pb.Customer
		}
		return pa.Name < pb.Name
	})

	activities, err := api.Get(ctx, "activities", visible)
	if err != nil {
		return nil, fetchError(err)
	}
	for _, raw := range asList(activities) {
		m := asMap(raw)
		out.Activities = append(out.Activities, KimaiActivityPick{ID: asInt64(m["id"]), Name: asStr(m["name"]), ProjectID: refID(m["project"])})
	}
	sort.Slice(out.Activities, func(a, b int) bool { return out.Activities[a].Name < out.Activities[b].Name })
	return out, nil
}

func init() {
	Register(KimaiCatalogSource{})
}
