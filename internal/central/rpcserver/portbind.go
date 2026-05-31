// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package rpcserver

import (
	"errors"
	"fmt"
	"net"
	"strings"
)

// ErrNoPortInRange is returned by [listenInRange] when every port in
// [lo, hi] is already in use (or otherwise unavailable).
var ErrNoPortInRange = errors.New("rpcserver: no available port in range")

// PortRange holds a parsed lo..hi port range.
type PortRange struct {
	Lo int
	Hi int
}

// NewPortRange creates a [PortRange] from lo and hi. It does not
// validate — use [config.ParsePortRange] for validated input.
func NewPortRange(lo, hi int) *PortRange {
	return &PortRange{Lo: lo, Hi: hi}
}

// bindAddr is the central bind helper used by both server constructors.
// Decision table:
//   - addr has a fixed non-zero port → net.Listen(addr) (existing behavior).
//   - addr ends in ":0" AND pr != nil → listenInRange(host, pr.Lo, pr.Hi).
//   - addr ends in ":0" AND pr == nil → net.Listen(addr) (OS ephemeral).
func bindAddr(addr string, pr *PortRange) (net.Listener, error) {
	if pr != nil && strings.HasSuffix(addr, ":0") {
		host := strings.TrimSuffix(addr, ":0")
		ln, _, err := listenInRange(host, pr.Lo, pr.Hi)
		return ln, err
	}
	return net.Listen("tcp", addr)
}

// listenInRange tries net.Listen("tcp", "host:p") for p = lo..hi and
// returns the first listener that succeeds together with the bound port.
//
// The caller owns the returned listener and must close it when done.
// If all attempts fail, (nil, 0, ErrNoPortInRange) is returned.
func listenInRange(host string, lo, hi int) (net.Listener, int, error) {
	for p := lo; p <= hi; p++ {
		addr := fmt.Sprintf("%s:%d", host, p)
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			continue
		}
		// We just listened on TCP — Addr() is always a *net.TCPAddr.
		tcpAddr, ok := ln.Addr().(*net.TCPAddr)
		if !ok {
			_ = ln.Close()
			return nil, 0, fmt.Errorf("%w: unexpected addr type %T", ErrNoPortInRange, ln.Addr())
		}
		return ln, tcpAddr.Port, nil
	}
	return nil, 0, fmt.Errorf("%w: [%d, %d] on %s", ErrNoPortInRange, lo, hi, host)
}
