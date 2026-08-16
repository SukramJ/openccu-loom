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

// TestUnknownDeviceIsNotRetried covers the code that cost the most:
// the one a CCU sends when it does not know the address at all.
//
// It used to sit in the retryable set under the name "timeout", with a
// comment saying it was not a CCU-native code and that the daemon's own
// transports raised it. Nothing in the daemon ever raised it — every
// -2 reaching the classifier came off the wire, where it means the
// device or channel does not exist. So a call against a device removed
// on the CCU, or an address a stale automation still holds, ran the
// full exponential backoff before the operator saw anything, spending
// duty cycle on a question with a fixed answer.
//
// Verified against the published catalogue (HomeMatic XML-RPC
// specification §6: "Unbekanntes Gerät / unbekannter Kanal") and read
// back from a live CCU on both interface processes.
func TestUnknownDeviceIsNotRetried(t *testing.T) {
	v := startFaultCodeCCU(t)

	fault := faultFor(t, v, "getValue", "NOSUCHDEVICE:1", "STATE")

	const wireCode = -2
	if fault.Code != wireCode {
		t.Fatalf("simulator answered an unknown device with %d, want %d", fault.Code, wireCode)
	}
	if fault.FaultCode().IsRetryable() {
		t.Errorf("fault %d (%q) classifies as retryable — the address will not start existing "+
			"between attempts, so every repeat spends radio time and delays the error the "+
			"operator needs to see", fault.Code, fault.Message)
	}
}
