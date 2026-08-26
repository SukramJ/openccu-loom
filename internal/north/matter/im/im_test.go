// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package im

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// --- Path round-trips ---

// TestAttributePathRoundTripConcrete locks the encode→decode contract
// for a fully-qualified path.
func TestAttributePathRoundTripConcrete(t *testing.T) {
	in := ConcreteAttributePath{
		Endpoint: 1, HasEndpoint: true,
		Cluster: 0x0006, HasCluster: true,
		Attribute: 0x0000, HasAttribute: true,
	}
	enc := tlv.NewEncoder()
	in.MarshalTLV(enc, tlv.AnonymousTag())
	wire, err := enc.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	dec := tlv.NewDecoder(wire)
	out, err := UnmarshalAttributePathTLV(dec)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if out != in {
		t.Fatalf("got %+v, want %+v", out, in)
	}
}

// TestAttributePathWildcardEndpoint covers the "wildcard endpoint"
// shape used by commissioner-issued bulk reads.
func TestAttributePathWildcardEndpoint(t *testing.T) {
	in := ConcreteAttributePath{
		Cluster: 0x0006, HasCluster: true,
		Attribute: 0x0000, HasAttribute: true,
	}
	enc := tlv.NewEncoder()
	in.MarshalTLV(enc, tlv.AnonymousTag())
	wire, _ := enc.Bytes()
	dec := tlv.NewDecoder(wire)
	out, err := UnmarshalAttributePathTLV(dec)
	if err != nil {
		t.Fatal(err)
	}
	if !out.IsWildcardEndpoint() || out.HasEndpoint {
		t.Fatalf("expected wildcard endpoint, got %+v", out)
	}
	if out.Cluster != 0x0006 || out.Attribute != 0x0000 {
		t.Fatalf("non-wildcard fields drifted: %+v", out)
	}
}

// TestCommandPathRoundTrip covers the full (endpoint, cluster, command)
// triple.
func TestCommandPathRoundTrip(t *testing.T) {
	in := ConcreteCommandPath{
		Endpoint: 2, HasEndpoint: true,
		Cluster: 0x0006, HasCluster: true,
		Command: 0x01, HasCommand: true,
	}
	enc := tlv.NewEncoder()
	in.MarshalTLV(enc, tlv.AnonymousTag())
	wire, _ := enc.Bytes()
	dec := tlv.NewDecoder(wire)
	out, err := UnmarshalCommandPathTLV(dec)
	if err != nil {
		t.Fatal(err)
	}
	if out != in {
		t.Fatalf("got %+v, want %+v", out, in)
	}
}

// TestCommandPathRejectsMissingCluster catches the spec invariant —
// CommandPathIB MUST carry both cluster and command.
func TestCommandPathRejectsMissingCluster(t *testing.T) {
	enc := tlv.NewEncoder()
	enc.StartList(tlv.AnonymousTag())
	enc.PutUint(tlv.ContextTag(tagCmdPathCommand), 1)
	_ = enc.EndContainer()
	wire, _ := enc.Bytes()
	dec := tlv.NewDecoder(wire)
	_, err := UnmarshalCommandPathTLV(dec)
	if !errors.Is(err, ErrInvalidPath) {
		t.Fatalf("err=%v, want ErrInvalidPath", err)
	}
}

// --- StatusIB ---

// TestStatusIBRoundTripWithClusterStatus locks the optional
// ClusterStatus byte that rides next to the IM status code.
func TestStatusIBRoundTripWithClusterStatus(t *testing.T) {
	in := StatusIB{Status: StatusFailure, ClusterStatus: 0x42, HasClusterStatus: true}
	enc := tlv.NewEncoder()
	in.MarshalTLV(enc, tlv.AnonymousTag())
	wire, _ := enc.Bytes()
	dec := tlv.NewDecoder(wire)
	out, err := UnmarshalStatusIBTLV(dec)
	if err != nil {
		t.Fatal(err)
	}
	if out != in {
		t.Fatalf("got %+v, want %+v", out, in)
	}
}

// TestStatusIBRequiresStatusField — Status is mandatory; missing
// surfaces ErrInvalidStatusIB.
func TestStatusIBRequiresStatusField(t *testing.T) {
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	_ = enc.EndContainer()
	wire, _ := enc.Bytes()
	dec := tlv.NewDecoder(wire)
	_, err := UnmarshalStatusIBTLV(dec)
	if !errors.Is(err, ErrInvalidStatusIB) {
		t.Fatalf("err=%v, want ErrInvalidStatusIB", err)
	}
}

