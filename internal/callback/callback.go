// Package callback sends HTTP webhooks to the SaaS backend when
// print jobs fail or printers go offline. This allows the cashier
// browser (tomapedidos) to show real-time error notifications without
// polling the agent.
package callback

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/tomapedidos/print-agent/internal/eventbus"
)

// Event is the payload POSTed to the callback URL.
type Event struct {
	Type    string `json:"type"`
	JobID   string `json:"job_id,omitempty"`
	Printer string `json:"printer,omitempty"`
	Status  string `json:"status,omitempty"`
	Error   string `json:"error,omitempty"`
	Ts      string `json:"ts,omitempty"`
}

// Sender listens to the eventbus and POSTs relevant events to the
// configured callback URL. It is non-blocking: failed deliveries are
// logged and dropped.
type Sender struct {
	url      string
	tenantID string
	branchID string
	client   *http.Client
	log      *slog.Logger
	bus      *eventbus.Bus
	subID    string
	subCh    <-chan eventbus.Event
	stopOnce sync.Once
	stopCh   chan struct{}
}

// New starts a background goroutine that subscribes to the eventbus
// and POSTs job.failed and printer.status_changed events to callbackURL.
// If callbackURL is empty, New returns nil (no-op).
func New(callbackURL, tenantID, branchID string, bus *eventbus.Bus, log *slog.Logger) *Sender {
	if callbackURL == "" || bus == nil {
		return nil
	}
	s := &Sender{
		url:      callbackURL,
		tenantID: tenantID,
		branchID: branchID,
		client:   &http.Client{Timeout: 10 * time.Second},
		log:      log,
		bus:      bus,
		stopCh:   make(chan struct{}),
	}
	s.subID, s.subCh = bus.Subscribe(64)
	go s.loop()
	return s
}

// Stop terminates the background goroutine.
func (s *Sender) Stop() {
	if s == nil {
		return
	}
	s.stopOnce.Do(func() {
		close(s.stopCh)
		s.bus.Unsubscribe(s.subID)
	})
}

func (s *Sender) loop() {
	for {
		select {
		case <-s.stopCh:
			return
		case ev, ok := <-s.subCh:
			if !ok {
				return
			}
			s.handle(ev)
		}
	}
}

func (s *Sender) handle(ev eventbus.Event) {
	// Only forward events that matter to the SaaS.
	switch ev.Type {
	case "job.failed", "job.printed", "printer.status_changed":
	default:
		return
	}

	payload := Event{
		Type:    ev.Type,
		JobID:   ev.JobID,
		Printer: ev.Printer,
		Status:  ev.Status,
		Error:   ev.Error,
		Ts:      ev.Ts,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		s.log.Warn("callback marshal failed", "error", err)
		return
	}

	req, err := http.NewRequest(http.MethodPost, s.url, bytes.NewReader(body))
	if err != nil {
		s.log.Warn("callback request failed", "error", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if s.tenantID != "" {
		req.Header.Set("X-Tenant-ID", s.tenantID)
	}
	if s.branchID != "" {
		req.Header.Set("X-Branch-ID", s.branchID)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		s.log.Warn("callback delivery failed",
			"url", s.url,
			"type", ev.Type,
			"job_id", ev.JobID,
			"error", err,
		)
		return
	}
	resp.Body.Close()

	if resp.StatusCode >= 300 {
		s.log.Warn("callback rejected",
			"url", s.url,
			"status", resp.StatusCode,
			"type", ev.Type,
			"job_id", ev.JobID,
		)
	}
}
