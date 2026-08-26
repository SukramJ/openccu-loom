// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package rpcserver

import (
	"net"
	"sync"
	"testing"
	"time"
)

// TestBINRPCServerActiveTasksCount verifies ActiveTasksCount()
// returns the number of in-flight handleConn goroutines.
func TestBINRPCServerActiveTasksCount(t *testing.T) {
	srv, err := NewBINRPCServer(BINRPCConfig{Addr: ":0"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()

	if n := srv.ActiveTasksCount(); n != 0 {
		t.Fatalf("initial ActiveTasksCount=%d, want 0", n)
	}

	// Serve in background.
	ctx := t.Context()
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ctx) }()

	// Open a connection but don't send anything — handleConn will block on
	// ReadRequest (I/O timeout), keeping the counter elevated.
	addr := srv.Addr().String()
	conns := make([]net.Conn, 0, 3)
	var mu sync.Mutex
	for range 3 {
		c, dialErr := net.DialTimeout("tcp", addr, time.Second)
		if dialErr != nil {
			t.Fatalf("dial: %v", dialErr)
		}
		mu.Lock()
		conns = append(conns, c)
		mu.Unlock()
	}

	// Give the server goroutines time to enter handleConn.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if srv.ActiveTasksCount() == 3 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if n := srv.ActiveTasksCount(); n != 3 {
		t.Fatalf("expected 3 active tasks, got %d", n)
	}

	// Close all connections — handleConn will return (I/O error on
	// closed conn).
	for _, c := range conns {
		_ = c.Close()
	}

	// Wait for count to drop back to 0.
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if srv.ActiveTasksCount() == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if n := srv.ActiveTasksCount(); n != 0 {
		t.Fatalf("after connection close ActiveTasksCount=%d, want 0", n)
	}
}
