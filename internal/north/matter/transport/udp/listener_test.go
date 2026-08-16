// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package udp

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// newLoopbackPair returns a peer-A / peer-B pair bound on
// 127.0.0.1:0. Tests bind ephemeral ports so they run in parallel
// without port collisions.
func newLoopbackPair(t *testing.T) (a, b *Listener) {
	t.Helper()
	cfg := Config{LocalAddr: "127.0.0.1:0", PreferIPv4: true}
	la, err := New(cfg)
	if err != nil {
		t.Fatalf("a: %v", err)
	}
	lb, err := New(cfg)
	if err != nil {
		_ = la.Close()
		t.Fatalf("b: %v", err)
	}
	t.Cleanup(func() {
		_ = la.Close()
		_ = lb.Close()
	})
	return la, lb
}

// TestListenerEcho — peer A serves, peer B sends; assert the bytes
// arrive intact.
func TestListenerEcho(t *testing.T) {
	a, b := newLoopbackPair(t)

	got := make(chan []byte, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	wg.Go(func() {
		_ = a.Serve(ctx, func(buf []byte, _ *net.UDPAddr) {
			cp := make([]byte, len(buf))
			copy(cp, buf)
			got <- cp
		})
	})

	payload := []byte("hello matter")
	if err := b.Send(a.LocalAddr(), payload); err != nil {
		t.Fatalf("Send err: %v", err)
	}

	select {
	case rx := <-got:
		if !bytes.Equal(rx, payload) {
			t.Errorf("rx=%q, want %q", rx, payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for datagram")
	}
	cancel()
	wg.Wait()
}

// TestListenerSourceAddressTagged confirms the handler receives the
// peer's source address — required by MRP for response routing.
func TestListenerSourceAddressTagged(t *testing.T) {
	a, b := newLoopbackPair(t)

	gotSrc := make(chan *net.UDPAddr, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	wg.Go(func() {
		_ = a.Serve(ctx, func(_ []byte, src *net.UDPAddr) {
			gotSrc <- src
		})
	})

	if err := b.Send(a.LocalAddr(), []byte{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	select {
	case src := <-gotSrc:
		if src.Port != b.LocalAddr().Port {
			t.Errorf("src port=%d, want %d", src.Port, b.LocalAddr().Port)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
	cancel()
	wg.Wait()
}

// TestSendPayloadTooLarge rejects oversize traffic at the transport.
func TestSendPayloadTooLarge(t *testing.T) {
	a, _ := newLoopbackPair(t)
	payload := make([]byte, MaxDatagramSize+1)
	err := a.Send(&net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1}, payload)
	if !errors.Is(err, ErrPayloadTooLarge) {
		t.Fatalf("err = %v, want ErrPayloadTooLarge", err)
	}
}

// TestSendOnClosedListenerSurfacesErr asserts the post-Close
// invariant: Send() returns ErrListenerClosed instead of writing to
// a stale FD.
func TestSendOnClosedListenerSurfacesErr(t *testing.T) {
	a, _ := newLoopbackPair(t)
	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	err := a.Send(&net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 1}, []byte("x"))
	if !errors.Is(err, ErrListenerClosed) {
		t.Fatalf("err = %v, want ErrListenerClosed", err)
	}
}

// TestServeNilHandlerRefused — Serve must reject a nil handler
// rather than panicking on first datagram dispatch.
func TestServeNilHandlerRefused(t *testing.T) {
	a, _ := newLoopbackPair(t)
	err := a.Serve(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for nil handler")
	}
}

// TestServeContextCancelStops asserts ctx-cancellation tears the
// listener down without producing an error to the caller.
func TestServeContextCancelStops(t *testing.T) {
	a, _ := newLoopbackPair(t)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- a.Serve(ctx, func([]byte, *net.UDPAddr) {})
	}()

	// Give the goroutine a moment to enter ReadFromUDP, then cancel.
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned %v on ctx-cancel, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after ctx cancel")
	}
}

// TestCloseIsIdempotent — repeated Close() calls must not error or
// double-close the underlying socket.
func TestCloseIsIdempotent(t *testing.T) {
	a, _ := newLoopbackPair(t)
	if err := a.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := a.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// TestPanicInHandlerIsRecoveredCountedAndLogged pins the diagnostic
// half of the recover: a handler panic must not kill the receive loop,
// must be counted, and must reach the log — a datagram that vanishes
// without a trace is the failure mode this listener is meant to make
// attributable.
func TestPanicInHandlerIsRecoveredCountedAndLogged(t *testing.T) {
	a, b := newLoopbackPair(t)

	var logBuf bytes.Buffer
	var logMu sync.Mutex
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&lockedWriter{mu: &logMu, w: &logBuf}, &slog.HandlerOptions{Level: slog.LevelError})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	survived := make(chan struct{}, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	wg.Go(func() {
		_ = a.Serve(ctx, func(buf []byte, _ *net.UDPAddr) {
			if string(buf) == "boom" {
				panic("handler exploded")
			}
			survived <- struct{}{}
		})
	})

	if err := b.Send(a.LocalAddr(), []byte("boom")); err != nil {
		t.Fatalf("Send boom: %v", err)
	}
	// The second datagram proves the receive loop is still alive.
	deadline := time.After(2 * time.Second)
	for {
		if err := b.Send(a.LocalAddr(), []byte("ok")); err != nil {
			t.Fatalf("Send ok: %v", err)
		}
		select {
		case <-survived:
			cancel()
			wg.Wait()
			// The panicking datagram is dispatched on its own goroutine,
			// so the recovery can land after the second datagram was
			// handled — poll rather than sampling once.
			var recovered uint64
			for range 100 {
				recovered = a.RecoveredPanics()
				if recovered > 0 {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if recovered != 1 {
				t.Errorf("RecoveredPanics = %d, want 1", recovered)
			}
			// The counter is bumped before the log line is written, so
			// poll for the record rather than sampling once.
			var logged string
			for range 100 {
				logMu.Lock()
				logged = logBuf.String()
				logMu.Unlock()
				if strings.Contains(logged, "matter.udp.dispatch_panic") {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if !strings.Contains(logged, "matter.udp.dispatch_panic") {
				t.Errorf("recovered panic was not logged; log = %q", logged)
			}
			if !strings.Contains(logged, "handler exploded") {
				t.Errorf("panic value missing from log; log = %q", logged)
			}
			return
		case <-time.After(50 * time.Millisecond):
		case <-deadline:
			t.Fatal("receive loop did not survive the handler panic")
		}
	}
}

// lockedWriter serialises writes from the dispatch goroutine and the
// test goroutine onto one buffer.
type lockedWriter struct {
	mu *sync.Mutex
	w  *bytes.Buffer
}

func (l *lockedWriter) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(p)
}
