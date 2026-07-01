//go:build windows

package server

import (
	"context"
	"os/exec"
	"strings"
)

func detectSystemPrinters(ctx context.Context) ([]string, error) {
	cmd := exec.CommandContext(ctx, "powershell", "-Command",
		"(Get-Printer -ErrorAction Stop).Name")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, err
	}
	var printers []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			printers = append(printers, line)
		}
	}
	return printers, nil
}
