// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build integration

package integration

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client/backends"
)

// TestHomegearGetAllSystemVariablesOverXMLRPC exercises the real
// Homegear sysvar wire path: a godevccu instance in Homegear mode
// (XML-RPC only, no JSON-RPC) answers getAllSystemVariables, and a
// HomegearBackend reads the variables back through the XML-RPC
// transport. This is the path the JSON-RPC hub bootstrap
// (WireHub → SysVar.getAll) cannot serve for Homegear; the adapter's
// loadHomegearSysvars consumes exactly this []map[string]any shape
// (unit-tested in internal/central/adapter/homegear_hub_wiring_test.go,
// which also covers the bool / float / named-transport-type cases).
//
// The assertion confirms the transport delivers native Go scalar types
// (string, int) so the value-type inference in classifyHomegearSysvar
// lands on the right HubValueType (String / Integer) rather than seeing
// an unrecognised wrapper type.
func TestHomegearGetAllSystemVariablesOverXMLRPC(t *testing.T) {
	mock := startMockCCUWithDevices(t, []string{}) // no devices: this test is about sysvars

	xmlClient := newXMLRPCClient(t, mock.URL())
	caller := &xmlrpcBackendCaller{client: xmlClient}
	backend := backends.NewHomegearBackend(caller, nil)

	all, err := backend.GetAllSystemVariables(context.Background())
	if err != nil {
		t.Fatalf("HomegearBackend.GetAllSystemVariables: %v", err)
	}

	values := make(map[string]any, len(all))
	for _, entry := range all {
		name, _ := entry["name"].(string)
		values[name] = entry["value"]
	}

	// godevccu's Homegear getAllSystemVariables serves a stable pair:
	// a string and an integer variable.
	strVal, ok := values["sys_var1"]
	if !ok {
		t.Fatalf("sys_var1 missing from result: %v", values)
	}
	if s, isStr := strVal.(string); !isStr || s != "str_var" {
		t.Errorf("sys_var1 = %v (%T), want string %q", strVal, strVal, "str_var")
	}

	intVal, ok := values["sys_var2"]
	if !ok {
		t.Fatalf("sys_var2 missing from result: %v", values)
	}
	if n, isInt := intVal.(int); !isInt || n != 13 {
		t.Errorf("sys_var2 = %v (%T), want int 13", intVal, intVal)
	}
}
