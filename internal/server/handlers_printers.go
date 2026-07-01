package server

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/tomapedidos/print-agent/internal/printer"
)

func listPrintersHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		out := d.Registry.All()
		writeJSON(w, http.StatusOK, map[string]any{"printers": out})
	}
}

func getPrinterHandler(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := chi.URLParam(r, "id")
		info, ok := d.Registry.Get(id)
		if !ok {
			writeError(w, http.StatusNotFound, "printer_not_found", "printer "+id+" not registered")
			return
		}
		writeJSON(w, http.StatusOK, info)
	}
}

// PrinterListItem is the minimal projection of Info used by the agent
// panel to render a dropdown of available printers for a comanda.
// Defined here so the JSON shape is documented next to the handlers.
type PrinterListItem struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	Type       string `json:"type"`
	CodePage   string `json:"code_page"`
	CharsWidth int    `json:"chars_per_line"`
	Status     string `json:"status"`
	QueueDepth int    `json:"queue_depth"`
	LastPrint  string `json:"last_print_at,omitempty"`
}

// FormatInfo flattens a registry Info into the compact shape the
// tomaPedidos /admin/settings/comandas dropdown expects to consume
// once the SaaS browser fetches GET /printers.
func FormatInfo(p printer.Info) PrinterListItem {
	out := PrinterListItem{
		ID:         p.ID,
		Name:       p.Name,
		Type:       p.Type,
		CodePage:   p.CodePage,
		CharsWidth: p.CharsPerLine,
		Status:     string(p.Status),
		QueueDepth: p.QueueDepth,
	}
	if !p.LastPrintAt.IsZero() {
		out.LastPrint = p.LastPrintAt.UTC().Format(time.RFC3339)
	}
	return out
}
