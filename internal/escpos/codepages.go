package escpos

import (
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
)

// codePageNumbers maps the friendly names used in the JSON config to the
// numeric arguments expected by ESC t n. The list is intentionally short;
// add more as we encounter printers that ship with non-standard defaults.
//
// Reference: Epson ESC/POS programming manual, "Select character code table"
// (ESC t). Common values:
//
//	0  PC437 (USA, Standard Europe)
//	2  PC850 (Multilingual)        — default for most Latin American markets
//	3  PC860 (Portuguese)
//	4  PC863 (Canadian-French)
//	5  PC865 (Nordic)
//	16 WPC1252                       — Windows Latin-1
//	32  Thai
var codePageNumbers = map[string]int{
	"cp437":        0,
	"cp850":        2,
	"cp860":        3,
	"cp863":        4,
	"cp865":        5,
	"cp1252":       16,
	"windows-1252": 16,
	"wpc1252":      16,
}

// CodePageEncoders maps friendly names to their charmap.Charmap implementations.
var CodePageEncoders = map[string]encoding.Encoding{
	"cp437":        charmap.CodePage437,
	"cp850":        charmap.CodePage850,
	"cp860":        charmap.CodePage860,
	"cp863":        charmap.CodePage863,
	"cp865":        charmap.CodePage865,
	"cp1252":       charmap.Windows1252,
	"windows-1252": charmap.Windows1252,
	"wpc1252":      charmap.Windows1252,
}

// SupportedCodePages returns the list of names the agent understands.
// Useful for the panel UI to render a dropdown of valid choices.
func SupportedCodePages() []string {
	out := make([]string, 0, len(codePageNumbers))
	for k := range codePageNumbers {
		out = append(out, k)
	}
	return out
}
