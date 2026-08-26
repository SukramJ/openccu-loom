// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// White-box tests for the Subscribe-matched-path gate added to
// handleSubscribeRequest (subscribe.go): a Subscribe naming no paths at
// all, or whose (possibly wildcard) paths match zero attributes and
// zero events, must be rejected with a top-level
// StatusResponse(InvalidAction) rather than establishing an empty
// subscription. Mirrors matter.js InteractionServer.ts:628-633 (no
// attributes/events requested) and ServerSubscription.ts:610-614 (zero
// matched paths). This file lives in package bridge so it can call
// [Bridge.handleSubscribeRequest] directly — the method's own doc
// comment notes it is exposed precisely so tests can drive it with
// pre-built Go structs, skipping SubscribeRequest TLV encoding
// entirely.

package bridge

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im/subscription"
	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/message"
)

// dispatchSubscribeTestRequest ships req through
// [Bridge.handleSubscribeRequest] directly and returns the bridge's
// first reply datagram, captured on a real loopback UDP socket.
// Mirrors dispatchWriteChunkedTestRequest (write_chunked_dispatch_test.go)
// and dispatchReliabilityTestRequest (reply_reliability_test.go), adapted
// to call the Subscribe handler instead of the generic dispatch entry
// point.
func dispatchSubscribeTestRequest(t *testing.T, b *Bridge, hdr *message.Header, proto message.ProtocolHeader, req im.SubscribeRequest) []byte {
	t.Helper()
	peerConn, peerAddr := newSubscribeTestPeer(t)

	if err := b.handleSubscribeRequest(context.Background(), peerAddr, hdr, proto, req); err != nil {
		t.Fatalf("handleSubscribeRequest: %v", err)
	}

	_ = peerConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	rbuf := make([]byte, 1500)
	n, _, err := peerConn.ReadFromUDP(rbuf)
	if err != nil {
		t.Fatalf("ReadFromUDP: %v", err)
	}
	return rbuf[:n]
}

// newSubscribeTestPeer opens a real loopback UDP socket standing in for
// the commissioner, closed via t.Cleanup.
func newSubscribeTestPeer(t *testing.T) (*net.UDPConn, *net.UDPAddr) {
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
	return peerConn, peerAddr
}

// decodeSubscribeStatusResponse asserts got is a StatusResponse and
// returns its Status code. Mirrors decodeWriteChunkedStatusResponse
// (write_chunked_dispatch_test.go), reusing decodeStatusResponseCode
// (timed_conformance_test.go).
func decodeSubscribeStatusResponse(t *testing.T, got []byte) im.StatusCode {
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
		t.Fatalf("reply opcode = 0x%02X, want StatusResponse (0x%02X) — the subscribe must be rejected before any report or SubscribeResponse is sent", rproto.Opcode, im.OpcodeStatusResponse)
	}
	return decodeStatusResponseCode(t, got[hdrLen+protoLen:])
}

// TestHandleSubscribeRequest_EmptyRequest_RejectsInvalidAction verifies
// that a SubscribeRequest with no AttributeRequests and no
// EventRequests is rejected with StatusResponse(InvalidAction) and
// never reaches the subscription manager. Mirrors matter.js
// InteractionServer.ts:628-633 ("No attributes or events requested").
func TestHandleSubscribeRequest_EmptyRequest_RejectsInvalidAction(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	mgr := subscription.NewManager(subscription.Config{}, nil, nil)
	b.AttachSubscriptionManager(mgr)

	hdr := &message.Header{SessionID: 0, MessageCounter: 100}
	proto := message.ProtocolHeader{ProtocolID: im.InteractionModelProtocolID, Opcode: im.OpcodeSubscribeRequest, ExchangeID: 1}
	req := im.SubscribeRequest{MinIntervalFloor: 0, MaxIntervalCeiling: 60}

	got := dispatchSubscribeTestRequest(t, b, hdr, proto, req)
	if status := decodeSubscribeStatusResponse(t, got); status != im.StatusInvalidAction {
		t.Errorf("StatusResponse status = %v, want StatusInvalidAction (0x80)", status)
	}
	if n := mgr.Active(); n != 0 {
		t.Errorf("mgr.Active() = %d, want 0 — an empty Subscribe must not register a subscription", n)
	}
}

