// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build integration

package integration

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
)

// TestXMLRPCListMethodsOnMockCCU verifies that the XML-RPC client can
// dial the godevccu simulator and call system.listMethods.
func TestXMLRPCListMethodsOnMockCCU(t *testing.T) {
	srv := startMockCCU(t)
	client := newXMLRPCClient(t, srv.URL())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	v, err := client.Call(ctx, "system.listMethods", nil)
	if err != nil {
		t.Fatalf("system.listMethods: %v", err)
	}
	names, err := xmlrpc.AsStrings(v)
	if err != nil {
		t.Fatalf("AsStrings: %v", err)
	}
	if len(names) < 10 {
		t.Fatalf("expected many methods, got %d: %v", len(names), names)
	}

	// Spot-check a couple of methods every CCU dialect exposes.
	required := []string{"listDevices", "getParamsetDescription"}
	seen := make(map[string]bool, len(names))
	for _, n := range names {
		seen[n] = true
	}
	for _, want := range required {
		if !seen[want] {
			t.Errorf("godevccu method set missing %q", want)
		}
	}
}

func TestXMLRPCListDevicesOnMockCCU(t *testing.T) {
	srv := startMockCCU(t)
	client := newXMLRPCClient(t, srv.URL())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	v, err := client.Call(ctx, "listDevices", nil)
	if err != nil {
		t.Fatalf("listDevices: %v", err)
	}
	arr, err := xmlrpc.AsArray(v)
	if err != nil {
		t.Fatalf("AsArray: %v", err)
	}
	// godevccu pre-loads defaultMockDevices, so the response must
	// carry at least their channel rows.
	if len(arr) == 0 {
		t.Fatalf("listDevices returned 0 entries; expected the pre-loaded fleet")
	}
	t.Logf("listDevices returned %d devices", len(arr))
}

func TestXMLRPCGetVersionOnMockCCU(t *testing.T) {
	srv := startMockCCU(t)
	client := newXMLRPCClient(t, srv.URL())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	v, err := client.Call(ctx, "getVersion", nil)
	if err != nil {
		t.Fatalf("getVersion: %v", err)
	}
	s, err := xmlrpc.AsString(v)
	if err != nil {
		t.Fatalf("AsString: %v", err)
	}
	// Godevccu in HOMEGEAR mode reports
	// Historical sniff prefix that clients use to
	// recognise a simulator. We assert on the prefix rather than the
	// exact version so godevccu version bumps don't break us.
	if !strings.Contains(strings.ToLower(s), "pydevccu") {
		t.Fatalf("getVersion = %q, expected a simulator version string", s)
	}
}

func newXMLRPCClient(t *testing.T, url string) *xmlrpc.Client {
	t.Helper()
	c, err := xmlrpc.NewClient(xmlrpc.Config{
		URL:       url,
		Interface: "HmIP-RF",
	})
	if err != nil {
		t.Fatalf("xmlrpc.NewClient: %v", err)
	}
	return c
}
