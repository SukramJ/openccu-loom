// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package rpcserver

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
)

// newTestXMLRPCServer starts an XMLRPCServer on 127.0.0.1:0, registers
// h under centralName and returns the server, its base URL, and a
// cleanup function.
func newTestXMLRPCServer(t *testing.T, centralName string, h Handlers) (server *XMLRPCServer, baseURL string) {
	t.Helper()
	srv, err := NewXMLRPCServer(XMLRPCConfig{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	srv.Register(centralName, h)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()

	t.Cleanup(func() {
		cancel()
		<-done
	})

	return srv, "http://" + srv.Addr().String() + "/RPC2/" + centralName
}

// makeEventCall returns a single multicall sub-call struct for the
// "event" method with the given interface/channel/param/value.
func makeEventCall(iface, channel, param string, value xmlrpc.Value) xmlrpc.StructValue {
	return xmlrpc.StructValue{Members: []xmlrpc.Member{
		{Name: "methodName", Value: xmlrpc.StringValue("event")},
		{Name: "params", Value: xmlrpc.ArrayValue{
			xmlrpc.StringValue(iface),
			xmlrpc.StringValue(channel),
			xmlrpc.StringValue(param),
			value,
		}},
	}}
}

// TestSystemMulticallDispatchesAllSubCalls sends a multicall containing
// three event sub-calls and verifies that all three are dispatched to
// the Handlers implementation and that the result array has three
// success entries.
func TestSystemMulticallDispatchesAllSubCalls(t *testing.T) {
	h := &stubHandlers{}
	_, url := newTestXMLRPCServer(t, "main", h)

	client, err := xmlrpc.NewClient(xmlrpc.Config{URL: url})
	if err != nil {
		t.Fatal(err)
	}

	calls := xmlrpc.ArrayValue{
		makeEventCall("HmIP-RF", "ABC:1", "LEVEL", xmlrpc.DoubleValue(0.5)),
		makeEventCall("HmIP-RF", "ABC:2", "STATE", xmlrpc.BoolValue(true)),
		makeEventCall("HmIP-RF", "ABC:3", "RSSI_DEVICE", xmlrpc.IntValue(-60)),
	}

	v, err := client.Call(context.Background(), "system.multicall", []xmlrpc.Value{calls})
	if err != nil {
		t.Fatalf("system.multicall: %v", err)
	}

	results, err := xmlrpc.AsArray(v)
	if err != nil {
		t.Fatalf("result not array: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("want 3 results, got %d", len(results))
	}

	// Every sub-call succeeded: each result must be a single-element
	// array (XML-RPC multicall success convention).
	for i, r := range results {
		if _, err := xmlrpc.AsArray(r); err != nil {
			t.Errorf("result[%d]: expected success array, got %T (%v)", i, r, r)
		}
	}

	// All three event handler calls must have been counted.
	if got := h.events.Load(); got != 3 {
		t.Fatalf("events dispatched=%d, want 3", got)
	}
}

// TestSystemMulticallContinuesAfterUnknownMethod verifies that an
// unknown method in position 1 (middle) produces a fault struct in the
// result array while the surrounding sub-calls succeed normally.
func TestSystemMulticallContinuesAfterUnknownMethod(t *testing.T) {
	h := &stubHandlers{}
	_, url := newTestXMLRPCServer(t, "main", h)

	client, err := xmlrpc.NewClient(xmlrpc.Config{URL: url})
	if err != nil {
		t.Fatal(err)
	}

	calls := xmlrpc.ArrayValue{
		makeEventCall("HmIP-RF", "ABC:1", "LEVEL", xmlrpc.DoubleValue(1.0)),
		// unknown method
		xmlrpc.StructValue{Members: []xmlrpc.Member{
			{Name: "methodName", Value: xmlrpc.StringValue("nosuchmethod")},
			{Name: "params", Value: xmlrpc.ArrayValue{}},
		}},
		makeEventCall("HmIP-RF", "ABC:3", "STATE", xmlrpc.BoolValue(false)),
	}

	v, err := client.Call(context.Background(), "system.multicall", []xmlrpc.Value{calls})
	if err != nil {
		t.Fatalf("system.multicall: %v", err)
	}

	results, err := xmlrpc.AsArray(v)
	if err != nil {
		t.Fatalf("result not array: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("want 3 results, got %d", len(results))
	}

	// Position 0 — success.
	if _, err := xmlrpc.AsArray(results[0]); err != nil {
		t.Errorf("result[0]: expected success array, got %T", results[0])
	}

	// Position 1 — fault struct (unknown method).
	faultStruct, err := xmlrpc.AsStruct(results[1])
	if err != nil {
		t.Fatalf("result[1]: expected fault struct, got %T (%v)", results[1], results[1])
	}
	code, err := xmlrpc.StructField[xmlrpc.IntValue](faultStruct, "faultCode")
	if err != nil {
		t.Fatalf("result[1] faultCode: %v", err)
	}
	if int(code) == 0 {
		t.Errorf("result[1] faultCode must be non-zero")
	}

	// Position 2 — success.
	if _, err := xmlrpc.AsArray(results[2]); err != nil {
		t.Errorf("result[2]: expected success array, got %T", results[2])
	}

	// Two event sub-calls reached the handler; the unknown-method one did not.
	if got := h.events.Load(); got != 2 {
		t.Fatalf("events dispatched=%d, want 2", got)
	}
}

// TestSystemMulticallWithMalformedSubCall sends a sub-call struct that
// is missing the "params" field and verifies that the multicall returns
// a fault for that entry (or fails at the top-level with an error).
func TestSystemMulticallWithMalformedSubCall(t *testing.T) {
	h := &stubHandlers{}
	_, url := newTestXMLRPCServer(t, "main", h)

	client, err := xmlrpc.NewClient(xmlrpc.Config{URL: url})
	if err != nil {
		t.Fatal(err)
	}

	// Sub-call without "params" field.
	malformed := xmlrpc.ArrayValue{
		xmlrpc.StructValue{Members: []xmlrpc.Member{
			{Name: "methodName", Value: xmlrpc.StringValue("event")},
			// "params" intentionally omitted
		}},
	}

	// The server returns either a top-level error or a fault in the
	// result array — either is acceptable since the call is malformed.
	v, callErr := client.Call(context.Background(), "system.multicall", []xmlrpc.Value{malformed})
	if callErr != nil {
		// Top-level error is fine — malformed input.
		return
	}

	// If a value was returned it must contain a fault struct.
	results, err := xmlrpc.AsArray(v)
	if err != nil {
		t.Fatalf("unexpected result shape: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected at least one result entry for the malformed sub-call")
	}
	if _, err := xmlrpc.AsStruct(results[0]); err != nil {
		t.Errorf("expected fault struct for malformed sub-call, got %T", results[0])
	}
}

// TestSystemMulticallEmptyArray sends an empty multicall array and
// verifies the result is an empty array (no error, zero entries).
func TestSystemMulticallEmptyArray(t *testing.T) {
	h := &stubHandlers{}
	_, url := newTestXMLRPCServer(t, "main", h)

	client, err := xmlrpc.NewClient(xmlrpc.Config{URL: url})
	if err != nil {
		t.Fatal(err)
	}

	v, err := client.Call(context.Background(), "system.multicall", []xmlrpc.Value{
		xmlrpc.ArrayValue{}, // empty calls list
	})
	if err != nil {
		t.Fatalf("system.multicall with empty array: %v", err)
	}

	results, err := xmlrpc.AsArray(v)
	if err != nil {
		t.Fatalf("result not array: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("want 0 results for empty multicall, got %d", len(results))
	}

	// No events must have been dispatched.
	if got := h.events.Load(); got != 0 {
		t.Fatalf("events dispatched=%d, want 0", got)
	}
}
