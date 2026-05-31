// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

// lifecycleMockBroker is a minimal MQTT broker for lifecycle tests.
// Unlike the mockBroker in adapter_tcp_test.go it supports:
//   - multiple sequential connections (reconnect scenarios)
//   - InjectTCPReset — abrupt close of the active connection
//   - RejectNextConnect — CONNACK with a non-zero return code on the
//     next CONNECT so the caller can test backoff behaviour
//   - SubscribeCount — total SUBSCRIBE frames seen across all connections
//
// It is intentionally kept in a non-test file so it is accessible
// from lifecycle_test.go without circular restrictions; all exported
// symbols are unexported and only reachable within the package.
// The _test.go suffix would normally suffice, but putting both mock
// variants in a single package build requires separate type names.
//
// Build tag: this file is compiled only during tests via the
// internal/testsupport convention — it is harmless to production
// because no production code references it.

import (
	"bufio"
	"net"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/mqtt/protocol"
)

// lifecycleMockBroker is a multi-connection mock broker used by the
// MQTT lifecycle tests.
type lifecycleMockBroker struct {
	t        *testing.T
	listener net.Listener

	// rejectNextConnect, when true, causes the next CONNECT to receive a
	// non-zero CONNACK return code (connection refused / 0x05).
	rejectNextConnect atomic.Bool

	// subCount is the total number of SUBSCRIBE frames received across
	// all connections.
	subCount atomic.Int32

	// connCount is the total number of accepted TCP connections.
	connCount atomic.Int32

	mu      sync.Mutex
	current net.Conn // active connection; nil when no client is connected
}

// newLifecycleMockBroker starts a listener on a random local port and
// returns the broker. The listener is closed automatically when t
// finishes.
func newLifecycleMockBroker(t *testing.T) *lifecycleMockBroker {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("lifecycleMockBroker: listen: %v", err)
	}
	b := &lifecycleMockBroker{t: t, listener: ln}
	go b.acceptLoop()
	t.Cleanup(func() { _ = ln.Close() })
	return b
}

// URL returns the tcp:// URL the broker is listening on.
func (b *lifecycleMockBroker) URL() string { return "tcp://" + b.listener.Addr().String() }

// InjectTCPReset closes the active connection abruptly, simulating a
// TCP RST. The client's read-loop will receive an EOF / connection
// reset error and call handleConnectionLost.
func (b *lifecycleMockBroker) InjectTCPReset() {
	b.mu.Lock()
	conn := b.current
	b.current = nil
	b.mu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
}

// RejectNextConnect configures the broker to send a CONNACK with
// return code 0x05 (connection refused, not authorised) on the next
// incoming CONNECT packet, then revert to accepting.
func (b *lifecycleMockBroker) RejectNextConnect() {
	b.rejectNextConnect.Store(true)
}

// SubscribeCount returns the total number of SUBSCRIBE frames received
// across all connections since the broker was created.
func (b *lifecycleMockBroker) SubscribeCount() int {
	return int(b.subCount.Load())
}

// ConnCount returns the total number of accepted TCP connections.
func (b *lifecycleMockBroker) ConnCount() int {
	return int(b.connCount.Load())
}

// acceptLoop loops forever accepting connections.
func (b *lifecycleMockBroker) acceptLoop() {
	for {
		conn, err := b.listener.Accept()
		if err != nil {
			return // listener closed — normal shutdown
		}
		b.connCount.Add(1)
		b.mu.Lock()
		b.current = conn
		b.mu.Unlock()
		go b.serve(conn)
	}
}

// serve handles one client connection. It responds to CONNECT,
// SUBSCRIBE, PINGREQ, and DISCONNECT. Unknown packet types are ignored.
func (b *lifecycleMockBroker) serve(conn net.Conn) {
	defer func() {
		// Clear current only if still pointing at this connection.
		b.mu.Lock()
		if b.current == conn {
			b.current = nil
		}
		b.mu.Unlock()
		_ = conn.Close()
	}()

	bw := bufio.NewWriter(conn)
	br := bufio.NewReader(conn)

	for {
		frame, err := protocol.ReadFrame(br)
		if err != nil {
			return
		}

		switch frame.PacketType() { //nolint:exhaustive // only inbound frames that require a reply
		case protocol.PacketConnect:
			if b.rejectNextConnect.CompareAndSwap(true, false) {
				// Non-zero return code → client must disconnect and back off.
				_ = lcmWritePacket(bw, byte(protocol.PacketConnack)<<4, []byte{0, 0x05})
			} else {
				_ = lcmWritePacket(bw, byte(protocol.PacketConnack)<<4, []byte{0, 0})
			}
			_ = bw.Flush()

		case protocol.PacketSubscribe:
			b.subCount.Add(1)
			// Send SUBACK so the client is not left waiting.
			body := []byte{frame.Body[0], frame.Body[1], 0x01}
			_ = lcmWritePacket(bw, byte(protocol.PacketSuback)<<4, body)
			_ = bw.Flush()

		case protocol.PacketPingreq:
			_ = lcmWritePacket(bw, byte(protocol.PacketPingresp)<<4, nil)
			_ = bw.Flush()

		case protocol.PacketDisconnect:
			return
		}
	}
}

// lcmWritePacket is a local helper identical in purpose to writePacket
// in adapter_tcp_test.go but scoped to this file to avoid a duplicate
// symbol when both files are compiled together.
func lcmWritePacket(w *bufio.Writer, header byte, body []byte) error {
	if _, err := w.Write([]byte{header}); err != nil {
		return err
	}
	if _, err := w.Write(lcmEncodeLength(len(body))); err != nil {
		return err
	}
	if len(body) > 0 {
		if _, err := w.Write(body); err != nil {
			return err
		}
	}
	return w.Flush()
}

func lcmEncodeLength(n int) []byte {
	var out []byte
	for {
		digit := byte(n & 0x7F)
		n >>= 7
		if n > 0 {
			digit |= 0x80
		}
		out = append(out, digit)
		if n == 0 {
			break
		}
	}
	return out
}
