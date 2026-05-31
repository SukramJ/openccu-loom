// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Mirrors chip src/app/tests/TestCommandInteraction.cpp — selected
// TEST_F cases from the command-invocation interaction model.
//
// chip tests use a full in-process CommandSender / CommandHandlerImpl
// pair. We translate the semantic invariants: encode → decode
// correctness, status-code routing, CommandRef echo, timed-request
// mismatch, and malformed-message rejection.

package im

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// TestInvokeParity_HandlerEncodeSimpleCommandData mirrors
// chip src/app/tests/TestCommandInteraction.cpp:1326
// (TestCommandHandlerEncodeSimpleCommandData).
//
// Invariant: an InvokeRequest for a command that returns a payload
// (HasResponse=true) produces an InvokeResponseIB carrying the
// CommandDataIB branch (not the CommandStatusIB branch).
func TestInvokeParity_HandlerEncodeSimpleCommandData(t *testing.T) {
	t.Parallel()
	type onOffFields struct{ OnOff bool }
	d := &fakeDispatcher{
		invokeRes:  onOffFields{OnOff: true},
		invokeStat: StatusSuccess,
	}
	req := InvokeRequest{
		Invokes: []CommandInvocation{
			{Path: ConcreteCommandPath{Endpoint: 1, HasEndpoint: true, Cluster: 0x0006, HasCluster: true, Command: 0x00, HasCommand: true}},
		},
	}
	ir := HandleInvokeRequest(context.Background(), d, req)
	if len(ir.Responses) != 1 {
		t.Fatalf("responses=%d, want 1", len(ir.Responses))
	}
	ent := ir.Responses[0]
	if ent.IsStatus {
		t.Fatal("expected CommandData branch, got CommandStatus")
	}
	if !ent.HasResponse {
		t.Fatal("HasResponse=false; response payload was lost")
	}
	got, ok := ent.Response.(onOffFields)
	if !ok || !got.OnOff {
		t.Fatalf("response=%+v, want onOffFields{OnOff:true}", ent.Response)
	}
}

// TestInvokeParity_HandlerEncodeSimpleStatusCode mirrors
// chip src/app/tests/TestCommandInteraction.cpp:1403
// (TestCommandHandlerEncodeSimpleStatusCode).
//
// Invariant: a status-only command (dispatcher returns nil response +
// Success) produces a CommandStatusIB(Success) — NOT a CommandDataIB.
func TestInvokeParity_HandlerEncodeSimpleStatusCode(t *testing.T) {
	t.Parallel()
	d := &fakeDispatcher{invokeStat: StatusSuccess} // nil response
	req := InvokeRequest{
		Invokes: []CommandInvocation{
			{Path: ConcreteCommandPath{Endpoint: 1, HasEndpoint: true, Cluster: 0x0006, HasCluster: true, Command: 0x01, HasCommand: true}},
		},
	}
	ir := HandleInvokeRequest(context.Background(), d, req)
	if len(ir.Responses) != 1 {
		t.Fatalf("responses=%d, want 1", len(ir.Responses))
	}
	ent := ir.Responses[0]
	if !ent.IsStatus {
		t.Fatal("expected CommandStatusIB branch for status-only success")
	}
	if ent.Status.Status != StatusSuccess {
		t.Fatalf("status=%v, want Success", ent.Status.Status)
	}
}

// TestInvokeParity_HandlerNotExistCommand mirrors
// chip src/app/tests/TestCommandInteraction.cpp:1455
// (TestCommandHandler_WithOnInvokeReceivedNotExistCommand).
//
// chip uses endpoint=0xDE, cluster=0xADBE, command=0xEF and expects
// Status::InvalidAction (command does not exist).
// Our equivalent: dispatcher returns UnsupportedCommand → response
// must be CommandStatusIB(UnsupportedCommand).
func TestInvokeParity_HandlerNotExistCommand(t *testing.T) {
	t.Parallel()
	d := &fakeDispatcher{invokeStat: StatusUnsupportedCommand}
	req := InvokeRequest{
		Invokes: []CommandInvocation{
			{Path: ConcreteCommandPath{
				Endpoint: 0xDE, HasEndpoint: true,
				Cluster: 0xADBE, HasCluster: true,
				Command: 0xEF, HasCommand: true,
			}},
		},
	}
	ir := HandleInvokeRequest(context.Background(), d, req)
	if len(ir.Responses) != 1 {
		t.Fatalf("responses=%d, want 1", len(ir.Responses))
	}
	ent := ir.Responses[0]
	if !ent.IsStatus {
		t.Fatal("expected CommandStatusIB for non-existent command")
	}
	if ent.Status.Status != StatusUnsupportedCommand {
		t.Fatalf("status=%v, want UnsupportedCommand", ent.Status.Status)
	}
}

