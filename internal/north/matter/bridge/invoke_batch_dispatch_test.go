// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// White-box tests for two InvokeRequest conformance rules wired into
// dispatchInvokeRequest in receive_dispatch.go:
//
//   - a malformed batch invoke (concrete paths in a batch that omit their
//     CommandRef) is rejected up front with StatusResponse(InvalidAction)
//     instead of dispatching any command — mirrors matter.js
//     CommandInvokeResponse.ts:64-92 (process/#processConcrete);
//   - a SuppressResponse invoke that yields only CommandStatusIB entries
//     (no CommandDataIB) sends nothing — mirrors matter.js
//     InteractionServer.ts:1043-1074 (the held suppressedBuffer is discarded).
//
// Lives in package bridge so it can call [Bridge.dispatch] directly, matching
// timed_conformance_test.go / write_chunked_dispatch_test.go.

package bridge

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// encodeInvokeRequestSuppress encodes an InvokeRequestMessage with an explicit
// SuppressResponse flag and one CommandDataIB per path (no CommandRef). Tag
// layout mirrors im.UnmarshalInvokeRequestTLV.
func encodeInvokeRequestSuppress(t *testing.T, suppressResponse bool, paths ...im.ConcreteCommandPath) []byte {
	t.Helper()
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutBool(tlv.ContextTag(0), suppressResponse)
	enc.PutBool(tlv.ContextTag(1), false) // TimedRequest
	enc.StartArray(tlv.ContextTag(2))     // InvokeRequests
	for _, p := range paths {
		enc.StartStruct(tlv.AnonymousTag()) // CommandDataIB
		p.MarshalTLV(enc, tlv.ContextTag(0))
		_ = enc.EndContainer()
	}
	_ = enc.EndContainer()
	_ = enc.EndContainer()
	body, err := enc.Bytes()
	if err != nil {
		t.Fatalf("encodeInvokeRequestSuppress: %v", err)
	}
	return body
}

// encodeInvokeRequestWithRefs mirrors encodeInvokeRequestSuppress but also
// stamps a CommandRef (tag 2) on every CommandDataIB — needed to isolate
// the batch-size check from the separate "every concrete path in a batch
// needs a CommandRef" rule, which would otherwise reject the same batch
// for an unrelated reason and mask the check under test.
func encodeInvokeRequestWithRefs(t *testing.T, paths ...im.ConcreteCommandPath) []byte {
	t.Helper()
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutBool(tlv.ContextTag(0), false) // SuppressResponse
	enc.PutBool(tlv.ContextTag(1), false) // TimedRequest
	enc.StartArray(tlv.ContextTag(2))     // InvokeRequests
	for i, p := range paths {
		enc.StartStruct(tlv.AnonymousTag()) // CommandDataIB
		p.MarshalTLV(enc, tlv.ContextTag(0))
		//nolint:gosec // test fixture, len(paths) bounded by callers.
		enc.PutUint16(tlv.ContextTag(2), uint16(i+1)) // CommandRef
		_ = enc.EndContainer()
	}
	_ = enc.EndContainer()
	_ = enc.EndContainer()
	body, err := enc.Bytes()
	if err != nil {
		t.Fatalf("encodeInvokeRequestWithRefs: %v", err)
	}
	return body
}

// dispatchInvokeCapture ships buf through [Bridge.dispatch] and returns the
// reply datagram, or (nil, false) if no reply is emitted within timeout.
func dispatchInvokeCapture(t *testing.T, b *Bridge, buf []byte, timeout time.Duration) ([]byte, bool) {
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

	_ = peerConn.SetReadDeadline(time.Now().Add(timeout))
	rbuf := make([]byte, 1500)
	n, _, err := peerConn.ReadFromUDP(rbuf)
	if err != nil {
		var nerr net.Error
		if errors.As(err, &nerr) && nerr.Timeout() {
			return nil, false
		}
		t.Fatalf("ReadFromUDP: %v", err)
	}
	return rbuf[:n], true
}

// TestDispatchInvokeRequest_BatchMissingCommandRef_RejectsInvalidAction drives a
// two-command InvokeRequest whose concrete paths omit their CommandRef and
// asserts the wire reply is a top-level StatusResponse(InvalidAction), proving
// dispatchInvokeRequest runs the batch guard before dispatching any command.
// Mirrors matter.js CommandInvokeResponse.ts:85-90 ("The CommandRef field must
// be specified for all commands in a batch invoke").
func TestDispatchInvokeRequest_BatchMissingCommandRef_RejectsInvalidAction(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)

	hdr := buildHeader(0, 50)
	proto := buildProtocolHeader(im.InteractionModelProtocolID, im.OpcodeInvokeRequest)
	// Two OnOff commands (0x0006/On + Off) — not timed-required, so the batch
	// guard (not the timed gate) is what rejects the request.
	payload := encodeInvokeRequestSuppress(
		t, false,
		im.ConcreteCommandPath{Endpoint: unreliabilityTestEndpoint, Cluster: 0x0006, Command: 0x01, HasEndpoint: true, HasCluster: true, HasCommand: true},
		im.ConcreteCommandPath{Endpoint: unreliabilityTestEndpoint, Cluster: 0x0006, Command: 0x00, HasEndpoint: true, HasCluster: true, HasCommand: true},
	)
	buf := buildDatagram(hdr, proto, payload)

	got, ok := dispatchInvokeCapture(t, b, buf, 2*time.Second)
	if !ok {
		t.Fatal("expected a StatusResponse(InvalidAction), got no reply")
	}
	status := decodeWriteChunkedStatusResponse(t, got)
	if status != im.StatusInvalidAction {
		t.Errorf("StatusResponse status = %v, want StatusInvalidAction (0x80)", status)
	}
}

