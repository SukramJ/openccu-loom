// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// White-box tests for the chunked-write InvalidAction rules added to
// dispatchWriteRequest in receive_dispatch.go: a WriteRequest that
// combines MoreChunkedMessages with SuppressResponse, or with a valid
// timed window, must be rejected with StatusResponse(InvalidAction)
// rather than dispatched. This file lives in package bridge so it can
// call [Bridge.dispatch] directly and seed [exchangeRouting.timedDeadlines],
// matching the style of timed_conformance_test.go and
// reply_reliability_test.go.

package bridge

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/message"
)

// encodeWriteChunkedTestRequest encodes a WriteRequestMessage with
// explicit SuppressResponse, TimedRequest, and MoreChunkedMessages
// flags for exercising the two chunked-write InvalidAction rules
// (Matter §10.6.5; matter.js InteractionServer.ts:397-402 and
// :408-413). Tag layout mirrors the production decoder
// (im.UnmarshalWriteRequestTLV's tagWriteReq{SuppressResponse,
// TimedRequest,WriteRequests,MoreChunked}). Each path in writes gets
// a bool payload so the production attributeValueReader decodes it
// without error.
func encodeWriteChunkedTestRequest(t *testing.T, suppressResponse, timedRequest, moreChunked bool, writes ...im.ConcreteAttributePath) []byte {
	t.Helper()
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutBool(tlv.ContextTag(0), suppressResponse)
	enc.PutBool(tlv.ContextTag(1), timedRequest)
	enc.StartArray(tlv.ContextTag(2)) // WriteRequests
	for _, p := range writes {
		enc.StartStruct(tlv.AnonymousTag()) // AttributeDataIB
		p.MarshalTLV(enc, tlv.ContextTag(1))
		enc.PutBool(tlv.ContextTag(2), true)
		_ = enc.EndContainer()
	}
	_ = enc.EndContainer()
	enc.PutBool(tlv.ContextTag(3), moreChunked)
	_ = enc.EndContainer()
	body, err := enc.Bytes()
	if err != nil {
		t.Fatalf("encodeWriteChunkedTestRequest: %v", err)
	}
	return body
}

// dispatchWriteChunkedTestRequest ships buf as an unsecured
// (SessionID=0) datagram through [Bridge.dispatch] and returns the
// bridge's reply datagram captured on a real loopback UDP socket.
func dispatchWriteChunkedTestRequest(t *testing.T, b *Bridge, buf []byte) []byte {
	t.Helper()
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

// decodeWriteChunkedStatusResponse asserts got is a StatusResponse and
// returns its Status code. Mirrors the decode sequence in
// TestDispatchInvokeRequest_TimedRequiredWithoutWindow_NeedsTimedInteraction
// (timed_conformance_test.go), reusing decodeStatusResponseCode.
func decodeWriteChunkedStatusResponse(t *testing.T, got []byte) im.StatusCode {
	t.Helper()
	_, hdrLen, err := message.UnmarshalHeader(got)
	if err != nil {
		t.Fatalf("UnmarshalHeader: %v", err)
	}
	rproto, protoLen, err := message.UnmarshalProtocolHeader(got[hdrLen:])
	if err != nil {
		t.Fatalf("UnmarshalProtocolHeader: %v", err)
	}
	if rproto.Opcode != im.OpcodeStatusResponse {
		t.Fatalf("reply opcode = 0x%02X, want StatusResponse (0x%02X) — the write must not be dispatched", rproto.Opcode, im.OpcodeStatusResponse)
	}
	return decodeStatusResponseCode(t, got[hdrLen+protoLen:])
}

// TestDispatchWriteRequest_ChunkedSuppressResponse_RejectsInvalidAction
// mirrors matter.js InteractionServer.ts:397-402: a WriteRequest that
// sets both MoreChunkedMessages and SuppressResponse must be rejected
// with StatusResponse(InvalidAction) before any timed-interaction
// handling or dispatch. This is a meaningful regression guard: were
// the guard missing, dispatchWriteRequest would run
// HandleWriteRequest and then honor SuppressResponse by sending NO
// reply at all — this test would then fail on ReadFromUDP timing out
// rather than on a wrong status code.
func TestDispatchWriteRequest_ChunkedSuppressResponse_RejectsInvalidAction(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)

	hdr := buildHeader(0, 40)
	proto := buildProtocolHeader(im.InteractionModelProtocolID, im.OpcodeWriteRequest)
	payload := encodeWriteChunkedTestRequest(t, true, false, true, im.ConcreteAttributePath{
		Endpoint: unreliabilityTestEndpoint, Cluster: 0x0006, Attribute: 0x0000,
		HasEndpoint: true, HasCluster: true, HasAttribute: true,
	})
	buf := buildDatagram(hdr, proto, payload)

	got := dispatchWriteChunkedTestRequest(t, b, buf)
	status := decodeWriteChunkedStatusResponse(t, got)
	if status != im.StatusInvalidAction {
		t.Errorf("StatusResponse status = %v, want StatusInvalidAction (0x80)", status)
	}
}

