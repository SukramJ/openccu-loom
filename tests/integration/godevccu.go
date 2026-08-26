// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build integration

package integration

import (
	"fmt"
	"net"
	"testing"

	"github.com/SukramJ/godevccu/pkg/godevccu"
)

// defaultMockDevices is the device fleet pre-loaded into every
// `mockCCU` instance. The set is deliberately small but spans
// several domains (smoke detector, climate, switch+meter, cover) so
// the device-graph tests get coverage of multiple custom data point
// flavours without having to know about every supported model.
var defaultMockDevices = []string{
	"HmIP-SWSD",  // Smoke detector — STATE channel
	"HmIP-BWTH",  // Wall thermostat — climate domain
	"HmIP-BSM",   // Switch + power meter — switch + sensor channels
	"HmIP-BROLL", // Roller shutter — cover domain
}

// mockCCU is a running godevccu virtual CCU bound to OS-assigned
// ports. The struct exposes only the pieces integration tests
// actually need — URL (XML-RPC), JSONRPCURL (JSON-RPC, CCU/OpenCCU
// mode only) and Stop. Everything else stays behind the godevccu API.
type mockCCU struct {
	v *godevccu.VirtualCCU
}

// URL returns the XML-RPC endpoint URL.
func (m *mockCCU) URL() string {
	return fmt.Sprintf("http://%s/", m.v.XMLRPCAddr().String())
}

// JSONRPCURL returns the JSON-RPC endpoint URL
// (http://host:port/api/homematic.cgi). Returns "" for Homegear-mode
// instances where the JSON-RPC listener is not started.
func (m *mockCCU) JSONRPCURL() string {
	addr := m.v.JSONRPCAddr()
	if addr == nil {
		return ""
	}
	return fmt.Sprintf("http://%s/api/homematic.cgi", addr.String())
}

// Stop shuts the simulator down. Tests do not normally need to call
// this — startMockCCU registers a Cleanup that does it for them — but
// the reconnect test exercises the kill-restart cycle and needs a
// way to drop the listener mid-test.
func (m *mockCCU) Stop() error {
	if m == nil || m.v == nil {
		return nil
	}
	return m.v.Stop()
}

// startMockCCU spins up a godevccu instance on an OS-assigned XML-RPC
// port and registers a Cleanup that stops it. It runs in HOMEGEAR
// mode so only the XML-RPC server is started (no JSON-RPC, no auth)
// And `getVersion` reports
// Sniff prefix that clients use to recognise a
// simulator. EphemeralPort lets the OS pick a free port at bind
// time, which sidesteps the listener-reuse race window that the
// previous os/exec wrapper had.
func startMockCCU(t *testing.T) *mockCCU {
	return startMockCCUWithDevices(t, defaultMockDevices)
}

// startMockCCUWithDevices is like [startMockCCU] but lets the caller
// override the device fleet. Pass `nil` to load every embedded model
// (~399 devices).
func startMockCCUWithDevices(t *testing.T, devices []string) *mockCCU {
	return startMockCCUWithOptions(t, devices, nil)
}

// startMockCCUWithOptions extends startMockCCUWithDevices with a
// hook that fires on every successful SetValue / PutParamset on the
// virtual CCU. Tests that need to model CCU-side echo events for
// ACTION DPs (e.g. an RF-thermostat AUTO_MODE-write that triggers a
// CONTROL_MODE=AUTO-MODE sensor event in real firmware) wire the
// echo programmatically through this hook.
func startMockCCUWithOptions(t *testing.T, devices []string, onSetValue func(address, valueKey string, value any)) *mockCCU {
	t.Helper()
	v, err := godevccu.New(godevccu.Config{
		Mode:        godevccu.BackendModeHomegear,
		Host:        "127.0.0.1",
		XMLRPCPort:  godevccu.EphemeralPort,
		AuthEnabled: false,
		Devices:     devices,
		OnSetValue:  onSetValue,
	})
	if err != nil {
		t.Fatalf("godevccu.New: %v", err)
	}
	if err := v.Start(); err != nil {
		t.Fatalf("godevccu.Start: %v", err)
	}
	t.Cleanup(func() { _ = v.Stop() })

	addr, ok := v.XMLRPCAddr().(*net.TCPAddr)
	if !ok || addr == nil || addr.Port == 0 {
		_ = v.Stop()
		t.Fatalf("godevccu: ephemeral XML-RPC port not resolved: %v", v.XMLRPCAddr())
	}
	return &mockCCU{v: v}
}

// startMockCCUOpenCCU spins up a godevccu instance in BackendModeCCU
// (the OpenCCU/CCU personality) with both an XML-RPC listener and a
// JSON-RPC listener on OS-assigned ephemeral ports. Authentication is
// enabled, matching the default CCU behaviour that openccu-loom's
// CcuBackend and Rega runner expect.
//
// The returned *mockCCU exposes JSONRPCURL() for the JSON-RPC
// endpoint and URL() for the XML-RPC endpoint. A Cleanup is
// registered to stop both listeners when the test finishes.
//
// Design note: godevccu.BackendModeCCU and BackendModeOpenCCU share
// the same JSON-RPC handler table; the mode constant primarily
// affects the getVersion string. We use BackendModeCCU here because
// openccu-loom's CcuBackend tests target the CCU personality.
func startMockCCUOpenCCU(t *testing.T) *mockCCU {
	t.Helper()
	v, err := godevccu.New(godevccu.Config{
		Mode:          godevccu.BackendModeCCU,
		Host:          "127.0.0.1",
		XMLRPCPort:    godevccu.EphemeralPort,
		JSONRPCPort:   godevccu.EphemeralPort,
		Username:      "Admin",
		Password:      "",
		AuthEnabled:   true,
		Devices:       defaultMockDevices,
		Serial:        "GODEVCCU0001",
		SetupDefaults: true,
	})
	if err != nil {
		t.Fatalf("godevccu.New (OpenCCU): %v", err)
	}
	if err := v.Start(); err != nil {
		t.Fatalf("godevccu.Start (OpenCCU): %v", err)
	}
	t.Cleanup(func() { _ = v.Stop() })

	xmlAddr, ok := v.XMLRPCAddr().(*net.TCPAddr)
	if !ok || xmlAddr == nil || xmlAddr.Port == 0 {
		_ = v.Stop()
		t.Fatalf("godevccu: ephemeral XML-RPC port not resolved: %v", v.XMLRPCAddr())
	}
	jsonAddr, ok := v.JSONRPCAddr().(*net.TCPAddr)
	if !ok || jsonAddr == nil || jsonAddr.Port == 0 {
		_ = v.Stop()
		t.Fatalf("godevccu: ephemeral JSON-RPC port not resolved: %v", v.JSONRPCAddr())
	}
	return &mockCCU{v: v}
}
