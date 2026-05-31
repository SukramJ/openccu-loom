// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package udp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
)

// MatterPort is the IANA-registered Matter operational port (5540).
const MatterPort = 5540

// MaxDatagramSize caps the size of an outbound UDP datagram. Matter
// Core Spec §4.4.4 hands a 1280-byte hard guarantee (IPv6 minimum
// MTU); the IM layer chunks ReportData in [bridge/reply.go]
// chunkReportData to fit. We still allow up to 2048 bytes here because
// individual attributes (e.g. OperationalCredentials.Fabrics with
// multiple X.509 certificates per row, or ACL lists with many entries)
// can on their own exceed the chunk budget — the chunker isolates
// such attributes into their own chunk but cannot sub-split a single
// attribute. WiFi networks routinely deliver 1500-byte datagrams, and
// IPv6 fragmentation handles the 1500-2048 range when present. A hard
// 1280 cap here makes Apple Home pairing fail because the Subscribe
// initial report drops the over-budget chunk and Apple's HAP service
// rebuild times out.
const MaxDatagramSize = 2048

// Errors.
var (
	// ErrListenerClosed is returned by [Listener.Send] after Close.
	ErrListenerClosed = errors.New("udp: listener closed")
	// ErrPayloadTooLarge surfaces when a Send call exceeds
	// [MaxDatagramSize].
	ErrPayloadTooLarge = errors.New("udp: payload exceeds Matter datagram limit")
)

// Handler is invoked by [Listener.Serve] for every received datagram.
// The implementation must not retain the buf slice beyond the call —
// the listener reuses the buffer between iterations to avoid
// per-datagram allocation pressure.
type Handler func(buf []byte, src *net.UDPAddr)

// Config controls the listener's bind / send characteristics.
type Config struct {
	// LocalAddr is the bind address. Empty defaults to `[::]:5540`.
	LocalAddr string
	// PreferIPv4 forces an IPv4-only socket. Default (false) opens an
	// IPv6 dual-stack socket which also accepts IPv4 traffic.
	PreferIPv4 bool
}

// Listener is the Matter UDP transport. Construct with [New], invoke
// [Listener.Serve] in a goroutine to receive, [Listener.Send] to
// transmit, [Listener.Close] to tear down. The zero value is unusable.
type Listener struct {
	conn *net.UDPConn

	mu     sync.Mutex
	closed bool
}

// New opens the UDP socket per cfg and returns a Listener ready to
// Serve.
func New(cfg Config) (*Listener, error) {
	addr := cfg.LocalAddr
	if addr == "" {
		if cfg.PreferIPv4 {
			addr = fmt.Sprintf("0.0.0.0:%d", MatterPort)
		} else {
			addr = fmt.Sprintf("[::]:%d", MatterPort)
		}
	}
	network := "udp"
	if cfg.PreferIPv4 {
		network = "udp4"
	}
	udpAddr, err := net.ResolveUDPAddr(network, addr)
	if err != nil {
		return nil, fmt.Errorf("udp: resolve %s: %w", addr, err)
	}
	conn, err := net.ListenUDP(network, udpAddr)
	if err != nil {
		return nil, fmt.Errorf("udp: listen %s: %w", addr, err)
	}
	return &Listener{conn: conn}, nil
}

// LocalAddr returns the effective bound address — useful for tests
// that bind on `[::]:0` and need to learn the OS-assigned port. The
// type-assert is total because [net.ListenUDP] always returns a
// [*net.UDPConn] whose LocalAddr is a [*net.UDPAddr].
func (l *Listener) LocalAddr() *net.UDPAddr {
	addr, _ := l.conn.LocalAddr().(*net.UDPAddr)
	return addr
}

// Serve runs the receive loop until ctx is cancelled or the connection
// errors out. It is safe to call Serve at most once per Listener.
//
// Datagrams larger than [MaxDatagramSize] are still delivered to the
// handler — the protocol layer is responsible for rejecting them per
// the Matter framing rules. The listener does not silently drop
// over-sized inbound traffic so diagnostics see the actual wire bytes.
func (l *Listener) Serve(ctx context.Context, handler Handler) error {
	if handler == nil {
		return errors.New("udp: nil handler")
	}
	// Cancel-via-Close: when ctx fires we close the conn from a
	// helper goroutine to unblock ReadFromUDP. The Serve goroutine
	// then unwinds with a closed-conn error, which we suppress.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = l.Close()
		case <-done:
		}
	}()

	buf := make([]byte, MaxDatagramSize+64) // small headroom for diagnostics
	for {
		n, src, err := l.conn.ReadFromUDP(buf)
		if err != nil {
			if l.isClosed() || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return fmt.Errorf("udp: read: %w", err)
		}
		// Per-datagram goroutine + copy. Earlier code dispatched
		// synchronously on the same goroutine that owned the UDP read,
		// which serialised the whole receive pipeline behind whatever
		// the handler chose to block on. The Subscribe-Initial chunk
		// loop in `bridge/subscribe.go` blocks for up to
		// `perChunkStatusRespTimeout` (2 s) waiting for the
		// commissioner's IM:StatusResponse — but that StatusResponse
		// arrives on this same UDP socket and could not be processed
		// while the handler held the read goroutine. Net effect:
		// chip-tool retransmitted its StatusResponse four times, the
		// daemon timed out the wait, then drained all the retransmits
		// at once.
		// Spawning a fresh goroutine per datagram lets the Subscribe
		// handler's wait unblock as soon as the StatusResponse hits
		// the wire, cutting the round-trip from 2 s to ~1 ms.
		//
		// The receive buffer is reused on the next ReadFromUDP — must
		// copy before handing off.
		datagram := make([]byte, n)
		copy(datagram, buf[:n])
		go safeDispatch(handler, datagram, src)
	}
}

// safeDispatch wraps handler in a defer-recover so a single bad
// datagram cannot kill the receive loop. Matter peers (especially
// during commissioning) send a wide variety of payload shapes; a
// malformed TLV decode that panics two layers up should NOT take
// down the listener and starve every other in-flight exchange.
// The recovered panic is silently dropped — handlers are responsible
// for their own diagnostics, and panics here always indicate bugs in
// the parsing layer that must be fixed at the source.
func safeDispatch(handler Handler, buf []byte, src *net.UDPAddr) {
	defer func() {
		if r := recover(); r != nil {
			_ = r // intentionally suppressed — see comment above
		}
	}()
	handler(buf, src)
}

// Send transmits a datagram to dst. Returns [ErrPayloadTooLarge] if
// the payload exceeds [MaxDatagramSize] and [ErrListenerClosed] after
// Close.
func (l *Listener) Send(dst *net.UDPAddr, payload []byte) error {
	if len(payload) > MaxDatagramSize {
		return fmt.Errorf("%w: %d bytes", ErrPayloadTooLarge, len(payload))
	}
	if l.isClosed() {
		return ErrListenerClosed
	}
	if _, err := l.conn.WriteToUDP(payload, dst); err != nil {
		return fmt.Errorf("udp: write %s: %w", dst, err)
	}
	return nil
}

// Close shuts the listener down. Idempotent.
func (l *Listener) Close() error {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	l.closed = true
	l.mu.Unlock()
	return l.conn.Close()
}

func (l *Listener) isClosed() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.closed
}
