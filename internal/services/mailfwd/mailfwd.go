// Package mailfwd hands invoice mails to Paperless.
//
//	List:    invoice mails from the last background run, per mailbox,
//	         with the Paperless connection of the same space
//	Forward: USE on both connections → download the mail's PDF/image
//	         attachments → Paperless consumes each one
package mailfwd

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"dashboard/internal/db"
	"dashboard/internal/enums"
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
	// ErrNoPaperless means the mailbox's space has no Paperless connection.
	ErrNoPaperless = errors.New("mailfwd.no_paperless")
	// ErrNoFiles means the mail carries no PDF or image.
	ErrNoFiles = errors.New("mailfwd.no_files")
)

// Item is one invoice mail that can be forwarded.
type Item struct {
	MailConn  int64
	Mailbox   string
	Paperless bool
	sources.MailInvoice
}

// mailboxes returns the mail connections who may use, with their space's
// Paperless connection (nil if none).
func mailboxes(d *sql.DB, who *access.Principal) (map[*model.Connection]*model.Connection, error) {
	out := map[*model.Connection]*model.Connection{}
	views, err := connections.Listing(d, who, enums.RightUse)
	if err != nil {
		return nil, err
	}
	err = db.WithTx(d, func(tx *sql.Tx) error {
		for _, v := range views {
			if v.Service != enums.ServiceMail {
				continue
			}
			mail, err := content.Connection(tx, v.ID)
			if err != nil || mail == nil {
				return err
			}
			out[mail] = nil
			siblings, err := content.Connections(tx, []int64{mail.SpaceID})
			if err != nil {
				return err
			}
			for _, c := range siblings {
				if enums.ServiceType(c.Service) == enums.ServicePaperless {
					out[mail] = c
				}
			}
		}
		return nil
	})
	return out, err
}

// List returns the invoice mails of every usable mailbox.
func List(ctx context.Context, d *sql.DB, who *access.Principal) ([]Item, error) {
	boxes, err := mailboxes(d, who)
	if err != nil {
		return nil, err
	}
	uid := who.UserID
	var out []Item
	for mail, paperless := range boxes {
		res, err := svcdata.Get(ctx, d, sources.DataKey(enums.ServiceMail), nil, mail, &uid, svcdata.Stored)
		if err != nil {
			continue
		}
		data, ok := res.Data.(*sources.MailDataset)
		if !ok {
			continue
		}
		for _, inv := range data.Invoices {
			out = append(out, Item{MailConn: mail.ID, Mailbox: mail.Name, Paperless: paperless != nil, MailInvoice: inv})
		}
	}
	return out, nil
}

// Forward sends the attachments of one mail to Paperless; returns how
// many files went.
func Forward(ctx context.Context, d *sql.DB, who *access.Principal, mailConnID int64, uid uint32, ip string) (int, error) {
	boxes, err := mailboxes(d, who)
	if err != nil {
		return 0, err
	}
	var mail, paperless *model.Connection
	for m, p := range boxes {
		if m.ID == mailConnID {
			mail, paperless = m, p
		}
	}
	if mail == nil {
		return 0, access.ErrDenied
	}
	if paperless == nil {
		return 0, ErrNoPaperless
	}
	if _, err := connections.Get(d, who, paperless.ID); err != nil {
		return 0, err
	}

	sctx, err := svcdata.SourceCtx(d, mail, who.UserID)
	if err != nil {
		return 0, err
	}
	files, err := sources.MailFiles(ctx, sctx, uid)
	if err != nil {
		return 0, err
	}
	if len(files) == 0 {
		return 0, ErrNoFiles
	}
	token, err := svcdata.Secret(d, paperless, who.UserID)
	if err != nil {
		return 0, err
	}
	for _, f := range files {
		if _, err := outbound.PaperlessUpload(ctx, paperless.URL, token, paperless.VerifyTLS, f.Name, "", f.Content); err != nil {
			return 0, err
		}
	}
	svcdata.Forget(paperless.ID)
	return len(files), auditsvc.Log(d, &who.UserID, "mail.to_paperless", fmt.Sprintf("%s#%d", mail.Name, uid), ip,
		map[string]any{"files": len(files)})
}
