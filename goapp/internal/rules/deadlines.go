// Package rules: deadline rules — tax dates from the space settings, with
// the estimated VAT liability.
package rules

import (
	"dashboard/internal/enums"
	"dashboard/internal/metrics"
	"dashboard/internal/sources"
)

func taxSettings(env Env) (metrics.TaxSettings, bool) {
	raw, ok := env.Settings["tax"].(map[string]any)
	if !ok {
		return metrics.TaxSettings{}, false
	}
	tax := metrics.TaxSettings{}
	if vat, ok := raw["vat"].(map[string]any); ok {
		if interval, ok := vat["return_interval"].(string); ok {
			tax.VATReturnInterval = interval
		}
		if ext, ok := vat["extension"].(bool); ok {
			tax.VATExtension = ext
		}
	}
	if prepay, ok := raw["prepayments"].(map[string]any); ok {
		if amount, ok := prepay["amount"].(float64); ok {
			tax.PrepaymentAmount = &amount
		}
	}
	if annual, ok := raw["annual_due"].(string); ok {
		tax.AnnualDueMonthDay = annual
	}
	return tax, true
}

func vatMethod(env Env) string {
	raw, _ := env.Settings["tax"].(map[string]any)
	vat, _ := raw["vat"].(map[string]any)
	method, _ := vat["method"].(string)
	if method == "" {
		return "ist"
	}
	return method
}

func init() {
	Register("tax.deadlines", Deadlines, map[string]any{"notice_days": 14.0, "warn_days": 3.0},
		func(_ any, cfg map[string]any, env Env) []Finding {
			tax, ok := taxSettings(env)
			if !ok {
				return nil
			}
			ninja, _ := env.Datasets[string(enums.ServiceInvoiceNinja)].(*sources.NinjaDataset)

			var found []Finding
			for _, item := range metrics.UpcomingDeadlines(tax, env.Today, cfgInt(cfg, "notice_days")) {
				left := int(item.Due.Sub(env.Today).Hours() / 24)
				level := enums.SeverityInfo
				if left <= cfgInt(cfg, "warn_days") {
					level = enums.SeverityWarn
				}
				found = append(found, deadlineFinding(item, level, ninja, env))
			}
			return found
		})
}

func deadlineFinding(item metrics.TaxDeadline, level enums.Severity, ninja *sources.NinjaDataset, env Env) Finding {
	params := map[string]any{"day": Day(item.Due)}
	message := "tax." + item.Kind

	switch item.Kind {
	case "vat_return":
		params["period"] = item.Period
		if ninja != nil {
			out := metrics.NinjaOutputVAT(ninja, item.PeriodStart, item.PeriodEnd, vatMethod(env))
			inp := metrics.NinjaInputVAT(ninja, item.PeriodStart, item.PeriodEnd)
			params["amount"] = Money(out-inp, "")
			message = "tax.vat_return_amount"
		}
	case "prepayment":
		if item.Amount != nil {
			params["amount"] = Money(*item.Amount, "")
			message = "tax.prepayment_amount"
		}
	case "annual":
		params["year"] = item.Year
	}

	return Finding{
		Fingerprint: item.Kind + ":" + item.Due.Format("2006-01-02"), Rule: "tax.deadlines",
		Severity: level, Message: message, Params: params, Due: item.Due.Format("2006-01-02"),
		Sources: []string{"tax"},
	}
}
