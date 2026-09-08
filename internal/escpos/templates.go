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
	Qty       int
	Name      string
	Modifiers []string
	Notes     string
	UnitPrice float64
	Subtotal  float64
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
	BusinessName  string
	LegalName     string
	TaxID         string
	Phone         string
	Notes         string
}

// Options controls the trailing behavior of every template (cut, kick,
// feed lines, custom footer, and QR code).
type Options struct {
	Cut             string // "partial" | "full" | "none"
	OpenCashDrawer  bool
	Copies          int
	FeedLinesBefore int
	Footer          string
	QRCode          string
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

	if opts.Footer != "" {
		b.Alignment(1)
		lines := strings.Split(opts.Footer, "\n")
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed != "" {
				b.TextLine(trimmed)
			}
		}
		b.Separator("-", width)
	}

	if opts.QRCode != "" {
		b.Alignment(1)
		b.Feed(1)
		b.QRCode(opts.QRCode, 5, 'M')
		b.Feed(1)
	}

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

	if opts.Footer != "" {
		lines := strings.Split(opts.Footer, "\n")
		for _, l := range lines {
			trimmed := strings.TrimSpace(l)
			if trimmed != "" {
				center(trimmed)
			}
		}
		sep()
	}

	if opts.QRCode != "" {
		center("[QR: " + opts.QRCode + "]")
		crlf()
	}

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

// RenderReceipt produces the ESC/POS bytes for a customer receipt / cash voucher.
// It includes business branding, itemized table with prices, subtotal, discounts,
// taxes, total, payment method, optional footer lines and centered QR code.
func RenderReceipt(codePage string, charsPerLine int, h Header, items []Item, opts Options) ([]byte, error) {
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

	// 1. Business Header (Centered)
	b.Alignment(1)
	if h.BusinessName != "" {
		b.Bold(true).DoubleSize(true)
		b.TextLine(strings.ToUpper(h.BusinessName))
		b.DoubleSize(false).Bold(false)
	}
	if h.LegalName != "" {
		b.TextLine(h.LegalName)
	}
	if h.TaxID != "" {
		b.TextLine(h.TaxID)
	}
	if h.Address != "" {
		b.TextLine(h.Address)
	}
	phone := h.Phone
	if phone == "" {
		phone = h.CustomerPhone
	}
	if phone != "" {
		b.TextLine("Tel: " + phone)
	}
	b.Separator("-", width)

	// 2. Order Metadata
	b.Alignment(0) // Left
	b.Bold(true)
	b.TextLine(fmt.Sprintf("COMPROBANTE DE VENTA #%d", h.OrderNumber))
	b.Bold(false)
	if t := formatTime(h.CreatedAt); t != "" {
		b.TextLine("Fecha: " + t)
	}
	if d := deliveryLabel(h.DeliveryType); d != "" {
		b.TextLine("Canal: " + d)
	}
	if h.CustomerName != "" {
		b.TextLine("Cliente: " + h.CustomerName)
	}
	if h.CustomerPhone != "" && h.CustomerPhone != phone {
		b.TextLine("Tel: " + h.CustomerPhone)
	}
	b.Separator("-", width)

	// 3. Items Table Header
	b.Bold(true)
	b.TextLine(formatTwoColumns("Cant/Detalle", "Total", width))
	b.Bold(false)
	b.Separator("-", width)

	// 4. Items Rows
	var subtotal float64
	for _, it := range items {
		itemTotal := it.Subtotal
		if itemTotal == 0 && it.UnitPrice > 0 {
			itemTotal = it.UnitPrice * float64(it.Qty)
		}
		subtotal += itemTotal

		leftText := fmt.Sprintf("%s %s", formatQty(it.Qty), it.Name)
		var rightText string
		if itemTotal > 0 || it.UnitPrice > 0 {
			rightText = fmt.Sprintf("$%.2f", itemTotal)
		}

		b.Bold(true)
		b.TextLine(formatTwoColumns(leftText, rightText, width))
		b.Bold(false)

		for _, m := range it.Modifiers {
			b.TextLine("  + " + m)
		}
		if it.Notes != "" {
			b.TextLine("  * " + it.Notes)
		}
	}
	b.Separator("-", width)

	// 5. Summaries & Total
	if subtotal > 0 {
		b.TextLine(formatTwoColumns("Subtotal:", fmt.Sprintf("$%.2f", subtotal), width))
	}
	b.Bold(true).DoubleSize(true)
	b.TextLine(formatTwoColumns("TOTAL:", fmt.Sprintf("$%.2f", subtotal), width))
	b.DoubleSize(false).Bold(false)

	if h.PaymentMethod != "" {
		b.TextLine("Medio de Pago: " + h.PaymentMethod)
	}
	b.Separator("-", width)

	// 6. Custom Footer Lines (WiFi, thanks, instagram)
	if opts.Footer != "" {
		b.Alignment(1) // Center
		lines := strings.Split(opts.Footer, "\n")
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if trimmed != "" {
				b.TextLine(trimmed)
			}
		}
	}

	// 7. Native ESC/POS QR Code
	if opts.QRCode != "" {
		b.Alignment(1) // Center
		b.Feed(1)
		b.QRCode(opts.QRCode, 5, 'M')
		b.Feed(1)
	}

	// 8. Drawer kick, feed lines & cut
	if opts.OpenCashDrawer {
		b.KickDrawer(0, 100, 100)
	}
	feed := opts.FeedLinesBefore
	if feed <= 0 {
		feed = 3
	}
	b.Feed(feed)
	cut := opts.Cut
	if cut == "" {
		cut = "partial"
	}
	b.Cut(cut)

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

