package services

import (
	"context"

	"andon/internal/drivers/httpclient"
)

// KintsugiApi reads Kintsugi's status (/api/status) with its API token
// (Kintsugi → Einstellungen → API-Token).
type KintsugiApi struct {
	URL    string
	Token  string
	Verify bool
}

// Status returns the decoded /api/status body.
func (a KintsugiApi) Status(ctx context.Context) (any, error) {
	headers := map[string]string{"Authorization": "Bearer " + a.Token, "Accept": "application/json"}
	return fetchJSON(ctx, joinURL(a.URL, "api/status"), headers, nil, httpclient.TLSOf(a.Verify))
}
