// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package bridge

// White-box tests for the AckPump subsystem:
// owedInboundAck, dischargeOwedAck, emitStandaloneAck, RunAckPumpOnce,
// and AttachAckTracker.
// Lives in package bridge (not bridge_test) to access unexported methods.
// Helpers from receive_test.go (newStartedBridge, loopbackSrc) are
// available because they share the same compilation unit.

import (
	"net"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/message"
	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/mrp"
)

// buildNeedsAckProto builds a ProtocolHeader for the SecureChannel
// protocol with NeedsAck=true so owedInboundAck records an obligation.
func buildNeedsAckProto(exchangeID uint16, msgCounter uint32) message.ProtocolHeader {
	return message.ProtocolHeader{
		ProtocolID: mrp.SecureChannelProtocolID,
		Opcode:     mrp.SCOpcodePake1,
		ExchangeID: exchangeID,
		NeedsAck:   true,
	}
}

// buildMsgHdr builds a minimal message.Header with the given counter.
func buildMsgHdr(counter uint32) *message.Header {
	return &message.Header{SessionID: 0, MessageCounter: counter}
}

// openPeerSocket opens a real loopback UDP socket and returns it plus its
// address. The caller owns the socket and must close it after the test.
func openPeerSocket(t *testing.T) (*net.UDPConn, *net.UDPAddr) {
	t.Helper()
	conn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("openPeerSocket: ListenUDP: %v", err)
	}
	addr, ok := conn.LocalAddr().(*net.UDPAddr)
	if !ok {
		conn.Close()
		t.Fatalf("openPeerSocket: unexpected addr type %T", conn.LocalAddr())
	}
	return conn, addr
}

// TestAckPump_NoTrackerNoOp verifies that without AttachAckTracker,
// owedInboundAck does not panic and RunAckPumpOnce returns 0.
func TestAckPump_NoTrackerNoOp(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	// No AttachAckTracker call — tracker is nil.
	proto := buildNeedsAckProto(42, 1)
	hdr := buildMsgHdr(1)
	src := loopbackSrc()
	// Must not panic.
	b.owedInboundAck(src, hdr, proto)
	// No tracker → nothing due.
	if n := b.RunAckPumpOnce(time.Now()); n != 0 {
		t.Errorf("RunAckPumpOnce without tracker: want 0, got %d", n)
	}
}

