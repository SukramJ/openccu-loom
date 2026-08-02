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

// TestPickFreePortHandsOutUsablePorts is the smoke check: every port it
// returns must be distinct and bindable. It cannot fail the way the
// reservation guard can, so it does not stand in for the tests above.
func TestPickFreePortHandsOutUsablePorts(t *testing.T) {
	seen := make(map[int]struct{}, 20)
	for range 20 {
		p := pickFreePort(t)
		if _, dup := seen[p]; dup {
			t.Fatalf("port %d handed out twice", p)
		}
		seen[p] = struct{}{}
		l, err := net.Listen("tcp", loopbackAddr(p))
		if err != nil {
			t.Fatalf("port %d not bindable: %v", p, err)
		}
		if err := l.Close(); err != nil {
			t.Fatalf("close: %v", err)
		}
	}
}
