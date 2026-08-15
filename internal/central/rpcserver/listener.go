// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package rpcserver

import (
	"errors"
	"log/slog"
	"net"
	"net/netip"
	"syscall"
	"time"

	"golang.org/x/net/netutil"
)

// acceptRetryDelayInitial and acceptRetryDelayCap bound the exponential
// backoff applied between retries of a recoverable Accept failure. Same
// 5 ms → 1 s envelope [http.Server.Serve] uses, so both callback
// listeners behave alike under descriptor pressure.
const (
	acceptRetryDelayInitial = 5 * time.Millisecond
	acceptRetryDelayCap     = time.Second
)

// nextAcceptRetryDelay doubles the previous backoff within the
// [acceptRetryDelayInitial, acceptRetryDelayCap] envelope, starting the
// sequence when prev is zero.
func nextAcceptRetryDelay(prev time.Duration) time.Duration {
	if prev <= 0 {
		return acceptRetryDelayInitial
	}
	if next := prev * 2; next < acceptRetryDelayCap {
		return next
	}
	return acceptRetryDelayCap
}

// isRecoverableAcceptError reports whether an Accept failure leaves the
// listening socket healthy, so the accept loop should back off and retry
// instead of tearing itself down.
//
// The set mirrors what [http.Server.Serve] retries — which is what the
// sibling XML-RPC callback listener inherits for free by delegating to
// it, and what the BIN-RPC loop has to do for itself. A peer that resets
// between SYN and accept (ECONNABORTED/ECONNRESET), a transient
// descriptor or buffer shortage (EMFILE/ENFILE/ENOBUFS), an interrupted
// syscall (EINTR) and a listener deadline (Timeout) all say nothing
// about the socket. Returning on one of them leaves the port bound with
// nobody accepting, which silently stops every CUxD push callback for
// the rest of the process lifetime.
func isRecoverableAcceptError(err error) bool {
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}
	return errors.Is(err, syscall.ECONNABORTED) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.EMFILE) ||
		errors.Is(err, syscall.ENFILE) ||
		errors.Is(err, syscall.ENOBUFS) ||
		errors.Is(err, syscall.EINTR)
}

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

// PeerAllowlist resolves the source-IP prefixes a callback listener accepts.
// It is called once per accepted connection rather than sampled at
// construction: the set of CCUs allowed to push callbacks changes while the
// daemon runs (a CCU adopted through the admin surface, a DHCP lease that
// moves an existing one), and a listener holding a boot-time snapshot
// blackholes those peers until the next restart with nothing above DEBUG to
// say so. A nil func, or one returning no prefixes, accepts every peer — the
// default, open-LAN behaviour.
type PeerAllowlist func() []netip.Prefix

// staticPeerAllowlist adapts a fixed prefix set to [PeerAllowlist], for
// callers whose peer set genuinely cannot change.
func staticPeerAllowlist(prefixes []netip.Prefix) PeerAllowlist {
	if len(prefixes) == 0 {
		return nil
	}
	return func() []netip.Prefix { return prefixes }
}

// peerAllowed reports whether remote's IP falls within one of the currently
// allowed prefixes. Shared by the BIN-RPC accept loop and the XML-RPC
// [peerFilterListener] so both listeners apply identical source-IP semantics.
func peerAllowed(allowlist PeerAllowlist, remote net.Addr) bool {
	if allowlist == nil {
		return true
	}
	allow := allowlist()
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
	allow  PeerAllowlist
	logger *slog.Logger
}

// newPeerFilterListener wraps ln so only allowlisted source IPs are
// accepted. When allow is nil ln is returned unwrapped.
func newPeerFilterListener(ln net.Listener, allow PeerAllowlist, logger *slog.Logger) net.Listener {
	if allow == nil {
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
