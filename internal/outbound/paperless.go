package outbound

import (
	"context"

	"dashboard/internal/drivers/services"
)

// PaperlessUpload hands one file to Paperless for consumption and returns
// its task id.
func PaperlessUpload(ctx context.Context, baseURL, token string, verifyTLS bool, filename, title string, content []byte) (string, error) {
	return services.PaperlessApi{URL: baseURL, Token: token, Verify: verifyTLS}.Upload(ctx, filename, title, content)
}
