package printer

import "strings"

// DetectType classifies a printer as "usb" (thermal/ESC/POS) or
// "usb-office" (laser/inkjet/plain-text) based on its make-and-model.
func DetectType(makeAndModel string) string {
	m := strings.ToLower(makeAndModel)

	// Strong thermal indicators — model prefixes that are unambiguously thermal
	strongThermal := []string{
		"tm-t", "tm-u", "tm-p", "tm-c", // Epson TM-series (T=thermal, U=impact)
		"tsp", "tp-", // Star Micronics
		"pos-", "rp-", // Bixolon, Citizen
		"80mm", "58mm", "76mm", // Paper width indicates receipt printer
	}
	for _, kw := range strongThermal {
		if strings.Contains(m, kw) {
			return "usb"
		}
	}

	// Weak thermal indicators — brand names that MAKE thermal printers
	// but also make office printers. Only match if combined with a
	// thermal model indicator.
	weakThermal := []string{"epson", "star", "bixolon", "citizen", "zeworth", "npc", "gainscha"}
	for _, brand := range weakThermal {
		if strings.Contains(m, brand) {
			// Brand matches — but is it a thermal model?
			// Check for receipt/thermal hints
			thermalHints := []string{"receipt", "thermal", "pos", "printer", "tm-", "tsp"}
			for _, hint := range thermalHints {
				if strings.Contains(m, hint) {
					return "usb"
				}
			}
		}
	}

	// Default to office (plain text) — safer for laser/inkjet
	return "usb-office"
}
