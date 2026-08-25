// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// binrpc_multicall_test.go pins the BIN-RPC callback server against the
// envelope CUxD actually sends.
//
// CUxD never calls `event` directly. Every callback — a single value change
// included — arrives wrapped in `system.multicall`, recorded verbatim from a
// live CUxD in [cuxdPongEnvelope]. The server's dispatch used to read
// params[0] as the interface_id, which is a string for a bare call and an
// array for an envelope, so every CUxD push faulted and was dropped. Nothing
// noticed: the fault was logged below the default level, and no test ever
// sent the shape CUxD sends.
//
// The two guards here are deliberately different in kind. The first replays
// the recorded envelope end-to-end. The second holds `system.listMethods`
// and the dispatch switch together, so a method can never again be
// advertised to a peer that the server then refuses.

package rpcserver

import (
	"context"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client/transport/binrpc"
	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
)

// binrpcRecordingHandlers captures what the dispatch delivered, so a test can
// assert the sub-call arrived with its arguments intact rather than merely
// that the envelope did not fault.
type binrpcRecordingHandlers struct {
	mu            sync.Mutex
	events        [][]string
	newDeviceSets int
}

func (h *binrpcRecordingHandlers) Event(_ context.Context, iface, addr, param string, v xmlrpc.Value) error {
	val, _ := xmlrpc.AsString(v)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.events = append(h.events, []string{iface, addr, param, val})
	return nil
}

func (h *binrpcRecordingHandlers) NewDevices(context.Context, string, xmlrpc.ArrayValue) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.newDeviceSets++
	return nil
}

func (h *binrpcRecordingHandlers) DeleteDevices(context.Context, string, []string) error { return nil }

func (h *binrpcRecordingHandlers) UpdateDevice(context.Context, string, string, int) error {
	return nil
}

func (h *binrpcRecordingHandlers) ReplaceDevice(context.Context, string, string, string) error {
	return nil
}

func (h *binrpcRecordingHandlers) ReaddedDevice(context.Context, string, []string) error { return nil }

func (h *binrpcRecordingHandlers) Error(context.Context, string, int, string) error { return nil }

func (h *binrpcRecordingHandlers) ListDevices(context.Context, string) (xmlrpc.ArrayValue, error) {
	return xmlrpc.ArrayValue{}, nil
}

func (h *binrpcRecordingHandlers) recordedEvents() [][]string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([][]string, len(h.events))
	copy(out, h.events)
	return out
}

// subCall builds one multicall member in the layout CUxD uses: a struct with
// `methodName` and a `params` array whose first element is the interface_id.
func subCall(method string, params ...xmlrpc.Value) xmlrpc.StructValue {
	return xmlrpc.StructValue{Members: []xmlrpc.Member{
		{Name: "methodName", Value: xmlrpc.StringValue(method)},
		{Name: "params", Value: xmlrpc.ArrayValue(params)},
	}}
}

// cuxdPongEnvelope reproduces a callback captured from a live CUxD
// (192.0.2.39:8701) after an outbound `ping`:
//
//	system.multicall([{methodName: "event",
//	                   params: [<interface_id>, "CENTRAL", "PONG", <token>]}])
//
// The PONG is the case that matters most: it is what refreshes the
// callback-liveness timestamp, so a daemon that cannot parse this envelope
// declares a perfectly healthy CUxD dead after callbackFreshness.
func cuxdPongEnvelope(interfaceID string) []xmlrpc.Value {
	return []xmlrpc.Value{xmlrpc.ArrayValue{
		subCall(
			"event",
			xmlrpc.StringValue(interfaceID),
			xmlrpc.StringValue("CENTRAL"),
			xmlrpc.StringValue("PONG"),
			xmlrpc.StringValue(interfaceID+"#tok1"),
		),
	}}
}

