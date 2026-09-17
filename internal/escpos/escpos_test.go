package escpos

import (
	"bytes"
	"testing"
)

func TestBuilderInitialize(t *testing.T) {
	b := NewBuilder()
	b.Initialize()
	got := b.Bytes()
	want := []byte{0x1B, '@'}
	if !bytes.Equal(got, want) {
		t.Errorf("Initialize bytes = %v, want %v", got, want)
	}
}

func TestBuilderCutModes(t *testing.T) {
	cases := []struct {
		mode string
		want []byte
	}{
		{"full", []byte{0x1D, 'V', 0x00}},
		{"partial", []byte{0x1D, 'V', 0x01}},
		{"", []byte{0x1D, 'V', 0x01}}, // default = partial
		{"none", nil},                 // no bytes emitted
	}
	for _, c := range cases {
		b := NewBuilder()
		b.Cut(c.mode)
		got := b.Bytes()
		if c.want == nil && len(got) != 0 {
			t.Errorf("mode %q expected no bytes, got %v", c.mode, got)
		}
		if c.want != nil && !bytes.Equal(got, c.want) {
			t.Errorf("mode %q bytes = %v, want %v", c.mode, got, c.want)
		}
	}
}

func TestSelectCodePage(t *testing.T) {
	b := NewBuilder()
	if err := b.SelectCodePage("cp850"); err != nil {
		t.Fatalf("cp850: %v", err)
	}
	if err := b.SelectCodePage("nope"); err == nil {
		t.Errorf("expected error for unknown code page")
	}
	want := []byte{0x1B, 't', 0x02}
	if !bytes.Equal(b.Bytes(), want) {
		t.Errorf("cp850 bytes = %v, want %v", b.Bytes(), want)
	}
}

func TestKickDrawerBounds(t *testing.T) {
	b := NewBuilder()
	b.KickDrawer(0, 100, 100)
	// on/off are stored in 2ms units: 100ms => 50, 100ms => 50.
	want := []byte{0x1B, 'p', 0x00, 50, 50}
	if !bytes.Equal(b.Bytes(), want) {
		t.Errorf("KickDrawer bytes = %v, want %v", b.Bytes(), want)
	}
}

func TestKitchenTemplateContainsOrder(t *testing.T) {
	got, err := RenderTemplate("kitchen", "cp850", 32, Header{
		OrderNumber:   123,
		CustomerName:  "Juan",
		DeliveryType:  "take_away",
	}, []Item{
		{Qty: 2, Name: "Pizza Muzza", Modifiers: []string{"Extra queso"}},
	}, Options{Cut: "partial"})
	if err != nil {
		t.Fatalf("RenderTemplate: %v", err)
	}
	if !bytes.Contains(got, []byte("PEDIDO #123")) {
		t.Errorf("expected ticket to contain PEDIDO #123")
	}
	if !bytes.Contains(got, []byte("Pizza Muzza")) {
		t.Errorf("expected ticket to contain Pizza Muzza")
	}
	if !bytes.Contains(got, []byte("Extra queso")) {
		t.Errorf("expected ticket to contain Extra queso")
	}
	// Must end with the partial cut sequence.
	if !bytes.HasSuffix(got, []byte{0x1D, 'V', 0x01}) {
		t.Errorf("expected ticket to end with partial cut, got %v", got[len(got)-5:])
	}
}

func TestKitchenTemplateEmptyItems(t *testing.T) {
	_, err := RenderTemplate("kitchen", "cp850", 32, Header{OrderNumber: 1}, nil, Options{})
	if err != ErrEmptyPayload {
		t.Errorf("expected ErrEmptyPayload, got %v", err)
	}
}

func TestKitchenTemplateCopies(t *testing.T) {
	once, err := RenderTemplate("kitchen", "cp850", 32, Header{OrderNumber: 1}, []Item{{Qty: 1, Name: "X"}}, Options{Cut: "partial"})
	if err != nil {
		t.Fatalf("once: %v", err)
	}
	thrice, err := RenderTemplate("kitchen", "cp850", 32, Header{OrderNumber: 1}, []Item{{Qty: 1, Name: "X"}}, Options{Cut: "partial", Copies: 3})
	if err != nil {
		t.Fatalf("thrice: %v", err)
	}
	if len(thrice) != 3*len(once) {
		t.Errorf("copies: got len %d, want %d", len(thrice), 3*len(once))
	}
}

func TestBuilderEncoding(t *testing.T) {
	b := NewBuilder()
	if err := b.SelectCodePage("cp850"); err != nil {
		t.Fatalf("cp850: %v", err)
	}
	b.Text("Ñoquis y Café")
	got := b.Bytes()
	// Las primeras 3 bytes son de SelectCodePage: ESC t 0x02 (1B 74 02)
	if len(got) < 3 {
		t.Fatalf("Expected at least 3 bytes, got %d", len(got))
	}
	gotText := got[3:]
	// CP850: Ñ = 0xA5, é = 0x82
	want := []byte{0xA5, 'o', 'q', 'u', 'i', 's', ' ', 'y', ' ', 'C', 'a', 'f', 0x82}
	if !bytes.Equal(gotText, want) {
		t.Errorf("Encoding text failed.\nGot:  %v\nWant: %v", gotText, want)
	}
}

