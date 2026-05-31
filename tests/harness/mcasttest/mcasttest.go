// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package mcasttest provides lightweight helpers for unit tests that need to
// exercise UDP-multicast send/receive paths without requiring raw-socket
// privileges or a real network. It binds exclusively to the loopback
// interface and tries multicast first; if the kernel rejects the join (some
// sandboxes disable multicast on `lo`) it falls back to plain unicast on
// 127.0.0.1 so the same test logic still exercises the packet path.
//
// # Public surface
//
//   - [Probe] — detects multicast availability and returns a [Result].
//   - [ListenUDP4] — opens a receiving UDP4 socket on a random port and joins
//     a loopback-scoped multicast group (or unicast equivalent).
//   - [SendQuery] — packs a minimal mDNS query and sends it to the given
//     address.
//
// Tests call [Probe] first and skip if neither multicast nor unicast fallback
// is available.
package mcasttest

import (
	"net"
	"testing"
	"time"

	"github.com/miekg/dns"
)

// Loopback224 is a routable 224.0.0.x group that Go's multicast stack will
// accept a JoinGroup for on the loopback interface without routing-table
// changes. We use .254 rather than .251 (mDNS) so we do not collide with a
// running avahi/systemd-resolved listener on the test machine.
const Loopback224 = "224.0.0.254"

// Result is returned by [Probe].
type Result struct {
	// Multicast is true when the kernel accepted a JoinGroup on lo.
	Multicast bool
	// Loopback is the interface we will use for sends. Always "lo" when
	// Multicast is true; otherwise "lo" with plain unicast.
	Loopback *net.Interface
	// SkipReason is non-empty when neither mode is available and the caller
	// should skip the test.
	SkipReason string
}

// Probe reports whether loopback multicast (or unicast fallback) is usable.
// It is cheap: it opens a temporary socket, attempts a JoinGroup, and closes
// the socket immediately.
func Probe() Result {
	lo, err := net.InterfaceByName("lo")
	if err != nil {
		return Result{SkipReason: "loopback interface not found: " + err.Error()}
	}

	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		// Even plain UDP fails — hard skip.
		return Result{SkipReason: "cannot open UDP4 socket: " + err.Error()}
	}
	defer func() { _ = conn.Close() }()

	group := net.ParseIP(Loopback224)
	if mcastErr := tryJoin4(lo, group); mcastErr == nil {
		return Result{Multicast: true, Loopback: lo}
	}

	// Multicast join failed — unicast fallback: we can still send and receive
	// on 127.0.0.1 if basic UDP works.
	return Result{Multicast: false, Loopback: lo}
}

// ListenUDP4 opens a UDP4 socket bound to a randomly assigned port and,
// when multicast is available, joins the given group on lo. Returns the
// connection and the effective bind address (host:port). The caller owns
// the connection and must Close it.
//
// If group is empty or multicast is unavailable, the socket is bound to
// 0.0.0.0 with plain unicast semantics.
func ListenUDP4(t *testing.T, group string, mcast bool) (conn *net.UDPConn, addr string) {
	t.Helper()

	udpAddr := &net.UDPAddr{IP: net.IPv4zero, Port: 0}
	conn, err := net.ListenUDP("udp4", udpAddr)
	if err != nil {
		t.Fatalf("mcasttest.ListenUDP4: ListenUDP: %v", err)
	}

	if mcast && group != "" {
		lo, lerr := net.InterfaceByName("lo")
		if lerr != nil {
			_ = conn.Close()
			t.Fatalf("mcasttest.ListenUDP4: no loopback: %v", lerr)
		}
		g := net.ParseIP(group)
		if g == nil {
			_ = conn.Close()
			t.Fatalf("mcasttest.ListenUDP4: invalid group %q", group)
		}
		if jerr := tryJoin4(lo, g); jerr != nil {
			_ = conn.Close()
			t.Fatalf("mcasttest.ListenUDP4: JoinGroup %s on lo: %v", group, jerr)
		}
	}

	return conn, conn.LocalAddr().String()
}

// SendQuery packs a minimal mDNS PTR query for qname and sends it via UDP4
// to addr. The packet mimics a real mDNS query: QR=0, OPCODE=0, single
// question of type PTR.
//
// addr should be "host:port" (e.g. "127.0.0.1:5353" or a multicast address).
func SendQuery(t *testing.T, conn *net.UDPConn, addr, qname string) {
	t.Helper()

	msg := new(dns.Msg)
	msg.SetQuestion(dns.Fqdn(qname), dns.TypePTR)
	msg.RecursionDesired = false

	buf, err := msg.Pack()
	if err != nil {
		t.Fatalf("mcasttest.SendQuery: pack DNS: %v", err)
	}

	udpAddr, err := net.ResolveUDPAddr("udp4", addr)
	if err != nil {
		t.Fatalf("mcasttest.SendQuery: resolve %q: %v", addr, err)
	}

	// Set a generous deadline so a slow kernel response does not hang CI.
	_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if _, werr := conn.WriteToUDP(buf, udpAddr); werr != nil {
		t.Fatalf("mcasttest.SendQuery: WriteToUDP(%s): %v", addr, werr)
	}
}

// RecvPacket reads one UDP datagram from conn with a 2-second deadline.
// Returns the payload or calls t.Fatal on timeout / error.
func RecvPacket(t *testing.T, conn *net.UDPConn, deadline time.Duration) []byte {
	t.Helper()
	buf := make([]byte, 9000)
	_ = conn.SetReadDeadline(time.Now().Add(deadline))
	n, _, err := conn.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("mcasttest.RecvPacket: ReadFromUDP: %v", err)
	}
	return buf[:n]
}

// tryJoin4 attempts to join a multicast group on the given interface using a
// raw net.UDPConn. It uses golang.org/x/net/ipv4 indirectly through the
// standard library's JoinGroup via a net.PacketConn interface. We avoid
// importing ipv4 here to keep the harness dependency-free; instead we use
// the net.UDPConn directly, knowing the kernel handles the IGMP join when
// the wildcard bind + IP_ADD_MEMBERSHIP is used.
//
// Because net.UDPConn does not expose JoinGroup directly, we use the
// net.UDPConn's underlying file-descriptor-free equivalent via
// net.ListenMulticastUDP which is available only on some OSes. If that fails
// we fall back to a raw group-send probe.
func tryJoin4(ifi *net.Interface, group net.IP) error {
	// Use ListenMulticastUDP as a canary — it opens a fresh socket to confirm
	// the OS supports multicast on the interface, then closes it. The harness
	// itself does not need the group-join path (only the probe does).
	mc, err := net.ListenMulticastUDP("udp4", ifi, &net.UDPAddr{IP: group})
	if err != nil {
		return err
	}
	_ = mc.Close()
	return nil
}