// TestDispatchWriteRequest_TimedWithMoreChunked_RejectsInvalidActionAndConsumesWindow
// mirrors matter.js InteractionServer.ts:408-413 ("Write Request
// action that is part of a Timed Write Interaction SHALL NOT be
// chunked"): a WriteRequest inside a valid timed window that also
// sets MoreChunkedMessages must be rejected with
// StatusResponse(InvalidAction) after the timed gate passes. The
// timed window is still consumed by checkTimedGate before the
// chunked check runs, so a retry re-tests as "no prior TimedRequest".
func TestDispatchWriteRequest_TimedWithMoreChunked_RejectsInvalidActionAndConsumesWindow(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	const (
		sessionID  = uint16(0)
		exchangeID = uint16(1) // buildProtocolHeader fixes ExchangeID at 1.
	)
	b.routing.timedDeadlines.Store(timedKey{sessionID: sessionID, exchangeID: exchangeID}, time.Now().Add(10*time.Second))

	hdr := buildHeader(sessionID, 41)
	proto := buildProtocolHeader(im.InteractionModelProtocolID, im.OpcodeWriteRequest)
	payload := encodeWriteChunkedTestRequest(t, false, true, true, im.ConcreteAttributePath{
		Endpoint: unreliabilityTestEndpoint, Cluster: 0x0006, Attribute: 0x0000,
		HasEndpoint: true, HasCluster: true, HasAttribute: true,
	})
	buf := buildDatagram(hdr, proto, payload)

	got := dispatchWriteChunkedTestRequest(t, b, buf)
	status := decodeWriteChunkedStatusResponse(t, got)
	if status != im.StatusInvalidAction {
		t.Errorf("StatusResponse status = %v, want StatusInvalidAction (0x80)", status)
	}

	if _, ok := b.routing.timedDeadlines.Load(timedKey{sessionID: sessionID, exchangeID: exchangeID}); ok {
		t.Error("timed window still present after a timed+chunked write — checkTimedGate must consume it before the chunked check rejects")
	}
}

// TestDispatchWriteRequest_ChunkedUntimedNotSuppressed_DispatchesAndRepliesWriteResponse
// is the positive control: an untimed WriteRequest with
// MoreChunkedMessages set but SuppressResponse clear is a legal chunk
// of a multi-chunk write and must dispatch normally, answered by its
// own WriteResponse. Mirrors matter.js InteractionServer.ts:521-532,
// which sends a per-chunk WriteResponse and then reads the next
// chunk.
func TestDispatchWriteRequest_ChunkedUntimedNotSuppressed_DispatchesAndRepliesWriteResponse(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)

	hdr := buildHeader(0, 42)
	proto := buildProtocolHeader(im.InteractionModelProtocolID, im.OpcodeWriteRequest)
	payload := encodeWriteChunkedTestRequest(t, false, false, true, im.ConcreteAttributePath{
		Endpoint: unreliabilityTestEndpoint, Cluster: 0x0006, Attribute: 0x0000,
		HasEndpoint: true, HasCluster: true, HasAttribute: true,
	})
	buf := buildDatagram(hdr, proto, payload)

	got := dispatchWriteChunkedTestRequest(t, b, buf)
	decodeReliabilityTestReply(t, got, im.OpcodeWriteResponse)
}
