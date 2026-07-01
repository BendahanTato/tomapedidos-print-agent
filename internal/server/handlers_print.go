package server

import (
	"errors"
	"net/http"

	"github.com/tomapedidos/print-agent/internal/escpos"
	"github.com/tomapedidos/print-agent/internal/printer"
	"github.com/tomapedidos/print-agent/internal/queue"
)

// PrintJob is the payload the browser sends to POST /print (one job) or
// POST /print/batch (multiple jobs). The same struct is used in both
// endpoints; /print/batch wraps a slice.
type PrintJob struct {
	JobID      string      `json:"job_id,omitempty"`
	PrinterID  string      `json:"printer_id"`
	Template   string      `json:"template,omitempty"` // currently "kitchen" only
	Header     PrintHeader `json:"header"`
	Items      []PrintItem `json:"items"`
	Footer     string      `json:"footer,omitempty"`
	Options    PrintOpts   `json:"options,omitempty"`
}

// PrintHeader mirrors escpos.Header. Kept separate so the JSON contract
// is decoupled from the renderer and can grow new fields without
// breaking the printer package.
type PrintHeader struct {
	OrderNumber     int    `json:"order_number"`
	CustomerName    string `json:"customer_name,omitempty"`
	CustomerPhone   string `json:"customer_phone,omitempty"`
	CustomerAddress string `json:"customer_address,omitempty"`
	DeliveryType    string `json:"delivery_type,omitempty"`
	PaymentMethod   string `json:"payment_method,omitempty"`
	CreatedAt       string `json:"created_at"` // RFC3339, optional
}

// PrintItem mirrors escpos.Item. UnitPrice/Subtotal are reserved for the
// future "cash" template.
type PrintItem struct {
	Qty       int      `json:"qty"`
	Name      string   `json:"name"`
	Modifiers []string `json:"modifiers,omitempty"`
	Notes     string   `json:"notes,omitempty"`
	UnitPrice float64  `json:"unit_price,omitempty"`
	Subtotal  float64  `json:"subtotal,omitempty"`
}

// PrintOpts controls trailing behavior (cut, kick, copies, feed).
type PrintOpts struct {
	Cut             string `json:"cut,omitempty"`              // partial | full | none
	OpenCashDrawer  bool   `json:"open_cash_drawer,omitempty"`
	Copies          int    `json:"copies,omitempty"`
	FeedLinesBefore int    `json:"feed_lines_before,omitempty"`
}

// BatchRequest is the body of POST /print/batch.
type BatchRequest struct {
	Jobs []PrintJob `json:"jobs"`
}

// BatchResponse mirrors the slice the SaaS side expects.
type BatchResponse struct {
	Jobs []JobRef `json:"jobs"`
}

// JobRef is the per-job slice of a batch response.
type JobRef struct {
	JobID     string `json:"job_id"`
	PrinterID string `json:"printer_id"`
	Status    string `json:"status"`
	Bytes     int    `json:"bytes,omitempty"`
}

// SingleResponse is the body of POST /print (single job).
type SingleResponse struct {
	JobID     string `json:"job_id"`
	PrinterID string `json:"printer_id"`
	Status    string `json:"status"`
	Bytes     int    `json:"bytes"`
}

// printHandler accepts a single job and enqueues it.
func printHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req PrintJob
		if !decodeJSON(w, r, d.Log, &req) {
			return
		}
		job, err := submitJob(d, req)
		if err != nil {
			if errors.Is(err, errPrinterNotFound{}) || IsNotFound(err) {
				writeError(w, http.StatusNotFound, "printer_not_found", err.Error())
				return
			}
			if errors.Is(err, escpos.ErrEmptyPayload) {
				writeError(w, http.StatusBadRequest, "empty_payload", err.Error())
				return
			}
			writeError(w, http.StatusInternalServerError, "internal", err.Error())
			return
		}
		writeJSON(w, http.StatusAccepted, SingleResponse{
			JobID:     job.ID,
			PrinterID: job.PrinterID,
			Status:    string(job.Status),
			Bytes:     job.Bytes,
		})
	}
}

