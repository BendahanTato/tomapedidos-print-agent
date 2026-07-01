package escpos

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

// SupportedCodePages returns the list of names the agent understands.
// Useful for the panel UI to render a dropdown of valid choices.
func SupportedCodePages() []string {
	out := make([]string, 0, len(codePageNumbers))
	for k := range codePageNumbers {
		out = append(out, k)
	}
	return out
}
