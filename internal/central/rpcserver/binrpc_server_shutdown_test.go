// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package rpcserver

import (
	"context"
	"net"
	"sync"
	"testing"
)

// TestTrackConnOrdersAgainstShutdown pins the ordering the accept loop
// depends on. A plain check-then-Add lets shutdown land between the two, so
// the Add runs concurrently with the wg.Wait that Close has already entered —
// the WaitGroup misuse the shutdown flag exists to prevent. Every accepted
// connection is therefore either tracked strictly before the flag is set, or
// refused outright.
//
// The test dials while Close runs, which is exactly the interleaving a CCU
// pushing events into a shutting-down daemon produces.
func TestTrackConnOrdersAgainstShutdown(t *testing.T) {
	t.Parallel()

	for range 20 {
		srv, err := NewBINRPCServer(BINRPCConfig{Addr: "127.0.0.1:0"})
		if err != nil {
			t.Fatalf("NewBINRPCServer: %v", err)
		}
		addr := srv.Addr().String()

		ctx, cancel := context.WithCancel(context.Background())
		served := make(chan error, 1)
		go func() { served <- srv.Serve(ctx) }()

		var dialers sync.WaitGroup
		start := make(chan struct{})
		for range 8 {
			dialers.Add(1)
			go func() {
				defer dialers.Done()
				<-start
				conn, err := net.Dial("tcp", addr)
				if err != nil {
					// Refused because the listener is already down — the
					// other legitimate outcome of this race.
					return
				}
				_ = conn.Close()
			}()
		}
		closed := make(chan struct{})
		go func() {
			defer close(closed)
			<-start
			// Close is what enters wg.Wait; a handler tracked after that
			// point would be the misuse.
			_ = srv.Close()
		}()

		close(start)
		dialers.Wait()
		<-closed
		cancel()
		<-served
	}
}

// TestTrackConnRefusesAfterShutdown pins the plain half of the contract: once
// shutdown has begun, no further handler may be registered, so a connection
// that arrives late is dropped rather than counted into a WaitGroup nobody
// waits on any more.
func TestTrackConnRefusesAfterShutdown(t *testing.T) {
	t.Parallel()

	srv, err := NewBINRPCServer(BINRPCConfig{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatalf("NewBINRPCServer: %v", err)
	}
	if !srv.trackConn() {
		t.Fatal("trackConn refused a connection on a running server")
	}
	srv.wg.Done()

	if err := srv.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if srv.trackConn() {
		t.Error("trackConn accepted a connection after shutdown began")
	}
}