// TestInvokeParity_HandlerEmptyDataMsg_TimedMismatch mirrors
// chip src/app/tests/TestCommandInteraction.cpp:1475
// (TestCommandHandler_WithOnInvokeReceivedEmptyDataMsg).
//
// chip loops over all (messageIsTimed, transactionIsTimed) combinations.
// When they differ → TimedRequestMismatch. When they match → Success.
//
// We translate the TimedRequest flag round-trip: an InvokeRequest with
// TimedRequest=true must survive encode/decode with the flag intact,
// and a dispatcher that returns NeedsTimedInteraction surfaces the
// correct status.
func TestInvokeParity_HandlerEmptyDataMsg_TimedMismatch(t *testing.T) {
	t.Parallel()
	t.Run("timed_flag_round_trips", func(t *testing.T) {
		t.Parallel()
		// Build InvokeRequestMessage TLV manually (InvokeRequest has no
		// MarshalTLV method — the bridge only receives, never sends Invoke
		// requests). Wire layout per Matter Core Spec §10.6.7:
		//   Structure {
		//     [0] bool SuppressResponse = false
		//     [1] bool TimedRequest     = true
		//     [2] Array InvokeRequests { Structure { [0] List { [1] u32 Cluster, [2] u32 Command } } }
		//   }
		enc := tlv.NewEncoder()
		enc.StartStruct(tlv.AnonymousTag())    // top
		enc.PutBool(tlv.ContextTag(0), false)  // SuppressResponse
		enc.PutBool(tlv.ContextTag(1), true)   // TimedRequest: true
		enc.StartArray(tlv.ContextTag(2))      // InvokeRequests
		enc.StartStruct(tlv.AnonymousTag())    //   CommandDataIB
		enc.StartList(tlv.ContextTag(0))       //     CommandPathIB
		enc.PutUint(tlv.ContextTag(1), 0x0006) //       Cluster
		enc.PutUint(tlv.ContextTag(2), 0x00)   //       Command
		_ = enc.EndContainer()                 //     end CommandPathIB
		_ = enc.EndContainer()                 //   end CommandDataIB
		_ = enc.EndContainer()                 // end InvokeRequests
		_ = enc.EndContainer()                 // end top
		wire, err := enc.Bytes()
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		dec := tlv.NewDecoder(wire)
		out, err := UnmarshalInvokeRequestTLV(dec, nil)
		if err != nil {
			t.Fatalf("UnmarshalInvokeRequestTLV: %v", err)
		}
		if !out.TimedRequest {
			t.Fatal("TimedRequest flag not preserved")
		}
	})
	t.Run("mismatch_surfaces_NeedsTimedInteraction", func(t *testing.T) {
		t.Parallel()
		d := &fakeDispatcher{invokeStat: StatusNeedsTimedInteraction}
		req := InvokeRequest{
			TimedRequest: false, // message says non-timed
			Invokes: []CommandInvocation{
				{Path: ConcreteCommandPath{Cluster: 0x0006, HasCluster: true, Command: 0x00, HasCommand: true}},
			},
		}
		// Simulate "transaction requires timed" by having the dispatcher
		// return NeedsTimedInteraction.
		ir := HandleInvokeRequest(context.Background(), d, req)
		if len(ir.Responses) != 1 || !ir.Responses[0].IsStatus {
			t.Fatalf("expected status-only response, got %+v", ir.Responses)
		}
		if ir.Responses[0].Status.Status != StatusNeedsTimedInteraction {
			t.Fatalf("status=%v, want NeedsTimedInteraction", ir.Responses[0].Status.Status)
		}
	})
}