// --- ReadRequest ---

// TestReadRequestRoundTrip covers the typical commissioner request:
// one fully-qualified path, FabricFiltered=true.
func TestReadRequestRoundTrip(t *testing.T) {
	in := ReadRequest{
		AttributeRequests: []ConcreteAttributePath{
			{Endpoint: 1, HasEndpoint: true, Cluster: 0x0006, HasCluster: true, Attribute: 0, HasAttribute: true},
		},
		FabricFiltered: true,
	}
	enc := tlv.NewEncoder()
	in.MarshalTLV(enc)
	wire, _ := enc.Bytes()
	dec := tlv.NewDecoder(wire)
	out, err := UnmarshalReadRequestTLV(dec)
	if err != nil {
		t.Fatal(err)
	}
	if !out.FabricFiltered || len(out.AttributeRequests) != 1 {
		t.Fatalf("decoded %+v", out)
	}
	if out.AttributeRequests[0] != in.AttributeRequests[0] {
		t.Fatalf("path mismatch: %+v vs %+v", out.AttributeRequests[0], in.AttributeRequests[0])
	}
}

// --- Dispatcher round-trip ---

// fakeDispatcher implements the smallest surface that exercises every
// branch of the IM handlers.
type fakeDispatcher struct {
	readVal    AttributeValue
	readStat   StatusCode
	writeStat  StatusCode
	invokeRes  any
	invokeStat StatusCode
}

func (d *fakeDispatcher) Read(_ context.Context, p ConcreteAttributePath) []ReadResult {
	return []ReadResult{{Path: p, Value: d.readVal, Status: d.readStat}}
}

func (d *fakeDispatcher) Write(_ context.Context, p ConcreteAttributePath, _ AttributeValue) []WriteResult {
	return []WriteResult{{Path: p, Status: d.writeStat}}
}

func (d *fakeDispatcher) Invoke(_ context.Context, p ConcreteCommandPath, _ any) InvokeResult {
	return InvokeResult{Path: p, Response: d.invokeRes, Status: d.invokeStat}
}

// TestHandleReadRequestSuccessProducesData routes a successful read
// through to a data-bearing AttributeReport.
func TestHandleReadRequestSuccessProducesData(t *testing.T) {
	d := &fakeDispatcher{
		readVal:  AttributeValue{Value: true},
		readStat: StatusSuccess,
	}
	req := ReadRequest{
		AttributeRequests: []ConcreteAttributePath{
			{Endpoint: 1, HasEndpoint: true, Cluster: 0x0006, HasCluster: true, Attribute: 0, HasAttribute: true},
		},
	}
	rd := HandleReadRequest(context.Background(), d, req)
	if len(rd.Reports) != 1 {
		t.Fatalf("reports=%d, want 1", len(rd.Reports))
	}
	if rd.Reports[0].IsStatus {
		t.Fatal("success report flagged as status")
	}
	if v, ok := rd.Reports[0].Value.Value.(bool); !ok || !v {
		t.Fatalf("value=%v, want true", rd.Reports[0].Value.Value)
	}
}

// TestHandleReadRequestFailureProducesStatus routes a Dispatcher
// failure into an AttributeStatusIB-style report.
func TestHandleReadRequestFailureProducesStatus(t *testing.T) {
	d := &fakeDispatcher{readStat: StatusUnsupportedAttribute}
	req := ReadRequest{
		AttributeRequests: []ConcreteAttributePath{
			{Endpoint: 1, HasEndpoint: true, Cluster: 0x0006, HasCluster: true, Attribute: 0x99, HasAttribute: true},
		},
	}
	rd := HandleReadRequest(context.Background(), d, req)
	if len(rd.Reports) != 1 || !rd.Reports[0].IsStatus {
		t.Fatalf("expected one status report, got %+v", rd.Reports)
	}
	if rd.Reports[0].Status.Status != StatusUnsupportedAttribute {
		t.Fatalf("status=%v, want UnsupportedAttribute", rd.Reports[0].Status.Status)
	}
}