// RenderReceiptPlainText produces plain-text bytes for office/laser printers.
func RenderReceiptPlainText(h Header, items []Item, opts Options) ([]byte, error) {
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

	sep()
	if h.BusinessName != "" {
		center(strings.ToUpper(h.BusinessName))
	}
	if h.LegalName != "" {
		center(h.LegalName)
	}
	if h.TaxID != "" {
		center(h.TaxID)
	}
	if h.Address != "" {
		center(h.Address)
	}
	center(fmt.Sprintf("COMPROBANTE #%d", h.OrderNumber))
	if t := formatTime(h.CreatedAt); t != "" {
		center(t)
	}
	sep()

	if h.CustomerName != "" {
		line("Cliente: " + h.CustomerName)
	}
	if h.CustomerPhone != "" {
		line("Tel: " + h.CustomerPhone)
	}
	sep()

	line(formatTwoColumns("Cant/Detalle", "Total", width))
	sep()

	var subtotal float64
	for _, it := range items {
		itemTotal := it.Subtotal
		if itemTotal == 0 && it.UnitPrice > 0 {
			itemTotal = it.UnitPrice * float64(it.Qty)
		}
		subtotal += itemTotal

		left := fmt.Sprintf("%s %s", formatQty(it.Qty), it.Name)
		var right string
		if itemTotal > 0 || it.UnitPrice > 0 {
			right = fmt.Sprintf("$%.2f", itemTotal)
		}
		line(formatTwoColumns(left, right, width))
		for _, m := range it.Modifiers {
			line("  + " + m)
		}
		if it.Notes != "" {
			line("  * " + it.Notes)
		}
	}
	sep()

	if subtotal > 0 {
		line(formatTwoColumns("Subtotal:", fmt.Sprintf("$%.2f", subtotal), width))
	}
	line(formatTwoColumns("TOTAL:", fmt.Sprintf("$%.2f", subtotal), width))

	if h.PaymentMethod != "" {
		line("Medio de Pago: " + h.PaymentMethod)
	}
	sep()

	if opts.Footer != "" {
		lines := strings.Split(opts.Footer, "\n")
		for _, l := range lines {
			trimmed := strings.TrimSpace(l)
			if trimmed != "" {
				center(trimmed)
			}
		}
	}

	if opts.QRCode != "" {
		center("[QR: " + opts.QRCode + "]")
	}

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

// formatTwoColumns formats two strings across width chars, aligning left
// to the left and right to the right with padding spaces in between.
func formatTwoColumns(left, right string, width int) string {
	if width <= 0 {
		width = 42
	}
	leftRunes := []rune(left)
	rightRunes := []rune(right)
	rightLen := len(rightRunes)

	if rightLen >= width {
		return string(rightRunes[:width])
	}

	availableLeft := width - rightLen - 1
	if availableLeft < 0 {
		availableLeft = 0
	}

	if len(leftRunes) > availableLeft {
		if availableLeft > 1 {
			left = string(leftRunes[:availableLeft-1]) + "…"
		} else {
			left = string(leftRunes[:availableLeft])
		}
	}

	leftRunes = []rune(left)
	spaces := width - len(leftRunes) - rightLen
	if spaces < 1 {
		spaces = 1
	}

	var sb strings.Builder
	sb.WriteString(left)
	for i := 0; i < spaces; i++ {
		sb.WriteByte(' ')
	}
	sb.WriteString(right)
	return sb.String()
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
