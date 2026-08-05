// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build integration

// cuxd_push_test.go covers the CUxD push path against the simulator's
// BIN-RPC transport, which delivers callbacks the way real CUxD does:
// wrapped in a `system.multicall` envelope, always, even for a single
// value change.
//
// Everything about CUxD used to be covered except this. The outbound
// direction had a live wire-compatibility smoke, the codec had
// round-trip and fuzz tests, and the callback server had dispatch tests
// — all of which passed while no CUxD event had ever been delivered,
// because the envelope was the one shape nothing produced. The gap was
// structural: the simulator had no BIN-RPC at all, so the only test that
// could have caught it needed real hardware.

package integration

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/godevccu/pkg/godevccu"

	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/central/rpcserver"
	"github.com/SukramJ/openccu-loom/internal/client/transport/binrpc"
	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
)

// mockCUxD is a simulator instance with the BIN-RPC transport enabled,
// standing in for a CUxD add-on.
type mockCUxD struct {
	v    *godevccu.VirtualCCU
	addr string
}

// startMockCUxD boots a simulator whose BIN-RPC listener answers on an
// OS-assigned port.
func startMockCUxD(t *testing.T) *mockCUxD {
	t.Helper()
	v, err := godevccu.New(godevccu.Config{
		Mode:        godevccu.BackendModeCCU,
		Host:        "127.0.0.1",
		XMLRPCPort:  godevccu.EphemeralPort,
		JSONRPCPort: godevccu.EphemeralPort,
		BINRPCPort:  godevccu.EphemeralPort,
		Devices:     defaultMockDevices,
	})
	if err != nil {
		t.Fatalf("godevccu.New: %v", err)
	}
	if err := v.Start(); err != nil {
		t.Fatalf("godevccu.Start: %v", err)
	}
	t.Cleanup(func() { _ = v.Stop() })

	addr, ok := v.BINRPCAddr().(*net.TCPAddr)
	if !ok || addr == nil || addr.Port == 0 {
		t.Fatalf("godevccu: BIN-RPC port not resolved: %v", v.BINRPCAddr())
	}
	return &mockCUxD{v: v, addr: addr.String()}
}

// deliveredEvent is one callback that reached the production handlers.
type deliveredEvent struct {
	interfaceID string
	address     string
	parameter   string
}

// pushRecorder implements the callback-server handler contract and
// records what the production dispatch delivered.
type pushRecorder struct {
	events chan deliveredEvent
}

func newPushRecorder() *pushRecorder {
	return &pushRecorder{events: make(chan deliveredEvent, 32)}
}

func (r *pushRecorder) Event(_ context.Context, iface, addr, param string, _ xmlrpc.Value) error {
	select {
	case r.events <- deliveredEvent{interfaceID: iface, address: addr, parameter: param}:
	default:
	}
	return nil
}

func (r *pushRecorder) NewDevices(context.Context, string, xmlrpc.ArrayValue) error { return nil }
func (r *pushRecorder) DeleteDevices(context.Context, string, []string) error       { return nil }
func (r *pushRecorder) UpdateDevice(context.Context, string, string, int) error     { return nil }
func (r *pushRecorder) ReplaceDevice(context.Context, string, string, string) error { return nil }
func (r *pushRecorder) ReaddedDevice(context.Context, string, []string) error       { return nil }
func (r *pushRecorder) Error(context.Context, string, int, string) error            { return nil }

func (r *pushRecorder) ListDevices(context.Context, string) (xmlrpc.ArrayValue, error) {
	// The daemon's real handler answers empty so the peer re-announces
	// everything; mirror that here.
	return xmlrpc.ArrayValue{}, nil
}

// waitForEvent blocks until an event for the given parameter arrives.
func (r *pushRecorder) waitForEvent(t *testing.T, parameter string) deliveredEvent {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case e := <-r.events:
			if e.parameter == parameter {
				return e
			}
		case <-deadline:
			t.Fatalf("no %q event reached the callback handlers within 10s", parameter)
		}
	}
}

