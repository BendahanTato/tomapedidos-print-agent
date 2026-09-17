package escpos

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/template"
	"time"
)

// Item is one line in a print job, with optional modifiers and a free-form
// note. UnitPrice and Subtotal are only honored by the "cash" template.
type Item struct {
	Qty        int
	Name       string
	Modifiers  []string
	Notes      string
	UnitPrice  float64
	Subtotal   float64
}

// Header carries the order-level metadata rendered at the top of the ticket.
type Header struct {
	OrderNumber   int
	CustomerName  string
	CustomerPhone string
	Address       string
	DeliveryType  string
	PaymentMethod string
	CreatedAt     time.Time
}

// Options controls the trailing behavior of every template (cut, kick,
// feed lines).
type Options struct {
	Cut              string // "partial" | "full" | "none"
	OpenCashDrawer   bool
	Copies           int
	FeedLinesBefore  int
	ItemDoubleHeight bool
}

// formatTime renders a timestamp as "YYYY-MM-DD HH:MM" in the local timezone.
func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Local().Format("2006-01-02 15:04")
}

// formatQty pads "Nx " so columns line up at narrow widths.
func formatQty(qty int) string {
	if qty <= 0 {
		return "1  "
	}
	return fmt.Sprintf("%dx", qty)
}

// deliveryLabel returns a short uppercase label for the fulfillment type.
func deliveryLabel(t string) string {
	switch strings.ToLower(strings.TrimSpace(t)) {
	case "delivery":
		return "DELIVERY"
	case "take_away", "takeaway", "pickup":
		return "TAKE AWAY"
	default:
		if t == "" {
			return ""
		}
		return strings.ToUpper(t)
	}
}

const defaultKitchenTemplate = `{{esc "center"}}{{size 2 2}}{{esc "bold"}}PEDIDO #{{.Header.OrderNumber}}{{size 1 1}}{{esc "normal"}}
{{if .Header.CreatedAt}}{{.Header.CreatedAt | formatTime}}{{end}}
{{if .Header.DeliveryType}}{{.Header.DeliveryType | deliveryLabel}}{{end}}
{{esc "left"}}{{sep "-"}}
{{if .Header.CustomerName}}Cliente: {{.Header.CustomerName}}
{{end}}{{if .Header.CustomerPhone}}Tel: {{.Header.CustomerPhone}}
{{end}}{{if .Header.Address}}Dir: {{.Header.Address}}
{{end}}{{sep "-"}}
{{range .Items}}{{if $.Opts.ItemDoubleHeight}}{{size 1 2}}{{end}}{{esc "bold"}}{{.Qty | formatQty}} {{esc "normal"}}{{if $.Opts.ItemDoubleHeight}}{{size 1 2}}{{end}}{{.Name | truncate}}{{size 1 1}}
{{range .Modifiers}}  - {{.}}
{{end}}{{if .Notes}}  OBS: {{.Notes}}
{{end}}{{esc "normal"}}{{end}}{{sep "-"}}`

type templateData struct {
	Header Header
	Items  []Item
	Opts   Options
}

