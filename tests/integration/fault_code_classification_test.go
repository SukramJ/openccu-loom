// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build integration

package integration

import (
	"errors"
	"testing"

	"github.com/SukramJ/godevccu/pkg/godevccu"

	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// The retrier decides from the fault code alone whether a failed call
// is worth repeating, and until the simulator could emit the HomeMatic
// fault catalogue every failure came back as -1 — the one code that is
// unambiguously retryable. The classification therefore had no
// wire-level test at all: every code the daemon treats as permanent was
// asserted only against a hand-built error value.
//
// These tests drive real failures through the real transport and read
// the classification off the typed fault the daemon produces.

// startFaultCodeCCU boots a simulator that answers failures with the
// HomeMatic fault catalogue instead of a blanket -1.
func startFaultCodeCCU(t *testing.T) *godevccu.VirtualCCU {
	t.Helper()

	v, err := godevccu.New(godevccu.Config{
		Mode:       godevccu.BackendModeHomegear,
		Host:       "127.0.0.1",
		XMLRPCPort: godevccu.EphemeralPort,
		Devices:    defaultMockDevices,
		Realism:    godevccu.Realism{FaultCodes: true},
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

// faultFor issues method against the simulator and returns the typed
// fault it answers with.
func faultFor(t *testing.T, v *godevccu.VirtualCCU, method string, args ...string) *hmerr.XMLRPCFault {
	t.Helper()

	client := newXMLRPCClient(t, "http://"+v.XMLRPCAddr().String()+"/")
	params := make([]xmlrpc.Value, 0, len(args))
	for _, a := range args {
		params = append(params, xmlrpc.StringValue(a))
	}
	_, err := client.Call(t.Context(), method, params)
	if err == nil {
		t.Fatalf("%s(%v) succeeded; expected a fault", method, args)
	}
	var fault *hmerr.XMLRPCFault
	if !errors.As(err, &fault) {
		t.Fatalf("%s(%v): error is not a typed XML-RPC fault: %v", method, args, err)
	}
	return fault
}

// TestUnknownParameterIsNotRetried pins the half of the classification
// the daemon gets right on the wire: a parameter the device does not
// have will not appear on a retry, and repeating the call only spends
// duty cycle on a request that cannot succeed.
func TestUnknownParameterIsNotRetried(t *testing.T) {
	v := startFaultCodeCCU(t)

	fault := faultFor(t, v, "getValue", "VCU8537918:4", "NOSUCHPARAM")

	if fault.FaultCode().IsRetryable() {
		t.Errorf("fault %d (%q) classifies as retryable — a parameter the device does not "+
			"have cannot appear on a retry, so the call is repeated through the whole backoff "+
			"before the operator sees the error", fault.Code, fault.Message)
	}
}

// TestUnknownDeviceFaultClassification records what the daemon does
// with the code a CCU sends for an unknown paramset, which is where the
// two catalogues disagree.
//
// The HomeMatic catalogue assigns -2 to "unknown paramset", a permanent
// failure. The daemon's own table assigns -2 to a timeout it raises
// itself when a call exceeds its deadline — retryable, and documented
// there as "not a CCU-native fault code". Now that a CCU-shaped -2 can
// reach the classifier, the two meanings collide on one number: the
// permanent failure is retried through the full backoff.
//
// This test characterises the collision rather than endorsing it. If
// the mapping is changed, this test fails and the change is a decision
// somebody made, not a side effect.
func TestUnknownDeviceFaultClassification(t *testing.T) {
	v := startFaultCodeCCU(t)

	fault := faultFor(t, v, "getValue", "NOSUCHDEVICE:1", "STATE")

	const wireCode = -2
	if fault.Code != wireCode {
		t.Fatalf("simulator answered an unknown device with %d, want %d — the catalogue this "+
			"test characterises has changed", fault.Code, wireCode)
	}
	if !fault.FaultCode().IsRetryable() {
		t.Errorf("fault -2 now classifies as permanent. That is very likely the correct " +
			"reading of the CCU catalogue, but it changes retry behaviour on every " +
			"installation — update this test deliberately, and check the timeout fault the " +
			"daemon raises under the same number")
	}
}
