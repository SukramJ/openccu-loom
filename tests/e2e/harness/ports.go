// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build e2e

package harness

import (
	"fmt"
	"net"
	"sync"
	"testing"
)

// portsInUse records every port this process has handed out. The OS
// recycles ephemeral ports aggressively, so two parallel tests asking for
// one within the same instant can be given the same number — one binds,
// the other dies with "address already in use". Remembering what we
// handed out removes that case, which is the one that actually fires:
// the tests race each other, not the rest of the machine.
//
// Only the in-process servers go through here now. The daemon binds its
// own ports from ":0" and reports them, so nothing hands it a number to
// lose in the meantime.
var (
	portsMu      sync.Mutex
	portsInUse   = map[int]struct{}{}
	portAttempts = 40
)

// pickFreeListener binds an ephemeral loopback port and returns the live
// listener together with its port.
//
// Handing the bound listener straight to an in-process server is what
// makes this safe: the port is never unbound between being chosen and
// being served on, so nothing can take it in between. A helper that
// returns a bare number cannot offer that, which is why the daemon —
// a separate process, and unable to inherit a listener — is configured
// with ":0" and asked afterwards what it bound.
func pickFreeListener(t *testing.T) (net.Listener, int) {
	t.Helper()
	for range portAttempts {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("pickFreeListener: listen: %v", err)
		}
		port := l.Addr().(*net.TCPAddr).Port
		if reservePort(port) {
			return l, port
		}
		if err := l.Close(); err != nil {
			t.Fatalf("pickFreeListener: close: %v", err)
		}
	}
	t.Fatalf("pickFreeListener: no unused port after %d attempts", portAttempts)
	return nil, 0
}

// reservePort claims a port for this process, reporting false when it was
// already handed out.
func reservePort(port int) bool {
	portsMu.Lock()
	defer portsMu.Unlock()
	if _, taken := portsInUse[port]; taken {
		return false
	}
	portsInUse[port] = struct{}{}
	return true
}

// loopbackAddr returns "127.0.0.1:<port>".
func loopbackAddr(port int) string {
	return fmt.Sprintf("127.0.0.1:%d", port)
}

// binPortFor reserves a BIN-RPC callback port for one daemon.
//
// Unlike the XML-RPC callback, which walks `callback.port_range` at bind time,
// the BIN-RPC listener takes a single fixed port and has no range equivalent —
// and `bin_port: 0` is not a dynamic mode, because applyDefaults rewrites it
// to 8129. Parallel harnesses therefore have to be handed distinct ports, or
// all but the first come up with no BIN-RPC listener while still reporting
// themselves healthy.
//
// The listener is released immediately: the daemon binds it a moment later,
// and reservePort keeps this process from handing the same number out twice.
// A foreign process taking it inside that window is the same small risk the
// REST listener's OIDC path already accepts, and it surfaces loudly now that
// /health carries a callback.listeners component.
func binPortFor(t *testing.T) int {
	t.Helper()
	ln, port := pickFreeListener(t)
	if err := ln.Close(); err != nil {
		t.Fatalf("releasing the reserved BIN-RPC port: %v", err)
	}
	return port
}
