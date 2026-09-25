package rules

import (
	"fmt"
	"strings"

	"dashboard/internal/enums"
	"dashboard/internal/metrics"
	"dashboard/internal/sources"
)

func init() {
	ninja := string(enums.ServiceInvoiceNinja)

	// A client paying notably slower than usual is an early cash warning.
	Register("in.payment_worse", ninja, map[string]any{"days": 10.0}, func(raw any, cfg map[string]any, env Env) []Finding {
		data, _ := raw.(*sources.NinjaDataset)
		var found []Finding
		for _, m := range metrics.PaymentMorale(data, cfgInt(cfg, "days")) {
			if !m.Worse {
				continue
			}
			found = append(found, Finding{Fingerprint: fmt.Sprintf("slower:%d", m.ClientID), Rule: "in.payment_worse",
				Severity: enums.SeverityWarn, Message: "in.payment_worse",
				Params:    map[string]any{"client": m.Client, "recent": m.RecentDays, "avg": m.AvgDays},
				ActionURL: strings.TrimRight(data.URL, "/") + "/#/clients", ActionLabel: "open_in_invoiceninja", Sources: []string{ninja}})
		}
		return found
	})
}
