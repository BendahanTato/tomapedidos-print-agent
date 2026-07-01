package printer

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"
)

// NetworkPrinter is a Printer backed by a raw TCP socket. Most ESC/POS
// thermal receipt printers listen on port 9100 (the de-facto standard)
// but any host:port pair is configurable.
type NetworkPrinter struct {
	id      string
	host    string
	port    int
	timeout time.Duration

	conn net.Conn
}

// NewNetwork returns a NetworkPrinter configured for host:port with a
// per-operation timeout of 3 seconds (overridable via SetTimeout).
func NewNetwork(id, host string, port int) *NetworkPrinter {
	return &NetworkPrinter{
		id:      id,
		host:    host,
		port:    port,
		timeout: 3 * time.Second,
	}
}

// ID returns the printer's logical identifier.
func (p *NetworkPrinter) ID() string { return p.id }

// SetTimeout adjusts the per-call deadline. Mainly for tests.
func (p *NetworkPrinter) SetTimeout(d time.Duration) {
	p.timeout = d
}

// Open establishes a TCP connection. The socket is held open for the
// lifetime of the process and reused across writes.
func (p *NetworkPrinter) Open(ctx context.Context) error {
	if p.conn != nil {
		return nil
	}
	d := net.Dialer{Timeout: p.timeout}
	addr := fmt.Sprintf("%s:%d", p.host, p.port)
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("dial %s: %w", addr, err)
	}
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetNoDelay(true)
	}
	p.conn = conn
	return nil
}

// Write sends the payload to the printer. If the socket is closed it
// transparently reconnects once before failing.
func (p *NetworkPrinter) Write(ctx context.Context, payload []byte) error {
	if p.conn == nil {
		if err := p.Open(ctx); err != nil {
			return err
		}
	}
	if err := p.conn.SetWriteDeadline(time.Now().Add(p.timeout)); err != nil {
		return fmt.Errorf("set write deadline: %w", err)
	}
	if _, err := p.conn.Write(payload); err != nil {
		// On any write error, drop the connection so the next attempt
		// reconnects cleanly instead of failing forever.
		_ = p.Close()
		return fmt.Errorf("write to %s:%d: %w", p.host, p.port, err)
	}
	return nil
}

// Close releases the underlying TCP connection. Safe to call multiple times.
func (p *NetworkPrinter) Close() error {
	if p.conn == nil {
		return nil
	}
	err := p.conn.Close()
	p.conn = nil
	return err
}

// Ping performs a fast TCP connect check without sending data. Used by
// the heartbeat goroutine to keep the Status field in /health fresh.
func (p *NetworkPrinter) Ping(ctx context.Context) error {
	d := net.Dialer{Timeout: p.timeout}
	addr := fmt.Sprintf("%s:%d", p.host, p.port)
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	return conn.Close()
}

// ErrNetworkUnreachable is returned when the host refuses the connection.
var ErrNetworkUnreachable = errors.New("network printer unreachable")
