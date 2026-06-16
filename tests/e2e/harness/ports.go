// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build e2e

package harness

import (
	"fmt"
	"net"
	"testing"
)

// pickFreePort asks the OS for an ephemeral TCP port, closes the
// listener, and returns the port number. There is an unavoidable
// TOCTOU race window between Close and the daemon's Listen on the
// returned port: another process may grab the port in between. In
// practice the window is short and ports are rarely recycled at
// exactly that instant, so the approach is acceptable for tests.
//
// Eliminating the window entirely would require ":0" binding in the
// daemon and reading the effective port back from the process after
// start. The REST and UI servers support ":0" (see
// internal/north/rest/server.go), but the harness has no readback
// path for those ports (no status endpoint or structured log line
// exposing them). The callback and bin-RPC servers expose their
// effective ports only via the CCU re-advertisement path, which is
// not observable from the test process. Until a structured startup
// signal (e.g. a ready-file or a startup-port JSON line) is added to
// the daemon, pickFreePort remains the least-bad option.
func pickFreePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("pickFreePort: listen: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	if err := l.Close(); err != nil {
		t.Fatalf("pickFreePort: close: %v", err)
	}
	return port
}

// loopbackAddr returns "127.0.0.1:<port>".
func loopbackAddr(port int) string {
	return fmt.Sprintf("127.0.0.1:%d", port)
}
