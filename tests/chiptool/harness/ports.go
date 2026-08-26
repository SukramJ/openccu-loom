// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build chiptool

package harness

import (
	"net"
	"testing"
)

// pickFreeTCPPort returns a TCP port the OS just confirmed free.
// The caller binds in the daemon shortly after; the TOCTOU window
// is small but real — if it bites, the daemon fails fast on bind
// and the test reports a clear error.
func pickFreeTCPPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("pick TCP port: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port
}

// pickFreeUDPPort returns a UDP port the OS just confirmed free.
// Matter uses UDP; for the bridge listener we either prebind (this
// helper) and pass the explicit port to the daemon, or hand the
// daemon a `:0` and read the chosen port out of /matter/status
// afterwards. The harness uses the latter to dodge UDP-bind races
// — pickFreeUDPPort is kept here for callback ports.
func pickFreeUDPPort(t *testing.T) int {
	t.Helper()
	port, err := pickFreeUDPPortNoT()
	if err != nil {
		t.Fatalf("pick UDP port: %v", err)
	}
	return port
}

// pickFreeTCPPortNoT is the no-testing.T variant used by
// [StartShared].
func pickFreeTCPPortNoT() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port, nil
}

// pickFreeUDPPortNoT is the no-testing.T variant used by
// [StartShared].
func pickFreeUDPPortNoT() (int, error) {
	c, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	port := c.LocalAddr().(*net.UDPAddr).Port
	_ = c.Close()
	return port, nil
}
