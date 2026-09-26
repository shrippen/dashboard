package outbound

import (
	"context"
	"strings"

	"dashboard/internal/drivers/services"
)

const hassToggle = "toggle"

// HassToggle switches one Home Assistant entity ("light.desk") via its
// domain's toggle service.
func HassToggle(ctx context.Context, to Target, entityID string) error {
	domain, _, _ := strings.Cut(entityID, ".")
	return services.HassApi{URL: to.URL, Token: to.Token, Verify: to.VerifyTLS}.Call(ctx, domain, hassToggle, entityID)
}
