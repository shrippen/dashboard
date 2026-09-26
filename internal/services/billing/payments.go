package billing

// Bank incomes that pay open invoices, booked in Invoice Ninja on request.
//
//	Payments: spaces the caller edits with Sure and Invoice Ninja →
//	          metrics.PaymentMatches on stored data
//	Book:     fresh data, the match must still hold → payment in Ninja
//	          for exactly the booked amount (at most the open balance)

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"andon/internal/db"
	"andon/internal/enums"
	"andon/internal/metrics"
	"andon/internal/model"
	"andon/internal/outbound"
	"andon/internal/repos/content"
	"andon/internal/services/access"
	auditsvc "andon/internal/services/audit"
	"andon/internal/services/connections"
	"andon/internal/services/svcdata"
	"andon/internal/sources"
)

// paymentDays is how far back incomes are matched.
const paymentDays = 90

// ErrNoMatch means the income no longer pays that invoice (booked meanwhile).
var ErrNoMatch = errors.New("billing.no_match")

// Payment is one proposed booking.
type Payment struct {
	SpaceID   int64
	SpaceName string
	metrics.PaymentMatch
}

// bank is a space's Sure and Invoice Ninja connection.
type bank struct {
	space       access.SpaceRef
	sure, ninja *model.Connection
}

func banks(d *sql.DB, who *access.Principal, spaceID int64) ([]bank, error) {
	var out []bank
	err := db.WithTx(d, func(tx *sql.Tx) error {
		for id, ref := range who.Spaces {
			if (spaceID != 0 && id != spaceID) || access.SpaceRight(who, &ref) < enums.RightEdit {
				continue
			}
			conns, err := content.Connections(tx, []int64{id})
			if err != nil {
				return err
			}
			b := bank{space: ref}
			for _, c := range conns {
				switch enums.ServiceType(c.Service) {
				case enums.ServiceSure:
					b.sure = c
				case enums.ServiceInvoiceNinja:
					b.ninja = c
				}
			}
			if b.sure != nil && b.ninja != nil {
				out = append(out, b)
			}
		}
		return nil
	})
	return out, err
}

func loadBank(ctx context.Context, d *sql.DB, who *access.Principal, b bank, fresh svcdata.Freshness) (*sources.SureDataset, *sources.NinjaDataset, error) {
	uid := who.UserID
	s, err := svcdata.Get(ctx, d, sources.DataKey(enums.ServiceSure), nil, b.sure, &uid, fresh)
	if err != nil {
		return nil, nil, err
	}
	n, err := svcdata.Get(ctx, d, sources.DataKey(enums.ServiceInvoiceNinja), nil, b.ninja, &uid, fresh)
	if err != nil {
		return nil, nil, err
	}
	sure, _ := s.Data.(*sources.SureDataset)
	ninja, _ := n.Data.(*sources.NinjaDataset)
	return sure, ninja, nil
}

// Payments lists proposed bookings from the last background run.
func Payments(ctx context.Context, d *sql.DB, who *access.Principal) ([]Payment, error) {
	found, err := banks(d, who, 0)
	if err != nil {
		return nil, err
	}
	var out []Payment
	for _, b := range found {
		sure, ninja, err := loadBank(ctx, d, who, b, svcdata.Stored)
		if err != nil || sure == nil || ninja == nil {
			continue
		}
		for _, m := range metrics.PaymentMatches(sure, ninja, time.Now().UTC(), paymentDays) {
			out = append(out, Payment{SpaceID: b.space.ID, SpaceName: b.space.Name, PaymentMatch: m})
		}
	}
	return out, nil
}

// Book records one matched income as a payment of its invoice.
func Book(ctx context.Context, d *sql.DB, who *access.Principal, spaceID int64, txnID string, invoiceID int64, ip string) error {
	found, err := banks(d, who, spaceID)
	if err != nil {
		return err
	}
	if len(found) == 0 {
		return access.ErrDenied
	}
	b := found[0]
	for _, c := range []*model.Connection{b.sure, b.ninja} {
		if _, err := connections.Get(d, who, c.ID); err != nil {
			return err
		}
	}

	// Fresh data: an invoice paid meanwhile must not be paid twice.
	sure, ninja, err := loadBank(ctx, d, who, b, svcdata.Force)
	if err != nil {
		return err
	}
	if sure == nil || ninja == nil {
		return ErrNoMatch
	}
	var match *metrics.PaymentMatch
	for _, m := range metrics.PaymentMatches(sure, ninja, time.Now().UTC(), paymentDays) {
		if m.Txn.ID == txnID && m.Invoice.ID == invoiceID {
			match = &m
		}
	}
	if match == nil || match.Invoice.Key == "" {
		return ErrNoMatch
	}
	clientKey := ""
	for _, c := range ninja.Clients {
		if c.ID == match.Invoice.ClientID {
			clientKey = c.Key
		}
	}
	token, err := svcdata.Secret(d, b.ninja, who.UserID)
	if err != nil {
		return err
	}
	amount := min(match.Txn.Amount, match.Invoice.Balance)
	if err := outbound.NinjaPayment(ctx, outbound.Target{URL: b.ninja.URL, Token: token, VerifyTLS: b.ninja.VerifyTLS}, clientKey, match.Invoice.Key, amount,
		match.Day.Format(time.DateOnly), match.Txn.Name); err != nil {
		return err
	}
	svcdata.Forget(b.ninja.ID)
	return auditsvc.Log(d, &who.UserID, "billing.payment", match.Invoice.Number, ip,
		map[string]any{"amount": fmt.Sprintf("%.2f", amount)})
}