// startBINRPCCallbackServer runs the production BIN-RPC callback server with
// handlers registered under interfaceID and returns its address.
func startBINRPCCallbackServer(t *testing.T, interfaceID string, h rpcserver.Handlers) string {
	t.Helper()
	srv, err := rpcserver.NewBINRPCServer(rpcserver.BINRPCConfig{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatalf("NewBINRPCServer: %v", err)
	}
	srv.Register(interfaceID, h)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()
	t.Cleanup(func() { cancel(); <-done; _ = srv.Close() })
	return srv.Addr().String()
}

// TestCuxdMulticallPushReachesCallbackHandlers is the hermetic twin of
// the live-hardware guard: the simulator pushes the CUxD envelope, and
// the production callback server must deliver the inner event to the
// handlers. Removing the system.multicall case from the server's
// dispatch turns this red.
func TestCuxdMulticallPushReachesCallbackHandlers(t *testing.T) {
	cuxd := startMockCUxD(t)
	recorder := newPushRecorder()

	// The id the daemon would advertise, built by the production helper
	// so the test cannot drift from the real wire-boundary shape.
	interfaceID := adapter.InitInterfaceID("loom-test", "ccu-1", "CUxD")
	callbackAddr := startBINRPCCallbackServer(t, interfaceID, recorder)

	client, err := binrpc.NewClient(binrpc.Config{Addr: cuxd.addr, Interface: interfaceID})
	if err != nil {
		t.Fatalf("binrpc.NewClient: %v", err)
	}
	ctx := context.Background()

	callbackURL := "xmlrpc_bin://" + callbackAddr
	if _, err := client.Call(ctx, "init", []xmlrpc.Value{
		xmlrpc.StringValue(callbackURL), xmlrpc.StringValue(interfaceID),
	}); err != nil {
		t.Fatalf("init: %v", err)
	}

	// Which device fires is irrelevant — the assertion is about delivery.
	channel, parameter := firstEventTarget(t, cuxd)
	if err := cuxd.v.SimulateDeviceEvent(channel, parameter, true); err != nil {
		t.Fatalf("SimulateDeviceEvent(%s/%s): %v", channel, parameter, err)
	}

	got := recorder.waitForEvent(t, parameter)
	if got.interfaceID != interfaceID {
		t.Errorf("event arrived under interface_id %q, want %q — the callback route keys on it",
			got.interfaceID, interfaceID)
	}
}

// TestCuxdDeinitStopsPush pins the deregistration shape end to end: the
// URL alone removes the registration, so no further event arrives.
// Sending the interface id with an empty URL instead leaves the old
// registration live, which is the defect this pins against.
func TestCuxdDeinitStopsPush(t *testing.T) {
	cuxd := startMockCUxD(t)
	recorder := newPushRecorder()

	interfaceID := adapter.InitInterfaceID("loom-test", "ccu-1", "CUxD")
	callbackAddr := startBINRPCCallbackServer(t, interfaceID, recorder)
	callbackURL := "xmlrpc_bin://" + callbackAddr

	client, err := binrpc.NewClient(binrpc.Config{Addr: cuxd.addr, Interface: interfaceID})
	if err != nil {
		t.Fatalf("binrpc.NewClient: %v", err)
	}
	ctx := context.Background()
	if _, err := client.Call(ctx, "init", []xmlrpc.Value{
		xmlrpc.StringValue(callbackURL), xmlrpc.StringValue(interfaceID),
	}); err != nil {
		t.Fatalf("init: %v", err)
	}
	channel, parameter := firstEventTarget(t, cuxd)
	if err := cuxd.v.SimulateDeviceEvent(channel, parameter, true); err != nil {
		t.Fatalf("SimulateDeviceEvent: %v", err)
	}
	recorder.waitForEvent(t, parameter)

	// Deregister with the URL alone — the shape the CCU honours.
	if _, err := client.Call(ctx, "init", []xmlrpc.Value{xmlrpc.StringValue(callbackURL)}); err != nil {
		t.Fatalf("deinit: %v", err)
	}
	drain(recorder)
	if err := cuxd.v.SimulateDeviceEvent(channel, parameter, false); err != nil {
		t.Fatalf("SimulateDeviceEvent: %v", err)
	}
	select {
	case e := <-recorder.events:
		t.Errorf("a deregistered callback must receive nothing further, got %+v", e)
	case <-time.After(time.Second):
	}
}

// drain empties any pending events so a post-deregistration assertion
// cannot trip over one recorded earlier.
func drain(r *pushRecorder) {
	for {
		select {
		case <-r.events:
		default:
			return
		}
	}
}

// firstEventTarget discovers a (channel, parameter) pair the simulated
// fleet can actually fire, by asking each channel for its VALUES
// paramset. Hard-coding a pair would couple this test to whichever
// devices defaultMockDevices happens to list.
func firstEventTarget(t *testing.T, cuxd *mockCUxD) (channel, parameter string) {
	t.Helper()
	client, err := binrpc.NewClient(binrpc.Config{Addr: cuxd.addr, Interface: "probe"})
	if err != nil {
		t.Fatalf("binrpc.NewClient: %v", err)
	}
	ctx := context.Background()
	res, err := client.Call(ctx, "listDevices", nil)
	if err != nil {
		t.Fatalf("listDevices: %v", err)
	}
	arr, err := xmlrpc.AsArray(res)
	if err != nil {
		t.Fatalf("listDevices result: %v", err)
	}
	for _, e := range arr {
		st, err := xmlrpc.AsStruct(e)
		if err != nil {
			continue
		}
		var addr, typ string
		for _, m := range st.Members {
			switch m.Name {
			case "ADDRESS":
				addr, _ = xmlrpc.AsString(m.Value)
			case "TYPE":
				typ, _ = xmlrpc.AsString(m.Value)
			}
		}
		// Only channels carry a VALUES paramset, and MAINTENANCE holds
		// housekeeping parameters rather than the device's own state.
		if typ == "MAINTENANCE" || !strings.Contains(addr, ":") {
			continue
		}
		desc, err := client.Call(ctx, "getParamsetDescription",
			[]xmlrpc.Value{xmlrpc.StringValue(addr), xmlrpc.StringValue("VALUES")})
		if err != nil {
			continue
		}
		params, err := xmlrpc.AsStruct(desc)
		if err != nil || len(params.Members) == 0 {
			continue
		}
		return addr, params.Members[0].Name
	}
	t.Fatal("no channel with a VALUES paramset in the simulated fleet")
	return "", ""
}
