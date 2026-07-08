// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package rpcserver

import (
	"log/slog"
	"net"
	"net/netip"

	"golang.org/x/net/netutil"
)

// limitListener wraps ln in a [netutil.LimitListener] capping the number
// of simultaneously-accepted connections at maxConns. Both callback
// listeners are unauthenticated LAN sockets; without a cap a peer can
// open thousands of connections and force one goroutine (plus its
// per-connection read buffers) per socket, an amplified memory/goroutine
// exhaustion DoS. Accept blocks once the cap is reached and resumes as
// in-flight connections close, so the existing per-connection IO
// deadlines drain stalled peers. maxConns <= 0 leaves ln unwrapped
// (uncapped) — the composition root supplies a secure default; tests
// that pass 0 opt out.
func limitListener(ln net.Listener, maxConns int) net.Listener {
	if maxConns <= 0 {
		return ln
	}
	return netutil.LimitListener(ln, maxConns)
}

// peerAllowed reports whether remote's IP falls within one of the
// allowlist prefixes. An empty allowlist accepts every peer (the default,
// preserving open-LAN behaviour). Shared by the BIN-RPC accept loop and
// the XML-RPC [peerFilterListener] so both listeners apply identical
// source-IP semantics.
func peerAllowed(allow []netip.Prefix, remote net.Addr) bool {
	if len(allow) == 0 {
		return true
	}
	host, _, err := net.SplitHostPort(remote.String())
	if err != nil {
		return false
	}
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	addr = addr.Unmap()
	for _, prefix := range allow {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// peerFilterListener rejects connections whose source IP is not covered
// by the allowlist BEFORE they reach the wrapped server. It is used for
// the XML-RPC callback listener, whose [http.Server.Serve] consumes
// Accept directly and so has no other place to gate the peer. Rejected
// peers are closed inside Accept and never consume an outer
// [limitListener] slot. The BIN-RPC server applies the same allowlist in
// its own accept loop instead (see [BINRPCServer.Serve]).
type peerFilterListener struct {
	net.Listener
	allow  []netip.Prefix
	logger *slog.Logger
}

// newPeerFilterListener wraps ln so only allowlisted source IPs are
// accepted. When allow is empty ln is returned unwrapped.
func newPeerFilterListener(ln net.Listener, allow []netip.Prefix, logger *slog.Logger) net.Listener {
	if len(allow) == 0 {
		return ln
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &peerFilterListener{Listener: ln, allow: allow, logger: logger}
}

// Accept returns the next connection whose source IP is allowlisted,
// closing and skipping any disallowed peer.
func (l *peerFilterListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		if peerAllowed(l.allow, conn.RemoteAddr()) {
			return conn, nil
		}
		l.logger.Debug("xmlrpc callback: peer not in allowlist, closing",
			slog.String("remote", conn.RemoteAddr().String()))
		_ = conn.Close()
	}
}
