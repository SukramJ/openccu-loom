// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Unit coverage for listener.go's two connection-cap primitives:
//   - peerAllowed: the source-IP allowlist check shared by the BIN-RPC
//     accept loop and peerFilterListener.
//   - limitListener: the netutil.LimitListener wrapper that caps
//     simultaneously-accepted connections.

package rpcserver

import (
	"log/slog"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"
)

// fakeAddr is a minimal net.Addr whose String() is fully controlled by
// the test, so peerAllowed's net.SplitHostPort + netip.ParseAddr parsing
// can be exercised without opening a real socket.
type fakeAddr string

func (a fakeAddr) Network() string { return "tcp" }
func (a fakeAddr) String() string  { return string(a) }

func TestPeerAllowed(t *testing.T) {
	tests := []struct {
		name   string
		allow  []netip.Prefix
		remote net.Addr
		want   bool
	}{
		{
			name:   "empty allowlist accepts any peer",
			allow:  nil,
			remote: fakeAddr("203.0.113.5:12345"),
			want:   true,
		},
		{
			name:   "loopback allowed when 127.0.0.0/8 is present",
			allow:  []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")},
			remote: fakeAddr("127.0.0.1:9"),
			want:   true,
		},
		{
			name:   "loopback denied when only an unrelated prefix is present",
			allow:  []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")},
			remote: fakeAddr("127.0.0.1:9"),
			want:   false,
		},
		{
			name:   "IPv6 loopback allowed by ::1/128",
			allow:  []netip.Prefix{netip.MustParsePrefix("::1/128")},
			remote: fakeAddr("[::1]:9"),
			want:   true,
		},
		{
			name:   "v4-in-v6 mapped address matches the v4 prefix via Unmap",
			allow:  []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")},
			remote: fakeAddr("[::ffff:127.0.0.1]:9"),
			want:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := peerAllowed(StaticPeerAllowlist(tc.allow), tc.remote); got != tc.want {
				t.Errorf("peerAllowed(%v, %q) = %v, want %v", tc.allow, tc.remote, got, tc.want)
			}
		})
	}
}

// TestLimitListener_NonPositiveMaxConns_ReturnsSameListener verifies the
// maxConns <= 0 escape hatch: the listener is returned unwrapped so
// tests (and any caller that deliberately opts out) get uncapped Accept.
func TestLimitListener_NonPositiveMaxConns_ReturnsSameListener(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	if got := limitListener(ln, 0); got != ln {
		t.Errorf("limitListener(ln, 0) = %v, want the original listener (identity)", got)
	}
	if got := limitListener(ln, -1); got != ln {
		t.Errorf("limitListener(ln, -1) = %v, want the original listener (identity)", got)
	}
}

// TestLimitListener_CapsConcurrentAccepts proves the cap is enforced: with
// maxConns=1, a second connection is not handed out by Accept until the
// first accepted connection is closed.
func TestLimitListener_CapsConcurrentAccepts(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	limited := limitListener(ln, 1)
	addr := ln.Addr().String()

	accepted := make(chan net.Conn, 2)
	go func() {
		for {
			c, acceptErr := limited.Accept()
			if acceptErr != nil {
				return
			}
			accepted <- c
		}
	}()

	conn1, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		t.Fatalf("dial 1: %v", err)
	}
	defer func() { _ = conn1.Close() }()

	var serverSide1 net.Conn
	select {
	case serverSide1 = <-accepted:
	case <-time.After(2 * time.Second):
		t.Fatal("first connection was not accepted within the cap")
	}
	defer func() { _ = serverSide1.Close() }()

	conn2, err := net.DialTimeout("tcp", addr, time.Second)
	if err != nil {
		t.Fatalf("dial 2: %v", err)
	}
	defer func() { _ = conn2.Close() }()

	// The cap is full (serverSide1 still open) — the second Accept must
	// stay blocked. A short probe window is enough to distinguish "blocked"
	// from "immediately accepted" without slowing the suite down.
	select {
	case c := <-accepted:
		_ = c.Close()
		t.Fatal("second connection was accepted before the first was released; cap not enforced")
	case <-time.After(100 * time.Millisecond):
		// Still blocked, as expected.
	}

	// Release the cap slot; the second connection must now be accepted.
	_ = serverSide1.Close()

	select {
	case c := <-accepted:
		_ = c.Close()
	case <-time.After(2 * time.Second):
		t.Fatal("second connection was not accepted after the first was released")
	}
}

// TestPeerFilterListenerResolvesTheAllowlistPerConnection pins that the
// listener asks for the allowlist on every accept instead of capturing it
// once.
//
// The peer set is not static: the daemon adopts CCUs at runtime, and a CCU's
// address can move under it. A listener holding the prefix set it was built
// with refuses those peers until the process restarts, and the only trace is
// a DEBUG line — the CCU looks connected and simply stops delivering events.
func TestPeerFilterListenerResolvesTheAllowlistPerConnection(t *testing.T) {
	t.Parallel()

	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = base.Close() })

	var mu sync.Mutex
	allowLoopback := false
	ln := newPeerFilterListener(base, func() []netip.Prefix {
		mu.Lock()
		defer mu.Unlock()
		if allowLoopback {
			return []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")}
		}
		// TEST-NET-1 (RFC 5737): reachable from nowhere, so loopback is
		// rejected while this is the answer.
		return []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")}
	}, slog.New(slog.DiscardHandler))

	accepted := make(chan net.Conn, 2)
	go func() {
		for {
			conn, aErr := ln.Accept()
			if aErr != nil {
				close(accepted)
				return
			}
			accepted <- conn
		}
	}()

	dial := func() {
		t.Helper()
		conn, dErr := net.DialTimeout("tcp", base.Addr().String(), time.Second)
		if dErr != nil {
			t.Fatalf("dial: %v", dErr)
		}
		t.Cleanup(func() { _ = conn.Close() })
	}

	dial()
	select {
	case conn := <-accepted:
		t.Fatalf("a peer outside the allowlist was accepted: %v", conn.RemoteAddr())
	case <-time.After(200 * time.Millisecond):
	}

	mu.Lock()
	allowLoopback = true
	mu.Unlock()

	dial()
	select {
	case conn, ok := <-accepted:
		if !ok {
			t.Fatal("listener stopped accepting")
		}
		_ = conn.Close()
	case <-time.After(2 * time.Second):
		t.Fatal("a peer allowed by the current allowlist was still refused; " +
			"the listener must re-read the allowlist per connection")
	}
}
