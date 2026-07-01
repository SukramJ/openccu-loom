// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package bridge

// White-box tests for the timed-required-invoke conformance path added
// in receive_dispatch.go: anyTimedRequiredInvoke (the batched-invoke
// scan) and its composition with dispatchInvokeRequest's checkTimedGate
// call. This file lives in package bridge so it can call the unexported
// helper and construct a bare Bridge directly, matching the style of
// receive_test.go's checkTimedGate unit tests.

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/message"
)

// ─── anyTimedRequiredInvoke ───────────────────────────────────────────────

// TestAnyTimedRequiredInvoke covers the batched-invoke scan that folds
// into dispatchInvokeRequest's timed gate: a request is timed-required
// as a whole if any one of its commands is (schema.IsTimedInvoke).
func TestAnyTimedRequiredInvoke(t *testing.T) {
	t.Parallel()

	openCommissioningWindow := im.ConcreteCommandPath{
		Cluster: 0x003C, Command: 0x0, HasCluster: true, HasCommand: true,
	}
	onOffOn := im.ConcreteCommandPath{
		Cluster: 0x0006, Command: 0x0, HasCluster: true, HasCommand: true,
	}

	cases := []struct {
		name string
		req  im.InvokeRequest
		want bool
	}{
		{
			name: "single timed-required command",
			req:  im.InvokeRequest{Invokes: []im.CommandInvocation{{Path: openCommissioningWindow}}},
			want: true,
		},
		{
			name: "single non-timed command",
			req:  im.InvokeRequest{Invokes: []im.CommandInvocation{{Path: onOffOn}}},
			want: false,
		},
		{
			name: "empty invokes",
			req:  im.InvokeRequest{},
			want: false,
		},
		{
			name: "batched: non-timed then timed-required",
			req: im.InvokeRequest{Invokes: []im.CommandInvocation{
				{Path: onOffOn},
				{Path: openCommissioningWindow},
			}},
			want: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := anyTimedRequiredInvoke(tc.req); got != tc.want {
				t.Errorf("anyTimedRequiredInvoke(%+v) = %v, want %v", tc.req, got, tc.want)
			}
		})
	}
}

// ─── dispatch-level composition ───────────────────────────────────────────

// encodeTimedTestInvokeRequest encodes a minimal InvokeRequestMessage
// carrying one CommandDataIB per path, with no CommandFields — the
// dispatcher's checkTimedGate call runs from the path alone, before
// route resolution ever needs fields (see dispatchInvokeRequest in
// receive_dispatch.go). Tag numbers 0/1/2 mirror
// tagInvokeReq{SuppressResponse,TimedRequest,InvokeRequests} in
// internal/north/matter/im/invoke.go; CommandPathIB is encoded via
// [im.ConcreteCommandPath.MarshalTLV]. Mirrors the shape of
// encodeScenarioInvokeMoveToLevel in scenario_tlv_test.go.
func encodeTimedTestInvokeRequest(t *testing.T, timedRequest bool, paths ...im.ConcreteCommandPath) []byte {
	t.Helper()
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutBool(tlv.ContextTag(0), false) // SuppressResponse
	enc.PutBool(tlv.ContextTag(1), timedRequest)
	enc.StartArray(tlv.ContextTag(2)) // InvokeRequests
	for _, p := range paths {
		enc.StartStruct(tlv.AnonymousTag()) // CommandDataIB
		p.MarshalTLV(enc, tlv.ContextTag(0))
		_ = enc.EndContainer()
	}
	_ = enc.EndContainer()
	_ = enc.EndContainer()
	body, err := enc.Bytes()
	if err != nil {
		t.Fatalf("encodeTimedTestInvokeRequest: %v", err)
	}
	return body
}

// decodeStatusResponseCode extracts the Status field (tag 0) from a
// StatusResponseMessage body. Mirrors the tag layout in
// internal/north/matter/im/timed.go (tagStatusResponseStatus = 0).
func decodeStatusResponseCode(t *testing.T, body []byte) im.StatusCode {
	t.Helper()
	dec := tlv.NewDecoder(body)
	open, err := dec.Next()
	if err != nil || !open.IsContainer {
		t.Fatalf("decode StatusResponse: open struct: %v", err)
	}
	for {
		el, err := dec.Next()
		if err != nil {
			t.Fatalf("decode StatusResponse: %v", err)
		}
		if el.IsEndContainer {
			t.Fatal("decode StatusResponse: no Status (tag 0) field found")
		}
		if el.Tag.Kind == tlv.TagKindContext && el.Tag.Number == 0 {
			return im.StatusCode(el.Uint)
		}
	}
}

// TestDispatchInvokeRequest_TimedRequiredWithoutWindow_NeedsTimedInteraction
// drives a real Bridge.dispatch with an OpenCommissioningWindow InvokeRequest
// whose own TimedRequest flag is clear and with no preceding TimedRequest
// registered, and asserts the wire reply is a StatusResponse carrying
// NEEDS_TIMED_INTERACTION (0xC6). This exercises the full composition added
// to dispatchInvokeRequest: anyTimedRequiredInvoke(req) || req.TimedRequest
// feeding checkTimedGate. Uses SessionID=0 (unsecured), matching
// TestDispatch_IMReadRoutes's precedent, so no CASE session pair is needed —
// the reply is captured on a real loopback UDP socket instead of only
// asserting a nil dispatch error.
func TestDispatchInvokeRequest_TimedRequiredWithoutWindow_NeedsTimedInteraction(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)

	peerConn, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatalf("ListenUDP: %v", err)
	}
	t.Cleanup(func() { _ = peerConn.Close() })
	peerAddr, ok := peerConn.LocalAddr().(*net.UDPAddr)
	if !ok {
		t.Fatalf("unexpected peer addr type %T", peerConn.LocalAddr())
	}

	hdr := buildHeader(0, 20)
	proto := buildProtocolHeader(im.InteractionModelProtocolID, im.OpcodeInvokeRequest)
	payload := encodeTimedTestInvokeRequest(t, false, im.ConcreteCommandPath{
		Endpoint: 1, Cluster: 0x003C, Command: 0x0, // AdministratorCommissioning.OpenCommissioningWindow
		HasEndpoint: true, HasCluster: true, HasCommand: true,
	})
	buf := buildDatagram(hdr, proto, payload)

	if err := b.dispatch(context.Background(), buf, peerAddr); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	_ = peerConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	rbuf := make([]byte, 1500)
	n, _, err := peerConn.ReadFromUDP(rbuf)
	if err != nil {
		t.Fatalf("ReadFromUDP: %v", err)
	}
	got := rbuf[:n]

	rhdr, hdrLen, err := message.UnmarshalHeader(got)
	if err != nil {
		t.Fatalf("UnmarshalHeader: %v", err)
	}
	if rhdr.SessionID != 0 {
		t.Fatalf("reply SessionID = %d, want 0 (unsecured)", rhdr.SessionID)
	}
	rproto, protoLen, err := message.UnmarshalProtocolHeader(got[hdrLen:])
	if err != nil {
		t.Fatalf("UnmarshalProtocolHeader: %v", err)
	}
	if rproto.Opcode != im.OpcodeStatusResponse {
		t.Fatalf("reply opcode = 0x%02X, want StatusResponse (0x%02X)", rproto.Opcode, im.OpcodeStatusResponse)
	}

	status := decodeStatusResponseCode(t, got[hdrLen+protoLen:])
	if status != im.StatusNeedsTimedInteraction {
		t.Errorf("StatusResponse status = %v, want StatusNeedsTimedInteraction (0xC6)", status)
	}
}
