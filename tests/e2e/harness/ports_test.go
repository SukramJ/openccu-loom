// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build e2e

package harness

import (
	"net"
	"sync"
	"sync/atomic"
	"testing"
)

// forgetPort drops a port from the process-wide bookkeeping so a test
// that claims a fixed number stays repeatable under -count>1. Production
// code never releases: a port handed to a daemon stays spoken for.
func forgetPort(t *testing.T, port int) {
	t.Helper()
	t.Cleanup(func() {
		portsMu.Lock()
		defer portsMu.Unlock()
		delete(portsInUse, port)
	})
}

// TestReservePortRejectsARepeat covers the guard directly. The
// concurrent test below cannot prove it: the OS rarely hands out the same
// ephemeral port twice inside one run, so it passes with or without the
// reservation — it is a smoke check, not the assertion.
func TestReservePortRejectsARepeat(t *testing.T) {
	// A port far outside the ephemeral range, so a real allocation cannot
	// collide with this bookkeeping.
	const port = 1
	forgetPort(t, port)

	if !reservePort(port) {
		t.Fatal("first claim rejected")
	}
	if reservePort(port) {
		t.Error("second claim accepted — two parallel tests would get the same port")
	}
}

// TestReservePortIsConcurrencySafe pins that the bookkeeping survives the
// parallel use it exists for: whichever caller wins, exactly one does.
func TestReservePortIsConcurrencySafe(t *testing.T) {
	const port, callers = 2, 32
	forgetPort(t, port)

	var wins atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if reservePort(port) {
				wins.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()

	if got := wins.Load(); got != 1 {
		t.Errorf("%d callers claimed the same port, want exactly 1", got)
	}
}

// TestPickFreeListenerHoldsThePort is the whole point of the helper: the
// returned listener is already bound, so nothing else on the machine can
// take the port between handing it out and serving on it. That is the
// window pickFreePort cannot close, and the one that produced
// "mqtt: add listener: bind: address already in use" in parallel e2e runs.
func TestPickFreeListenerHoldsThePort(t *testing.T) {
	ln, port := pickFreeListener(t)
	defer func() {
		if err := ln.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	}()
	forgetPort(t, port)

	// Binding the same port again must fail while the listener lives.
	// If it succeeds, the helper handed out a port it does not hold.
	second, err := net.Listen("tcp", loopbackAddr(port))
	if err == nil {
		_ = second.Close()
		t.Fatalf("port %d was still free — the listener does not hold it", port)
	}
}

// TestPickFreeListenerPortsAreDistinct pins the reservation bookkeeping:
// two callers within one process never receive the same port.
func TestPickFreeListenerPortsAreDistinct(t *testing.T) {
	seen := make(map[int]struct{}, 10)
	for range 5 {
		ln, port := pickFreeListener(t)
		defer func() { _ = ln.Close() }()
		forgetPort(t, port)
		if _, dup := seen[port]; dup {
			t.Fatalf("port %d handed out twice", port)
		}
		seen[port] = struct{}{}
	}
}
