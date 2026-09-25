package outbound

import (
	"context"

	"dashboard/internal/drivers/services"
)

// NinjaLine is one invoice position to create.
type NinjaLine struct {
	Product, Notes string
	Quantity, Cost float64
}

// NinjaDraftInvoice creates an invoice draft for a client (hashed v5 id)
// and returns the new invoice's number.
func NinjaDraftInvoice(ctx context.Context, baseURL, token string, verifyTLS bool, clientKey string, lines []NinjaLine) (string, error) {
	items := make([]map[string]any, 0, len(lines))
	for _, l := range lines {
		items = append(items, map[string]any{"product_key": l.Product, "notes": l.Notes, "quantity": l.Quantity, "cost": l.Cost})
	}
	body, err := services.NinjaApi{URL: baseURL, Token: token, Verify: verifyTLS}.Post(ctx, "invoices",
		map[string]any{"client_id": clientKey, "line_items": items})
	if err != nil {
		return "", err
	}
	data, _ := body.(map[string]any)
	inner, _ := data["data"].(map[string]any)
	number, _ := inner["number"].(string)
	return number, nil
}
