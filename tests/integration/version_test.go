// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
)

// TestXMLRPCListDevicesAndGetVersionOnMockCCU verifies that the XML-RPC
// client can call `listMethods`, `listDevices`, and `getVersion` against
// the godevccu simulator.
func TestXMLRPCListDevicesAndGetVersionOnMockCCU(t *testing.T) {
	srv := startMockCCU(t)
	client := newXMLRPCClient(t, srv.URL())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	devs, err := client.Call(ctx, "listDevices", nil)
	if err != nil {
		t.Fatalf("listDevices: %v", err)
	}
	if arr, ok := devs.(xmlrpc.ArrayValue); !ok || len(arr) == 0 {
		t.Fatalf("listDevices shape=%T len=%d", devs, len(arr))
	}

	version, err := client.Call(ctx, "getVersion", nil)
	if err != nil {
		t.Fatalf("getVersion: %v", err)
	}
	s, err := xmlrpc.AsString(version)
	if err != nil || s == "" {
		t.Fatalf("getVersion value: %q err=%v", s, err)
	}
}
