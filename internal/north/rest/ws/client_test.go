// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package ws

import (
	"bufio"
	"crypto/rand"
	"encoding/json"
	"log/slog"
	"net"
	"testing"
	"time"
)

// maskedTextFrameBytes encodes s as a masked client→server text frame,
// mirroring ws_test.go's writeClientText but returning bytes instead of
// writing them — used where the write happens on a background goroutine
// (see TestClientReadPumpNotStalledBySlowWriter) and therefore must not
// call testing.T.Fatal, which the shared t-taking helper does on error.
func maskedTextFrameBytes(s string) []byte {
	payload := []byte(s)
	header := []byte{0x81} // FIN + text opcode
	var mask [4]byte
	_, _ = rand.Read(mask[:])
	header = append(header, byte(len(payload))|0x80) //nolint:gosec // test-only, short fixed strings
	header = append(header, mask[:]...)
	masked := make([]byte, len(payload))
	for i, b := range payload {
		masked[i] = b ^ mask[i%4]
	}
	return append(header, masked...)
}

// TestClientTeardownStopsBothPumpsWithoutLeak asserts that closing a
// client — via [client.close], the same path an idle timeout or an
// admin-triggered disconnect uses — reliably stops both the reader and
// the single writer goroutine. Regression guard for the writer-goroutine
// refactor: readPump and writePump now communicate purely through the
// c.out / c.ctrl channels and the c.closed signal, so a stuck writer
// goroutine (one that never observes c.closed) would leak forever
// instead of exiting alongside the connection.
func TestClientTeardownStopsBothPumpsWithoutLeak(t *testing.T) {
	t.Parallel()
	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() { _ = clientConn.Close() })

	hub := NewHub()
	br := bufio.NewReader(serverConn)
	bw := bufio.NewWriter(serverConn)
	c := newClient(serverConn, br, bw, hub, slog.Default())

	readDone := make(chan struct{})
	writeDone := make(chan struct{})
	go func() { defer close(readDone); c.readPump() }()
	go func() { defer close(writeDone); c.writePump() }()

	// Simulate an external close trigger (idle timeout, admin
	// disconnect) rather than a read/write error, so this test exercises
	// c.closed independently of I/O failure paths already covered by
	// TestLiveWritePumpEventWriteError / TestWritePumpPingAfterConnClose.
	c.close()

	select {
	case <-readDone:
	case <-time.After(2 * time.Second):
		t.Fatal("readPump did not exit after close")
	}
	select {
	case <-writeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("writePump did not exit after close")
	}
}

// TestClientNoWriteAfterCloseDoesNotPanicOrBlock asserts that once a
// client is closed, queuing further outbound frames — a straggling event
// broadcast or a `call` response racing the teardown — neither panics
// (c.out / c.ctrl are never closed themselves, precisely to avoid a
// "send on closed channel" panic from a concurrent producer) nor blocks
// the caller once the writer goroutine has stopped draining them.
func TestClientNoWriteAfterCloseDoesNotPanicOrBlock(t *testing.T) {
	t.Parallel()
	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() { _ = clientConn.Close() })

	hub := NewHub()
	br := bufio.NewReader(serverConn)
	bw := bufio.NewWriter(serverConn)
	c := newClient(serverConn, br, bw, hub, slog.Default())

	writeDone := make(chan struct{})
	go func() { defer close(writeDone); c.writePump() }()

	c.close()
	select {
	case <-writeDone:
	case <-time.After(2 * time.Second):
		t.Fatal("writePump did not exit after close")
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		c.enqueue(Event{Topic: "test.after.close", Type: "T", When: time.Now()})
		buf, _ := json.Marshal(outboundOp{Op: "ping"})
		c.enqueueCtrl(opText, buf)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("enqueue/enqueueCtrl blocked after writer goroutine exited")
	}
}

// TestClientReadPumpNotStalledBySlowWriter is the regression test for the
// stall this refactor fixes: readPump must keep consuming inbound frames
// even while the writer goroutine is stuck on a slow physical write.
//
// Before the writer-goroutine + channel split, readPump wrote ACKs
// synchronously (under a 10s write deadline) for every subscribe frame
// it processed, so a peer that stopped draining its socket could pause
// the read loop — and therefore all inbound frame processing — for up
// to that deadline. With the split, readPump only ever queues the ACK
// onto c.ctrl and moves on; the physical write (and any resulting
// stall) is entirely the writer goroutine's problem.
func TestClientReadPumpNotStalledBySlowWriter(t *testing.T) {
	t.Parallel()
	serverConn, clientConn := net.Pipe()
	t.Cleanup(func() { _ = clientConn.Close() })

	hub := NewHub()
	br := bufio.NewReader(serverConn)
	bw := bufio.NewWriter(serverConn)
	c := newClient(serverConn, br, bw, hub, slog.Default())
	hub.register(c)

	go c.readPump()
	go c.writePump()

	// This subscribe's ACK is queued via enqueueCtrl and then attempted
	// by writePump. Nobody reads from clientConn, so net.Pipe's
	// synchronous rendezvous blocks that physical write for up to
	// rawWrite's 10s deadline.
	writeClientText(t, clientConn, `{"op":"subscribe","topics":["first.topic"]}`)

	// Give writePump a moment to pick the ACK off c.ctrl and start
	// blocking on the physical write.
	time.Sleep(50 * time.Millisecond)

	// A second subscribe must still be processed promptly — proving
	// readPump is not blocked behind the stuck writer goroutine. The
	// write itself happens on net.Pipe's synchronous rendezvous (it only
	// completes once readPump calls Read again), so it runs in the
	// background rather than on the assertion goroutine: on the old
	// synchronous-write code this send would block for as long as
	// readPump is stuck, which would make the *test's own write* — not
	// readPump — the thing timing out, hiding the bug this test targets.
	// Errors are ignored (not t.Fatal'd) since this goroutine may still
	// be in flight after the assertion below completes.
	frame2 := maskedTextFrameBytes(`{"op":"subscribe","topics":["second.topic"]}`)
	go func() { _, _ = clientConn.Write(frame2) }()

	// The assertion window is far short of the 10s write deadline that
	// gated the old synchronous behavior, so this only passes quickly
	// when readPump genuinely kept reading instead of blocking on the
	// stuck first ACK write.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if c.matches("second.topic") {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("readPump did not process the second subscribe while writePump was stuck on a slow consumer")
}
