//go:build windows

package server

import (
	"context"
	"os/exec"
	"strings"

	"github.com/tomapedidos/print-agent/internal/printer"
)

func detectSystemPrinters(ctx context.Context) ([]DetectedPrinter, error) {
	cmd := exec.CommandContext(ctx, "powershell", "-Command",
		`Get-Printer -ErrorAction Stop | ForEach-Object { "$($_.Name)|$($_.DriverName)" }`)
	out, err := cmd.CombinedOutput()
	if err != nil {
		// Fallback to name only if DriverName formatting fails
		cmd2 := exec.CommandContext(ctx, "powershell", "-Command", "(Get-Printer -ErrorAction Stop).Name")
		out2, err2 := cmd2.CombinedOutput()
		if err2 != nil {
			return nil, err
		}
		out = out2
	}

	var printers []DetectedPrinter
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, "|", 2)
		name := strings.TrimSpace(parts[0])
		if name == "" {
			continue
		}
		driver := ""
		if len(parts) > 1 {
			driver = strings.TrimSpace(parts[1])
		}

		mam := driver
		if mam == "" {
			mam = name
		}
		suggested := printer.DetectType(name + " " + driver)

		printers = append(printers, DetectedPrinter{
			Name:          name,
			MakeAndModel:  driver,
			SuggestedType: suggested,
		})
	}
	return printers, nil
}

