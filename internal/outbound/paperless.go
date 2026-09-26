package outbound

import (
	"context"

	"dashboard/internal/drivers/services"
)

// PaperlessUpload hands one file to Paperless for consumption and returns
// its task id.
func PaperlessUpload(ctx context.Context, to Target, filename, title string, content []byte) (string, error) {
	return services.PaperlessApi{URL: to.URL, Token: to.Token, Verify: to.VerifyTLS}.Upload(ctx, filename, title, content)
}