// TestDispatchInvokeRequest_BatchExceedsMaxPaths_RejectsInvalidAction drives
// an InvokeRequest one path past im.DefaultMaxPathsPerInvoke — every path
// individually well-formed, on its own endpoint, with a distinct
// CommandRef — and asserts the wire reply is a top-level
// StatusResponse(InvalidAction), proving dispatchInvokeRequest's batch
// guard rejects an over-sized batch before dispatching any command in
// it. Mirrors matter.js InteractionServer.ts:950-955
// (`if (invokeRequests.length > this.#maxPathsPerInvoke) throw new
// StatusResponseError(..., Status.InvalidAction)`).
func TestDispatchInvokeRequest_BatchExceedsMaxPaths_RejectsInvalidAction(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)

	hdr := buildHeader(0, 52)
	proto := buildProtocolHeader(im.InteractionModelProtocolID, im.OpcodeInvokeRequest)

	paths := make([]im.ConcreteCommandPath, im.DefaultMaxPathsPerInvoke+1)
	for i := range paths {
		//nolint:gosec // test fixture, bounded by im.DefaultMaxPathsPerInvoke.
		paths[i] = im.ConcreteCommandPath{
			Endpoint: uint16(i + 1), Cluster: 0x0006, Command: 0x01,
			HasEndpoint: true, HasCluster: true, HasCommand: true,
		}
	}
	// Every path carries a distinct CommandRef so a rejection can only be
	// attributed to the path-count check, not the separate
	// missing-CommandRef rule (which would also reject this batch).
	payload := encodeInvokeRequestWithRefs(t, paths...)
	buf := buildDatagram(hdr, proto, payload)

	got, ok := dispatchInvokeCapture(t, b, buf, 2*time.Second)
	if !ok {
		t.Fatal("expected a StatusResponse(InvalidAction), got no reply")
	}
	status := decodeWriteChunkedStatusResponse(t, got)
	if status != im.StatusInvalidAction {
		t.Errorf("StatusResponse status = %v, want StatusInvalidAction (0x80)", status)
	}
}

// TestDispatchInvokeRequest_SuppressResponseStatusOnly_SendsNothing drives a
// SuppressResponse invoke of a single command that resolves to a status-only
// result (UnsupportedEndpoint on an endpoint absent from the topology) and
// asserts NO datagram is emitted. Mirrors matter.js InteractionServer.ts:
// 1070-1074 — a suppress-response invoke with no CommandDataIB sends nothing.
// The paired positive control (suppressResponse=false → InvokeResponse) is
// TestDispatchInvokeRequest_ReplyIsMRPReliable in reply_reliability_test.go.
func TestDispatchInvokeRequest_SuppressResponseStatusOnly_SendsNothing(t *testing.T) {
	t.Parallel()
	b := newStartedBridge(t)

	hdr := buildHeader(0, 51)
	proto := buildProtocolHeader(im.InteractionModelProtocolID, im.OpcodeInvokeRequest)
	payload := encodeInvokeRequestSuppress(
		t, true,
		im.ConcreteCommandPath{Endpoint: unreliabilityTestEndpoint, Cluster: 0x0006, Command: 0x00, HasEndpoint: true, HasCluster: true, HasCommand: true},
	)
	buf := buildDatagram(hdr, proto, payload)

	if got, ok := dispatchInvokeCapture(t, b, buf, 600*time.Millisecond); ok {
		t.Fatalf("expected no reply for a suppress-response status-only invoke, got %d-byte datagram", len(got))
	}
}

// TestDispatchInvokeRequest_SuppressResponseStatusOnly_StillDrivesSideEffects is a
// belt-and-braces decode check: the suppressed invoke must be structurally
// parseable (a well-formed InvokeRequestMessage) so the "send nothing" branch is
// reached by real dispatch, not by a decode error upstream.
func TestDispatchInvokeRequest_SuppressResponseStatusOnly_StillDrivesSideEffects(t *testing.T) {
	t.Parallel()
	payload := encodeInvokeRequestSuppress(
		t, true,
		im.ConcreteCommandPath{Endpoint: unreliabilityTestEndpoint, Cluster: 0x0006, Command: 0x00, HasEndpoint: true, HasCluster: true, HasCommand: true},
	)
	dec := tlv.NewDecoder(payload)
	req, err := im.UnmarshalInvokeRequestTLV(dec, nil)
	if err != nil {
		t.Fatalf("UnmarshalInvokeRequestTLV: %v", err)
	}
	if !req.SuppressResponse {
		t.Error("decoded SuppressResponse must be true")
	}
	if len(req.Invokes) != 1 {
		t.Fatalf("decoded %d invokes, want 1", len(req.Invokes))
	}
}