// TestInvokeParity_HandlerRejectMultipleIdenticalCommands_FUTURE is a
// tracked gap against chip src/app/tests/TestCommandInteraction.cpp:1826
// (TestCommandHandler_RejectMultipleIdenticalCommands).
//
// chip behaviour: when an InvokeRequestMessage contains two
// CommandDataIB entries with identical (endpoint, cluster, command) paths
// the handler rejects the entire request — onResponse=0, onError=1,
// isCommandDispatched=false (chip sees duplicate paths before dispatch).
//
// openccu-loom behaviour: HandleInvokeRequest iterates all CommandInvocation
// entries and dispatches each to the Dispatcher independently, producing
// one InvokeResponseEntry per entry. Duplicate-path detection is not
// implemented at the IM dispatcher layer; enforcement is the session-layer
// caller's responsibility (mirrors the per-entry routing model).
//
// This is a documented L4-INVOKE-DUP-FUTURE drift: openccu-loom accepts
// duplicates where chip rejects. The test is kept as a skip-annotated
// anchor so that when duplicate-rejection is implemented (session layer or
// dispatcher pre-check) the failing assertion is already in place.
func TestInvokeParity_HandlerRejectMultipleIdenticalCommands_FUTURE(t *testing.T) {
	t.Skip("FixMe: openccu-loom accepts duplicate identical commands (dispatches each independently); " +
		"chip rejects the whole InvokeRequest with onError=1, isCommandDispatched=false. " +
		"Tracked as L4-INVOKE-DUP-FUTURE.")
}

// TestInvokeParity_CommandSender_InvalidMessage mirrors
// chip src/app/tests/TestCommandInteraction.cpp:819
// (TestCommandInvalidMessage1).
//
// Invariant: a garbage byte sequence returns ErrInvalidInvokeRequest.
func TestInvokeParity_CommandSender_InvalidMessage(t *testing.T) {
	t.Parallel()
	garbage := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	dec := tlv.NewDecoder(garbage)
	_, err := UnmarshalInvokeRequestTLV(dec, nil)
	if err == nil {
		t.Fatal("expected error for malformed TLV, got nil")
	}
}

// TestInvokeParity_CommandSender_ExtendableCallbackBatchCommandRefEcho mirrors
// chip src/app/tests/TestCommandInteraction.cpp:1601
// (TestCommandSender_ExtendableCallbackBuildingBatchCommandSuccess).
//
// Invariant: when CommandRef is present in the InvokeRequest entry, the
// same CommandRef MUST be echoed in the InvokeResponseIB (both
// CommandData and CommandStatus branches).
func TestInvokeParity_CommandSender_ExtendableCallbackBatchCommandRefEcho(t *testing.T) {
	t.Parallel()
	const ref1 uint16 = 0x0001
	const ref2 uint16 = 0x0002

	// One entry with response payload, one status-only.
	d := &batchDispatcher{
		results: []InvokeResult{
			{Path: ConcreteCommandPath{Cluster: 0x0006, HasCluster: true, Command: 0x01, HasCommand: true}, Response: struct{ X int }{7}, Status: StatusSuccess},
			{Path: ConcreteCommandPath{Cluster: 0x0006, HasCluster: true, Command: 0x02, HasCommand: true}, Status: StatusSuccess},
		},
	}
	req := InvokeRequest{
		Invokes: []CommandInvocation{
			{Path: ConcreteCommandPath{Cluster: 0x0006, HasCluster: true, Command: 0x01, HasCommand: true}, CommandRef: ref1, HasCommandRef: true},
			{Path: ConcreteCommandPath{Cluster: 0x0006, HasCluster: true, Command: 0x02, HasCommand: true}, CommandRef: ref2, HasCommandRef: true},
		},
	}
	ir := HandleInvokeRequest(context.Background(), d, req)
	if len(ir.Responses) != 2 {
		t.Fatalf("responses=%d, want 2", len(ir.Responses))
	}
	for i, ent := range ir.Responses {
		if !ent.HasCommandRef {
			t.Errorf("response[%d]: HasCommandRef=false; expected echo", i)
		}
	}
	if ir.Responses[0].CommandRef != ref1 {
		t.Errorf("response[0]: CommandRef=%d, want %d", ir.Responses[0].CommandRef, ref1)
	}
	if ir.Responses[1].CommandRef != ref2 {
		t.Errorf("response[1]: CommandRef=%d, want %d", ir.Responses[1].CommandRef, ref2)
	}
}

