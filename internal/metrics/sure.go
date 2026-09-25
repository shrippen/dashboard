package metrics

import (
	"time"

	"dashboard/internal/sources"
)

// SureDue sums active recurring expenses expected within days.
func SureDue(data *sources.SureDataset, today time.Time, days int) float64 {
	horizon := today.AddDate(0, 0, days)
	var total float64
	for _, r := range data.Recurring {
		next, ok := ParseDay(r.Next)
		if r.Status == "active" && r.Expense && ok && !next.After(horizon) {
			total += r.Amount
		}
	}
	return round2(total)
}

// SureCash is the balance of all bank (depository) accounts.
func SureCash(data *sources.SureDataset) float64 {
	var total float64
	for _, a := range data.Accounts {
		if a.Type == "depository" {
			total += a.Balance
		}
	}
	return round2(total)
}

// SureInfo: net worth for the link tile.
func SureInfo(data *sources.SureDataset) []InfoPart {
	return []InfoPart{part("sure.cash", map[string]any{"amount": map[string]any{"$money": SureCash(data), "currency": data.Currency}})}
}
