// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package rpcserver

// portbind_test.go — unit tests for the PortRange bind helper (§11/1).
//
// Two behavioural invariants under test:
//
// 1. When the first port of a range is busy, listenInRange picks the
// next available port — it does not give up after the first failure.
//
// 2. When every port in the range is busy, listenInRange returns
// ErrNoPortInRange and the rpcserver constructors propagate it.
//
// Port-allocation strategy: each test allocates a small cluster of
// real OS-assigned ports (net.Listen(":0")), records them, closes the
// listeners, and immediately re-opens the specific ports it wants to
// saturate. The TOCTOU window is minimal (same process, sequential),
// which is sufficient for CI.

import (
	"context"
	"errors"
	"net"
	"sort"
	"testing"
)

// ---------------------------------------------------------------------------
// TestPortRangePicksFirstAvailableInRange (§11/1 un-SKIPped)
// ---------------------------------------------------------------------------

// TestPortRangePicksFirstAvailableInRange verifies that listenInRange
// skips a busy lo-port and succeeds on the next available one.
func TestPortRangePicksFirstAvailableInRange(t *testing.T) {
	t.Parallel()

	// Hold one listener as the occupier and probe a wide range above it.
	// A 50-port window is wide enough that at least one mid-port is
	// free even when other tests in the same binary are simultaneously
	// dialling ephemeral ports.
	listeners, ports := acquireHeldListeners(t, 1)
	defer closeListeners(listeners)
	lo := ports[0]
	hi := loWindowHi(lo, 50)

	// listenInRange must return any port in (lo, hi].
	ln, got, err := listenInRange("127.0.0.1", lo, hi)
	if err != nil {
		t.Fatalf("listenInRange([%d,%d]): unexpected error: %v", lo, hi, err)
	}
	defer func() { _ = ln.Close() }()

	if got <= lo || got > hi {
		t.Fatalf("listenInRange([%d,%d]) returned port %d — expected in (%d, %d]", lo, hi, got, lo, hi)
	}
}

// loWindowHi returns a port close to lo+window but capped at the top
// of the legal port range. The 50-port window is wide enough that at
// least one port in (lo, hi] is free during normal CI load.
func loWindowHi(lo, window int) int {
	hi := lo + window
	if hi > 65535 {
		hi = 65535
	}
	return hi
}

