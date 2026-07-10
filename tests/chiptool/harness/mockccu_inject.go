// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build chiptool

package harness

import "errors"

// errMockCCUNotStarted is returned by the injection helpers below
// when called against a nil or not-yet-started [MockCCU] — a
// programmer error in test setup ordering rather than a runtime
// condition tests are expected to assert on.
var errMockCCUNotStarted = errors.New("harness: MockCCU not started")

// SetDPValue seeds or mutates a live paramset value on the simulator
// with force=true, so the write bypasses the paramset's WRITE
// operations bit (some measurement DPs are otherwise read-only).
// Routes through the simulator's RPCFunctions.SetValue → PutParamset,
// which runs the device's ComputeEvents follow-ups and fires the
// resulting events to every registered remote (the daemon's
// XML-RPC/BIN-RPC callback listeners) exactly as a real CCU-side
// write would. Tests use this to seed a starting value before a
// SEND-direction chip-tool write, or to drive any other CCU-side
// precondition.
func (m *MockCCU) SetDPValue(address, valueKey string, value any) error {
	if m == nil || m.v == nil {
		return errMockCCUNotStarted
	}
	return m.v.RPC().SetValue(address, valueKey, value, true)
}

// GetDPValue reads the CCU-side ground truth for (address, valueKey).
// It prefers the live paramset via RPCFunctions.GetValue and falls
// back to the state-manager's cached value only when the live read
// errors (e.g. the paramset description has not loaded yet for a
// brand-new device). Any error collapses to (nil, false) — tests use
// the boolean to assert presence, not to distinguish the error cause.
//
// This is the read side of a SEND-direction assertion: after a
// chip-tool WRITE/INVOKE, GetDPValue confirms the value actually
// landed on the simulated CCU rather than only on the bridge's own
// cache.
func (m *MockCCU) GetDPValue(address, valueKey string) (any, bool) {
	if m == nil || m.v == nil {
		return nil, false
	}
	if v, err := m.v.RPC().GetValue(address, valueKey); err == nil {
		return v, true
	}
	if v, ok := m.v.State().DeviceValue(address, valueKey); ok {
		return v, true
	}
	return nil, false
}

// FireDeviceEvent simulates a spontaneous device-originated push —
// the RECEIVE direction of the send/receive suite (a real device
// reports a value change on its own, not in response to a CCU
// write).
//
// It routes through the simulator's dedicated
// VirtualCCU.SimulateDeviceEvent primitive, which frames the write as
// an unsolicited device-originated value change (force=true so
// read-only ops=RE telemetry params like ACTUAL_TEMPERATURE are not
// rejected) and runs the identical ComputeEvents + fire-to-registered-
// remotes sequence a real device report takes, so the daemon-side
// subscription pipeline cannot tell the difference from a live device.
func (m *MockCCU) FireDeviceEvent(address, valueKey string, value any) error {
	if m == nil || m.v == nil {
		return errMockCCUNotStarted
	}
	return m.v.SimulateDeviceEvent(address, valueKey, value)
}
