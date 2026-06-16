// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build e2e

package harness

import (
	"fmt"
	"net"
	"testing"
)

// reservedPort holds an OS-assigned ephemeral TCP port open until
// Release is called. Callers should call Release as close as possible
// to the moment the daemon is started so the listening socket is freed
// only at the last moment before the daemon tries to bind.
//
// Full elimination of the TOCTOU window would require the daemon to
// accept ":0" for callback / bin-RPC ports and expose a readback path
// (e.g. a ready-file or a structured startup log line) that the
// harness can parse. The REST and UI servers already support ":0" (see
// internal/north/rest/server.go) and emit a "rest.listen" JSON log
// line with the effective address, but the harness has no readback
// path wired from that log into a known-before-start REST base URL
// (needed to drive waitForHealth). The callback and bin-RPC servers
// expose their effective ports only via the CCU re-advertisement path,
// which is not observable from the test process.
//
// TODO: add a structured startup signal (e.g. a "--ready-file" flag
// that the daemon writes once all listeners are bound) so the harness
// can use ":0" for all ports and read back the effective addresses
// without any TOCTOU window.
type reservedPort struct {
	ln   net.Listener
	port int
}

// Port returns the reserved port number.
func (r *reservedPort) Port() int { return r.port }

// Release closes the underlying listener, freeing the port for the
// daemon to bind. It should be called as late as possible — ideally
// immediately before exec.Cmd.Start().
func (r *reservedPort) Release(t *testing.T) {
	t.Helper()
	if r.ln == nil {
		return
	}
	if err := r.ln.Close(); err != nil {
		t.Fatalf("reservedPort.Release: %v", err)
	}
	r.ln = nil
}

// pickFreePort asks the OS for an ephemeral TCP port and keeps the
// listener open (via [reservedPort]) until the caller explicitly
// releases it. This minimises the TOCTOU window: the port is held by
// the test process right up until the daemon is exec'd. Callers must
// call Release immediately before starting the daemon.
func pickFreePort(t *testing.T) *reservedPort {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("pickFreePort: listen: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	return &reservedPort{ln: l, port: port}
}

// loopbackAddr returns "127.0.0.1:<port>".
func loopbackAddr(port int) string {
	return fmt.Sprintf("127.0.0.1:%d", port)
}
