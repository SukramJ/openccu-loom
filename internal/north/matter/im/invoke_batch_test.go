// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package im

import (
	"context"
	"testing"
)

// concretePath is a fully-qualified command path helper for the batch tests.
func concretePath(endpoint uint16, cluster, command uint32) ConcreteCommandPath {
	return ConcreteCommandPath{
		Endpoint: endpoint, HasEndpoint: true,
		Cluster: cluster, HasCluster: true,
		Command: command, HasCommand: true,
	}
}

// TestValidateInvokeBatch mirrors the batch-invoke guards in matter.js
// packages/protocol/src/action/server/CommandInvokeResponse.ts:64-92,171-185
// (process/#processConcrete): a wildcard-endpoint path in a batch, a concrete
// path missing its CommandRef in a batch, a duplicate CommandRef, and a
// duplicate concrete path all abort the whole invoke with InvalidAction, while
// a single command (wildcard or concrete) and a well-formed distinct-path batch
// pass.
func TestValidateInvokeBatch(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		req  InvokeRequest
		want StatusCode
	}{
		{
			name: "single_concrete_no_ref_ok",
			req: InvokeRequest{Invokes: []CommandInvocation{
				{Path: concretePath(1, 0x0006, 0x01)},
			}},
			want: StatusSuccess,
		},
		{
			name: "single_wildcard_endpoint_ok",
			req: InvokeRequest{Invokes: []CommandInvocation{
				{Path: ConcreteCommandPath{Cluster: 0x0006, HasCluster: true, Command: 0x01, HasCommand: true}},
			}},
			want: StatusSuccess,
		},
		{
			name: "batch_distinct_paths_with_refs_ok",
			req: InvokeRequest{Invokes: []CommandInvocation{
				{Path: concretePath(1, 0x0006, 0x01), CommandRef: 1, HasCommandRef: true},
				{Path: concretePath(2, 0x0006, 0x00), CommandRef: 2, HasCommandRef: true},
			}},
			want: StatusSuccess,
		},
		{
			name: "batch_missing_commandref_invalid",
			req: InvokeRequest{Invokes: []CommandInvocation{
				{Path: concretePath(1, 0x0006, 0x01), CommandRef: 1, HasCommandRef: true},
				{Path: concretePath(2, 0x0006, 0x00)}, // no CommandRef
			}},
			want: StatusInvalidAction,
		},
		{
			name: "batch_duplicate_commandref_invalid",
			req: InvokeRequest{Invokes: []CommandInvocation{
				{Path: concretePath(1, 0x0006, 0x01), CommandRef: 7, HasCommandRef: true},
				{Path: concretePath(2, 0x0006, 0x00), CommandRef: 7, HasCommandRef: true},
			}},
			want: StatusInvalidAction,
		},
		{
			name: "batch_duplicate_concrete_path_invalid",
			req: InvokeRequest{Invokes: []CommandInvocation{
				{Path: concretePath(1, 0x0006, 0x01), CommandRef: 1, HasCommandRef: true},
				{Path: concretePath(1, 0x0006, 0x01), CommandRef: 2, HasCommandRef: true},
			}},
			want: StatusInvalidAction,
		},
		{
			name: "batch_wildcard_endpoint_invalid",
			req: InvokeRequest{Invokes: []CommandInvocation{
				{Path: concretePath(1, 0x0006, 0x01), CommandRef: 1, HasCommandRef: true},
				{Path: ConcreteCommandPath{Cluster: 0x0006, HasCluster: true, Command: 0x00, HasCommand: true}, CommandRef: 2, HasCommandRef: true},
			}},
			want: StatusInvalidAction,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := ValidateInvokeBatch(tc.req); got != tc.want {
				t.Fatalf("ValidateInvokeBatch = %v, want %v", got, tc.want)
			}
		})
	}
}

