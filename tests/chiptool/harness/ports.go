// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build chiptool

package harness

import (
	"context"
	"fmt"
	"net"
)

// pickFreeTCPPortNoT returns a TCP port the OS just confirmed free.
// The caller binds in the daemon shortly after; the TOCTOU window
// is small but real — if it bites, the daemon fails fast on bind
// and the bring-up reports a clear error. Used by the bridge bring-up.
func pickFreeTCPPortNoT() (int, error) {
	var lc net.ListenConfig
	l, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	addr, ok := l.Addr().(*net.TCPAddr)
	_ = l.Close()
	if !ok {
		return 0, fmt.Errorf("unexpected TCP listener address type %T", l.Addr())
	}
	return addr.Port, nil
}

// pickFreeUDPPortNoT returns a UDP port the OS just confirmed free.
// Used by the bridge bring-up for the Matter listener port.
func pickFreeUDPPortNoT() (int, error) {
	var lc net.ListenConfig
	c, err := lc.ListenPacket(context.Background(), "udp4", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	addr, ok := c.LocalAddr().(*net.UDPAddr)
	_ = c.Close()
	if !ok {
		return 0, fmt.Errorf("unexpected UDP listener address type %T", c.LocalAddr())
	}
	return addr.Port, nil
}
