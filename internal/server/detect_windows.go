//go:build windows

package server

import (
	"context"
	"os/exec"
	"strings"
)

func detectSystemPrinters(ctx context.Context) ([]DetectedPrinter, error) {
	cmd := exec.CommandContext(ctx, "powershell", "-Command",
		"(Get-Printer -ErrorAction Stop).Name")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, err
	}
	var printers []DetectedPrinter
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			printers = append(printers, DetectedPrinter{
				Name:          line,
				SuggestedType: "usb-office", // safe default on Windows
			})
		}
	}
	return printers, nil
}
