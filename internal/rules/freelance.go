package rules

import (
	"fmt"
	"strings"
	"time"

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

func init() {
	paperless := string(enums.ServicePaperless)

	// Cancellation deadlines of contracts filed in Paperless.
	Register("paperless.contract_notice", paperless, map[string]any{"info_days": 90.0, "warn_days": 45.0, "critical_days": 14.0},
		func(raw any, cfg map[string]any, env Env) []Finding {
			data, _ := raw.(*sources.PaperlessDataset)
			var found []Finding
			for _, c := range data.Contracts {
				if c.Deadline.IsZero() || c.Deadline.Before(env.Today) {
					continue
				}
				days := int(c.Deadline.Sub(env.Today).Hours() / hoursPerDay)
				level, ok := expiryLevel(days, cfg)
				if !ok {
					continue
				}
				found = append(found, Finding{Fingerprint: fmt.Sprintf("contract:%d:%s", c.ID, c.Deadline.Format(time.DateOnly)),
					Rule: "paperless.contract_notice", Severity: level, Message: "paperless.contract_notice",
					Params: map[string]any{"title": c.Title, "correspondent": c.Correspondent, "day": Day(c.Deadline), "days": days,
						"end": Day(c.End)},
					Due:       c.Deadline.Format(time.DateOnly),
					ActionURL: strings.TrimRight(data.URL, "/") + fmt.Sprintf("/documents/%d/details", c.ID), ActionLabel: "open_in_paperless",
					Sources: []string{paperless}})
			}
			return found
		})
}
