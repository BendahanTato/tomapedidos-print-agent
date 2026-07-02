//go:build !windows

package server

import (
	"context"
	"os/exec"
	"strings"

	"github.com/tomapedidos/print-agent/internal/printer"
)

func detectSystemPrinters(ctx context.Context) ([]DetectedPrinter, error) {
	names := detectPrinterNames(ctx)
	result := make([]DetectedPrinter, 0, len(names))
	for _, name := range names {
		mam := queryMakeAndModel(ctx, name)
		suggested := printer.DetectType(mam)
		result = append(result, DetectedPrinter{
			Name:          name,
			MakeAndModel:  mam,
			SuggestedType: suggested,
		})
	}
	return result, nil
}

func detectPrinterNames(ctx context.Context) []string {
	cmd := exec.CommandContext(ctx, "lpstat", "-p")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil
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
			p = strings.TrimRight(p, ".。．,，")
			if p == "" || skip[p] || strings.HasPrefix(p, "activ") || strings.HasPrefix(p, "Busc") {
				continue
			}
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
	return printers
}

func queryMakeAndModel(ctx context.Context, systemName string) string {
	ctx2, cancel := context.WithTimeout(ctx, 3e9)
	defer cancel()
	cmd := exec.CommandContext(ctx2, "lpoptions", "-p", systemName)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return ""
	}
	s := string(out)
	const prefix = "printer-make-and-model="
	idx := strings.Index(s, prefix)
	if idx < 0 {
		return ""
	}
	s = s[idx+len(prefix):]
	if len(s) > 0 && s[0] == '\'' {
		end := strings.Index(s[1:], "'")
		if end >= 0 {
			return s[1 : end+1]
		}
	}
	return strings.TrimSpace(s)
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
	for _, r := range s {
		if r >= '0' && r <= '9' {
			return true
		}
	}
	return false
}