// TestHandleWriteRequestRoutesValue routes a write through to the
// dispatcher and surfaces its status.
func TestHandleWriteRequestRoutesValue(t *testing.T) {
	d := &fakeDispatcher{writeStat: StatusSuccess}
	req := WriteRequest{
		Writes: []AttributeWrite{
			{
				Path: ConcreteAttributePath{
					Endpoint: 1, HasEndpoint: true,
					Cluster: 0x0006, HasCluster: true,
					Attribute: 0, HasAttribute: true,
				},
				Value: AttributeValue{Value: true},
			},
		},
	}
	wr := HandleWriteRequest(context.Background(), d, req)
	if len(wr.Responses) != 1 {
		t.Fatalf("responses=%d, want 1", len(wr.Responses))
	}
	if !wr.Responses[0].Status.Status.IsSuccess() {
		t.Fatalf("status=%v, want Success", wr.Responses[0].Status.Status)
	}
}

// TestHandleInvokeRequestSuccessWithResponse covers the "command with
// payload" path.
func TestHandleInvokeRequestSuccessWithResponse(t *testing.T) {
	type cmdRsp struct{ Result int }
	d := &fakeDispatcher{
		invokeRes:  cmdRsp{Result: 7},
		invokeStat: StatusSuccess,
	}
	req := InvokeRequest{
		Invokes: []CommandInvocation{
			{Path: ConcreteCommandPath{Cluster: 0x0006, HasCluster: true, Command: 1, HasCommand: true}},
		},
	}
	ir := HandleInvokeRequest(context.Background(), d, req)
	if len(ir.Responses) != 1 || ir.Responses[0].IsStatus {
		t.Fatalf("expected one data response, got %+v", ir.Responses)
	}
	rsp, ok := ir.Responses[0].Response.(cmdRsp)
	if !ok || rsp.Result != 7 {
		t.Fatalf("response=%+v, want {Result:7}", ir.Responses[0].Response)
	}
}

// TestHandleInvokeRequestStatusOnlySuccess covers the "command with
// no payload" path — the IM layer SHOULD synthesise a Success
// CommandStatusIB so the controller knows the command landed.
func TestHandleInvokeRequestStatusOnlySuccess(t *testing.T) {
	d := &fakeDispatcher{invokeStat: StatusSuccess}
	req := InvokeRequest{
		Invokes: []CommandInvocation{
			{Path: ConcreteCommandPath{Cluster: 0x0006, HasCluster: true, Command: 0, HasCommand: true}},
		},
	}
	ir := HandleInvokeRequest(context.Background(), d, req)
	if len(ir.Responses) != 1 || !ir.Responses[0].IsStatus {
		t.Fatalf("expected one status-only response, got %+v", ir.Responses)
	}
	if ir.Responses[0].Status.Status != StatusSuccess {
		t.Fatalf("status=%v, want Success", ir.Responses[0].Status.Status)
	}
}

// TestHandleInvokeRequestFailureProducesStatusIB routes a
// Dispatcher-reported UnsupportedCommand through to a CommandStatusIB.
func TestHandleInvokeRequestFailureProducesStatusIB(t *testing.T) {
	d := &fakeDispatcher{invokeStat: StatusUnsupportedCommand}
	req := InvokeRequest{
		Invokes: []CommandInvocation{
			{Path: ConcreteCommandPath{Cluster: 0x0006, HasCluster: true, Command: 0xFF, HasCommand: true}},
		},
	}
	ir := HandleInvokeRequest(context.Background(), d, req)
	if !ir.Responses[0].IsStatus || ir.Responses[0].Status.Status != StatusUnsupportedCommand {
		t.Fatalf("got %+v", ir.Responses[0])
	}
}

// --- SubscribeRequest ---

// TestSubscribeRequestRoundTrip locks the basic subscribe message
// shape (cadence + path).
func TestSubscribeRequestRoundTrip(t *testing.T) {
	in := SubscribeRequest{
		KeepSubscriptions:  true,
		MinIntervalFloor:   0,
		MaxIntervalCeiling: 60,
		AttributeRequests: []ConcreteAttributePath{
			{Endpoint: 1, HasEndpoint: true, Cluster: 0x0006, HasCluster: true, Attribute: 0, HasAttribute: true},
		},
	}
	enc := tlv.NewEncoder()
	in.MarshalTLV(enc)
	wire, _ := enc.Bytes()
	dec := tlv.NewDecoder(wire)
	out, err := UnmarshalSubscribeRequestTLV(dec)
	if err != nil {
		t.Fatal(err)
	}
	if out.KeepSubscriptions != in.KeepSubscriptions ||
		out.MinIntervalFloor != in.MinIntervalFloor ||
		out.MaxIntervalCeiling != in.MaxIntervalCeiling {
		t.Fatalf("got %+v", out)
	}
	if len(out.AttributeRequests) != 1 || out.AttributeRequests[0] != in.AttributeRequests[0] {
		t.Fatalf("paths drifted: %+v", out.AttributeRequests)
	}
}

