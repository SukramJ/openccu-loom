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
// race window between Close and the next Listen on the same port —
// the harness mitigates this by using ephemeral ports for the *real*
// listener too (port=0 in the daemon config), so the value returned
// here is only used where the daemon insists on knowing the port up
// front (callback servers, advertised URLs).
//
// Tests that need an OS-assigned port for a daemon listener should
// use port=0 in the configuration and read the effective port from
// the harness's accessor methods after Start returns.
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
