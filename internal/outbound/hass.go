package outbound

import (
	"context"
	"strings"

	"dashboard/internal/drivers/services"
)

const hassToggle = "toggle"

// HassToggle switches one Home Assistant entity ("light.desk") via its
// domain's toggle service.
func HassToggle(ctx context.Context, baseURL, token string, verifyTLS bool, entityID string) error {
	domain, _, _ := strings.Cut(entityID, ".")
	return services.HassApi{URL: baseURL, Token: token, Verify: verifyTLS}.Call(ctx, domain, hassToggle, entityID)
}