// --- Status code helpers ---

// TestStatusCodeStrings covers the diagnostic surface — drift here
// breaks observability tooling more than user-facing behaviour, but
// the named codes form a small contract worth pinning.
func TestStatusCodeStrings(t *testing.T) {
	cases := map[StatusCode]string{
		StatusSuccess:              "Success",
		StatusFailure:              "Failure",
		StatusUnsupportedAttribute: "UnsupportedAttribute",
		StatusBusy:                 "Busy",
		StatusCode(0xAA):           "Status(0xAA)",
	}
	for s, want := range cases {
		if got := s.String(); got != want {
			t.Errorf("%v: got %q, want %q", s, got, want)
		}
	}
}

// TestStatusCodeIsSuccess covers the IsSuccess() short-circuit used
// at every Dispatcher call site.
func TestStatusCodeIsSuccess(t *testing.T) {
	if !StatusSuccess.IsSuccess() {
		t.Fatal("StatusSuccess.IsSuccess() = false")
	}
	if StatusFailure.IsSuccess() {
		t.Fatal("StatusFailure.IsSuccess() = true")
	}
}

// TestHandleInvokeRequest_CommandRef_AbsentWhenNotInRequest verifies the
// CommandRef echo behavior.
//
// matter.js ref:
//
//	packages/protocol/src/interaction/InteractionMessenger.ts:1060-1080
//	— CommandRef is only emitted in InvokeResponseIB when it was present
//	in the inbound CommandDataIB; absent tag → absent echo.
//
// chip ref:
//
//	src/app/CommandHandlerImpl.cpp — CommandRef (tag 0x02) written only
//	when the inbound CommandDataIB carried one.
//
// Guard is `HasCommandRef` on [CommandInvocation]; this test verifies
// [HandleInvokeRequest] propagates the flag correctly through
// [InvokeResponseEntry.HasCommandRef] for both the present and absent cases.
func TestHandleInvokeRequest_CommandRef_AbsentWhenNotInRequest(t *testing.T) {
	t.Parallel()

	d := &fakeDispatcher{invokeStat: StatusSuccess}
	path := ConcreteCommandPath{Cluster: 0x0006, HasCluster: true, Command: 0x01, HasCommand: true}

	t.Run("absent_in_request_absent_in_response", func(t *testing.T) {
		t.Parallel()
		req := InvokeRequest{
			Invokes: []CommandInvocation{
				// HasCommandRef = false (zero value) — no CommandRef in the request.
				{Path: path},
			},
		}
		ir := HandleInvokeRequest(context.Background(), d, req)
		if len(ir.Responses) != 1 {
			t.Fatalf("responses=%d, want 1", len(ir.Responses))
		}
		if ir.Responses[0].HasCommandRef {
			t.Fatalf("HasCommandRef=true in response when request had none")
		}
		if ir.Responses[0].CommandRef != 0 {
			t.Fatalf("CommandRef=%d, want 0 (should be zero when absent)", ir.Responses[0].CommandRef)
		}
	})

	t.Run("present_in_request_echoed_in_response", func(t *testing.T) {
		t.Parallel()
		const wantRef uint16 = 0x1234
		req := InvokeRequest{
			Invokes: []CommandInvocation{
				{Path: path, CommandRef: wantRef, HasCommandRef: true},
			},
		}
		ir := HandleInvokeRequest(context.Background(), d, req)
		if len(ir.Responses) != 1 {
			t.Fatalf("responses=%d, want 1", len(ir.Responses))
		}
		if !ir.Responses[0].HasCommandRef {
			t.Fatal("HasCommandRef=false in response when request carried a CommandRef")
		}
		if ir.Responses[0].CommandRef != wantRef {
			t.Fatalf("CommandRef=%#x, want %#x", ir.Responses[0].CommandRef, wantRef)
		}
	})
}
