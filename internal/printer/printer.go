// Package printer defines the Printer interface and the runtime status
// tracked by the agent. Implementations live in network.go, file.go and
// (later) usb.go.
package printer

import (
	"context"
	"sync"
	"time"
)

// Status is the runtime state of a physical printer.
type Status string

const (
	StatusOnline   Status = "online"
	StatusOffline  Status = "offline"
	StatusPrinting Status = "printing"
	StatusError    Status = "error"
	StatusPaused   Status = "paused"
)

// Info is the runtime snapshot exposed via /printers and /health.
type Info struct {
	ID           string    `json:"id"`
	Name         string    `json:"name"`
	Type         string    `json:"type"`
	Host         string    `json:"host,omitempty"`
	Port         int       `json:"port,omitempty"`
	SystemName   string    `json:"system_name,omitempty"`
	FilePath     string    `json:"file_path,omitempty"`
	MakeAndModel string    `json:"make_and_model,omitempty"`
	CodePage     string    `json:"code_page"`
	CharsPerLine int       `json:"chars_per_line"`
	Cut          string    `json:"cut"`
	Status       Status    `json:"status"`
	QueueDepth   int       `json:"queue_depth"`
	LastPrintAt  time.Time `json:"last_print_at"`
	LastError    string    `json:"last_error,omitempty"`
	LastSeenAt   time.Time `json:"last_seen_at"`
}

// Printer is the contract every concrete backend (network, usb, file) must
// satisfy. Write is the only hot-path method; Ping is called by the
// heartbeat goroutine to keep Status fresh.
type Printer interface {
	ID() string
	Open(ctx context.Context) error
	Write(ctx context.Context, payload []byte) error
	Close() error
	Ping(ctx context.Context) error
}

// Registry holds the live set of printers keyed by ID. It is the single
// source of truth for /printers, /health and the worker pool.
type Registry struct {
	mu        sync.RWMutex
	printers  map[string]Printer
	info      map[string]Info
	lastQueue map[string]int
}

// NewRegistry returns an empty Registry.
func NewRegistry() *Registry {
	return &Registry{
		printers:  make(map[string]Printer),
		info:      make(map[string]Info),
		lastQueue: make(map[string]int),
	}
}

// Add registers a printer and seeds its Info from the supplied metadata.
func (r *Registry) Add(p Printer, meta Info) {
	r.mu.Lock()
	defer r.mu.Unlock()
	meta.Status = StatusOffline
	meta.LastSeenAt = time.Now()
	r.printers[p.ID()] = p
	r.info[p.ID()] = meta
}

// Remove deletes a printer from the registry.
func (r *Registry) Remove(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.printers, id)
	delete(r.info, id)
	delete(r.lastQueue, id)
}

// Get returns the live Info for id.
func (r *Registry) Get(id string) (Info, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	info, ok := r.info[id]
	return info, ok
}

// All returns a snapshot of every registered printer, sorted by ID for
// deterministic /printers and /health output.
func (r *Registry) All() []Info {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Info, 0, len(r.info))
	for _, v := range r.info {
		v.QueueDepth = r.lastQueue[v.ID]
		out = append(out, v)
	}
	// stable order: leave to caller, do not sort here
	return out
}

// SetStatus updates the runtime status and optional error message.
func (r *Registry) SetStatus(id string, status Status, errMsg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	info, ok := r.info[id]
	if !ok {
		return
	}
	info.Status = status
	info.LastError = errMsg
	if status == StatusOnline {
		info.LastSeenAt = time.Now()
	}
	r.info[id] = info
}

// MarkPrinted stamps LastPrintAt and clears the error.
func (r *Registry) MarkPrinted(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	info, ok := r.info[id]
	if !ok {
		return
	}
	info.LastPrintAt = time.Now()
	info.LastError = ""
	info.Status = StatusOnline
	r.info[id] = info
}

// SetQueueDepth is called by the queue worker to keep the per-printer
// backlog visible in /health and /printers.
func (r *Registry) SetQueueDepth(id string, depth int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lastQueue[id] = depth
}

// Printers returns the underlying map of printers (read-only intent).
func (r *Registry) Printers() map[string]Printer {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]Printer, len(r.printers))
	for k, v := range r.printers {
		out[k] = v
	}
	return out
}
