// Package escpos generates the byte sequences understood by ESC/POS thermal
// receipt printers. The Builder accumulates commands; Bytes returns the
// final buffer ready to send to the printer.
//
// The implementation is intentionally tiny (~300 lines) and dependency-free.
// We chose to roll our own rather than depend on github.com/knq/escpos or
// similar to keep full control over code pages, QR codes and templates
// without pulling a heavy runtime.
package escpos

import (
	"bytes"
	"errors"
	"fmt"

	"golang.org/x/text/encoding"
	"golang.org/x/text/transform"
)

// Standard ESC/POS command constants.
const (
	esc    byte = 0x1B
	gs     byte = 0x1D
	fs     byte = 0x1C
	dle    byte = 0x10
	lf     byte = 0x0A
	ff     byte = 0x0C
	dleEOT byte = 0x04 // DLE EOT (used to query printer status)
)

// Builder accumulates ESC/POS commands into a buffer. It is not goroutine
// safe; create one per render call.
type Builder struct {
	buf     bytes.Buffer
	encoder transform.Transformer
}

// NewBuilder returns a fresh Builder.
func NewBuilder() *Builder {
	return &Builder{}
}

// Bytes returns the accumulated bytes. The buffer is not modified.
func (b *Builder) Bytes() []byte {
	out := make([]byte, b.buf.Len())
	copy(out, b.buf.Bytes())
	return out
}

// Len returns the number of bytes accumulated so far.
func (b *Builder) Len() int {
	return b.buf.Len()
}

// Reset clears the buffer. Useful for re-using a Builder across renders.
func (b *Builder) Reset() {
	b.buf.Reset()
	b.encoder = nil
}

// Initialize sends ESC @ which resets the printer to its default state.
func (b *Builder) Initialize() *Builder {
	b.buf.WriteByte(esc)
	b.buf.WriteByte('@')
	return b
}

// LineFeed emits n LF bytes.
func (b *Builder) LineFeed(n int) *Builder {
	for i := 0; i < n; i++ {
		b.buf.WriteByte(lf)
	}
	return b
}

// FormFeed advances to the top of the next page.
func (b *Builder) FormFeed() *Builder {
	b.buf.WriteByte(ff)
	return b
}

// Text appends raw bytes to the buffer after translating the UTF-8 string to the selected Code Page.
func (b *Builder) Text(s string) *Builder {
	if b.encoder != nil {
		encoded, _, err := transform.Bytes(b.encoder, []byte(s))
		if err == nil {
			b.buf.Write(encoded)
			return b
		}
		// If transform fails, write whatever it managed to encode (with replacements).
		// Never fallback to raw UTF-8 as it will corrupt ESC/POS output.
		b.buf.Write(encoded)
		return b
	}
	b.buf.WriteString(s)
	return b
}

// TextLine appends s followed by a LF.
func (b *Builder) TextLine(s string) *Builder {
	b.Text(s)
	b.buf.WriteByte(lf)
	return b
}

// Alignment sets the text justification.
//
//	0 = left, 1 = center, 2 = right.
func (b *Builder) Alignment(n int) *Builder {
	if n < 0 || n > 2 {
		n = 0
	}
	b.buf.WriteByte(esc)
	b.buf.WriteByte('a')
	b.buf.WriteByte(byte(n))
	return b
}

// Bold enables or disables bold (ESC E n).
func (b *Builder) Bold(on bool) *Builder {
	b.buf.WriteByte(esc)
	b.buf.WriteByte('E')
	if on {
		b.buf.WriteByte(1)
	} else {
		b.buf.WriteByte(0)
	}
	return b
}

// Underline enables or disables underline (ESC - n).
func (b *Builder) Underline(on bool) *Builder {
	b.buf.WriteByte(esc)
	b.buf.WriteByte('-')
	if on {
		b.buf.WriteByte(1)
	} else {
		b.buf.WriteByte(0)
	}
	return b
}

// DoubleSize toggles the 2x width / height flag (GS ! n).
func (b *Builder) DoubleSize(on bool) *Builder {
	var n byte
	if on {
		n = 0x11 // 2x width + 2x height
	} else {
		n = 0x00
	}
	b.buf.WriteByte(gs)
	b.buf.WriteByte('!')
	b.buf.WriteByte(n)
	return b
}

// SelectCodePage issues ESC t n with the value looked up from the table in
// codepages.go. Unknown names return an error and the builder is left
// untouched.
func (b *Builder) SelectCodePage(name string) error {
	n, ok := codePageNumbers[name]
	if !ok {
		return fmt.Errorf("unknown code page %q", name)
	}
	b.buf.WriteByte(esc)
	b.buf.WriteByte('t')
	b.buf.WriteByte(byte(n))

	if enc, ok := CodePageEncoders[name]; ok {
		b.encoder = encoding.ReplaceUnsupported(enc.NewEncoder())
	} else {
		b.encoder = nil
	}
	return nil
}

// Separator emits a row of '-' characters n chars wide followed by LF.
func (b *Builder) Separator(char string, n int) *Builder {
	for i := 0; i < n; i++ {
		b.buf.WriteString(char)
	}
	b.buf.WriteByte(lf)
	return b
}

// Cut emits the paper cut command (GS V m).
//
//	m = 0: full cut, m = 1: partial cut.
func (b *Builder) Cut(mode string) *Builder {
	var m byte
	switch mode {
	case "full":
		m = 0
	case "partial", "":
		m = 1
	case "none":
		return b
	}
	b.buf.WriteByte(gs)
	b.buf.WriteByte('V')
	b.buf.WriteByte(m)
	return b
}

// KickDrawer emits the cash-drawer pulse (ESC p m t1 t2).
// m selects the pin (0 or 1), t1/t2 are on-time/off-time in 2ms units.
func (b *Builder) KickDrawer(pin byte, onMs, offMs int) *Builder {
	if pin > 1 {
		pin = 0
	}
	if onMs < 0 {
		onMs = 50
	}
	if offMs < 0 {
		offMs = 50
	}
	on := byte(onMs / 2)
	off := byte(offMs / 2)
	if on == 0 {
		on = 1
	}
	if off == 0 {
		off = 1
	}
	b.buf.WriteByte(esc)
	b.buf.WriteByte('p')
	b.buf.WriteByte(pin)
	b.buf.WriteByte(on)
	b.buf.WriteByte(off)
	return b
}

// Feed emits n LF characters. Convenience alias for LineFeed.
func (b *Builder) Feed(n int) *Builder {
	return b.LineFeed(n)
}

// ErrEmptyPayload is returned by RenderKitchen when the builder would
// produce no output (e.g. no items and no header).
var ErrEmptyPayload = errors.New("escpos: nothing to print")

// Raw exposes the underlying buffer for callers that need to inspect
// intermediate state.
func (b *Builder) Raw() *bytes.Buffer {
	return &b.buf
}