// TestXMLRPCServerPortRangePicksFirstAvailable verifies end-to-end:
// NewXMLRPCServer with PortRange skips a busy lo-port and binds successfully.
func TestXMLRPCServerPortRangePicksFirstAvailable(t *testing.T) {
	t.Parallel()

	listeners, ports := acquireHeldListeners(t, 1)
	defer closeListeners(listeners)
	lo := ports[0]
	hi := loWindowHi(lo, 50)

	srv, err := NewXMLRPCServer(XMLRPCConfig{
		Addr:      "127.0.0.1:0",
		PortRange: NewPortRange(lo, hi),
	})
	if err != nil {
		t.Fatalf("NewXMLRPCServer with PortRange: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()
	defer func() { cancel(); <-done }()

	got := effectivePort(t, srv.Addr())
	if got <= lo || got > hi {
		t.Fatalf("effective port %d not in expected range (%d, %d]", got, lo, hi)
	}
}

// TestBINRPCServerPortRangePicksFirstAvailable is the BIN-RPC analogue.
func TestBINRPCServerPortRangePicksFirstAvailable(t *testing.T) {
	t.Parallel()

	listeners, ports := acquireHeldListeners(t, 1)
	defer closeListeners(listeners)
	lo := ports[0]
	hi := loWindowHi(lo, 50)

	srv, err := NewBINRPCServer(BINRPCConfig{
		Addr:      "127.0.0.1:0",
		PortRange: NewPortRange(lo, hi),
	})
	if err != nil {
		t.Fatalf("NewBINRPCServer with PortRange: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()
	defer func() { cancel(); _ = srv.Close(); <-done }()

	got := effectivePort(t, srv.Addr())
	if got <= lo || got > hi {
		t.Fatalf("effective port %d not in expected range (%d, %d]", got, lo, hi)
	}
}

// ---------------------------------------------------------------------------
// TestPortRangeReturnsErrorWhenAllUsed (§11/1 un-SKIPped)
// ---------------------------------------------------------------------------

// TestPortRangeReturnsErrorWhenAllUsed verifies that listenInRange
// returns ErrNoPortInRange when every port in the range is occupied.
// Uses a degenerate single-port range [p, p] to guarantee 100%
// saturation without gaps.
func TestPortRangeReturnsErrorWhenAllUsed(t *testing.T) {
	t.Parallel()

	// Hold a single port open as the occupier; the helper's range
	// [p, p] is then fully saturated by that listener.
	listeners, ports := acquireHeldListeners(t, 1)
	defer closeListeners(listeners)
	p := ports[0]

	_, _, err := listenInRange("127.0.0.1", p, p)
	if err == nil {
		t.Fatal("listenInRange: expected error when all ports busy, got nil")
	}
	if !errors.Is(err, ErrNoPortInRange) {
		t.Fatalf("listenInRange: got %v, want wrapping ErrNoPortInRange", err)
	}
}

// TestXMLRPCServerPortRangeAllBusyReturnsError verifies that
// NewXMLRPCServer propagates ErrNoPortInRange when the range is
// completely saturated (single-port range).
func TestXMLRPCServerPortRangeAllBusyReturnsError(t *testing.T) {
	t.Parallel()

	listeners, ports := acquireHeldListeners(t, 1)
	defer closeListeners(listeners)
	p := ports[0]

	_, err := NewXMLRPCServer(XMLRPCConfig{
		Addr:      "127.0.0.1:0",
		PortRange: NewPortRange(p, p),
	})
	if err == nil {
		t.Fatal("NewXMLRPCServer: expected error, got nil")
	}
	if !errors.Is(err, ErrNoPortInRange) {
		t.Fatalf("NewXMLRPCServer: got %v, want wrapping ErrNoPortInRange", err)
	}
}

// TestBINRPCServerPortRangeAllBusyReturnsError is the BIN-RPC analogue.
func TestBINRPCServerPortRangeAllBusyReturnsError(t *testing.T) {
	t.Parallel()

	listeners, ports := acquireHeldListeners(t, 1)
	defer closeListeners(listeners)
	p := ports[0]

	_, err := NewBINRPCServer(BINRPCConfig{
		Addr:      "127.0.0.1:0",
		PortRange: NewPortRange(p, p),
	})
	if err == nil {
		t.Fatal("NewBINRPCServer: expected error, got nil")
	}
	if !errors.Is(err, ErrNoPortInRange) {
		t.Fatalf("NewBINRPCServer: got %v, want wrapping ErrNoPortInRange", err)
	}
}

// ---------------------------------------------------------------------------
// TestBindAddrIgnoresPortRangeWhenPortIsFixed
// ---------------------------------------------------------------------------

// TestBindAddrIgnoresPortRangeWhenPortIsFixed verifies that when the
// Addr has a fixed non-zero port, PortRange is ignored entirely.
func TestBindAddrIgnoresPortRangeWhenPortIsFixed(t *testing.T) {
	t.Parallel()

	// Hold the fixed port until the moment NewXMLRPCServer dials it,
	// to keep parallel tests from snatching it between freePort and bind.
	listeners, ports := acquireHeldListeners(t, 1)
	fixed := ports[0]

	// PortRange with range that does NOT include fixed — if PortRange were
	// consulted, the bind would fail (different ports).
	lo, hi := fixed+10, fixed+20
	if hi > 65535 {
		closeListeners(listeners)
		t.Skip("fixed port too close to 65535 for this test")
	}

	closeListeners(listeners)
	srv, err := NewXMLRPCServer(XMLRPCConfig{
		Addr:      addrOf("127.0.0.1", fixed),
		PortRange: NewPortRange(lo, hi),
	})
	if err != nil {
		t.Fatalf("NewXMLRPCServer fixed port with PortRange: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()
	defer func() { cancel(); <-done }()

	got := effectivePort(t, srv.Addr())
	if got != fixed {
		t.Fatalf("effective port=%d, want fixed=%d (PortRange must be ignored for fixed-port Addr)", got, fixed)
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// acquireHeldListeners binds n listeners on ":0" simultaneously and
// returns the open listeners alongside their assigned ports, sorted
// in ascending order. Callers MUST close every listener (use
// closeListeners). Holding the listeners open eliminates the
// release-then-reopen race that lets a parallel test grab the same
// port between Close and re-Listen.
func acquireHeldListeners(t *testing.T, n int) (listeners []net.Listener, ports []int) {
	t.Helper()
	type binding struct {
		ln   net.Listener
		port int
	}
	bindings := make([]binding, n)
	for i := range n {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			for _, b := range bindings[:i] {
				_ = b.ln.Close()
			}
			t.Fatalf("acquireHeldListeners: listen: %v", err)
		}
		bindings[i] = binding{ln: ln, port: ln.Addr().(*net.TCPAddr).Port}
	}
	sort.Slice(bindings, func(i, j int) bool { return bindings[i].port < bindings[j].port })
	listeners = make([]net.Listener, n)
	ports = make([]int, n)
	for i, b := range bindings {
		listeners[i] = b.ln
		ports[i] = b.port
	}
	return listeners, ports
}

// closeListeners closes every listener, ignoring already-closed errors.
func closeListeners(listeners []net.Listener) {
	for _, ln := range listeners {
		_ = ln.Close()
	}
}

// addrOf formats "host:port".
func addrOf(host string, port int) string {
	return net.JoinHostPort(host, itoa(port))
}

// itoa converts an int to its decimal string representation without
// importing strconv (it's a test helper with tiny range).
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := make([]byte, 0, 6)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		buf = append([]byte{byte('0' + n%10)}, buf...)
		n /= 10
	}
	if neg {
		buf = append([]byte{'-'}, buf...)
	}
	return string(buf)
}