// TestAckPump_OweAndEmit verifies that after wiring a tracker (delay=0),
// owedInboundAck records an obligation and RunAckPumpOnce emits exactly
// one StandaloneAck datagram to the peer socket.
func TestAckPump_OweAndEmit(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	tracker := mrp.NewAckTracker(0) // delay=0 → immediately due
	b.AttachAckTracker(tracker)

	// Open a peer socket that will receive the StandaloneAck.
	peerConn, peerAddr := openPeerSocket(t)
	defer peerConn.Close()

	const (
		exchangeID uint16 = 7
		msgCounter uint32 = 0xABCD
	)
	proto := buildNeedsAckProto(exchangeID, msgCounter)
	hdr := buildMsgHdr(msgCounter)

	b.owedInboundAck(peerAddr, hdr, proto)

	n := b.RunAckPumpOnce(time.Now())
	if n != 1 {
		t.Fatalf("RunAckPumpOnce: want 1, got %d", n)
	}

	// Receive the datagram from the peer socket.
	if err := peerConn.SetReadDeadline(time.Now().Add(200 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	buf := make([]byte, 512)
	nRead, _, err := peerConn.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("ReadFromUDP: %v (no StandaloneAck datagram received)", err)
	}
	datagram := buf[:nRead]

	// Parse the Message Header.
	rxHdr, hdrLen, err := message.UnmarshalHeader(datagram)
	if err != nil {
		t.Fatalf("UnmarshalHeader: %v", err)
	}
	if rxHdr.SessionID != 0 {
		t.Errorf("SessionID = %d, want 0", rxHdr.SessionID)
	}

	// Parse the Protocol Header.
	rxProto, _, err := message.UnmarshalProtocolHeader(datagram[hdrLen:])
	if err != nil {
		t.Fatalf("UnmarshalProtocolHeader: %v", err)
	}
	if rxProto.Opcode != mrp.StandaloneAckOpcode {
		t.Errorf("Opcode = 0x%02X, want StandaloneAckOpcode (0x%02X)", rxProto.Opcode, mrp.StandaloneAckOpcode)
	}
	if rxProto.ProtocolID != mrp.SecureChannelProtocolID {
		t.Errorf("ProtocolID = 0x%04X, want SecureChannelProtocolID (0x%04X)", rxProto.ProtocolID, mrp.SecureChannelProtocolID)
	}
	if !rxProto.HasAck {
		t.Error("HasAck = false, want true")
	}
	if rxProto.AckCounter != msgCounter {
		t.Errorf("AckCounter = %d, want %d", rxProto.AckCounter, msgCounter)
	}
}

// TestAckPump_EchoesPeerSourceNodeID verifies that when the inbound
// reliable message carried HasSourceNodeID=true, the synthesised
// StandaloneAck echoes the value as DestNodeID (Matter §4.4.1.2 —
// chip-tool's commissioner rejects unsecured replies that omit the
// echo). Same rule sendReply enforces for piggybacked ACKs.
func TestAckPump_EchoesPeerSourceNodeID(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	tracker := mrp.NewAckTracker(0)
	b.AttachAckTracker(tracker)

	peerConn, peerAddr := openPeerSocket(t)
	defer peerConn.Close()

	const (
		exchangeID uint16 = 21
		msgCounter uint32 = 0xDEAD
		peerNodeID uint64 = 0xE6834AF097E578C1 // chip-tool-style ephemeral
	)
	proto := buildNeedsAckProto(exchangeID, msgCounter)
	hdr := &message.Header{
		SessionID:       0,
		MessageCounter:  msgCounter,
		HasSourceNodeID: true,
		SourceNodeID:    peerNodeID,
	}

	b.owedInboundAck(peerAddr, hdr, proto)
	if n := b.RunAckPumpOnce(time.Now()); n != 1 {
		t.Fatalf("RunAckPumpOnce: want 1, got %d", n)
	}

	if err := peerConn.SetReadDeadline(time.Now().Add(200 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	buf := make([]byte, 512)
	nRead, _, err := peerConn.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("ReadFromUDP: %v", err)
	}
	rxHdr, _, err := message.UnmarshalHeader(buf[:nRead])
	if err != nil {
		t.Fatalf("UnmarshalHeader: %v", err)
	}
	if rxHdr.DestSize != message.DestNodeID {
		t.Errorf("DestSize = %d, want DestNodeID(%d)", rxHdr.DestSize, message.DestNodeID)
	}
	if rxHdr.DestNodeID != peerNodeID {
		t.Errorf("DestNodeID = 0x%X, want 0x%X", rxHdr.DestNodeID, peerNodeID)
	}
}

// TestAckPump_NoEchoWhenPeerHadNoSourceNodeID verifies that absent
// HasSourceNodeID on the inbound message, the StandaloneAck stays
// bare-header (DestSize=DestNone). Matches the sendReply behaviour
// for plain unsecured replies.
func TestAckPump_NoEchoWhenPeerHadNoSourceNodeID(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	tracker := mrp.NewAckTracker(0)
	b.AttachAckTracker(tracker)

	peerConn, peerAddr := openPeerSocket(t)
	defer peerConn.Close()

	const (
		exchangeID uint16 = 22
		msgCounter uint32 = 0xBEEF
	)
	proto := buildNeedsAckProto(exchangeID, msgCounter)
	hdr := buildMsgHdr(msgCounter)

	b.owedInboundAck(peerAddr, hdr, proto)
	if n := b.RunAckPumpOnce(time.Now()); n != 1 {
		t.Fatalf("RunAckPumpOnce: want 1, got %d", n)
	}

	if err := peerConn.SetReadDeadline(time.Now().Add(200 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	buf := make([]byte, 512)
	nRead, _, err := peerConn.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("ReadFromUDP: %v", err)
	}
	rxHdr, _, err := message.UnmarshalHeader(buf[:nRead])
	if err != nil {
		t.Fatalf("UnmarshalHeader: %v", err)
	}
	if rxHdr.DestSize != message.DestNone {
		t.Errorf("DestSize = %d, want DestNone(%d)", rxHdr.DestSize, message.DestNone)
	}
}

// TestAckPump_Discharge verifies that after dischargeOwedAck removes an
// obligation, RunAckPumpOnce returns 0 (nothing to emit).
func TestAckPump_Discharge(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	tracker := mrp.NewAckTracker(0)
	b.AttachAckTracker(tracker)

	peerConn, peerAddr := openPeerSocket(t)
	defer peerConn.Close()

	const (
		exchangeID uint16 = 11
		msgCounter uint32 = 99
	)
	proto := buildNeedsAckProto(exchangeID, msgCounter)
	hdr := buildMsgHdr(msgCounter)

	b.owedInboundAck(peerAddr, hdr, proto)
	// Discharge before pump fires — simulates a piggybacked ACK on a reply.
	// buildMsgHdr always sets SessionID 0, matching owedInboundAck's key.
	b.dischargeOwedAck(0, exchangeID, true)

	if n := b.RunAckPumpOnce(time.Now()); n != 0 {
		t.Errorf("RunAckPumpOnce after Discharge: want 0, got %d", n)
	}
}

// TestAckPump_SessionScopedExchangeCollision verifies that two inbound
// reliable messages carrying different SessionIDs on the SAME ExchangeID
// are tracked as independent obligations, and that dischargeOwedAck for one
// session leaves the other session's obligation AND reply-target intact.
// Exchange IDs are picked independently by every peer, so two controllers
// (or an old and a new CASE session of the same controller) sharing an
// exchange ID is a real scenario, not hypothetical — mirrors matter.js
// ExchangeManager.ts:287 (an exchange lookup is invalidated as soon as the
// session no longer matches).
func TestAckPump_SessionScopedExchangeCollision(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	tracker := mrp.NewAckTracker(0)
	b.AttachAckTracker(tracker)

	// Session 0 (unsecured/PASE pre-fabric) — this is the survivor and the
	// only one that can actually round-trip over UDP without a wired
	// operational session, so it doubles as proof-of-emission.
	peerSurvivor, addrSurvivor := openPeerSocket(t)
	defer peerSurvivor.Close()
	// Session 99 — discharged before the pump runs; its socket must never
	// see a datagram.
	peerDischarged, addrDischarged := openPeerSocket(t)
	defer peerDischarged.Close()

	const (
		exchangeID       uint16 = 30
		survivorSession  uint16 = 0
		dischargeSession uint16 = 99
		survivorCounter  uint32 = 0x1111
		dischargeCounter uint32 = 0x2222
	)

	protoSurvivor := buildNeedsAckProto(exchangeID, survivorCounter)
	hdrSurvivor := buildMsgHdr(survivorCounter) // SessionID 0
	protoDischarged := buildNeedsAckProto(exchangeID, dischargeCounter)
	hdrDischarged := &message.Header{SessionID: dischargeSession, MessageCounter: dischargeCounter}

	b.owedInboundAck(addrSurvivor, hdrSurvivor, protoSurvivor)
	b.owedInboundAck(addrDischarged, hdrDischarged, protoDischarged)

	if got := tracker.Pending(); got != 2 {
		t.Fatalf("Pending() = %d after two sessions on the same exchange, want 2", got)
	}

	// Discharge session 99's obligation — session 0's must survive intact.
	b.dischargeOwedAck(dischargeSession, exchangeID, true)
	if got := tracker.Pending(); got != 1 {
		t.Fatalf("Pending() = %d after discharging session %d, want 1 (session %d must survive)", got, dischargeSession, survivorSession)
	}

	n := b.RunAckPumpOnce(time.Now())
	if n != 1 {
		t.Fatalf("RunAckPumpOnce: want 1 (only the surviving session's obligation), got %d", n)
	}

	// The discharged session's peer must never receive a datagram.
	if err := peerDischarged.SetReadDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	buf := make([]byte, 512)
	if _, _, err := peerDischarged.ReadFromUDP(buf); err == nil {
		t.Error("discharged session's peer socket received a datagram; its reply target should be gone")
	}

	// The surviving session's peer must receive its own StandaloneAck,
	// carrying its own counter — not the discharged session's.
	if err := peerSurvivor.SetReadDeadline(time.Now().Add(200 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	nRead, _, err := peerSurvivor.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("ReadFromUDP (surviving session): %v", err)
	}
	rxHdr, hdrLen, err := message.UnmarshalHeader(buf[:nRead])
	if err != nil {
		t.Fatalf("UnmarshalHeader: %v", err)
	}
	if rxHdr.SessionID != survivorSession {
		t.Errorf("SessionID = %d, want %d", rxHdr.SessionID, survivorSession)
	}
	rxProto, _, err := message.UnmarshalProtocolHeader(buf[hdrLen:nRead])
	if err != nil {
		t.Fatalf("UnmarshalProtocolHeader: %v", err)
	}
	if rxProto.AckCounter != survivorCounter {
		t.Errorf("AckCounter = 0x%X, want 0x%X (surviving session's own counter)", rxProto.AckCounter, survivorCounter)
	}
}

// TestAckPumpStandaloneAckCarriesTheBridgeExchangeRole pins the
// Initiator flag of a synthesised StandaloneAck to the bridge's role on
// the exchange, which is the inverse of the inbound message's flag. The
// ongoing-subscription path opens its own exchange
// ([Bridge.sendInitiatedReport]), so the controller's IM StatusResponse
// arrives with Initiator=false and the ack the pump answers with must
// carry Initiator=true. An ack whose flag does not invert the peer's is
// discarded as unsolicited — chip's ExchangeContext::MatchExchange
// requires `payloadHeader.IsInitiator() != IsInitiator()`
// (src/messaging/ExchangeContext.cpp:384) and matter.js applies the
// same rule (packages/protocol/src/protocol/ExchangeManager.ts:319) —
// leaving the StatusResponse unacked until the peer's retransmit cap
// fires. matter.js stamps every message it sends, standalone acks
// included, with the exchange's own role
// (packages/protocol/src/protocol/MessageExchange.ts:738).
func TestAckPumpStandaloneAckCarriesTheBridgeExchangeRole(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name          string
		peerInitiator bool
		wantInitiator bool
	}{
		{
			name:          "peer opened the exchange",
			peerInitiator: true,
			wantInitiator: false,
		},
		{
			name:          "bridge opened the exchange",
			peerInitiator: false,
			wantInitiator: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			b := newStartedBridge(t)
			b.AttachAckTracker(mrp.NewAckTracker(0))

			peerConn, peerAddr := openPeerSocket(t)
			defer peerConn.Close()

			const (
				exchangeID uint16 = 64
				msgCounter uint32 = 0x4242
			)
			proto := buildNeedsAckProto(exchangeID, msgCounter)
			proto.Initiator = tc.peerInitiator
			b.owedInboundAck(peerAddr, buildMsgHdr(msgCounter), proto)
			if n := b.RunAckPumpOnce(time.Now()); n != 1 {
				t.Fatalf("RunAckPumpOnce: want 1, got %d", n)
			}

			if err := peerConn.SetReadDeadline(time.Now().Add(200 * time.Millisecond)); err != nil {
				t.Fatalf("SetReadDeadline: %v", err)
			}
			buf := make([]byte, 512)
			nRead, _, err := peerConn.ReadFromUDP(buf)
			if err != nil {
				t.Fatalf("ReadFromUDP: %v", err)
			}
			_, hdrLen, err := message.UnmarshalHeader(buf[:nRead])
			if err != nil {
				t.Fatalf("UnmarshalHeader: %v", err)
			}
			rxProto, _, err := message.UnmarshalProtocolHeader(buf[hdrLen:nRead])
			if err != nil {
				t.Fatalf("UnmarshalProtocolHeader: %v", err)
			}
			if rxProto.Initiator != tc.wantInitiator {
				t.Errorf("StandaloneAck Initiator = %v, want %v (inbound Initiator=%v)",
					rxProto.Initiator, tc.wantInitiator, tc.peerInitiator)
			}
		})
	}
}

// TestAckPump_MultipleExchanges verifies that two distinct obligations
// produce two emissions from RunAckPumpOnce.
func TestAckPump_MultipleExchanges(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	tracker := mrp.NewAckTracker(0)
	b.AttachAckTracker(tracker)

	peerConn, peerAddr := openPeerSocket(t)
	defer peerConn.Close()

	for i, exchangeID := range []uint16{1, 2} {
		proto := buildNeedsAckProto(exchangeID, uint32(100+i))
		hdr := buildMsgHdr(uint32(100 + i))
		b.owedInboundAck(peerAddr, hdr, proto)
	}

	if n := b.RunAckPumpOnce(time.Now()); n != 2 {
		t.Errorf("RunAckPumpOnce with two exchanges: want 2, got %d", n)
	}
}

// TestAckPump_NoSrcDropsObligation verifies that an obligation added
// directly to the tracker (without a src in exchangeSrcs) is drained by
// RunAckPumpOnce but no UDP datagram is sent.
func TestAckPump_NoSrcDropsObligation(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	tracker := mrp.NewAckTracker(0)
	b.AttachAckTracker(tracker)

	// Open a peer socket purely to detect any unexpected datagrams.
	peerConn, _ := openPeerSocket(t)
	defer peerConn.Close()

	// Add an obligation directly to the tracker — no src in exchangeSrcs.
	const (
		exchangeID uint16 = 77
		msgCounter uint32 = 1234
	)
	tracker.Owe(msgCounter, 0, exchangeID, false, time.Now())

	// Pump should drain the obligation (returns 1) but not send to any peer.
	n := b.RunAckPumpOnce(time.Now())
	if n != 1 {
		t.Errorf("RunAckPumpOnce: want 1 (drained), got %d", n)
	}

	// Verify no datagram arrives at the peer socket.
	if err := peerConn.SetReadDeadline(time.Now().Add(50 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	buf := make([]byte, 512)
	_, _, err := peerConn.ReadFromUDP(buf)
	if err == nil {
		t.Error("unexpected datagram received; emitStandaloneAck should have dropped for missing src")
	}
	// A deadline-exceeded or "i/o timeout" error is the expected outcome.
}

// TestTickOutboundReliable_NilListener verifies that tickOutboundReliable
// is a no-op when the bridge's listener is nil (not yet started).
func TestTickOutboundReliable_NilListener(t *testing.T) {
	t.Parallel()
	// Build an unstarted bridge — listener is nil.
	b, err := New(
		NewFakeStore(),
		wbEmptySnapshotter,
		nil,
		Config{
			Listen:    ":0",
			VendorID:  0x1234,
			ProductID: 0x5678,
			NodeLabel: "wb-test",
		},
		nil,
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	// Attach the outbound reliable tracker via AttachAckTracker which
	// also creates the outboundReliable field inside AttachAckTracker.
	tracker := mrp.NewAckTracker(0)
	b.AttachAckTracker(tracker)

	// Direct call to tickOutboundReliable with nil listener must not panic.
	if b.outboundReliable != nil {
		b.tickOutboundReliable(b.outboundReliable, time.Now())
	}
	// Nothing to assert beyond no panic.
}

// TestAckPump_ExpediteDuplicateAck verifies that expediteDuplicateAck makes
// a just-registered obligation immediately due, so the following
// RunAckPumpOnce call emits its StandaloneAck without waiting out the
// piggyback grace window. The tracker is wired with
// [mrp.DefaultStandaloneAckDelay] (not 0) so the negative control is
// meaningful: a zero-delay fixture would make every obligation immediately
// due regardless of expediting, masking a regression of the underlying fix.
// Mirrors matter.js MessageExchange.ts:428-433 (duplicate + requiresAck →
// sendStandaloneAckForMessage immediately).
func TestAckPump_ExpediteDuplicateAck(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	tracker := mrp.NewAckTracker(mrp.DefaultStandaloneAckDelay)
	b.AttachAckTracker(tracker)

	peerConn, peerAddr := openPeerSocket(t)
	defer peerConn.Close()

	const (
		exchangeID uint16 = 88
		msgCounter uint32 = 0x4242
	)
	proto := buildNeedsAckProto(exchangeID, msgCounter)
	hdr := buildMsgHdr(msgCounter)

	b.owedInboundAck(peerAddr, hdr, proto)

	// Negative control: the grace window has not elapsed, so an immediate
	// pump pass must emit nothing.
	if n := b.RunAckPumpOnce(time.Now()); n != 0 {
		t.Fatalf("RunAckPumpOnce before expedite: want 0 (grace window not elapsed), got %d", n)
	}

	// Simulate the duplicate-retransmit path: expedite, then pump.
	b.expediteDuplicateAck(0, exchangeID, true)
	if n := b.RunAckPumpOnce(time.Now()); n != 1 {
		t.Fatalf("RunAckPumpOnce after expedite: want 1, got %d", n)
	}

	if err := peerConn.SetReadDeadline(time.Now().Add(200 * time.Millisecond)); err != nil {
		t.Fatalf("SetReadDeadline: %v", err)
	}
	buf := make([]byte, 512)
	nRead, _, err := peerConn.ReadFromUDP(buf)
	if err != nil {
		t.Fatalf("ReadFromUDP: %v (no StandaloneAck datagram received)", err)
	}
	_, hdrLen, err := message.UnmarshalHeader(buf[:nRead])
	if err != nil {
		t.Fatalf("UnmarshalHeader: %v", err)
	}
	rxProto, _, err := message.UnmarshalProtocolHeader(buf[hdrLen:nRead])
	if err != nil {
		t.Fatalf("UnmarshalProtocolHeader: %v", err)
	}
	if rxProto.Opcode != mrp.StandaloneAckOpcode {
		t.Errorf("Opcode = 0x%02X, want StandaloneAckOpcode (0x%02X)", rxProto.Opcode, mrp.StandaloneAckOpcode)
	}
	if rxProto.AckCounter != msgCounter {
		t.Errorf("AckCounter = %d, want %d", rxProto.AckCounter, msgCounter)
	}
}

// TestAckPump_ExpediteDuplicateAck_UnknownObligationNoOp verifies that
// expediteDuplicateAck is a no-op (no panic, nothing emitted) when called
// for a (session, exchange) pair with no pending obligation — the shape a
// duplicate arriving without a prior owedInboundAck registration would take.
func TestAckPump_ExpediteDuplicateAck_UnknownObligationNoOp(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	tracker := mrp.NewAckTracker(mrp.DefaultStandaloneAckDelay)
	b.AttachAckTracker(tracker)

	b.expediteDuplicateAck(0, 4242, true)
	if n := b.RunAckPumpOnce(time.Now()); n != 0 {
		t.Errorf("RunAckPumpOnce after expediting an unknown obligation: want 0, got %d", n)
	}
}

// TestAckPump_AttachAckTrackerAlsoSetsAckHandler verifies that after
// AttachAckTracker, the bridge's AckHandler (wired via AttachAckHandler
// internally) discharges through the same tracker when dispatchSecureChannel
// receives a datagram with HasAck=true.
func TestAckPump_AttachAckTrackerAlsoSetsAckHandler(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	tracker := mrp.NewAckTracker(0)
	b.AttachAckTracker(tracker)

	const (
		exchangeID uint16 = 55
		msgCounter uint32 = 500
	)

	// Plant an obligation so Discharge has something to clear. The
	// inbound StandaloneAck below carries Initiator=false (the peer is
	// responding on an exchange WE opened), so our role on that exchange
	// is initiator=true — the obligation has to be keyed the same way.
	tracker.Owe(msgCounter, 0, exchangeID, true, time.Now())
	if tracker.Pending() != 1 {
		t.Fatalf("pre-condition: tracker.Pending() = %d, want 1", tracker.Pending())
	}

	// Send a StandaloneAck with HasAck=true targeting the exchange.
	proto := message.ProtocolHeader{
		ProtocolID: mrp.SecureChannelProtocolID,
		Opcode:     mrp.StandaloneAckOpcode,
		ExchangeID: exchangeID,
		HasAck:     true,
		AckCounter: msgCounter,
	}
	hdr := buildMsgHdr(1)
	_ = b.dispatchSecureChannel(loopbackSrc(), hdr, proto, nil)

	// The AckHandler wired by AttachAckTracker should have discharged the obligation.
	if tracker.Pending() != 0 {
		t.Errorf("tracker.Pending() = %d after dispatchSecureChannel with HasAck=true; want 0 (discharged)", tracker.Pending())
	}
}
