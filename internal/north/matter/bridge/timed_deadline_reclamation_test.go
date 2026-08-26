// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package bridge

// White-box tests for the reclamation of [exchangeRouting.timedDeadlines].
// A TimedRequest whose follow-up Write / Invoke never arrives — the
// datagram is lost on Wi-Fi, the controller app is backgrounded, the
// controller reboots — leaves the deadline behind. Consumption alone
// therefore cannot bound the table: without the expiry sweep and the
// session-teardown drop the map grows for the daemon's whole uptime,
// and a peer that opens exchanges and stops after the TimedRequest can
// drive it there deliberately.

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/internal/north/matter/mdns"
	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/message"
	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/mrp"
)

// timedDeadlineCount reports how many entries the table currently holds.
func timedDeadlineCount(r *exchangeRouting) int {
	n := 0
	r.timedDeadlines.Range(func(_, _ any) bool {
		n++
		return true
	})
	return n
}

// hasTimedDeadline reports whether the table holds an entry for key.
func hasTimedDeadline(r *exchangeRouting, key timedKey) bool {
	_, ok := r.timedDeadlines.Load(key)
	return ok
}

// TestDispatchTimedRequest_ReclaimsExpiredDeadlines drives a real
// TimedRequest datagram through [Bridge.dispatch] and asserts the
// registration path reclaims deadlines that can no longer be consumed.
// The abandoned entries of aborted interactions are the ordinary case;
// only an expiry pass ever removes them.
func TestDispatchTimedRequest_ReclaimsExpiredDeadlines(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	b.AttachAckTracker(mrp.NewAckTracker(50 * time.Millisecond))

	abandoned := timedKey{sessionID: 11, exchangeID: 1}
	inFlight := timedKey{sessionID: 12, exchangeID: 2}
	b.routing.timedDeadlines.Store(abandoned, time.Now().Add(-time.Minute))
	b.routing.timedDeadlines.Store(inFlight, time.Now().Add(30*time.Second))

	peerConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	t.Cleanup(func() { _ = peerConn.Close() })
	peerAddr, ok := peerConn.LocalAddr().(*net.UDPAddr)
	if !ok {
		t.Fatalf("unexpected peer addr type %T", peerConn.LocalAddr())
	}

	buf := buildDatagram(
		buildHeader(0, 32),
		buildProtocolHeader(im.InteractionModelProtocolID, im.OpcodeTimedRequest),
		encodeReliabilityTestTimedRequest(t, 5000),
	)
	if err := b.dispatch(context.Background(), buf, peerAddr); err != nil {
		t.Fatalf("dispatch(TimedRequest): %v", err)
	}

	if hasTimedDeadline(&b.routing, abandoned) {
		t.Error("expired deadline of an aborted interaction survived the sweep")
	}
	if !hasTimedDeadline(&b.routing, inFlight) {
		t.Error("live deadline was swept — a legitimate timed Write/Invoke would be rejected with TIMEOUT")
	}
	// The dispatched request must still have registered its own
	// deadline: the sweep may never eat the entry it was called for.
	if got := timedDeadlineCount(&b.routing); got != 2 {
		t.Errorf("timedDeadlines holds %d entries, want 2 (the live one + the freshly dispatched one)", got)
	}
}

// TestBridgeStart_AckPumpReclaimsExpiredTimedDeadlines pins the idle
// path: a peer that stops sending altogether never triggers the
// registration-site sweep, so the periodic pump the bridge spawns in
// [Bridge.Start] has to do it.
func TestBridgeStart_AckPumpReclaimsExpiredTimedDeadlines(t *testing.T) {
	t.Parallel()
	b, err := New(
		NewFakeStore(),
		wbEmptySnapshotter,
		mdns.NewNoop(),
		Config{
			Listen:    ":0",
			VendorID:  0x1234,
			ProductID: 0x5678,
			NodeLabel: "timed-sweep-test",
		},
		nil,
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// The pump goroutine only runs when a tracker is wired before Start,
	// exactly as the daemon wires it.
	b.AttachAckTracker(mrp.NewAckTracker(50 * time.Millisecond))
	abandoned := timedKey{sessionID: 21, exchangeID: 3}
	b.routing.timedDeadlines.Store(abandoned, time.Now().Add(-time.Minute))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := b.Start(ctx); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer stopCancel()
		_ = b.Stop(stopCtx)
	})

	deadline := time.Now().Add(3 * time.Second)
	for hasTimedDeadline(&b.routing, abandoned) {
		if time.Now().After(deadline) {
			t.Fatal("expired deadline still present — the bridge never sweeps while idle")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestInboundCloseSessionStatusReport_DropsTimedDeadlines pins the
// teardown half: every exchange dies with its session, so the deadlines
// it registered can never be consumed. Without the drop a peer that
// rotates sessions accumulates entries far faster than the expiry sweep
// reclaims them.
func TestInboundCloseSessionStatusReport_DropsTimedDeadlines(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	b.AttachSessionRegistry(&fakeSessionRegistry{})

	closing := timedKey{sessionID: 9, exchangeID: 4}
	surviving := timedKey{sessionID: 10, exchangeID: 4}
	b.routing.timedDeadlines.Store(closing, time.Now().Add(30*time.Second))
	b.routing.timedDeadlines.Store(surviving, time.Now().Add(30*time.Second))

	hdr := &message.Header{SessionID: 9, MessageCounter: 1}
	proto := scProto(mrp.SCOpcodeStatusReport, 5, false, 0)
	if err := b.dispatchSecureChannel(loopbackSrc(), hdr, proto, closeSessionStatusReportBody()); err != nil {
		t.Fatalf("dispatchSecureChannel(CloseSession): %v", err)
	}

	if hasTimedDeadline(&b.routing, closing) {
		t.Error("closed session's timed deadline survived the teardown")
	}
	if !hasTimedDeadline(&b.routing, surviving) {
		t.Error("another session's timed deadline was dropped — the drop must be session-scoped")
	}
}