// RenderTemplate produces the bytes for a ticket by evaluating a text/template.
// It searches for "templates/<templateName>.tmpl" and falls back to a default.
func RenderTemplate(templateName string, codePage string, charsPerLine int, h Header, items []Item, opts Options) ([]byte, error) {
	if len(items) == 0 {
		return nil, ErrEmptyPayload
	}
	width := charsPerLine
	if width <= 0 {
		width = 42
	}

	funcMap := template.FuncMap{
		"esc": func(cmd string) string {
			switch cmd {
			case "bold":
				return "\x1bE\x01"
			case "normal":
				return "\x1bE\x00\x1b-\x00\x1d!\x00\x1ba\x00"
			case "center":
				return "\x1ba\x01"
			case "left":
				return "\x1ba\x00"
			case "right":
				return "\x1ba\x02"
			case "double_size":
				return "\x1d!\x11"
			case "double_height":
				return "\x1d!\x01"
			case "double_width":
				return "\x1d!\x10"
			default:
				return ""
			}
		},
		"size": func(w, h int) string {
			if w < 1 { w = 1 }
			if w > 8 { w = 8 }
			if h < 1 { h = 1 }
			if h > 8 { h = 8 }
			n := byte(((w - 1) << 4) | (h - 1))
			return fmt.Sprintf("\x1d!%c", n)
		},
		"sep": func(char string) string {
			return strings.Repeat(char, width) + "\n"
		},
		"formatTime": formatTime,
		"formatQty":  formatQty,
		"formatMoney": func(v float64) string {
			return fmt.Sprintf("$%.2f", v)
		},
		"deliveryLabel": deliveryLabel,
		"truncate": func(s string) string {
			return truncate(s, width-4)
		},
	}

	tmplName := templateName
	if tmplName == "" {
		tmplName = "kitchen"
	}
	
	tmplPath := filepath.Join("templates", tmplName+".tmpl")
	
	tmpl := template.New(tmplName).Funcs(funcMap)
	body, err := os.ReadFile(tmplPath)
	if err != nil {
		// Fallback to default.tmpl on disk
		defaultPath := filepath.Join("templates", "default.tmpl")
		body, err = os.ReadFile(defaultPath)
	}

	if err == nil {
		tmpl, err = tmpl.Parse(string(body))
		if err != nil {
			return nil, fmt.Errorf("parse template %s: %w", tmplPath, err)
		}
	} else {
		// Fallback to hardcoded string
		tmpl, err = tmpl.Parse(defaultKitchenTemplate)
		if err != nil {
			return nil, err
		}
	}

	var buf bytes.Buffer
	data := templateData{Header: h, Items: items, Opts: opts}
	if err := tmpl.Execute(&buf, data); err != nil {
		return nil, fmt.Errorf("execute template: %w", err)
	}

	b := NewBuilder()
	if err := b.SelectCodePage(codePage); err != nil {
		return nil, err
	}
	b.Initialize()

	b.Text(buf.String())

	if opts.OpenCashDrawer {
		b.KickDrawer(0, 100, 100)
	}
	if opts.FeedLinesBefore <= 0 {
		opts.FeedLinesBefore = 3
	}
	b.Feed(opts.FeedLinesBefore)
	if opts.Cut == "" {
		opts.Cut = "partial"
	}
	b.Cut(opts.Cut)

	out := b.Bytes()
	if opts.Copies > 1 {
		repeat := make([]byte, 0, len(out)*opts.Copies)
		for i := 0; i < opts.Copies; i++ {
			repeat = append(repeat, out...)
		}
		return repeat, nil
	}
	return out, nil
}

// RenderKitchenPlainText produces plain-text bytes for office/laser printers.
// The layout mirrors RenderKitchen but uses only printable ASCII characters
// and CRLF line endings — no ESC/POS commands, code pages, or cut directives.
func RenderKitchenPlainText(h Header, items []Item, opts Options) ([]byte, error) {
	if len(items) == 0 {
		return nil, ErrEmptyPayload
	}
	width := 42
	b := &bytes.Buffer{}

	crlf := func() {
		b.WriteByte('\r')
		b.WriteByte('\n')
	}

	center := func(s string) {
		runes := []rune(s)
		if len(runes) >= width {
			b.WriteString(s)
			crlf()
			return
		}
		pad := (width - len(runes)) / 2
		for i := 0; i < pad; i++ {
			b.WriteByte(' ')
		}
		b.WriteString(s)
		crlf()
	}

	sep := func() {
		for i := 0; i < width; i++ {
			b.WriteByte('=')
		}
		crlf()
	}

	line := func(s string) {
		b.WriteString(s)
		crlf()
	}

	// Header
	sep()
	center(fmt.Sprintf("PEDIDO #%d", h.OrderNumber))

	if t := formatTime(h.CreatedAt); t != "" {
		center(t)
	}
	if d := deliveryLabel(h.DeliveryType); d != "" {
		center(d)
	}
	sep()

	if h.CustomerName != "" {
		line("Cliente: " + h.CustomerName)
	}
	if h.CustomerPhone != "" {
		line("Tel: " + h.CustomerPhone)
	}
	if h.Address != "" {
		line("Dir: " + h.Address)
	}
	sep()

	for _, it := range items {
		line(fmt.Sprintf("%s %s", formatQty(it.Qty), truncate(it.Name, width-4)))
		for _, m := range it.Modifiers {
			line("  - " + m)
		}
		if it.Notes != "" {
			line("  OBS: " + it.Notes)
		}
	}
	sep()

	if opts.Copies > 1 {
		content := b.Bytes()
		repeat := make([]byte, 0, len(content)*opts.Copies)
		for i := 0; i < opts.Copies; i++ {
			repeat = append(repeat, content...)
		}
		return repeat, nil
	}
	return b.Bytes(), nil
}

// truncate returns s shortened to at most n runes, with an ellipsis if it
// was actually shortened.
func truncate(s string, n int) string {
	if n <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	if n <= 1 {
		return string(runes[:n])
	}
	return string(runes[:n-1]) + "…"
}
