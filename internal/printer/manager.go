package printer

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/tomapedidos/print-agent/internal/config"
)

// NewFromConfig instantiates the right concrete Printer for a given
// config.Printer entry. It returns an error if the type is unknown or
// required fields are missing (Validate should have caught this already).
func NewFromConfig(ctx context.Context, p config.Printer) (Printer, Info, error) {
	info := Info{
		ID:           p.ID,
		Name:         p.Name,
		Type:         p.Type,
		Host:         p.Host,
		Port:         p.Port,
		SystemName:   p.SystemName,
		FilePath:     p.FilePath,
		CodePage:     p.CodePage,
		CharsPerLine: p.CharsPerLine,
		Cut:          p.Cut,
	}
	switch p.Type {
	case "network":
		np := NewNetwork(p.ID, p.Host, p.Port)
		if err := np.Open(ctx); err != nil {
			info.Status = StatusOffline
			info.LastError = err.Error()
			return np, info, nil
		}
		info.Status = StatusOnline
		return np, info, nil
	case "file":
		fp := NewFile(p.ID, p.FilePath, 1024*1024)
		if err := fp.Open(ctx); err != nil {
			info.Status = StatusError
			info.LastError = err.Error()
			return fp, info, err
		}
		info.Status = StatusOnline
		return fp, info, nil
	case "usb":
		up := NewUSB(p.ID, p.SystemName)
		if err := up.Open(ctx); err != nil {
			info.Status = StatusOffline
			info.LastError = err.Error()
			return up, info, nil
		}
		info.Status = StatusOnline
		return up, info, nil
	default:
		// Should be unreachable after config.Validate. Return a stub
		// file printer so the registry always has a working Printer
		// pointer; writes will fail with a clear error.
		return NewFile(p.ID, os.DevNull, 0), info, fmt.Errorf("unsupported printer type %q", p.Type)
	}
}

// Heartbeat runs Ping on every registered printer in a loop. It updates
// the Status field in the registry at the supplied interval. The loop
// exits when ctx is cancelled.
func Heartbeat(ctx context.Context, reg *Registry, log *slog.Logger, every time.Duration) {
	if every <= 0 {
		every = 10 * time.Second
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			for id, p := range reg.Printers() {
				if err := p.Ping(ctx); err != nil {
					reg.SetStatus(id, StatusOffline, err.Error())
					if log != nil {
						log.Warn("printer ping failed", slog.String("printer", id), slog.String("error", err.Error()))
					}
					continue
				}
				reg.SetStatus(id, StatusOnline, "")
			}
		}
	}
}
