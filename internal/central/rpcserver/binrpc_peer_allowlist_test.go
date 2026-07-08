// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Integration-level coverage for BINRPCServer.Serve's peer-allowlist
// enforcement: a disallowed source IP must be closed inside the accept
// loop, before a handleConn goroutine is ever spawned. See the allowlist
// check in binrpc_server.go's Serve method, right before wg.Add / the
// go handleConn(...) call.

package rpcserver

import (
	"context"
	"net"
	"net/netip"
	"testing"
	"time"
)

// TestBINRPCServerServe_DisallowedPeer_ClosedBeforeHandlerSpawns dials
// the BIN-RPC listener from loopback while the allowlist deliberately
// excludes loopback (only 10.0.0.0/8 is allowed). The connection must be
// closed by the server without ever incrementing ActiveTasksCount — that
// count only rises once a handleConn goroutine is spawned, so a
// non-zero count here would mean the allowlist check happened too late
// (inside/after the handler, not in the accept loop).
func TestBINRPCServerServe_DisallowedPeer_ClosedBeforeHandlerSpawns(t *testing.T) {
	srv, err := NewBINRPCServer(BINRPCConfig{
		Addr:          "127.0.0.1:0",
		PeerAllowlist: []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")},
	})
	if err != nil {
		t.Fatalf("NewBINRPCServer: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()
	defer func() { cancel(); _ = srv.Close(); <-done }()

	conn, err := net.DialTimeout("tcp", srv.Addr().String(), time.Second)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// The server must close its side promptly; a Read on our end should
	// observe that as EOF (or another I/O error) well within this deadline.
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 1)
	if n, readErr := conn.Read(buf); readErr == nil {
		t.Fatalf("expected the server to close a disallowed peer's connection, got n=%d err=nil", n)
	}

	// The peer check runs before wg.Add/activeTasks.Add, so no handler
	// goroutine is ever spawned for a rejected connection. Poll briefly
	// (the close + connection teardown is asynchronous) rather than
	// asserting instantaneously.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if srv.ActiveTasksCount() != 0 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if n := srv.ActiveTasksCount(); n != 0 {
		t.Fatalf("ActiveTasksCount = %d, want 0 — a disallowed peer must never spawn a handler goroutine", n)
	}
}

// TestBINRPCServerServe_AllowedPeer_IsHandled is the positive
// counterpart: with loopback included in the allowlist, a loopback
// connection reaches handleConn and the usual BIN-RPC framing errors
// (rather than an immediate close from the peer filter).
func TestBINRPCServerServe_AllowedPeer_IsHandled(t *testing.T) {
	tests := []struct {
		name      string
		allowlist []netip.Prefix
	}{
		{name: "empty allowlist", allowlist: nil},
		{name: "loopback explicitly allowed", allowlist: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv, err := NewBINRPCServer(BINRPCConfig{
				Addr:          "127.0.0.1:0",
				PeerAllowlist: tc.allowlist,
			})
			if err != nil {
				t.Fatalf("NewBINRPCServer: %v", err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- srv.Serve(ctx) }()
			defer func() { cancel(); _ = srv.Close(); <-done }()

			conn, err := net.DialTimeout("tcp", srv.Addr().String(), time.Second)
			if err != nil {
				t.Fatalf("dial: %v", err)
			}
			defer func() { _ = conn.Close() }()

			// Give the accept loop time to spawn the handler goroutine for
			// this connection. Since we send no bytes, handleConn blocks on
			// ReadRequest until its I/O deadline, keeping ActiveTasksCount
			// elevated for a window we can observe.
			deadline := time.Now().Add(2 * time.Second)
			for time.Now().Before(deadline) {
				if srv.ActiveTasksCount() == 1 {
					break
				}
				time.Sleep(5 * time.Millisecond)
			}
			if n := srv.ActiveTasksCount(); n != 1 {
				t.Fatalf("ActiveTasksCount = %d, want 1 — an allowed peer must reach handleConn", n)
			}
		})
	}
}
