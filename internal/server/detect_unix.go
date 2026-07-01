//go:build !windows

package server

import (
	"context"
	"os/exec"
	"strings"
)

func detectSystemPrinters(ctx context.Context) ([]string, error) {
	cmd := exec.CommandContext(ctx, "lpstat", "-p")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return []string{}, nil
	}
	printers := make([]string, 0)
	seen := make(map[string]struct{})
	skip := map[string]bool{
		"printer": true, "la": true, "impresora": true, "impresora.": true,
		"idle": true, "inactiva": true, "printing": true, "imprimiendo": true,
		"está": true,
	}
	for _, raw := range strings.Split(string(out), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		for _, p := range parts {
			// Normalise: trim trailing punctuation from end.
			p = strings.TrimRight(p, ".。．,，")
			if p == "" || skip[p] || strings.HasPrefix(p, "activ") || strings.HasPrefix(p, "Busc") {
				continue
			}
			// CUPS printer names typically contain underscores, hyphens
			// or uppercase letters. Filter out generic Spanish words.
			if !looksLikePrinterName(p) {
				continue
			}
			if _, ok := seen[p]; !ok {
				seen[p] = struct{}{}
				printers = append(printers, p)
			}
			break
		}
	}
	// Fallback: if the locale-aware parser returned nothing, try the
	// English-format parser as a second pass.
	if len(printers) == 0 {
		for _, raw := range strings.Split(string(out), "\n") {
			parts := strings.Fields(strings.TrimSpace(raw))
			if len(parts) >= 2 && parts[0] == "printer" {
				name := parts[1]
				if _, ok := seen[name]; !ok {
					printers = append(printers, name)
				}
			}
		}
	}
	return printers, nil
}

func looksLikePrinterName(s string) bool {
	for _, r := range s {
		if r == '_' || r == '-' {
			return true
		}
		if r >= 'A' && r <= 'Z' {
			return true
		}
	}
	// Numbers suggest a model name (e.g., TM-T20III, L3250).
	for _, r := range s {
		if r >= '0' && r <= '9' {
			return true
		}
	}
	return false
}
