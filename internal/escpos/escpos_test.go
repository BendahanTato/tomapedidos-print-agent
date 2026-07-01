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
		{"none", nil},                  // no bytes emitted
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
	got, err := RenderKitchen("cp850", 32, Header{
		OrderNumber:   123,
		CustomerName:  "Juan",
		DeliveryType:  "take_away",
	}, []Item{
		{Qty: 2, Name: "Pizza Muzza", Modifiers: []string{"Extra queso"}},
	}, Options{Cut: "partial"})
	if err != nil {
		t.Fatalf("RenderKitchen: %v", err)
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
	_, err := RenderKitchen("cp850", 32, Header{OrderNumber: 1}, nil, Options{})
	if err != ErrEmptyPayload {
		t.Errorf("expected ErrEmptyPayload, got %v", err)
	}
}

func TestKitchenTemplateCopies(t *testing.T) {
	once, err := RenderKitchen("cp850", 32, Header{OrderNumber: 1}, []Item{{Qty: 1, Name: "X"}}, Options{Cut: "partial"})
	if err != nil {
		t.Fatalf("once: %v", err)
	}
	thrice, err := RenderKitchen("cp850", 32, Header{OrderNumber: 1}, []Item{{Qty: 1, Name: "X"}}, Options{Cut: "partial", Copies: 3})
	if err != nil {
		t.Fatalf("thrice: %v", err)
	}
	if len(thrice) != 3*len(once) {
		t.Errorf("copies: got len %d, want %d", len(thrice), 3*len(once))
	}
}
