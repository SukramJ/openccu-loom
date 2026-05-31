// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mcasttest_test

import (
	"net"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/tests/harness/mcasttest"
)

// TestProbe_ReturnsResult verifies that Probe always returns a Result
// (not a zero value) and that SkipReason is set only when neither
// multicast nor unicast is available.
func TestProbe_ReturnsResult(t *testing.T) {
	t.Parallel()
	r := mcasttest.Probe()
	// If a skip reason is set, neither Multicast nor Loopback is valid.
	if r.SkipReason != "" {
		t.Logf("Probe returned SkipReason=%q — skipping mcast tests on this host", r.SkipReason)
		return
	}
	if r.Loopback == nil {
		t.Fatal("Probe: Loopback interface must not be nil when SkipReason is empty")
	}
}

// TestListenUDP4_OpenCloseUnicast verifies that ListenUDP4 with
// mcast=false opens a valid socket and that the returned addr is a
// non-empty "host:port" string.
func TestListenUDP4_OpenCloseUnicast(t *testing.T) {
	t.Parallel()
	conn, addr := mcasttest.ListenUDP4(t, "", false)
	defer conn.Close()
	if addr == "" {
		t.Fatal("addr must not be empty")
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("SplitHostPort(%q): %v", addr, err)
	}
	if host == "" {
		t.Fatalf("host part of addr is empty: %q", addr)
	}
	if port == "0" || port == "" {
		t.Fatalf("port must be non-zero (got %q)", port)
	}
}

// TestSendRecv_UnicastLoopback verifies the full send→receive round-trip
// on loopback UDP without multicast.  Uses SendQuery + RecvPacket.
func TestSendRecv_UnicastLoopback(t *testing.T) {
	t.Parallel()

	// Receiver socket.
	recv, recvAddr := mcasttest.ListenUDP4(t, "", false)
	defer recv.Close()

	// Sender socket.
	send, _ := mcasttest.ListenUDP4(t, "", false)
	defer send.Close()

	// Build a reachable unicast target — replace the 0.0.0.0 bind host
	// with the loopback address so the packet goes where the socket can
	// receive it.
	_, port, err := net.SplitHostPort(recvAddr)
	if err != nil {
		t.Fatalf("SplitHostPort: %v", err)
	}
	target := "127.0.0.1:" + port

	mcasttest.SendQuery(t, send, target, "_openccu-loom._tcp.local")
	pkt := mcasttest.RecvPacket(t, recv, 2*time.Second)
	if len(pkt) == 0 {
		t.Fatal("RecvPacket returned empty payload")
	}
}

// TestListenUDP4_MulticastJoinsGroup verifies that ListenUDP4 with
// mcast=true+group succeeds (or skips when multicast is unavailable).
// It does not attempt a send/recv round-trip through the multicast
// address because that requires IP_MULTICAST_LOOP to be set on the
// sending socket, which is OS/container-dependent.  The test here just
// exercises the join path.
func TestListenUDP4_MulticastJoinsGroup(t *testing.T) {
	t.Parallel()

	r := mcasttest.Probe()
	if r.SkipReason != "" {
		t.Skip(r.SkipReason)
	}
	if !r.Multicast {
		t.Skip("multicast not available on loopback (unicast-only fallback)")
	}

	// A successful return (no t.Fatal) means the join succeeded.
	conn, addr := mcasttest.ListenUDP4(t, mcasttest.Loopback224, true)
	defer conn.Close()
	if addr == "" {
		t.Fatal("ListenUDP4 returned empty addr")
	}
}

// TestSendQuery_ValidDNSPayload verifies that a packet sent by SendQuery
// is a parseable DNS message (at a minimum the 12-byte header is present).
func TestSendQuery_ValidDNSPayload(t *testing.T) {
	t.Parallel()

	recv, recvAddr := mcasttest.ListenUDP4(t, "", false)
	defer recv.Close()

	send, _ := mcasttest.ListenUDP4(t, "", false)
	defer send.Close()

	_, port, err := net.SplitHostPort(recvAddr)
	if err != nil {
		t.Fatalf("SplitHostPort: %v", err)
	}
	target := "127.0.0.1:" + port

	mcasttest.SendQuery(t, send, target, "_matter._tcp.local")
	pkt := mcasttest.RecvPacket(t, recv, 2*time.Second)
	// DNS header is 12 bytes; any valid DNS packet must be at least that.
	if len(pkt) < 12 {
		t.Fatalf("received packet too short (%d bytes) to be a valid DNS message", len(pkt))
	}
}

// TestProbe_LoopbackNilSafe verifies that Probe handles a missing
// "lo" interface gracefully (some CI environments rename the loopback
// to "lo0" or similar). We cannot force a failure of InterfaceByName,
// but we can at least verify the code path returns a non-panicking Result.
func TestProbe_LoopbackNilSafe(t *testing.T) {
	t.Parallel()
	r := mcasttest.Probe()
	// Either we have a loopback or a skip reason — never both empty.
	if r.SkipReason == "" && r.Loopback == nil {
		t.Fatal("Probe: either Loopback or SkipReason must be set")
	}
	// SkipReason must not contain a bare newline (sanity).
	if strings.ContainsRune(r.SkipReason, '\n') {
		t.Fatalf("SkipReason must not contain a newline: %q", r.SkipReason)
	}
}