// printBatchHandler accepts multiple jobs, renders each one and enqueues
// them. A single printer_not_found error fails the whole request with
// 404; an empty payload per job skips that job (status printed in the
// response so the caller can retry).
func printBatchHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req BatchRequest
		if !decodeJSON(w, r, d.Log, &req) {
			return
		}
		resp := BatchResponse{Jobs: make([]JobRef, 0, len(req.Jobs))}
		for _, j := range req.Jobs {
			job, err := submitJob(d, j)
			if err != nil {
				if errors.Is(err, errPrinterNotFound{}) || IsNotFound(err) {
					writeError(w, http.StatusNotFound, "printer_not_found", err.Error())
					return
				}
				if errors.Is(err, escpos.ErrEmptyPayload) {
					// Skip empty jobs but still acknowledge so the caller
					// can move on.
					resp.Jobs = append(resp.Jobs, JobRef{
						JobID:     j.JobID,
						PrinterID: j.PrinterID,
						Status:    "skipped_empty",
					})
					continue
				}
				writeError(w, http.StatusInternalServerError, "internal", err.Error())
				return
			}
			resp.Jobs = append(resp.Jobs, JobRef{
				JobID:     job.ID,
				PrinterID: job.PrinterID,
				Status:    string(job.Status),
				Bytes:     job.Bytes,
			})
		}
		writeJSON(w, http.StatusAccepted, resp)
	}
}

// submitJob renders the ESC/POS bytes and pushes the job onto the queue.
// The actual write to the physical printer is performed asynchronously
// by the worker pool.
func submitJob(d Deps, req PrintJob) (*queue.Job, error) {
	info, ok := d.Registry.Get(req.PrinterID)
	if !ok {
		return nil, errPrinterNotFound{ID: req.PrinterID}
	}
	if len(req.Items) == 0 {
		return nil, escpos.ErrEmptyPayload
	}
	payload, err := renderToBytes(info, req)
	if err != nil {
		return nil, err
	}
	job, err := d.Queue.Submit(req.PrinterID, req.JobID, payload)
	if err != nil {
		// ErrDuplicate is fine; return the existing job so the caller
		// knows the print was already accepted.
		return job, nil
	}
	return job, nil
}

// renderToBytes turns the request payload into ESC/POS bytes using the
// default kitchen template. M5 will dispatch on req.Template for cash
// and receipt templates.
func renderToBytes(info printer.Info, req PrintJob) ([]byte, error) {
	codePage := info.CodePage
	if codePage == "" {
		codePage = "cp850"
	}
	chars := info.CharsPerLine
	if chars <= 0 {
		chars = 42
	}
	header := escpos.Header{
		OrderNumber:   req.Header.OrderNumber,
		CustomerName:  req.Header.CustomerName,
		CustomerPhone: req.Header.CustomerPhone,
		Address:       req.Header.CustomerAddress,
		DeliveryType:  req.Header.DeliveryType,
		PaymentMethod: req.Header.PaymentMethod,
		// CreatedAt is left zero on purpose: the renderer formats
		// time.Time; if M5 needs the exact creation timestamp it
		// can be added here.
	}
	items := make([]escpos.Item, 0, len(req.Items))
	for _, it := range req.Items {
		items = append(items, escpos.Item{
			Qty:       it.Qty,
			Name:      it.Name,
			Modifiers: it.Modifiers,
			Notes:     it.Notes,
			UnitPrice: it.UnitPrice,
			Subtotal:  it.Subtotal,
		})
	}
	cut := firstNonEmpty(req.Options.Cut, info.Cut, "partial")
	opts := escpos.Options{
		Cut:             cut,
		OpenCashDrawer:  req.Options.OpenCashDrawer,
		Copies:          req.Options.Copies,
		FeedLinesBefore: req.Options.FeedLinesBefore,
	}
	return escpos.RenderKitchen(codePage, chars, header, items, opts)
}
