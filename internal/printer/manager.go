package printer

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/tomapedidos/print-agent/internal/config"
	"github.com/tomapedidos/print-agent/internal/eventbus"
)

// NewFromConfig instantiates the right concrete Printer for a given
// config.Printer entry.

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
	case "usb", "usb-office", "":
		up := NewUSB(p.ID, p.SystemName)
		// Auto-detect: query CUPS for make-and-model and determine
		// the rendering type if not explicitly set.
		mam := up.MakeAndModel(ctx)
		printerType := p.Type
		if printerType == "" {
			// No type configured — classify from hardware info.
			if mam != "" {
				printerType = DetectType(mam)
			} else {
				printerType = "usb-office" // safe default
			}
		}
		up.SetType(printerType)
		if mam != "" {
			info.MakeAndModel = mam
		}
		if err := up.Open(ctx); err != nil {
			info.Type = printerType
			info.Status = StatusOffline
			info.LastError = err.Error()
			return up, info, nil
		}
		info.Type = printerType
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
// the Status field in the registry at the supplied interval and publishes
// eventbus events when a printer goes offline or comes back online.
func Heartbeat(ctx context.Context, reg *Registry, log *slog.Logger, every time.Duration, bus *eventbus.Bus) {
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
				info, _ := reg.Get(id)
				prevStatus := info.Status
				if err := p.Ping(ctx); err != nil {
					reg.SetStatus(id, StatusOffline, err.Error())
					if log != nil {
						log.Warn("printer ping failed", slog.String("printer", id), slog.String("error", err.Error()))
					}
				} else {
					reg.SetStatus(id, StatusOnline, "")
				}
				newInfo, _ := reg.Get(id)
				if bus != nil && prevStatus != newInfo.Status {
					bus.Publish(eventbus.Event{
						Type:    "printer.status_changed",
						Printer: id,
						Status:  string(newInfo.Status),
					})
				}
			}
		}
	}
}

// SyncFromConfig reconciles the registry with the provided config.
// New printers are opened and registered; printers removed from the
// config are closed and removed from the registry. Used by PUT /config
// so the operator can add/remove printers without restarting the agent.
func SyncFromConfig(ctx context.Context, reg *Registry, cfg config.Config, log *slog.Logger) []string {
	// Collect desired IDs from the config.
	want := make(map[string]config.Printer, len(cfg.Printers))
	for _, p := range cfg.Printers {
		want[p.ID] = p
	}
	// Remove printers that are no longer in the config.
	for _, info := range reg.All() {
		if _, ok := want[info.ID]; !ok {
			if pr, ok := reg.Printers()[info.ID]; ok {
				_ = pr.Close()
			}
			reg.Remove(info.ID)
			if log != nil {
				log.Info("printer removed by config reload", "printer", info.ID)
			}
		}
	}
	// Add printers that are new in the config.
	var added []string
	for id, p := range want {
		if _, ok := reg.Get(id); ok {
			continue // already registered
		}
		pr, info, err := NewFromConfig(ctx, p)
		if err != nil && log != nil {
			log.Warn("printer init failed on reload",
				"printer", id,
				"error", err.Error(),
			)
		}
		if pr != nil {
			reg.Add(pr, info)
			added = append(added, id)
			if log != nil {
				log.Info("printer added by config reload", "printer", id)
			}
		}
	}
	return added
}