// TestHandleSubscribeRequest_NoMatchingPaths_RejectsInvalidAction
// verifies that a Subscribe whose sole path matches zero attributes is
// rejected with StatusResponse(InvalidAction) and never registers.
// Mirrors matter.js ServerSubscription.ts:610-614.
//
// The path uses a wildcard endpoint (HasEndpoint=false) with a concrete
// cluster ID no endpoint in the topology hosts: the dispatcher's
// wildcard expansion (endpoint/dispatcher.go TopologyDispatcher.Read →
// serversFor) silently SKIPS endpoints that lack the requested cluster
// under wildcard-endpoint addressing rather than synthesising an
// UnsupportedCluster status IB — that synthesis only fires for CONCRETE
// endpoint addressing (path.HasEndpoint=true), which always yields at
// least one ReadResult (an Unsupported* status) and therefore never
// triggers this "no_match" rejection. A concrete-but-absent endpoint or
// cluster is a distinct, already-covered case (see
// TestDispatchInvokeRequest_ReplyIsMRPReliable /
// TestDispatchWriteRequest_ReplyIsMRPReliable in
// reply_reliability_test.go, which drive that path and still get a
// full reply). This test targets the one code path where wildcard
// expansion over the requested scope produces genuinely zero results.
func TestHandleSubscribeRequest_NoMatchingPaths_RejectsInvalidAction(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)
	mgr := subscription.NewManager(subscription.Config{}, nil, nil)
	b.AttachSubscriptionManager(mgr)

	hdr := &message.Header{SessionID: 0, MessageCounter: 101}
	proto := message.ProtocolHeader{ProtocolID: im.InteractionModelProtocolID, Opcode: im.OpcodeSubscribeRequest, ExchangeID: 1}
	const noSuchCluster uint32 = 0xFFFF0001 // no cluster server anywhere in the topology advertises this ID.
	req := im.SubscribeRequest{
		AttributeRequests: []im.ConcreteAttributePath{
			{HasEndpoint: false, HasCluster: true, Cluster: noSuchCluster, HasAttribute: false},
		},
		MinIntervalFloor:   0,
		MaxIntervalCeiling: 60,
	}

	got := dispatchSubscribeTestRequest(t, b, hdr, proto, req)
	if status := decodeSubscribeStatusResponse(t, got); status != im.StatusInvalidAction {
		t.Errorf("StatusResponse status = %v, want StatusInvalidAction (0x80)", status)
	}
	if n := mgr.Active(); n != 0 {
		t.Errorf("mgr.Active() = %d, want 0 — a Subscribe matching zero paths must not register a subscription", n)
	}
}

// TestHandleSubscribeRequest_ManagerRejection_SendsStatusResponse pins
// that a Subscribe the subscription manager refuses is answered with a
// StatusResponse — never with a SubscribeResponse. A SubscribeResponse
// carrying SubscriptionID=0 tells the controller the subscription was
// established: it then waits for reports that no subscription will ever
// produce, and the bridged devices go stale with nothing on the wire
// explaining why.
//
// Status mapping mirrors matter.js
// packages/node/src/node/server/InteractionServer.ts:665-682 (cadence
// constraints → InvalidAction); a quota rejection is ResourceExhausted
// per Matter §8.10.
func TestHandleSubscribeRequest_ManagerRejection_SendsStatusResponse(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		cfg        subscription.Config
		req        im.SubscribeRequest
		wantStatus im.StatusCode
	}{
		"cadence inverted": {
			cfg:        subscription.Config{},
			req:        im.SubscribeRequest{MinIntervalFloor: 120, MaxIntervalCeiling: 60},
			wantStatus: im.StatusInvalidAction,
		},
		"cadence inverted after clamp": {
			cfg:        subscription.Config{MaxIntervalCeilingSeconds: 30, MinIntervalFloorSeconds: 60},
			req:        im.SubscribeRequest{MinIntervalFloor: 0, MaxIntervalCeiling: 3600},
			wantStatus: im.StatusInvalidAction,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			b := newStartedBridge(t)
			mgr := subscription.NewManager(tc.cfg, nil, nil)
			b.AttachSubscriptionManager(mgr)

			hdr := &message.Header{SessionID: 0, MessageCounter: 102}
			proto := message.ProtocolHeader{ProtocolID: im.InteractionModelProtocolID, Opcode: im.OpcodeSubscribeRequest, ExchangeID: 1}
			req := tc.req
			req.AttributeRequests = []im.ConcreteAttributePath{
				{HasEndpoint: true, Endpoint: 0, HasCluster: true, Cluster: 0x001D, HasAttribute: true, Attribute: 0x0003},
			}

			got := dispatchSubscribeTestRequest(t, b, hdr, proto, req)
			if status := decodeSubscribeStatusResponse(t, got); status != tc.wantStatus {
				t.Errorf("StatusResponse status = %v, want %v", status, tc.wantStatus)
			}
			if n := mgr.Active(); n != 0 {
				t.Errorf("mgr.Active() = %d, want 0", n)
			}
		})
	}
}