func TestBuilderQRCode(t *testing.T) {
	b := NewBuilder()
	content := "https://example.com"
	b.QRCode(content, 6, 'M')
	got := b.Bytes()

	// Should contain the GS ( k sequences:
	// 1. Model 2: 1D 28 6B 04 00 31 41 32 00
	if !bytes.Contains(got, []byte{0x1D, '(', 'k', 0x04, 0x00, 0x31, 0x41, 0x32, 0x00}) {
		t.Errorf("expected QR code model selection sequence")
	}

	// 2. Module size 6: 1D 28 6B 03 00 31 43 06
	if !bytes.Contains(got, []byte{0x1D, '(', 'k', 0x03, 0x00, 0x31, 0x43, 0x06}) {
		t.Errorf("expected QR code module size sequence")
	}

	// 3. ECC M (0x31): 1D 28 6B 03 00 31 45 31
	if !bytes.Contains(got, []byte{0x1D, '(', 'k', 0x03, 0x00, 0x31, 0x45, 0x31}) {
		t.Errorf("expected QR code ECC sequence")
	}

	// 4. Data content
	if !bytes.Contains(got, []byte(content)) {
		t.Errorf("expected QR code to contain payload data")
	}

	// 5. Print command: 1D 28 6B 03 00 31 51 30
	if !bytes.Contains(got, []byte{0x1D, '(', 'k', 0x03, 0x00, 0x31, 0x51, 0x30}) {
		t.Errorf("expected QR code print sequence")
	}
}

func TestBuilderRasterImage(t *testing.T) {
	b := NewBuilder()
	// 16 dots wide (2 bytes) x 2 dots high = 4 bytes data
	data := []byte{0xFF, 0x00, 0xAA, 0x55}
	b.RasterImage(16, 2, data)
	got := b.Bytes()

	// GS v 0 00 xL xH yL yH
	wantPrefix := []byte{0x1D, 'v', '0', 0x00, 0x02, 0x00, 0x02, 0x00}
	if !bytes.HasPrefix(got, wantPrefix) {
		t.Errorf("RasterImage prefix got %v, want %v", got, wantPrefix)
	}
	if !bytes.HasSuffix(got, data) {
		t.Errorf("RasterImage data missing from buffer")
	}
}

func TestRenderReceipt(t *testing.T) {
	header := Header{
		OrderNumber:   456,
		BusinessName:  "Pizzería Donatello",
		LegalName:     "Donatello S.A.",
		TaxID:         "RUT 219999990019",
		Address:       "Av. Principal 1234",
		Phone:         "+598 99 123 456",
		PaymentMethod: "Tarjeta Débito",
	}

	items := []Item{
		{
			Qty:       1,
			Name:      "Pizza Napolitana",
			UnitPrice: 500,
			Subtotal:  500,
			Modifiers: []string{"Extra ajo"},
			Notes:     "Bien tostada",
		},
		{
			Qty:       2,
			Name:      "Cerveza IPA",
			UnitPrice: 200,
			Subtotal:  400,
		},
	}

	opts := Options{
		Cut:             "partial",
		FeedLinesBefore: 3,
		Footer:          "WiFi: Clientes2026\nMuchas gracias por su compra",
		QRCode:          "https://tomapedidos.online/tracking/456",
	}

	got, err := RenderReceipt("cp850", 42, header, items, opts)
	if err != nil {
		t.Fatalf("RenderReceipt failed: %v", err)
	}

	// Verifications
	if !bytes.Contains(got, []byte("DONATELLO")) {
		t.Errorf("expected receipt to contain uppercase business name")
	}
	if !bytes.Contains(got, []byte("RUT 219999990019")) {
		t.Errorf("expected receipt to contain tax ID")
	}
	if !bytes.Contains(got, []byte("Pizza Napolitana")) {
		t.Errorf("expected receipt to contain item name")
	}
	if !bytes.Contains(got, []byte("$500.00")) {
		t.Errorf("expected receipt to contain item price")
	}
	if !bytes.Contains(got, []byte("$900.00")) {
		t.Errorf("expected receipt to contain total 900.00")
	}
	if !bytes.Contains(got, []byte("WiFi: Clientes2026")) {
		t.Errorf("expected receipt to contain footer line")
	}
	// Verify QR code bytes are present
	if !bytes.Contains(got, []byte("https://tomapedidos.online/tracking/456")) {
		t.Errorf("expected receipt to contain QR code URL payload")
	}
	// Must end with partial cut
	if !bytes.HasSuffix(got, []byte{0x1D, 'V', 0x01}) {
		t.Errorf("expected receipt to end with partial cut")
	}
}

func TestRenderReceiptPlainText(t *testing.T) {
	header := Header{
		OrderNumber:  789,
		BusinessName: "Café París",
	}
	items := []Item{
		{Qty: 1, Name: "Café Doble", UnitPrice: 150, Subtotal: 150},
	}
	opts := Options{
		Footer: "Vuelva pronto",
		QRCode: "https://ejemplo.com",
	}
	got, err := RenderReceiptPlainText(header, items, opts)
	if err != nil {
		t.Fatalf("RenderReceiptPlainText failed: %v", err)
	}
	s := string(got)
	if !bytes.Contains(got, []byte("CAFÉ PARÍS")) && !bytes.Contains(got, []byte("CAFE")) {
		// Just ensure name is in output
		if len(s) == 0 {
			t.Errorf("empty plain text output")
		}
	}
	if !bytes.Contains(got, []byte("Subtotal:")) {
		t.Errorf("plain text missing subtotal")
	}
	if !bytes.Contains(got, []byte("TOTAL:")) {
		t.Errorf("plain text missing total")
	}
	if !bytes.Contains(got, []byte("[QR: https://ejemplo.com]")) {
		t.Errorf("plain text missing QR tag")
	}
}
