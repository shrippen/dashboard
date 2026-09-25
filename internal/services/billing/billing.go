// Package billing turns unbilled Kimai time into Invoice Ninja drafts.
//
//	Candidates: spaces the caller edits with a Kimai and an Invoice Ninja
//	            connection → one draft per customer (stored data, fast)
//	Create:     fresh data → draft invoice in Ninja → optionally flag the
//	            Kimai sheets as exported, so they are not billed twice
package billing

import (
	"context"
	"database/sql"
	"errors"
	"strconv"

	"dashboard/internal/db"
	"dashboard/internal/enums"
	"dashboard/internal/metrics"
	"dashboard/internal/model"
	"dashboard/internal/outbound"
	"dashboard/internal/repos/content"
	"dashboard/internal/services/access"
	auditsvc "dashboard/internal/services/audit"
	"dashboard/internal/services/connections"
	"dashboard/internal/services/svcdata"
	"dashboard/internal/sources"
)

var (
	// ErrNoClient means no Invoice Ninja client carries the customer's name.
	ErrNoClient = errors.New("billing.no_client")
	// ErrNothing means the customer has no unbilled time (any more).
	ErrNothing = errors.New("billing.nothing")
)

// ExportMode says whether billed Kimai sheets get flagged as exported.
type ExportMode bool

const (
	KeepSheets ExportMode = false
	MarkSheets ExportMode = true
)

// Candidate is one customer's draft in one space.
type Candidate struct {
	SpaceID   int64
	SpaceName string
	metrics.Draft
}

// pair is a space's Kimai and Invoice Ninja connection.
type pair struct {
	space        access.SpaceRef
	kimai, ninja *model.Connection
}

func pairs(d *sql.DB, who *access.Principal, spaceID int64) ([]pair, error) {
	var out []pair
	err := db.WithTx(d, func(tx *sql.Tx) error {
		for id, ref := range who.Spaces {
			if spaceID != 0 && id != spaceID {
				continue
			}
			if access.SpaceRight(who, &ref) < enums.RightEdit {
				continue
			}
			conns, err := content.Connections(tx, []int64{id})
			if err != nil {
				return err
			}
			p := pair{space: ref}
			for _, c := range conns {
				switch enums.ServiceType(c.Service) {
				case enums.ServiceKimai:
					p.kimai = c
				case enums.ServiceInvoiceNinja:
					p.ninja = c
				}
			}
			if p.kimai != nil && p.ninja != nil {
				out = append(out, p)
			}
		}
		return nil
	})
	return out, err
}

func load(ctx context.Context, d *sql.DB, who *access.Principal, p pair, fresh svcdata.Freshness) (*sources.KimaiDataset, *sources.NinjaDataset, error) {
	uid := who.UserID
	k, err := svcdata.Get(ctx, d, sources.DataKey(enums.ServiceKimai), nil, p.kimai, &uid, fresh)
	if err != nil {
		return nil, nil, err
	}
	n, err := svcdata.Get(ctx, d, sources.DataKey(enums.ServiceInvoiceNinja), nil, p.ninja, &uid, fresh)
	if err != nil {
		return nil, nil, err
	}
	kimai, _ := k.Data.(*sources.KimaiDataset)
	ninja, _ := n.Data.(*sources.NinjaDataset)
	return kimai, ninja, nil
}

// Candidates lists drafts from the last background run.
func Candidates(ctx context.Context, d *sql.DB, who *access.Principal) ([]Candidate, error) {
	found, err := pairs(d, who, 0)
	if err != nil {
		return nil, err
	}
	var out []Candidate
	for _, p := range found {
		kimai, ninja, err := load(ctx, d, who, p, svcdata.Stored)
		if err != nil || kimai == nil {
			continue
		}
		for _, draft := range metrics.Drafts(kimai, ninja) {
			out = append(out, Candidate{SpaceID: p.space.ID, SpaceName: p.space.Name, Draft: draft})
		}
	}
	return out, nil
}

// Create writes one customer's draft to Invoice Ninja and returns its number.
func Create(ctx context.Context, d *sql.DB, who *access.Principal, spaceID, customerID int64, mode ExportMode, ip string) (string, error) {
	found, err := pairs(d, who, spaceID)
	if err != nil {
		return "", err
	}
	if len(found) == 0 {
		return "", access.ErrDenied
	}
	p := found[0]
	for _, c := range []*model.Connection{p.kimai, p.ninja} {
		if _, err := connections.Get(d, who, c.ID); err != nil {
			return "", err
		}
	}

	// Fresh data: a sheet billed a minute ago must not be billed again.
	kimai, ninja, err := load(ctx, d, who, p, svcdata.Force)
	if err != nil {
		return "", err
	}
	if kimai == nil || ninja == nil {
		return "", ErrNothing
	}
	draft, ok := draftFor(metrics.Drafts(kimai, ninja), customerID)
	if !ok {
		return "", ErrNothing
	}
	if draft.ClientKey == "" {
		return "", ErrNoClient
	}

	ninjaSecret, err := svcdata.Secret(d, p.ninja, who.UserID)
	if err != nil {
		return "", err
	}
	lines := make([]outbound.NinjaLine, 0, len(draft.Lines))
	for _, l := range draft.Lines {
		lines = append(lines, outbound.NinjaLine{Product: l.Product, Notes: l.Notes, Quantity: l.Hours, Cost: l.Rate})
	}
	number, err := outbound.NinjaDraftInvoice(ctx, p.ninja.URL, ninjaSecret, p.ninja.VerifyTLS, draft.ClientKey, lines)
	if err != nil {
		return "", err
	}

	if mode == MarkSheets {
		kimaiSecret, err := svcdata.Secret(d, p.kimai, who.UserID)
		if err != nil {
			return number, err
		}
		for _, id := range draft.SheetIDs {
			if err := outbound.KimaiMarkExported(ctx, p.kimai.URL, kimaiSecret, p.kimai.VerifyTLS, id); err != nil {
				return number, err
			}
		}
	}
	svcdata.Forget(p.kimai.ID)
	svcdata.Forget(p.ninja.ID)
	return number, auditsvc.Log(d, &who.UserID, "billing.draft", draft.Customer, ip,
		map[string]any{"number": number, "sheets": len(draft.SheetIDs), "total": strconv.FormatFloat(draft.Total, 'f', 2, 64)})
}

func draftFor(drafts []metrics.Draft, customerID int64) (metrics.Draft, bool) {
	for _, d := range drafts {
		if d.CustomerID == customerID {
			return d, true
		}
	}
	return metrics.Draft{}, false
}
