// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package udp

import (
	"bytes"
	"context"
	"errors"
	"net"
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
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = a.Serve(ctx, func(buf []byte, _ *net.UDPAddr) {
			cp := make([]byte, len(buf))
			copy(cp, buf)
			got <- cp
		})
	}()

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
	wg.Add(1)
	go func() {
		defer wg.Done()
		_ = a.Serve(ctx, func(_ []byte, src *net.UDPAddr) {
			gotSrc <- src
		})
	}()

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
