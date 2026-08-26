// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
	"github.com/SukramJ/openccu-loom/internal/config"
)

// callbackStubHandlers is a minimal [rpcserver.Handlers] so a callback can be
// dispatched end-to-end without a CCU or a loaded model.
type callbackStubHandlers struct{}

func (callbackStubHandlers) Event(context.Context, string, string, string, xmlrpc.Value) error {
	return nil
}

func (callbackStubHandlers) NewDevices(context.Context, string, xmlrpc.ArrayValue) error { return nil }

func (callbackStubHandlers) DeleteDevices(context.Context, string, []string) error { return nil }

func (callbackStubHandlers) UpdateDevice(context.Context, string, string, int) error { return nil }

func (callbackStubHandlers) ReplaceDevice(context.Context, string, string, string) error { return nil }

func (callbackStubHandlers) ReaddedDevice(context.Context, string, []string) error { return nil }

func (callbackStubHandlers) ListDevices(context.Context, string) (xmlrpc.ArrayValue, error) {
	return xmlrpc.ArrayValue{}, nil
}
func (callbackStubHandlers) Error(context.Context, string, int, string) error { return nil }

// TestCallbackMetricsReachTheCentralsAggregator pins the metrics wiring by
// its effect: the listener is stood up through the production wiring
// function, a real XML-RPC callback is delivered over the socket, and the
// number is read back through the aggregator the diagnostics dump renders.
// Nothing in the test writes to an observer — a test that did would stay
// green while no callback in a running daemon was ever measured, which is
// exactly the state this wiring replaced.
func TestCallbackMetricsReachTheCentralsAggregator(t *testing.T) {
	cfg := config.Default()
	cfg.Callback.Host = "127.0.0.1"
	cfg.Callback.Port = 0
	cfg.Callback.PortRange = ""
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-alpha", Host: "127.0.0.1"}}
	reg := buildTestRegistry(t, "ccu-alpha")
	logger := slog.New(slog.DiscardHandler)

	// Production wiring: this is what attaches an aggregator (and with it
	// the observer) to every central.
	seedCentralHealthAndMetrics(reg, cfg, nil, logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cb, teardown := wireXMLRPCCallback(ctx, cfg, nil, newCallbackMetrics(reg), logger)
	defer teardown()
	if cb.srv == nil {
		t.Fatal("callback listener did not bind")
	}
	cb.srv.Register("ccu-alpha", callbackStubHandlers{})

	client, err := xmlrpc.NewClient(xmlrpc.Config{
		URL:       "http://" + cb.srv.Addr().String() + "/RPC2/ccu-alpha",
		Interface: "HmIP-RF",
	})
	if err != nil {
		t.Fatalf("xmlrpc.NewClient: %v", err)
	}
	if _, err := client.Call(ctx, "event", []xmlrpc.Value{
		xmlrpc.StringValue("ccu-alpha-HmIP-RF"),
		xmlrpc.StringValue("ABC0000001:1"),
		xmlrpc.StringValue("STATE"),
		xmlrpc.BoolValue(true),
	}); err != nil {
		t.Fatalf("callback call: %v", err)
	}

	unit, ok := reg.Get("ccu-alpha")
	if !ok || unit.Aggregator == nil {
		t.Fatal("central has no aggregator after the production seeding ran")
	}
	// The observation happens on the server goroutine after the response is
	// written, so poll rather than read once.
	deadline := time.Now().Add(2 * time.Second)
	for {
		if got := unit.Aggregator.RPCServer(); got.TotalRequests > 0 {
			if got.TotalErrors != 0 {
				t.Errorf("a successful callback must not count as an error: %+v", got)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("callback was never counted: %+v", unit.Aggregator.RPCServer())
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestCallbackMetricsResolveBINRPCInterfaceIDs pins the second routing shape:
// the BIN-RPC listener knows only the interface id the CCU echoes back, and
// the sink has to map that onto the owning central. Getting it wrong is
// silent — the observation is simply dropped.
func TestCallbackMetricsResolveBINRPCInterfaceIDs(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu-alpha", "ccu-beta")
	cfg := config.Default()
	cfg.Centrals = []config.CentralConfig{
		{Name: "ccu-alpha", Host: "127.0.0.1"},
		{Name: "ccu-beta", Host: "127.0.0.1"},
	}
	seedCentralHealthAndMetrics(reg, cfg, nil, slog.New(slog.DiscardHandler))
	m := newCallbackMetrics(reg)

	for _, tc := range []struct {
		name     string
		routeKey string
		want     string
	}{
		{"central name (xml-rpc route)", "ccu-alpha", "ccu-alpha"},
		{"prefixed interface id", "loom-inst-ccu-beta-CUxD", "ccu-beta"},
		{"collapsed interface id", "loom-ccu-alpha-CUxD", "ccu-alpha"},
		{"unknown central", "loom-inst-ccu-gamma-CUxD", ""},
	} {
		got, obs := m.observerFor(tc.routeKey)
		if got != tc.want {
			t.Errorf("%s: routeKey %q resolved to %q, want %q", tc.name, tc.routeKey, got, tc.want)
		}
		if (obs != nil) != (tc.want != "") {
			t.Errorf("%s: observer presence does not match the resolved central", tc.name)
		}
	}
}

// TestCallbackMetricsChargeTheLongerOfTwoPrefixNames pins the routing for the
// one fleet shape that makes the id reduction ambiguous: a central whose name
// is a dash-prefix of another's.
//
// The registry is walked in ascending name order, so `ccu` is inspected first
// and the canonical id of `ccu-2` starts with `ccu-` as well. Every CUxD
// callback of the second CCU was therefore counted against the first, and the
// second one's rpc_server diagnostics stayed at zero forever — with both
// numbers looking entirely plausible.
func TestCallbackMetricsChargeTheLongerOfTwoPrefixNames(t *testing.T) {
	t.Parallel()
	reg := buildTestRegistry(t, "ccu", "ccu-2")
	cfg := config.Default()
	cfg.Centrals = []config.CentralConfig{
		{Name: "ccu", Host: "127.0.0.1"},
		{Name: "ccu-2", Host: "127.0.0.1"},
	}
	seedCentralHealthAndMetrics(reg, cfg, nil, slog.New(slog.DiscardHandler))
	m := newCallbackMetrics(reg)

	for _, tc := range []struct {
		name     string
		routeKey string
		want     string
	}{
		{"the longer name owns its own id", "loom-ccu-2-CUxD", "ccu-2"},
		{"the shorter name still owns its own id", "loom-ccu-CUxD", "ccu"},
	} {
		got, obs := m.observerFor(tc.routeKey)
		if got != tc.want {
			t.Errorf("%s: routeKey %q resolved to %q, want %q", tc.name, tc.routeKey, got, tc.want)
		}
		if obs == nil {
			t.Errorf("%s: routeKey %q resolved to no observer; the callback is not counted anywhere", tc.name, tc.routeKey)
		}
	}
}