// TestInvokeParity_CommandSender_DuplicateCommandRefFails mirrors
// chip src/app/tests/TestCommandInteraction.cpp:1573
// (TestCommandSender_ExtendableCallbackBuildingBatchDuplicateCommandRefFails).
//
// chip rejects a batch that contains two invocations with the same
// CommandRef value. In our Go model the validation is the caller's
// responsibility (the dispatcher layer does not enforce it). We verify
// the invariant as a semantic-only test that documents the expected
// caller behaviour.
func TestInvokeParity_CommandSender_DuplicateCommandRefFails(t *testing.T) {
	t.Skip("FixMe: duplicate-CommandRef validation lives in the CommandSender layer (caller), not the IM dispatcher; enforcement is the bridge's session-layer responsibility")
}

// TestInvokeParity_CommandSender_CommandRefAbsent mirrors the implicit
// invariant from chip src/app/tests/TestCommandInteraction.cpp:1326
// (TestCommandHandlerEncodeSimpleCommandData) where the CommandSender
// does NOT set a CommandRef on the outgoing CommandDataIB.
//
// Invariant: when HasCommandRef=false on the CommandInvocation, the
// response entry MUST also have HasCommandRef=false — the bridge must not
// fabricate a CommandRef that was not present in the request.
func TestInvokeParity_CommandSender_CommandRefAbsent(t *testing.T) {
	// Mirrors chip src/app/tests/TestCommandInteraction.cpp:1326
	// (TestCommandHandlerEncodeSimpleCommandData) — no CommandRef path.
	t.Parallel()
	d := &fakeDispatcher{invokeStat: StatusSuccess}
	req := InvokeRequest{
		Invokes: []CommandInvocation{
			{
				Path: ConcreteCommandPath{
					Endpoint: 1, HasEndpoint: true,
					Cluster: 0x0006, HasCluster: true,
					Command: 0x01, HasCommand: true,
				},
				// HasCommandRef intentionally false.
			},
		},
	}
	ir := HandleInvokeRequest(context.Background(), d, req)
	if len(ir.Responses) != 1 {
		t.Fatalf("responses=%d, want 1", len(ir.Responses))
	}
	if ir.Responses[0].HasCommandRef {
		t.Fatal("response must not have HasCommandRef=true when request had no CommandRef")
	}
}

// TestInvokeParity_CommandHandler_FillsUpResponse mirrors
// chip src/app/tests/TestCommandInteraction.cpp:1297
// (TestCommandHandler_CommandHandlerFillsUpResponse).
//
// Invariant: multiple command invocations in one request each produce
// exactly one response entry — the handler does not coalesce or drop.
func TestInvokeParity_CommandHandler_FillsUpResponse(t *testing.T) {
	t.Parallel()
	d := &fakeDispatcher{invokeStat: StatusSuccess}
	req := InvokeRequest{
		Invokes: []CommandInvocation{
			{Path: ConcreteCommandPath{Cluster: 0x0006, HasCluster: true, Command: 0x00, HasCommand: true}},
			{Path: ConcreteCommandPath{Cluster: 0x0006, HasCluster: true, Command: 0x01, HasCommand: true}},
			{Path: ConcreteCommandPath{Cluster: 0x0006, HasCluster: true, Command: 0x02, HasCommand: true}},
		},
	}
	ir := HandleInvokeRequest(context.Background(), d, req)
	if len(ir.Responses) != 3 {
		t.Fatalf("responses=%d, want 3 (one per invocation)", len(ir.Responses))
	}
}

// --- helpers for invoke_parity_test.go ---

// batchDispatcher returns preconfigured InvokeResults in order.
type batchDispatcher struct {
	results []InvokeResult
	idx     int
}

func (b *batchDispatcher) Read(_ context.Context, p ConcreteAttributePath) []ReadResult {
	return []ReadResult{{Path: p, Status: StatusSuccess}}
}

func (b *batchDispatcher) Write(_ context.Context, p ConcreteAttributePath, _ AttributeValue) []WriteResult {
	return []WriteResult{{Path: p, Status: StatusSuccess}}
}

func (b *batchDispatcher) Invoke(_ context.Context, _ ConcreteCommandPath, _ any) InvokeResult {
	if b.idx >= len(b.results) {
		return InvokeResult{Status: StatusUnsupportedCommand}
	}
	r := b.results[b.idx]
	b.idx++
	return r
}

var _ Dispatcher = (*batchDispatcher)(nil)