// batchOfN builds an InvokeRequest with n distinct, individually
// well-formed CommandInvocations — each on its own endpoint with a
// distinct CommandRef — so a rejection can only be attributed to the
// path-count check, not to any of ValidateInvokeBatch's other guards.
func batchOfN(n int) InvokeRequest {
	invokes := make([]CommandInvocation, n)
	for i := range invokes {
		//nolint:gosec // test fixture, n bounded by callers below.
		invokes[i] = CommandInvocation{
			Path:          concretePath(uint16(i+1), 0x0006, 0x01),
			CommandRef:    uint16(i + 1),
			HasCommandRef: true,
		}
	}
	return InvokeRequest{Invokes: invokes}
}

// TestValidateInvokeBatch_MaxPathsPerInvoke pins the batch-size ceiling
// mirroring matter.js InteractionServer.ts:950-955
// (`if (invokeRequests.length > this.#maxPathsPerInvoke) throw new
// StatusResponseError(..., Status.InvalidAction)`), evaluated before any
// command in the batch is dispatched. A batch at exactly the ceiling
// passes; one past it is rejected regardless of every individual
// command being otherwise well-formed.
func TestValidateInvokeBatch_MaxPathsPerInvoke(t *testing.T) {
	t.Parallel()
	if got := ValidateInvokeBatch(batchOfN(DefaultMaxPathsPerInvoke)); got != StatusSuccess {
		t.Errorf("batch of %d (at ceiling): got %v, want StatusSuccess", DefaultMaxPathsPerInvoke, got)
	}
	if got := ValidateInvokeBatch(batchOfN(DefaultMaxPathsPerInvoke + 1)); got != StatusInvalidAction {
		t.Errorf("batch of %d (over ceiling): got %v, want StatusInvalidAction", DefaultMaxPathsPerInvoke+1, got)
	}
}

// TestInvokeResponse_HasCommandData verifies the CommandDataIB detection that
// gates the SuppressResponse "send nothing" decision (Matter §8.8.3.2.1;
// matter.js InteractionServer.ts:1043-1074): a response is "command data"
// bearing iff at least one entry is a CommandDataIB (IsStatus=false).
func TestInvokeResponse_HasCommandData(t *testing.T) {
	t.Parallel()
	statusOnly := InvokeResponse{Responses: []InvokeResponseEntry{
		{Path: concretePath(1, 0x0006, 0x01), IsStatus: true, Status: StatusIB{Status: StatusSuccess}},
	}}
	if statusOnly.HasCommandData() {
		t.Error("status-only InvokeResponse must report HasCommandData()=false")
	}
	withData := InvokeResponse{Responses: []InvokeResponseEntry{
		{Path: concretePath(1, 0x0006, 0x01), IsStatus: true, Status: StatusIB{Status: StatusSuccess}},
		{Path: concretePath(1, 0x0003, 0x00), HasResponse: true, Response: struct{ X int }{X: 1}},
	}}
	if !withData.HasCommandData() {
		t.Error("InvokeResponse carrying a CommandDataIB must report HasCommandData()=true")
	}
	if (InvokeResponse{}).HasCommandData() {
		t.Error("empty InvokeResponse must report HasCommandData()=false")
	}
}

// TestHandleInvokeRequest_ResponseSuppressResponseAlwaysFalse pins that the
// InvokeResponseMessage the bridge marshals never carries SuppressResponse=true
// even when the request set it: matter.js always emits the deprecated field as
// false (InteractionServer.ts:987-988 `emptyInvokeResponse.suppressResponse:
// false`) and lets the send/no-send decision live at the dispatch layer.
func TestHandleInvokeRequest_ResponseSuppressResponseAlwaysFalse(t *testing.T) {
	t.Parallel()
	d := &fakeDispatcher{invokeStat: StatusSuccess}
	req := InvokeRequest{
		SuppressResponse: true,
		Invokes:          []CommandInvocation{{Path: concretePath(1, 0x0006, 0x01)}},
	}
	ir := HandleInvokeRequest(context.Background(), d, req)
	if ir.SuppressResponse {
		t.Error("HandleInvokeRequest must not echo req.SuppressResponse into the response message")
	}
}