// newBINRPCTestServer starts a BIN-RPC listener with h registered under
// interfaceID and returns a client pointed at it.
func newBINRPCTestServer(t *testing.T, interfaceID string, h Handlers) *binrpc.Client {
	t.Helper()
	srv, err := NewBINRPCServer(BINRPCConfig{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatalf("NewBINRPCServer: %v", err)
	}
	srv.Register(interfaceID, h)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()
	t.Cleanup(func() { cancel(); <-done; _ = srv.Close() })

	client, err := binrpc.NewClient(binrpc.Config{Addr: srv.Addr().String(), Interface: interfaceID})
	if err != nil {
		t.Fatalf("binrpc.NewClient: %v", err)
	}
	return client
}

// TestBINRPCServerDispatchesCUxDMulticallEnvelope replays the recorded CUxD
// callback and asserts the inner `event` reached the handler with its
// arguments intact. Removing the system.multicall case from dispatch turns
// this red — the envelope then faults with "first arg must be interface_id".
func TestBINRPCServerDispatchesCUxDMulticallEnvelope(t *testing.T) {
	t.Parallel()

	const ifaceID = "loom-ccu-CUxD"
	h := &binrpcRecordingHandlers{}
	client := newBINRPCTestServer(t, ifaceID, h)

	if _, err := client.Call(context.Background(), "system.multicall", cuxdPongEnvelope(ifaceID)); err != nil {
		t.Fatalf("CUxD multicall envelope must dispatch, got fault: %v", err)
	}

	got := h.recordedEvents()
	if len(got) != 1 {
		t.Fatalf("want exactly 1 delivered event, got %d (%v)", len(got), got)
	}
	want := []string{ifaceID, "CENTRAL", "PONG", ifaceID + "#tok1"}
	for i, w := range want {
		if got[0][i] != w {
			t.Errorf("event arg %d = %q, want %q (full: %v)", i, got[0][i], w, got[0])
		}
	}
}

// TestBINRPCServerMulticallBatchesAndIsolatesFailures sends a batch mixing a
// routable sub-call with one addressed to an unregistered interface. The good
// one must still be delivered and the batch must not fault as a whole:
// CUxD coalesces bursts, so one unroutable member discarding the batch would
// lose every real event alongside it.
func TestBINRPCServerMulticallBatchesAndIsolatesFailures(t *testing.T) {
	t.Parallel()

	const ifaceID = "loom-ccu-CUxD"
	h := &binrpcRecordingHandlers{}
	client := newBINRPCTestServer(t, ifaceID, h)

	batch := []xmlrpc.Value{xmlrpc.ArrayValue{
		subCall("event", xmlrpc.StringValue(ifaceID),
			xmlrpc.StringValue("CUX2801001:1"), xmlrpc.StringValue("STATE"), xmlrpc.StringValue("1")),
		subCall("event", xmlrpc.StringValue("some-other-daemon-CUxD"),
			xmlrpc.StringValue("CUX2801001:1"), xmlrpc.StringValue("STATE"), xmlrpc.StringValue("0")),
		subCall("event", xmlrpc.StringValue(ifaceID),
			xmlrpc.StringValue("CUX2801001:2"), xmlrpc.StringValue("STATE"), xmlrpc.StringValue("1")),
	}}

	res, err := client.Call(context.Background(), "system.multicall", batch)
	if err != nil {
		t.Fatalf("a batch with one unroutable member must not fault as a whole: %v", err)
	}
	arr, err := xmlrpc.AsArray(res)
	if err != nil {
		t.Fatalf("multicall result must be an array: %v", err)
	}
	if len(arr) != 3 {
		t.Errorf("want one result per sub-call (3), got %d", len(arr))
	}

	got := h.recordedEvents()
	if len(got) != 2 {
		t.Fatalf("both routable sub-calls must be delivered, got %d (%v)", len(got), got)
	}
	if got[0][1] != "CUX2801001:1" || got[1][1] != "CUX2801001:2" {
		t.Errorf("sub-calls delivered out of order or wrong: %v", got)
	}
}

// TestBINRPCListedMethodsAreDispatchable holds system.listMethods and the
// dispatch switch together. Every advertised method must be routable — a peer
// that reads the list and then gets "method not supported" has been misled,
// which is precisely how system.multicall went missing: it was absent from
// both sides at once, so neither contradicted the other.
func TestBINRPCListedMethodsAreDispatchable(t *testing.T) {
	t.Parallel()

	const ifaceID = "loom-ccu-CUxD"
	client := newBINRPCTestServer(t, ifaceID, &binrpcRecordingHandlers{})

	// One well-formed call per advertised method. The arguments only need to
	// satisfy each method's arity; the assertion is that dispatch routes it,
	// not what the handler does with it.
	args := map[string][]xmlrpc.Value{
		"event": {
			xmlrpc.StringValue(ifaceID), xmlrpc.StringValue("CUX0:1"),
			xmlrpc.StringValue("STATE"), xmlrpc.StringValue("1"),
		},
		"newDevices":         {xmlrpc.StringValue(ifaceID), xmlrpc.ArrayValue{}},
		"deleteDevices":      {xmlrpc.StringValue(ifaceID), xmlrpc.ArrayValue{}},
		"listDevices":        {xmlrpc.StringValue(ifaceID)},
		"error":              {xmlrpc.StringValue(ifaceID), xmlrpc.IntValue(1), xmlrpc.StringValue("boom")},
		"system.listMethods": {xmlrpc.StringValue(ifaceID)},
		"system.multicall":   cuxdPongEnvelope(ifaceID),
	}

	advertised := BINRPCSupportedMethods()
	if len(advertised) == 0 {
		t.Fatal("BINRPCSupportedMethods must not be empty")
	}
	for _, method := range advertised {
		params, ok := args[method]
		if !ok {
			t.Fatalf("no probe arguments defined for advertised method %q — "+
				"add a case here when adding a method to BINRPCSupportedMethods", method)
		}
		if _, err := client.Call(context.Background(), method, params); err != nil {
			t.Errorf("system.listMethods advertises %q but dispatch rejects it: %v", method, err)
		}
	}
}

// TestBINRPCListMethodsWithoutInterfaceID probes introspection the way the
// XML-RPC convention defines it: `system.listMethods` takes no arguments.
//
// Routing it through the interface_id preamble faulted the call twice over —
// once on the missing param, and once more for a peer that does pass an id
// before its interface has registered a route. The introspection answer is
// identical for every interface, so it must depend on neither.
func TestBINRPCListMethodsWithoutInterfaceID(t *testing.T) {
	t.Parallel()

	const ifaceID = "loom-ccu-CUxD"
	client := newBINRPCTestServer(t, ifaceID, &binrpcRecordingHandlers{})

	for _, tc := range []struct {
		name   string
		params []xmlrpc.Value
	}{
		{name: "no params", params: nil},
		{name: "unregistered interface", params: []xmlrpc.Value{xmlrpc.StringValue("loom-other-CUxD")}},
	} {
		res, err := client.Call(context.Background(), "system.listMethods", tc.params)
		if err != nil {
			t.Fatalf("%s: system.listMethods must answer, got fault: %v", tc.name, err)
		}
		arr, err := xmlrpc.AsStrings(res)
		if err != nil {
			t.Fatalf("%s: listMethods result is not a string array: %v", tc.name, err)
		}
		if len(arr) != len(BINRPCSupportedMethods()) {
			t.Errorf("%s: got %d methods, want %d", tc.name, len(arr), len(BINRPCSupportedMethods()))
		}
	}
}
