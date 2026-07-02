// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package bridge

// White-box tests locking in that the bridge's IM WriteResponse,
// InvokeResponse, and TimedRequest-gated StatusResponse replies are
// shipped MRP-reliably (NeedsAck=true, tracked for retransmission) via
// [Bridge.sendReplyReliable] rather than best-effort [Bridge.sendReply].
// A dropped InvokeResponse or WriteResponse leaves the controller
// waiting on a command that already executed — Apple Home surfaces
// this as "Not Responding" even though the underlying device state
// changed. A dropped TimedRequest StatusResponse strands the
// commissioner's timed Write/Invoke follow-up, which never arrives
// because the go-ahead was lost.
//
// Each test wires an [mrp.AckTracker] via [Bridge.AttachAckTracker]
// before dispatching — sendReplyOpts only sets NeedsAck when a
// tracker is present (see reply.go), so a test that forgot this step
// would pass trivially even against a reverted best-effort call.
// These tests use unresolvable endpoints/paths so no real cluster
// server or CASE session setup is needed: dispatchInvokeRequest and
// dispatchWriteRequest ship their reply unconditionally, carrying
// whatever status the dispatcher produced (UnsupportedEndpoint here),
// and dispatchTimedRequest always replies Success.

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/message"
	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/mrp"
)

// unreliabilityTestEndpoint is a concrete endpoint ID absent from every
// newStartedBridge topology (root-only: endpoint 0, no bridged
// devices). Targeting it forces the dispatcher down the
// UnsupportedEndpoint path, which still produces a full IM response —
// exactly the pure-outcome-report shape [Bridge.sendReplyReliable]'s
// idempotency contract requires.
const unreliabilityTestEndpoint uint16 = 99

// dispatchReliabilityTestRequest wires an ack tracker into a fresh
// started bridge, ships buf as an unsecured (SessionID=0) datagram
// through [Bridge.dispatch], and returns the bridge's reply datagram
// captured on a real loopback UDP socket.
func dispatchReliabilityTestRequest(t *testing.T, buf []byte) []byte {
	t.Helper()
	b := newStartedBridge(t)
	// Without a tracker, sendReplyOpts silently degrades reliable
	// replies to best-effort (see reply.go) — wiring one is the
	// precondition for this test to be able to observe NeedsAck=true
	// at all.
	b.AttachAckTracker(mrp.NewAckTracker(50 * time.Millisecond))

	peerConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	t.Cleanup(func() { _ = peerConn.Close() })
	peerAddr, ok := peerConn.LocalAddr().(*net.UDPAddr)
	if !ok {
		t.Fatalf("unexpected peer addr type %T", peerConn.LocalAddr())
	}

	if err := b.dispatch(context.Background(), buf, peerAddr); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	_ = peerConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	rbuf := make([]byte, 1500)
	n, _, err := peerConn.ReadFromUDP(rbuf)
	if err != nil {
		t.Fatalf("ReadFromUDP: %v", err)
	}
	return rbuf[:n]
}

// decodeReliabilityTestReply parses the message + protocol headers off
// a captured reply datagram, asserting the opcode matches wantOpcode.
func decodeReliabilityTestReply(t *testing.T, got []byte, wantOpcode uint8) message.ProtocolHeader {
	t.Helper()
	_, hdrLen, err := message.UnmarshalHeader(got)
	if err != nil {
		t.Fatalf("UnmarshalHeader: %v", err)
	}
	rproto, _, err := message.UnmarshalProtocolHeader(got[hdrLen:])
	if err != nil {
		t.Fatalf("UnmarshalProtocolHeader: %v", err)
	}
	if rproto.Opcode != wantOpcode {
		t.Fatalf("reply opcode = 0x%02X, want 0x%02X", rproto.Opcode, wantOpcode)
	}
	return rproto
}

// encodeReliabilityTestTimedRequest encodes a minimal TimedRequestMessage.
// Tag layout mirrors im.UnmarshalTimedRequestTLV's tagTimedReqTimeout (0).
func encodeReliabilityTestTimedRequest(t *testing.T, timeoutMs uint16) []byte {
	t.Helper()
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutUint16(tlv.ContextTag(0), timeoutMs)
	_ = enc.EndContainer()
	body, err := enc.Bytes()
	if err != nil {
		t.Fatalf("encodeReliabilityTestTimedRequest: %v", err)
	}
	return body
}

