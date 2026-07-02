package printer

import "strings"

// DetectType classifies a printer as "usb" (thermal/ESC/POS) or
// "usb-office" (laser/inkjet/plain-text) based on its make-and-model.
func DetectType(makeAndModel string) string {
	m := strings.ToLower(makeAndModel)
	// Thermal / receipt printer keywords
	thermalKeywords := []string{
		"tm-", "tm_", "tsp", "pos", "receipt", "thermal",
		"epson", "star ", "bixolon", "citizen", "zeworth",
		"npc", "gainscha", "printer", "80mm", "58mm", "76mm",
	}
	for _, kw := range thermalKeywords {
		if strings.Contains(m, kw) {
			return "usb"
		}
	}
	// Default to office (plain text) — safer for laser/inkjet
	return "usb-office"
}
