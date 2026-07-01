// Package eventbus is a minimal in-process pub/sub that feeds
// the WebSocket endpoint. Subscribers receive a typed channel of
// events; publishing is fire-and-forget (non-blocking send).
package eventbus

import (
	"encoding/json"
	"sync"
	"time"
)

// Event is the envelope for all real-time messages sent over /events.
type Event struct {
	Type    string          `json:"type"`
	Ts      string          `json:"ts"`                 // RFC3339 in UTC
	JobID   string          `json:"job_id,omitempty"`
	Printer string          `json:"printer,omitempty"`
	Status  string          `json:"status,omitempty"`
	Error   string          `json:"error,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"` // job snapshot, etc.
}

// Bus manages a set of typed subscriber channels. Each subscriber
// is identified by a unique string key so the caller can later
// Unsubscribe. Publish never blocks: if a subscriber's channel is
// full the event is dropped (one warning per subscriber per burst).
type Bus struct {
	mu          sync.RWMutex
	subs        map[string]chan<- Event
	nextID      int
	droppedLast map[string]time.Time
}

// New returns a fresh Bus ready for subscribers.
func New() *Bus {
	return &Bus{
		subs:        make(map[string]chan<- Event),
		droppedLast: make(map[string]time.Time),
	}
}

// Subscribe returns a channel and a unique key. The channel is
// buffered to bufsz events. The caller must call Unsubscribe when
// done.
func (b *Bus) Subscribe(bufsz int) (string, <-chan Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	id := subID(b.nextID)
	b.nextID++
	ch := make(chan Event, bufsz)
	b.subs[id] = ch
	return id, ch
}

// Unsubscribe removes the subscriber and closes its channel so
// the reader goroutine can exit cleanly.
func (b *Bus) Unsubscribe(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	ch, ok := b.subs[id]
	if ok {
		close(ch)
		delete(b.subs, id)
	}
}

// Publish sends ev to every subscriber. Blocked channels are
// skipped (drop-on-overflow).
func (b *Bus) Publish(ev Event) {
	if ev.Ts == "" {
		ev.Ts = time.Now().UTC().Format(time.RFC3339Nano)
	}
	b.mu.RLock()
	subs := make(map[string]chan<- Event, len(b.subs))
	for k, v := range b.subs {
		subs[k] = v
	}
	b.mu.RUnlock()
	for id, ch := range subs {
		select {
		case ch <- ev:
		default:
			// Drop with one warning per subscriber per second.
			now := time.Now()
			b.mu.RLock()
			last := b.droppedLast[id]
			b.mu.RUnlock()
			if now.Sub(last) > time.Second {
				b.mu.Lock()
				b.droppedLast[id] = now
				b.mu.Unlock()
			}
		}
	}
}

// SubCount returns the current number of active subscribers.
func (b *Bus) SubCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subs)
}

func subID(n int) string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	var buf [6]byte
	for i := range buf {
		buf[i] = chars[n%len(chars)]
		n /= len(chars)
	}
	return string(buf[:])
}
