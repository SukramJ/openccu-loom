// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build integration

// wire_state_machines_test.go covers two things the daemon derives from
// events a CCU sends on its own, rather than in answer to a call: the
// ping/pong round trip its connection monitor runs on, and the
// unreachability latch its availability model reads.
//
// Both were unreachable from a hermetic test until the simulator grew
// the state machines. `ping` returned true and sent nothing back, so the
// monitor's correlation had no wire-level coverage at all; UNREACH could
// only be produced by hand-feeding the callback handlers, which proves
// the handler works and nothing about what a CCU sends.

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/godevccu/pkg/godevccu"

	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// startStateMachineCCU boots a simulator running the device state
// machines a CCU runs.
func startStateMachineCCU(t *testing.T) *godevccu.VirtualCCU {
	t.Helper()

	v, err := godevccu.New(godevccu.Config{
		Mode:       godevccu.BackendModeHomegear,
		Host:       "127.0.0.1",
		XMLRPCPort: godevccu.EphemeralPort,
		Devices:    defaultMockDevices,
		Realism:    godevccu.Realism{Reachability: true},
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

// registerCallback brings up the production callback server, registers
// it with the simulator under interfaceID, and returns the recorder.
func registerCallback(t *testing.T, v *godevccu.VirtualCCU, interfaceID string) *pushRecorder {
	t.Helper()

	recorder := newPushRecorder()
	callbackURL := startXMLRPCCallbackServer(t, "ccu-1", recorder)
	client := newXMLRPCClient(t, "http://"+v.XMLRPCAddr().String()+"/")
	if _, err := client.Call(context.Background(), "init", []xmlrpc.Value{
		xmlrpc.StringValue(callbackURL), xmlrpc.StringValue(interfaceID),
	}); err != nil {
		t.Fatalf("init: %v", err)
	}
	return recorder
}

// TestPingProducesAPongEvent pins the round trip the connection monitor
// is built on.
//
// The monitor sends `ping` with a caller id and waits for the CENTRAL
// PONG carrying that id back; the id is keyed on the full
// daemon/central/interface triple so two daemons on one CCU can reject
// each other's echoes. Until the simulator answered a ping with an
// event, the whole correlation — send, echo, match — existed only in
// unit tests that fed the handler the PONG themselves, and a CCU that
// never echoed would have looked identical to a healthy one until the
// pending map hit its cap.
func TestPingProducesAPongEvent(t *testing.T) {
	v := startStateMachineCCU(t)

	interfaceID := adapter.InitInterfaceID("loom-test", "ccu-1", "HmIP-RF")
	recorder := registerCallback(t, v, interfaceID)

	client := newXMLRPCClient(t, "http://"+v.XMLRPCAddr().String()+"/")
	callerID := interfaceID + "#probe"
	if _, err := client.Call(context.Background(), "ping",
		[]xmlrpc.Value{xmlrpc.StringValue(callerID)}); err != nil {
		t.Fatalf("ping: %v", err)
	}

	got := recorder.waitForEvent(t, string(hmenum.ParameterPong))
	if got.interfaceID != interfaceID {
		t.Errorf("PONG arrived under interface_id %q, want %q — the daemon attributes the "+
			"echo by this id and drops the ones it does not recognise", got.interfaceID, interfaceID)
	}
}

// TestUnreachLatchesUntilAcknowledged pins the shape the availability
// model depends on: a device that stops answering reports UNREACH now,
// and STICKY_UNREACH keeps reporting that it happened after it comes
// back.
//
// The distinction is the whole point of the second parameter. UNREACH
// alone would let a device that dropped out overnight look untroubled
// by morning; the sticky flag is what an operator acknowledges.
func TestUnreachLatchesUntilAcknowledged(t *testing.T) {
	v := startStateMachineCCU(t)

	interfaceID := adapter.InitInterfaceID("loom-test", "ccu-1", "HmIP-RF")
	recorder := registerCallback(t, v, interfaceID)

	const device = "VCU8537918"
	if err := v.RPC().SetDeviceUnreachable(device, true); err != nil {
		t.Fatalf("SetDeviceUnreachable(%s): %v", device, err)
	}

	// Both flags must reach the handlers: the daemon reads them off
	// channel 0 and derives availability from the pair.
	seen := map[string]bool{}
	deadline := time.After(15 * time.Second)
	for !seen[string(hmenum.ParameterUnreach)] || !seen[string(hmenum.ParameterStickyUnreach)] {
		select {
		case e := <-recorder.events:
			seen[e.parameter] = true
		case <-deadline:
			t.Fatalf("saw %v; UNREACH and STICKY_UNREACH must both arrive — the model reads "+
				"the pair, and without the sticky flag a device that dropped out overnight "+
				"looks untroubled by morning", seen)
		}
	}
}
