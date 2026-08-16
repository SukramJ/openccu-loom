// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build integration

// batched_events_test.go covers the XML-RPC push path the way a CCU
// actually drives it: events queue per remote and travel as one
// `system.multicall`, rather than as one synchronous call per value.
//
// The envelope had dispatch tests, and the CUxD side had an end-to-end
// one over BIN-RPC. The XML-RPC side had neither — the simulator sent
// every event as a bare `event` call, so the batched shape the CCU uses
// was the one nothing produced. A parser test cannot close that gap: it
// builds the envelope it then parses, and agrees with itself.

package integration

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/SukramJ/godevccu/pkg/godevccu"

	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/central/rpcserver"
	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
)

// startBatchingCCU boots a simulator that delivers events the way a CCU
// does: asynchronously, from a per-remote dispatcher, bundled.
func startBatchingCCU(t *testing.T) *godevccu.VirtualCCU {
	t.Helper()

	v, err := godevccu.New(godevccu.Config{
		Mode:       godevccu.BackendModeHomegear,
		Host:       "127.0.0.1",
		XMLRPCPort: godevccu.EphemeralPort,
		Devices:    defaultMockDevices,
		Realism:    godevccu.Realism{BatchEvents: true},
	})
	if err != nil {
		t.Fatalf("godevccu.New: %v", err)
	}
	if err := v.Start(); err != nil {
		t.Fatalf("godevccu.Start: %v", err)
	}
	t.Cleanup(func() { _ = v.Stop() })
	return v
}

// startXMLRPCCallbackServer runs the production XML-RPC callback server
// with handlers registered for centralName and returns its base URL.
func startXMLRPCCallbackServer(t *testing.T, centralName string, h rpcserver.Handlers) string {
	t.Helper()

	srv, err := rpcserver.NewXMLRPCServer(rpcserver.XMLRPCConfig{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatalf("NewXMLRPCServer: %v", err)
	}
	srv.Register(centralName, h)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()
	t.Cleanup(func() { cancel(); <-done })

	addr, ok := srv.Addr().(*net.TCPAddr)
	if !ok || addr == nil {
		t.Fatalf("callback server address unresolved: %v", srv.Addr())
	}
	return "http://" + addr.String() + "/RPC2/" + centralName
}

// TestBatchedEventsReachTheCallbackHandlers pins the delivery of the
// envelope a CCU sends under load.
//
// A burst of value changes arrives as a single multicall carrying every
// event. Delivering only its first entry loses the rest silently: the
// affected data points keep their previous value and nothing anywhere
// reports a gap, because the transport call itself succeeded.
func TestBatchedEventsReachTheCallbackHandlers(t *testing.T) {
	v := startBatchingCCU(t)
	recorder := newPushRecorder()

	const centralName = "ccu-1"
	interfaceID := adapter.InitInterfaceID("loom-test", centralName, "HmIP-RF")
	callbackURL := startXMLRPCCallbackServer(t, centralName, recorder)

	client := newXMLRPCClient(t, "http://"+v.XMLRPCAddr().String()+"/")
	ctx := context.Background()
	if _, err := client.Call(ctx, "init", []xmlrpc.Value{
		xmlrpc.StringValue(callbackURL), xmlrpc.StringValue(interfaceID),
	}); err != nil {
		t.Fatalf("init: %v", err)
	}

	// A burst the dispatcher can bundle. Distinct parameters so the
	// assertion can tell which ones survived the envelope.
	const channel = "VCU8537918:4"
	burst := []struct {
		parameter string
		value     any
	}{
		{"LEVEL", 0.25},
		{"LEVEL_2", 0.5},
		{"ACTIVITY_STATE", 1},
	}
	fired := make([]string, 0, len(burst))
	for _, b := range burst {
		if err := v.SimulateDeviceEvent(channel, b.parameter, b.value); err != nil {
			// A parameter this fixture's device does not carry is not
			// what the test is about; skip it rather than fail.
			continue
		}
		fired = append(fired, b.parameter)
	}
	if len(fired) < 2 {
		t.Fatalf("only %d of %d parameters could be fired; the test needs a burst to be about "+
			"batching at all", len(fired), len(burst))
	}

	deadline := time.After(15 * time.Second)
	pending := make(map[string]bool, len(fired))
	for _, p := range fired {
		pending[p] = true
	}
	for len(pending) > 0 {
		select {
		case e := <-recorder.events:
			delete(pending, e.parameter)
		case <-deadline:
			missing := make([]string, 0, len(pending))
			for p := range pending {
				missing = append(missing, p)
			}
			t.Fatalf("fired %v, never delivered %v — every entry after the first is dropped "+
				"silently, and the data points keep their previous value while the transport "+
				"call reports success", fired, missing)
		}
	}
}
