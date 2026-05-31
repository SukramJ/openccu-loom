// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build e2e

package harness

import (
	"fmt"
	"net"
	"testing"

	"github.com/SukramJ/godevccu/pkg/godevccu"
)

// DefaultDevices is the device fleet pre-loaded into every harness
// run. The set mirrors defaultMockDevices in
// tests/integration/godevccu.go so existing fixtures stay
// recognisable.
var DefaultDevices = []string{
	"HmIP-SWSD",  // Smoke detector — STATE channel
	"HmIP-BWTH",  // Wall thermostat — climate domain
	"HmIP-BSM",   // Switch + power meter — switch + sensor channels
	"HmIP-BROLL", // Roller shutter — cover domain
}

// MockCCU is a running godevccu virtual CCU bound to OS-assigned
// ports. It is the south-bound substitute for an HmIP CCU in E2E
// tests.
type MockCCU struct {
	v *godevccu.VirtualCCU
}

// XMLRPCURL returns the XML-RPC endpoint URL.
func (m *MockCCU) XMLRPCURL() string {
	return fmt.Sprintf("http://%s/", m.v.XMLRPCAddr().String())
}

// JSONRPCURL returns the JSON-RPC endpoint URL, or "" if the mock
// runs in a Homegear personality where the JSON-RPC listener is not
// started.
func (m *MockCCU) JSONRPCURL() string {
	addr := m.v.JSONRPCAddr()
	if addr == nil {
		return ""
	}
	return fmt.Sprintf("http://%s/api/homematic.cgi", addr.String())
}

// jsonrpcPort extracts godevccu's JSON-RPC port for the daemon's
// CentralConfig.JSONRPCPort override. Returns 0 when the mock has
// no JSON-RPC listener (Homegear mode), in which case the daemon
// keeps the default port 80 — and JSON-RPC seeding will fail, as
// expected for a HOMEGEAR backend.
func jsonrpcPort(m *MockCCU) int {
	if m == nil || m.v == nil {
		return 0
	}
	addr, ok := m.v.JSONRPCAddr().(*net.TCPAddr)
	if !ok || addr == nil {
		return 0
	}
	return addr.Port
}

// V returns the underlying VirtualCCU. Tests that need to inject
// events or add devices at runtime (hot-plug tests) use this to
// drive godevccu's RPC surface directly.
func (m *MockCCU) V() *godevccu.VirtualCCU {
	if m == nil {
		return nil
	}
	return m.v
}

// Stop drops the listeners. Tests do not normally call this — Start
// registers a t.Cleanup that does it for them.
func (m *MockCCU) Stop() error {
	if m == nil || m.v == nil {
		return nil
	}
	return m.v.Stop()
}

// startMockCCU spins up a godevccu instance in CCU mode (auth
// enabled, JSON-RPC available) on OS-assigned ephemeral ports and
// registers a t.Cleanup that stops it.
func startMockCCU(t *testing.T, devices []string) *MockCCU {
	t.Helper()
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
		Serial:        "GODEVCCU0001",
		SetupDefaults: true,
	})
	if err != nil {
		t.Fatalf("godevccu.New: %v", err)
	}
	if err := v.Start(); err != nil {
		t.Fatalf("godevccu.Start: %v", err)
	}
	t.Cleanup(func() { _ = v.Stop() })

	if addr, ok := v.XMLRPCAddr().(*net.TCPAddr); !ok || addr == nil || addr.Port == 0 {
		_ = v.Stop()
		t.Fatalf("godevccu: ephemeral XML-RPC port not resolved: %v", v.XMLRPCAddr())
	}
	if addr, ok := v.JSONRPCAddr().(*net.TCPAddr); !ok || addr == nil || addr.Port == 0 {
		_ = v.Stop()
		t.Fatalf("godevccu: ephemeral JSON-RPC port not resolved: %v", v.JSONRPCAddr())
	}
	return &MockCCU{v: v}
}
