package server

import (
	"net/http"

	"github.com/tomapedidos/print-agent/internal/escpos"
	"github.com/tomapedidos/print-agent/internal/printer"
)

// CashDrawerRequest is the body of POST /cash-drawer/kick. The agent
// emits the ESC p m t1 t2 pulse on the printer that owns the cash
// drawer (typically the one marked as "caja" in the SaaS comanda CRUD).
type CashDrawerRequest struct {
	PrinterID string `json:"printer_id"`
	PulseMs   int    `json:"pulse_ms,omitempty"` // on-time in ms; default 100
	OffMs     int    `json:"off_ms,omitempty"`   // off-time in ms; default 100
}

func cashDrawerHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req CashDrawerRequest
		if !decodeJSON(w, r, d.Log, &req) {
			return
		}
		if req.PrinterID == "" {
			writeError(w, http.StatusBadRequest, "missing_printer_id", "printer_id is required")
			return
		}
		if _, ok := d.Registry.Get(req.PrinterID); !ok {
			writeError(w, http.StatusNotFound, "printer_not_found", "printer "+req.PrinterID+" not registered")
			return
		}
		onMs := req.PulseMs
		if onMs <= 0 {
			onMs = 100
		}
		offMs := req.OffMs
		if offMs <= 0 {
			offMs = 100
		}
		// Render a minimal ESC/POS payload that just kicks the drawer.
		// This bypasses the queue because the kick is not a real print
		// job; the bytes are flushed directly to the printer.
		payload := escpos.NewBuilder().
			Initialize().
			KickDrawer(0, onMs, offMs).
			Bytes()

		prs := d.Registry.Printers()
		pr, ok := prs[req.PrinterID]
		if !ok {
			writeError(w, http.StatusNotFound, "printer_not_found", "printer "+req.PrinterID+" not registered")
			return
		}
		if err := pr.Write(r.Context(), payload); err != nil {
			d.Registry.SetStatus(req.PrinterID, printer.StatusError, err.Error())
			writeError(w, http.StatusBadGateway, "drawer_kick_failed", err.Error())
			return
		}
		d.Registry.MarkPrinted(req.PrinterID)
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":         true,
			"printer_id": req.PrinterID,
			"pulse_ms":   onMs,
		})
	}
}
