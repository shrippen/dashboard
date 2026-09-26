// Package mailfwd hands invoice mails to Paperless.
//
//	List:    invoice mails from the last background run, per mailbox,
//	         with the Paperless connection of the same space
//	Forward: USE on both connections → download the mail's PDF/image
//	         attachments → Paperless consumes each one
//	Read:    on request, Claude reads the attachments' invoice fields;
//	         Forward then titles the documents "Vendor Number"
package mailfwd

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"andon/internal/db"
	"andon/internal/drivers/llm"
	"andon/internal/enums"
	"andon/internal/model"
	"andon/internal/outbound"
	"andon/internal/repos/content"
	repodata "andon/internal/repos/data"
	"andon/internal/services/access"
	"andon/internal/services/assist"
	auditsvc "andon/internal/services/audit"
	"andon/internal/services/connections"
	"andon/internal/services/svcdata"
	"andon/internal/sources"
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
	Read      *assist.Invoice // nil until read
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
		reads, err := repodata.MailReads(d, mail.ID)
		if err != nil {
			return nil, err
		}
		for _, inv := range data.Invoices {
			item := Item{MailConn: mail.ID, Mailbox: mail.Name, Paperless: paperless != nil, MailInvoice: inv}
			if fields, ok := reads[inv.UID]; ok {
				item.Read = invoiceOf(fields)
			}
			out = append(out, item)
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

	files, err := mailFiles(ctx, d, who, mail, uid)
	if err != nil {
		return 0, err
	}
	token, err := svcdata.Secret(d, paperless, who.UserID)
	if err != nil {
		return 0, err
	}
	title := ""
	if reads, err := repodata.MailReads(d, mail.ID); err == nil && reads[uid] != nil {
		title = invoiceOf(reads[uid]).Title()
	}
	for _, f := range files {
		if _, err := outbound.PaperlessUpload(ctx, outbound.Target{URL: paperless.URL, Token: token, VerifyTLS: paperless.VerifyTLS}, f.Name, title, f.Content); err != nil {
			return 0, err
		}
	}
	svcdata.Forget(paperless.ID)
	return len(files), auditsvc.Log(d, &who.UserID, "mail.to_paperless", fmt.Sprintf("%s#%d", mail.Name, uid), ip,
		map[string]any{"files": len(files)})
}

// mailFiles downloads one mail's attachments; USE on the mailbox is
// checked by the caller.
func mailFiles(ctx context.Context, d *sql.DB, who *access.Principal, mail *model.Connection, uid uint32) ([]sources.MailFile, error) {
	sctx, err := svcdata.SourceCtx(d, mail, who.UserID)
	if err != nil {
		return nil, err
	}
	files, err := sources.MailFiles(ctx, sctx, uid)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, ErrNoFiles
	}
	return files, nil
}

// Read lets Claude read the invoice fields of one mail's attachments and
// keeps them for the list and the Paperless title.
func Read(ctx context.Context, d *sql.DB, who *access.Principal, mailConnID int64, uid uint32, ip string) (assist.Invoice, error) {
	boxes, err := mailboxes(d, who)
	if err != nil {
		return assist.Invoice{}, err
	}
	var mail *model.Connection
	for m := range boxes {
		if m.ID == mailConnID {
			mail = m
		}
	}
	if mail == nil {
		return assist.Invoice{}, access.ErrDenied
	}
	files, err := mailFiles(ctx, d, who, mail, uid)
	if err != nil {
		return assist.Invoice{}, err
	}
	var readable []llm.File
	for _, f := range files {
		if media := http.DetectContentType(f.Content); llm.Readable(media) {
			readable = append(readable, llm.File{Media: media, Content: f.Content})
		}
	}
	if len(readable) == 0 {
		return assist.Invoice{}, ErrNoFiles
	}
	inv, err := assist.ReadInvoice(ctx, readable)
	if err != nil {
		return assist.Invoice{}, err
	}

	raw, _ := json.Marshal(inv)
	fields := map[string]any{}
	_ = json.Unmarshal(raw, &fields)
	if err := db.WithTx(d, func(tx *sql.Tx) error { return repodata.SaveMailRead(tx, mail.ID, uid, fields) }); err != nil {
		return assist.Invoice{}, err
	}
	return inv, auditsvc.Log(d, &who.UserID, "mail.read", fmt.Sprintf("%s#%d", mail.Name, uid), ip, nil)
}

func invoiceOf(fields map[string]any) *assist.Invoice {
	raw, _ := json.Marshal(fields)
	var inv assist.Invoice
	_ = json.Unmarshal(raw, &inv)
	return &inv
}
