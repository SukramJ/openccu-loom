// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestInterfaceWireStrings locks the exact wire tokens every CCU
// interface uses. Any drift from these strings silently reinterprets
// recorded sessions, paramset patches, and config files.
func TestInterfaceWireStrings(t *testing.T) {
	cases := map[hmenum.Interface]string{
		hmenum.InterfaceHmIPRF:         "HmIP-RF",
		hmenum.InterfaceBidCosRF:       "BidCos-RF",
		hmenum.InterfaceBidCosWired:    "BidCos-Wired",
		hmenum.InterfaceVirtualDevices: "VirtualDevices",
		hmenum.InterfaceCUxD:           "CUxD",
	}
	for iface, want := range cases {
		if got := iface.String(); got != want {
			t.Errorf("%s wire token=%q, want %q", want, got, want)
		}
	}
}

// TestCommandPriorityCriticalIsZero enforces CLAUDE.md §Critical Rules.
// If CRITICAL becomes non-zero, every `if priority != 0` in the
// codebase silently changes meaning.
func TestCommandPriorityCriticalIsZero(t *testing.T) {
	if hmenum.CommandPriorityCritical != 0 {
		t.Fatalf("CommandPriorityCritical=%d, must be 0", hmenum.CommandPriorityCritical)
	}
}

// TestBitmaskZeroIsEmpty locks in the Operations / Flag zero-value
// semantics: no bits set.
func TestBitmaskZeroIsEmpty(t *testing.T) {
	if hmenum.OperationsNone != 0 {
		t.Errorf("OperationsNone=%d, want 0", hmenum.OperationsNone)
	}
	var f hmenum.Flag
	if f.IsVisible() || f.IsInternal() || f.IsService() || f.IsTransform() || f.IsSticky() {
		t.Error("zero Flag must report no bits")
	}
}

// TestAllInterfacesPush pins down SPECIFICATION §8.1: every interface
// supports push callbacks. CCU-Jack was removed, so there is no
// pull-only path anymore.
func TestAllInterfacesPush(t *testing.T) {
	all := []hmenum.Interface{
		hmenum.InterfaceHmIPRF, hmenum.InterfaceBidCosRF,
		hmenum.InterfaceBidCosWired, hmenum.InterfaceVirtualDevices,
		hmenum.InterfaceCUxD,
	}
	for _, i := range all {
		if !i.SupportsRPCCallback() {
			t.Errorf("%s must support RPC callback", i)
		}
	}
	if len(hmenum.JSONRPCOnlyInterfaces) != 0 {
		t.Fatalf("JSONRPCOnlyInterfaces must be empty, got %d", len(hmenum.JSONRPCOnlyInterfaces))
	}
}

// TestCUxDIsBINRPCOnly enforces the "CUxD uses BIN-RPC" rule: CUxD
// must be in BINRPCInterfaces and must not appear anywhere else.
func TestCUxDIsBINRPCOnly(t *testing.T) {
	if !hmenum.InterfaceCUxD.IsBINRPC() {
		t.Error("CUxD must be in BINRPCInterfaces")
	}
	if hmenum.InterfaceCUxD.IsXMLRPC() {
		t.Error("CUxD must not be classified as XML-RPC")
	}
	if hmenum.InterfaceCUxD.IsJSONRPCOnly() {
		t.Error("CUxD must not be JSON-RPC-only")
	}
}

// TestInstallModeClassification pins the exact set of interfaces that support
// install (pairing) mode: HmIP-RF, BidCos-RF, and BidCos-Wired. VirtualDevices
// and CUxD must not be members — they cannot pair physical devices.
func TestInstallModeClassification(t *testing.T) {
	t.Parallel()

	wantTrue := []hmenum.Interface{
		hmenum.InterfaceHmIPRF,
		hmenum.InterfaceBidCosRF,
		hmenum.InterfaceBidCosWired,
	}
	wantFalse := []hmenum.Interface{
		hmenum.InterfaceVirtualDevices,
		hmenum.InterfaceCUxD,
	}

	// The set must contain exactly the three pairing-capable radios.
	if got := len(hmenum.InterfacesSupportingInstallMode); got != 3 {
		t.Fatalf("InterfacesSupportingInstallMode len=%d, want 3", got)
	}

	for _, iface := range wantTrue {
		if _, ok := hmenum.InterfacesSupportingInstallMode[iface]; !ok {
			t.Errorf("%s missing from InterfacesSupportingInstallMode", iface)
		}
		if !iface.SupportsInstallMode() {
			t.Errorf("%s.SupportsInstallMode() = false, want true", iface)
		}
	}

	for _, iface := range wantFalse {
		if _, ok := hmenum.InterfacesSupportingInstallMode[iface]; ok {
			t.Errorf("%s must not be in InterfacesSupportingInstallMode", iface)
		}
		if iface.SupportsInstallMode() {
			t.Errorf("%s.SupportsInstallMode() = true, want false", iface)
		}
	}
}

// TestCategoryToTypeCovers ensures every real DataPointCategory maps to
// a DataPointType. UNDEFINED is intentionally omitted.
func TestCategoryToTypeCovers(t *testing.T) {
	if _, ok := hmenum.CategoryToType[hmenum.DataPointCategoryUndefined]; ok {
		t.Error("undefined category must not have a mapping")
	}
	// Spot-check a handful of critical mappings.
	expect := map[hmenum.DataPointCategory]hmenum.DataPointType{
		hmenum.DataPointCategoryBinarySensor: hmenum.DataPointTypeBinarySensor,
		hmenum.DataPointCategoryClimate:      hmenum.DataPointTypeClimate,
		hmenum.DataPointCategoryCover:        hmenum.DataPointTypeCover,
		hmenum.DataPointCategorySwitch:       hmenum.DataPointTypeSwitch,
		hmenum.DataPointCategoryLight:        hmenum.DataPointTypeLight,
	}
	for cat, want := range expect {
		if got := hmenum.CategoryToType[cat]; got != want {
			t.Errorf("%s → %s, want %s", cat, got, want)
		}
	}
}
