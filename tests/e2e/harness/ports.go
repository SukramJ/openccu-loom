// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

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
var (
	portsMu      sync.Mutex
	portsInUse   = map[int]struct{}{}
	portAttempts = 40
)

// pickFreePort asks the OS for an ephemeral TCP port and returns it,
// never returning the same port twice within this process.
//
// A window remains between releasing the probe listener and the daemon
// binding the port, in which an unrelated process could take it. That is
// unavoidable without the daemon reporting its effective ports back:
// the REST and UI servers accept ":0" (internal/north/rest/server.go),
// but nothing surfaces the resulting port to the test process, and the
// callback and bin-RPC ports are observable only through the CCU
// re-advertisement path. Closing the in-process collision leaves that
// residual window, which is orders of magnitude rarer.
func pickFreePort(t *testing.T) int {
	t.Helper()
	for range portAttempts {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("pickFreePort: listen: %v", err)
		}
		port := l.Addr().(*net.TCPAddr).Port
		if err := l.Close(); err != nil {
			t.Fatalf("pickFreePort: close: %v", err)
		}
		if reservePort(port) {
			return port
		}
	}
	t.Fatalf("pickFreePort: no unused port after %d attempts", portAttempts)
	return 0
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
