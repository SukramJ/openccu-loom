// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package pairing

import "net"

// LocalHost reports whether host (an IP without port, as
// net.SplitHostPort yields it) belongs to a network a pairing request
// may come from: loopback, RFC 1918, link-local, or a ULA. The address
// is the connection's RemoteAddr — deliberately not X-Forwarded-For,
// matching the login rate limiter's policy (an unauthenticated header
// would let anyone claim to be local). Behind a reverse proxy the proxy
// itself is the peer, which is local by definition; the operator's proxy
// is then the place to gate pairing, exactly as it gates login.
func LocalHost(host string) bool {
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast()
}