// TestDispatchInvokeRequest_ReplyIsMRPReliable is the highest-value
// case: a lost InvokeResponse is what surfaces to Apple Home as
// "Not Responding" even though the command already executed. Drives a
// plain (non-timed) OnOff.Off invoke against an endpoint absent from
// the bridge's topology — the dispatcher answers UnsupportedEndpoint,
// but dispatchInvokeRequest still ships that status via
// sendReplyReliable regardless of outcome. Fails if
// dispatchInvokeRequest's call site in receive_dispatch.go reverts to
// the best-effort sendReply.
func TestDispatchInvokeRequest_ReplyIsMRPReliable(t *testing.T) {
	t.Parallel()

	hdr := buildHeader(0, 30)
	proto := buildProtocolHeader(im.InteractionModelProtocolID, im.OpcodeInvokeRequest)
	payload := encodeTimedTestInvokeRequest(t, false, im.ConcreteCommandPath{
		Endpoint: unreliabilityTestEndpoint, Cluster: 0x0006, Command: 0x0, // OnOff.Off — not timed-required.
		HasEndpoint: true, HasCluster: true, HasCommand: true,
	})
	buf := buildDatagram(hdr, proto, payload)

	got := dispatchReliabilityTestRequest(t, buf)
	rproto := decodeReliabilityTestReply(t, got, im.OpcodeInvokeResponse)

	if !rproto.NeedsAck {
		t.Error("InvokeResponse NeedsAck = false, want true (reply must be MRP-reliable, not best-effort)")
	}
}

// TestDispatchWriteRequest_ReplyIsMRPReliable covers the WriteResponse
// site. A write to an endpoint absent from the topology still produces
// a non-suppressed WriteResponse (UnsupportedEndpoint status), which
// dispatchWriteRequest ships via sendReplyReliable. Fails if
// dispatchWriteRequest's call site in receive_dispatch.go reverts to
// the best-effort sendReply.
func TestDispatchWriteRequest_ReplyIsMRPReliable(t *testing.T) {
	t.Parallel()

	hdr := buildHeader(0, 31)
	proto := buildProtocolHeader(im.InteractionModelProtocolID, im.OpcodeWriteRequest)
	writePayload, err := encodeScenarioWriteRequest(im.ConcreteAttributePath{
		Endpoint: unreliabilityTestEndpoint, Cluster: 0x0006, Attribute: 0x0000, // OnOff.OnOff
		HasEndpoint: true, HasCluster: true, HasAttribute: true,
	}, true)
	if err != nil {
		t.Fatalf("encodeScenarioWriteRequest: %v", err)
	}
	buf := buildDatagram(hdr, proto, writePayload)

	got := dispatchReliabilityTestRequest(t, buf)
	rproto := decodeReliabilityTestReply(t, got, im.OpcodeWriteResponse)

	if !rproto.NeedsAck {
		t.Error("WriteResponse NeedsAck = false, want true (reply must be MRP-reliable, not best-effort)")
	}
}

// TestDispatchTimedRequest_ReplyIsMRPReliable covers the
// TimedRequest→StatusResponse site (Matter §8.7): if this
// StatusResponse is lost, the commissioner never sends its follow-up
// timed Write/Invoke and the exchange dies. Fails if
// dispatchTimedRequest's call site in receive_dispatch.go reverts to
// the best-effort sendReply.
func TestDispatchTimedRequest_ReplyIsMRPReliable(t *testing.T) {
	t.Parallel()

	hdr := buildHeader(0, 32)
	proto := buildProtocolHeader(im.InteractionModelProtocolID, im.OpcodeTimedRequest)
	payload := encodeReliabilityTestTimedRequest(t, 5000)
	buf := buildDatagram(hdr, proto, payload)

	got := dispatchReliabilityTestRequest(t, buf)
	rproto := decodeReliabilityTestReply(t, got, im.OpcodeStatusResponse)

	if !rproto.NeedsAck {
		t.Error("TimedRequest StatusResponse NeedsAck = false, want true (reply must be MRP-reliable, not best-effort)")
	}
}
