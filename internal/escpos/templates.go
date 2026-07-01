package escpos

import (
	"bytes"
	"fmt"
	"strings"
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
	Cut             string // "partial" | "full" | "none"
	OpenCashDrawer  bool
	Copies          int
	FeedLinesBefore int
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

// RenderKitchen produces the bytes for a kitchen/bar ticket. The format is
// intentionally minimal: large order number, item list with modifiers and
// notes, no prices, no customer info beyond name and address.
func RenderKitchen(codePage string, charsPerLine int, h Header, items []Item, opts Options) ([]byte, error) {
	if len(items) == 0 {
		return nil, ErrEmptyPayload
	}
	width := charsPerLine
	if width <= 0 {
		width = 42
	}
	b := NewBuilder()
	if err := b.SelectCodePage(codePage); err != nil {
		return nil, err
	}
	b.Initialize()

	b.Alignment(1).Bold(true).DoubleSize(true)
	b.TextLine(fmt.Sprintf("PEDIDO #%d", h.OrderNumber))
	b.DoubleSize(false).Bold(false)

	if t := formatTime(h.CreatedAt); t != "" {
		b.Alignment(1).TextLine(t)
	}
	if d := deliveryLabel(h.DeliveryType); d != "" {
		b.Alignment(1).TextLine(d)
	}
	b.Separator("-", width)

	if h.CustomerName != "" {
		b.Alignment(0)
		b.TextLine("Cliente: " + h.CustomerName)
	}
	if h.CustomerPhone != "" {
		b.TextLine("Tel: " + h.CustomerPhone)
	}
	if h.Address != "" {
		b.TextLine("Dir: " + h.Address)
	}
	b.Separator("-", width)

	for _, it := range items {
		b.Bold(true)
		b.Text(formatQty(it.Qty) + " ")
		b.Bold(false)
		b.TextLine(truncate(it.Name, width-4))
		for _, m := range it.Modifiers {
			b.TextLine("  - " + m)
		}
		if it.Notes != "" {
			b.TextLine("  OBS: " + it.Notes)
		}
	}
	b.Separator("-", width)

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
