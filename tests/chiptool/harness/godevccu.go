// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build chiptool

package harness

import (
	"fmt"
	"net"
	"testing"

	"github.com/SukramJ/godevccu/pkg/godevccu"
)

// DefaultDevices is the device fleet the chip-tool suite uses by
// default. The choice is deliberately diverse: each entry exposes a
// different Matter cluster so the cluster-read sweep has something
// non-trivial to talk to on every front:
//
//   - HmIP-SWSD   — Smoke detector → BooleanState
//   - HmIP-BWTH   — Wall thermostat → Thermostat (limited subset),
//     TemperatureMeasurement, RelativeHumidity
//   - HmIP-BSM    — Switch + power meter → OnOff, ElectricalMeasurement
//   - HmIP-BROLL  — Roller shutter → WindowCovering
//   - HmIP-PS     — Plug-in switch → OnOff (mirrors the sanctioned
//     write target on the real CCU)
//   - HmIP-BDT    — Brand-recessed dimmer → OnOff + LevelControl
var DefaultDevices = []string{
	"HmIP-SWSD",
	"HmIP-BWTH",
	"HmIP-BSM",
	"HmIP-BROLL",
	"HmIP-PS",
	"HmIP-BDT",
}

// MockCCU wraps a running godevccu instance bound to OS-assigned
// loopback ports.
type MockCCU struct {
	v *godevccu.VirtualCCU
}

// XMLRPCPort returns the XML-RPC TCP port the simulator listens on.
func (m *MockCCU) XMLRPCPort() int {
	if m == nil || m.v == nil {
		return 0
	}
	addr, ok := m.v.XMLRPCAddr().(*net.TCPAddr)
	if !ok || addr == nil {
		return 0
	}
	return addr.Port
}

// JSONRPCPort returns the JSON-RPC TCP port the simulator listens
// on, or 0 when the personality has no JSON-RPC listener.
func (m *MockCCU) JSONRPCPort() int {
	if m == nil || m.v == nil {
		return 0
	}
	addr, ok := m.v.JSONRPCAddr().(*net.TCPAddr)
	if !ok || addr == nil {
		return 0
	}
	return addr.Port
}

// Stop releases the simulator's listeners. The harness wires this
// into a t.Cleanup; tests do not normally invoke it directly.
func (m *MockCCU) Stop() error {
	if m == nil || m.v == nil {
		return nil
	}
	return m.v.Stop()
}

// startMockCCU spins up a godevccu instance in CCU personality
// (auth enabled, XML-RPC + JSON-RPC active) on ephemeral loopback
// ports and registers a cleanup hook that stops it after the test
// (or TestMain) returns.
func startMockCCU(t *testing.T, devices []string) *MockCCU {
	t.Helper()
	ccu, stop, err := startMockCCUShared(devices)
	if err != nil {
		t.Fatalf("startMockCCU: %v", err)
	}
	t.Cleanup(stop)
	return ccu
}

// startMockCCUShared is the no-testing.T variant. The returned
// `stop` callback releases the listeners; the caller (TestMain or
// the t.Cleanup adapter in startMockCCU) owns its invocation.
func startMockCCUShared(devices []string) (*MockCCU, func(), error) {
	if len(devices) == 0 {
		devices = DefaultDevices
	}
	v, err := godevccu.New(godevccu.Config{
		Mode:          godevccu.BackendModeCCU,
		Host:          "127.0.0.1",
		XMLRPCPort:    godevccu.EphemeralPort,
		JSONRPCPort:   godevccu.EphemeralPort,
		Username:      "Admin",
		Password:      "",
		AuthEnabled:   true,
		Devices:       devices,
		Serial:        "CHIPTOOLDEV001",
		SetupDefaults: true,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("godevccu.New: %w", err)
	}
	if err := v.Start(); err != nil {
		return nil, nil, fmt.Errorf("godevccu.Start: %w", err)
	}
	return &MockCCU{v: v}, func() { _ = v.Stop() }, nil
}
