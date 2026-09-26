package hints

// What a hint costs, and which rules only produce noise.
//
//	Value      the largest money amount among a hint's params, so the list
//	           can be sorted by what doing nothing costs
//	ByValue    that order
//	Noisy      rules whose hints are nearly always dismissed, not fixed

import (
	"database/sql"
	"sort"
	"time"

	"andon/internal/enums"
	data "andon/internal/repos/data"
	"andon/internal/services/access"
)

const (
	moneyKey    = "$money"
	currencyKey = "currency"
	noisyDays   = 90
	noisyMin    = 5   // hints of a rule before judging it
	noisyShare  = 0.8 // share dismissed (acknowledged or snoozed)
	noisyLimit  = 5000
)

// moneyValue finds the largest typed money param ({"$money": 12.5}).
func moneyValue(params map[string]any) (float64, string) {
	best, currency := 0.0, ""
	for _, v := range params {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		amount, ok := m[moneyKey].(float64)
		if !ok || amount <= best {
			continue
		}
		best = amount
		currency, _ = m[currencyKey].(string)
	}
	return best, currency
}

// ByValue orders hints by the money they name, highest first; hints
// without an amount keep their order after them.
func ByValue(views []View) {
	sort.SliceStable(views, func(i, j int) bool { return views[i].Value > views[j].Value })
}

// Noisy is a rule whose hints are dismissed instead of fixed.
type Noisy struct {
	Rule, Space string
	SpaceID     int64
	Opened      int // hints opened or reopened
	Dismissed   int // acknowledged or snoozed
}

// NoisyRules lists rules of the caller's spaces where most hints of the
// last 90 days were dismissed: candidates for a different threshold.
func NoisyRules(d *sql.DB, who *access.Principal, now time.Time) ([]Noisy, error) {
	ids := make([]int64, 0, len(who.Spaces))
	for id := range who.Spaces {
		ids = append(ids, id)
	}
	kinds := []string{string(enums.EventOpened), string(enums.EventReopened), string(enums.EventAcked), string(enums.EventSnoozed)}
	events, err := data.HintEventsSince(d, ids, kinds, now.AddDate(0, 0, -noisyDays), noisyLimit)
	if err != nil {
		return nil, err
	}
	type key struct {
		space int64
		rule  string
	}
	counts := map[key]*Noisy{}
	for _, e := range events {
		if e.Owner != 0 && e.Owner != who.UserID {
			continue
		}
		k := key{e.SpaceID, e.Rule}
		n, ok := counts[k]
		if !ok {
			n = &Noisy{Rule: e.Rule, SpaceID: e.SpaceID, Space: who.Spaces[e.SpaceID].Name}
			counts[k] = n
		}
		switch e.Kind {
		case string(enums.EventOpened), string(enums.EventReopened):
			n.Opened++
		default:
			n.Dismissed++
		}
	}
	var out []Noisy
	for _, n := range counts {
		if n.Opened >= noisyMin && float64(n.Dismissed) >= float64(n.Opened)*noisyShare {
			out = append(out, *n)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Dismissed > out[j].Dismissed })
	return out, nil
}
