// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hmenum

import "testing"

func TestInterfaceWireStrings(t *testing.T) {
	// These strings are wire protocol. Changing them silently
	// reinterprets recorded sessions and paramset patches.
	cases := map[Interface]string{
		InterfaceHmIPRF:         "HmIP-RF",
		InterfaceBidCosRF:       "BidCos-RF",
		InterfaceBidCosWired:    "BidCos-Wired",
		InterfaceVirtualDevices: "VirtualDevices",
		InterfaceCUxD:           "CUxD",
	}
	for iface, want := range cases {
		if got := iface.String(); got != want {
			t.Errorf("%s.String() = %q, want %q", want, got, want)
		}
	}
}

func TestInterfaceProtocolClassification(t *testing.T) {
	xml := []Interface{
		InterfaceHmIPRF, InterfaceBidCosRF, InterfaceBidCosWired,
		InterfaceVirtualDevices,
	}
	for _, i := range xml {
		if !i.IsXMLRPC() {
			t.Errorf("%s should be XML-RPC", i)
		}
		if i.IsBINRPC() {
			t.Errorf("%s should not be BIN-RPC", i)
		}
	}
	if !InterfaceCUxD.IsBINRPC() {
		t.Error("CUxD should be BIN-RPC")
	}
	if InterfaceCUxD.IsXMLRPC() {
		t.Error("CUxD must not be classified as XML-RPC")
	}
}

func TestJSONRPCOnlyIsAlwaysEmpty(t *testing.T) {
	// SPECIFICATION §7.1 — CCU-Jack ist gestrichen. Regression guard.
	if len(JSONRPCOnlyInterfaces) != 0 {
		t.Fatalf("JSONRPCOnlyInterfaces must remain empty, got %d entries", len(JSONRPCOnlyInterfaces))
	}
}

func TestEveryInterfaceSupportsRPCCallback(t *testing.T) {
	// SPECIFICATION §8.1 — alle Interfaces im Daemon pushen.
	all := []Interface{
		InterfaceHmIPRF, InterfaceBidCosRF, InterfaceBidCosWired,
		InterfaceVirtualDevices, InterfaceCUxD,
	}
	for _, i := range all {
		if !i.SupportsRPCCallback() {
			t.Errorf("interface %s must support RPC callback", i)
		}
	}
}

func TestFirmwareUpdateCapableSet(t *testing.T) {
	want := []Interface{InterfaceBidCosRF, InterfaceBidCosWired, InterfaceHmIPRF}
	for _, i := range want {
		if !i.SupportsFirmwareUpdates() {
			t.Errorf("%s should support firmware updates", i)
		}
	}
	if InterfaceCUxD.SupportsFirmwareUpdates() {
		t.Error("CUxD must not support firmware updates")
	}
}

func TestDetectionPortsComplete(t *testing.T) {
	// Every interface must have a detection port.
	all := []Interface{
		InterfaceHmIPRF, InterfaceBidCosRF, InterfaceBidCosWired,
		InterfaceVirtualDevices, InterfaceCUxD,
	}
	for _, i := range all {
		if _, ok := DetectionPorts[i]; !ok {
			t.Errorf("DetectionPorts missing entry for %s", i)
		}
	}
}

func TestIsJSONRPCOnlyAlwaysFalse(t *testing.T) {
	// The set is empty per spec — all interfaces support push in our daemon.
	all := []Interface{
		InterfaceHmIPRF, InterfaceBidCosRF, InterfaceBidCosWired,
		InterfaceVirtualDevices, InterfaceCUxD,
	}
	for _, i := range all {
		if i.IsJSONRPCOnly() {
			t.Errorf("%s.IsJSONRPCOnly() = true, want false", i)
		}
	}
}

func TestProductGroupForModel(t *testing.T) {
	cases := []struct {
		model string
		iface Interface
		want  ProductGroup
	}{
		// Model prefix wins over interface — that is the load-bearing
		// case for HmIP-Wired, which arrives through the HmIP-RF
		// interface and can only be told apart by its "hmipw-" prefix.
		{"HmIPW-DRDI3", InterfaceHmIPRF, ProductGroupHmIPW},
		{"HmIP-STH", InterfaceBidCosRF, ProductGroupHmIP},
		{"HMW-LC-Sw2-DR", InterfaceHmIPRF, ProductGroupHmW},
		{"HM-CC-RT-DN", InterfaceHmIPRF, ProductGroupHM},
		// Case-insensitive prefix match.
		{"hmipw-drdi3", InterfaceHmIPRF, ProductGroupHmIPW},
		// Interface fallback when the prefix is unknown.
		{"UNKNOWN", InterfaceHmIPRF, ProductGroupHmIP},
		{"UNKNOWN", InterfaceBidCosRF, ProductGroupHM},
		{"UNKNOWN", InterfaceBidCosWired, ProductGroupHmW},
		{"UNKNOWN", InterfaceVirtualDevices, ProductGroupVirtual},
		{"UNKNOWN", InterfaceCUxD, ProductGroupUnknown},
		// Empty model still falls through to the interface.
		{"", InterfaceHmIPRF, ProductGroupHmIP},
	}
	for _, tc := range cases {
		if got := ProductGroupForModel(tc.model, tc.iface); got != tc.want {
			t.Errorf("ProductGroupForModel(%q, %s) = %s, want %s",
				tc.model, tc.iface, got, tc.want)
		}
	}
}

func TestPushesConfigPending(t *testing.T) {
	// HmIP-RF pushes CONFIG_PENDING reliably (covers both HmIP-RF and
	// HmIP-Wired devices, which share the single HmIP service).
	if !InterfaceHmIPRF.PushesConfigPending() {
		t.Errorf("%s.PushesConfigPending() = false, want true", InterfaceHmIPRF)
	}
	// BidCos, CUxD, VirtualDevices do not.
	for _, i := range []Interface{InterfaceBidCosRF, InterfaceBidCosWired, InterfaceCUxD, InterfaceVirtualDevices} {
		if i.PushesConfigPending() {
			t.Errorf("%s.PushesConfigPending() = true, want false", i)
		}
	}
}

func TestPushesConfigPendingFor(t *testing.T) {
	// HmIP product groups always push, regardless of interface.
	if !PushesConfigPendingFor(InterfaceVirtualDevices, ProductGroupHmIP) {
		t.Error("HmIP on VirtualDevices should push CONFIG_PENDING")
	}
	if !PushesConfigPendingFor(InterfaceVirtualDevices, ProductGroupHmIPW) {
		t.Error("HmIPW on VirtualDevices should push CONFIG_PENDING")
	}
	// BidCos product groups never push.
	if PushesConfigPendingFor(InterfaceHmIPRF, ProductGroupHM) {
		t.Error("HM product group should not push CONFIG_PENDING")
	}
	if PushesConfigPendingFor(InterfaceHmIPRF, ProductGroupHmW) {
		t.Error("HmW product group should not push CONFIG_PENDING")
	}
	// Unknown product group falls back to interface classification.
	if !PushesConfigPendingFor(InterfaceHmIPRF, ProductGroupUnknown) {
		t.Error("Unknown group on HmIP-RF should fall back to interface: true")
	}
	if PushesConfigPendingFor(InterfaceCUxD, ProductGroupUnknown) {
		t.Error("Unknown group on CUxD should fall back to interface: false")
	}
}
