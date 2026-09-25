package services

// Write calls, used only by the outbound layer:
//
//	Kimai        POST /api/timesheets, PATCH /api/timesheets/{id}/stop, PATCH …/export
//	InvoiceNinja POST /api/v1/invoices
//	Paperless    POST /api/documents/post_document/ (multipart)

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"net/http"

	"dashboard/internal/drivers/httpclient"
)

// Send runs one write against /api/<path> of Kimai.
func (a KimaiApi) Send(ctx context.Context, method, path string, body any) (any, error) {
	return sendJSON(ctx, method, a.URL+"/api/"+path, a.headers(), body, !a.Verify)
}

// Post creates one entity in Invoice Ninja, e.g. ("invoices", {...}).
func (a NinjaApi) Post(ctx context.Context, entity string, body any) (any, error) {
	return sendJSON(ctx, http.MethodPost, a.URL+"/api/v1/"+entity, a.headers(), body, !a.Verify)
}

// Upload sends one file to Paperless' consume endpoint; Paperless answers
// with the id of its consumption task.
func (a PaperlessApi) Upload(ctx context.Context, filename, title string, content []byte) (string, error) {
	var buf bytes.Buffer
	form := multipart.NewWriter(&buf)
	if title != "" {
		if err := form.WriteField("title", title); err != nil {
			return "", err
		}
	}
	part, err := form.CreateFormFile("document", filename)
	if err != nil {
		return "", err
	}
	if _, err := part.Write(content); err != nil {
		return "", err
	}
	if err := form.Close(); err != nil {
		return "", err
	}

	headers := map[string]string{"Authorization": "Token " + a.Token, "Content-Type": form.FormDataContentType()}
	resp, err := httpclient.Request(ctx, http.MethodPost, joinURL(a.URL, "api/documents/post_document/"),
		httpclient.Options{Headers: headers, Body: buf.Bytes(), SkipVerify: !a.Verify})
	if err != nil {
		return "", ApiError{err.Error()}
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		return "", ApiError{resp.Status}
	}
	task, _ := io.ReadAll(io.LimitReader(resp.Body, httpclient.MaxBody))
	return string(bytes.Trim(task, "\" \n")), nil
}
