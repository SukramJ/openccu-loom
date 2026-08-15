// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

import (
	"path/filepath"
	"sort"
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

// TestConfigRestoreClassification pins the exact set of interfaces that
// expose `restoreConfigToDevice`: HmIP-RF and BidCos-RF, because rfd and
// HMIPServer implement the method while hs485d (BidCos-Wired) does not.
// VirtualDevices and CUxD are synchronous / have no stored-config concept
// and must not be members either.
func TestConfigRestoreClassification(t *testing.T) {
	t.Parallel()

	wantTrue := []hmenum.Interface{
		hmenum.InterfaceHmIPRF,
		hmenum.InterfaceBidCosRF,
	}
	wantFalse := []hmenum.Interface{
		hmenum.InterfaceBidCosWired,
		hmenum.InterfaceVirtualDevices,
		hmenum.InterfaceCUxD,
	}

	// Exactly the two radios whose CCU-side process (rfd / HMIPServer)
	// implements restoreConfigToDevice.
	if got := len(hmenum.InterfacesSupportingConfigRestore); got != 2 {
		t.Fatalf("InterfacesSupportingConfigRestore len=%d, want 2", got)
	}

	for _, iface := range wantTrue {
		if _, ok := hmenum.InterfacesSupportingConfigRestore[iface]; !ok {
			t.Errorf("%s missing from InterfacesSupportingConfigRestore", iface)
		}
		if !iface.SupportsConfigRestore() {
			t.Errorf("%s.SupportsConfigRestore() = false, want true", iface)
		}
	}

	for _, iface := range wantFalse {
		if _, ok := hmenum.InterfacesSupportingConfigRestore[iface]; ok {
			t.Errorf("%s must not be in InterfacesSupportingConfigRestore", iface)
		}
		if iface.SupportsConfigRestore() {
			t.Errorf("%s.SupportsConfigRestore() = true, want false", iface)
		}
	}
}

// TestReplaceClassification pins the exact set of interfaces whose daemon
// exposes `listReplaceableDevices` / `replaceDevice`: BidCos-RF and
// BidCos-Wired, because rfd and hs485d implement the guided device-replace
// swap while HMIPServer (HmIP-RF) throws NotImplementedException. CUxD and
// VirtualDevices have no pairing concept and must not be members either.
func TestReplaceClassification(t *testing.T) {
	t.Parallel()

	wantTrue := []hmenum.Interface{
		hmenum.InterfaceBidCosRF,
		hmenum.InterfaceBidCosWired,
	}
	wantFalse := []hmenum.Interface{
		hmenum.InterfaceHmIPRF,
		hmenum.InterfaceVirtualDevices,
		hmenum.InterfaceCUxD,
	}

	// Exactly the two radios whose CCU-side process (rfd / hs485d)
	// implements replaceDevice.
	if got := len(hmenum.InterfacesSupportingReplace); got != 2 {
		t.Fatalf("InterfacesSupportingReplace len=%d, want 2", got)
	}

	for _, iface := range wantTrue {
		if _, ok := hmenum.InterfacesSupportingReplace[iface]; !ok {
			t.Errorf("%s missing from InterfacesSupportingReplace", iface)
		}
		if !iface.SupportsReplace() {
			t.Errorf("%s.SupportsReplace() = false, want true", iface)
		}
	}

	for _, iface := range wantFalse {
		if _, ok := hmenum.InterfacesSupportingReplace[iface]; ok {
			t.Errorf("%s must not be in InterfacesSupportingReplace", iface)
		}
		if iface.SupportsReplace() {
			t.Errorf("%s.SupportsReplace() = true, want false", iface)
		}
	}
}

// TestDeviceSearchClassification pins InterfacesSupportingDeviceSearch to
// exactly {BidCos-Wired}: only hs485d implements the wired-bus scan
// `searchDevices`. RF pairing (HmIP-RF, BidCos-RF) uses setInstallMode +
// addDevice instead of a bus scan, and CUxD / VirtualDevices have no bus
// to scan at all.
func TestDeviceSearchClassification(t *testing.T) {
	t.Parallel()

	wantTrue := []hmenum.Interface{
		hmenum.InterfaceBidCosWired,
	}
	wantFalse := []hmenum.Interface{
		hmenum.InterfaceBidCosRF,
		hmenum.InterfaceHmIPRF,
		hmenum.InterfaceCUxD,
		hmenum.InterfaceVirtualDevices,
	}

	if got := len(hmenum.InterfacesSupportingDeviceSearch); got != 1 {
		t.Fatalf("InterfacesSupportingDeviceSearch len=%d, want 1", got)
	}

	for _, iface := range wantTrue {
		if _, ok := hmenum.InterfacesSupportingDeviceSearch[iface]; !ok {
			t.Errorf("%s missing from InterfacesSupportingDeviceSearch", iface)
		}
		if !iface.SupportsDeviceSearch() {
			t.Errorf("%s.SupportsDeviceSearch() = false, want true", iface)
		}
	}

	for _, iface := range wantFalse {
		if _, ok := hmenum.InterfacesSupportingDeviceSearch[iface]; ok {
			t.Errorf("%s must not be in InterfacesSupportingDeviceSearch", iface)
		}
		if iface.SupportsDeviceSearch() {
			t.Errorf("%s.SupportsDeviceSearch() = true, want false", iface)
		}
	}
}

// TestCommunicationTestClassification pins the exact set of interfaces on
// which the CCU's per-device communication/function test can run: HmIP-RF,
// BidCos-RF, and BidCos-Wired reach real devices over radio/bus. CUxD has
// no ReGa com-test scripts and VirtualDevices has no radio to test.
func TestCommunicationTestClassification(t *testing.T) {
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

	if got := len(hmenum.InterfacesSupportingCommunicationTest); got != 3 {
		t.Fatalf("InterfacesSupportingCommunicationTest len=%d, want 3", got)
	}

	for _, iface := range wantTrue {
		if _, ok := hmenum.InterfacesSupportingCommunicationTest[iface]; !ok {
			t.Errorf("%s missing from InterfacesSupportingCommunicationTest", iface)
		}
		if !iface.SupportsCommunicationTest() {
			t.Errorf("%s.SupportsCommunicationTest() = false, want true", iface)
		}
	}

	for _, iface := range wantFalse {
		if _, ok := hmenum.InterfacesSupportingCommunicationTest[iface]; ok {
			t.Errorf("%s must not be in InterfacesSupportingCommunicationTest", iface)
		}
		if iface.SupportsCommunicationTest() {
			t.Errorf("%s.SupportsCommunicationTest() = true, want false", iface)
		}
	}
}

// TestTeamsClassification pins the team-assignment interface set: rfd
// (BidCos-RF) and HMIPServer (HmIP-RF) implement setTeam/listTeams;
// BidCos-Wired, CUxD and VirtualDevices do not.
func TestTeamsClassification(t *testing.T) {
	t.Parallel()

	wantTrue := []hmenum.Interface{hmenum.InterfaceBidCosRF, hmenum.InterfaceHmIPRF}
	wantFalse := []hmenum.Interface{
		hmenum.InterfaceBidCosWired,
		hmenum.InterfaceVirtualDevices,
		hmenum.InterfaceCUxD,
	}

	if got := len(hmenum.InterfacesSupportingTeams); got != 2 {
		t.Fatalf("InterfacesSupportingTeams len=%d, want 2", got)
	}
	for _, iface := range wantTrue {
		if !iface.SupportsTeams() {
			t.Errorf("%s.SupportsTeams() = false, want true", iface)
		}
	}
	for _, iface := range wantFalse {
		if iface.SupportsTeams() {
			t.Errorf("%s.SupportsTeams() = true, want false", iface)
		}
	}
}

// TestDataPointCategoryClassificationIsExhaustive drives
// [hmenum.ValidateStartup] from the enum itself.
//
// ValidateStartup walks the hand-maintained [hmenum.AllDataPointCategories]
// slice, so a category missing from that slice is invisible to it — which is
// exactly what happened to alarm_control_panel: declared, returned by the model
// and mapped in CategoryToType, but absent from the slice, so neither the
// exhaustiveness check nor any other walk driven from it would have noticed had
// its classification entry been missing too. A category that reaches no
// classification set serves an empty data_point_type on REST and on the
// WebSocket classify plane.
//
// Parsing the const block is what makes this a guard rather than a second
// hand-maintained list.
func TestDataPointCategoryClassificationIsExhaustive(t *testing.T) {
	t.Parallel()

	declared := extractEnumConstantsFromSource(t,
		filepath.Join(repoRoot(t), "pkg", "hmenum", "datapoint.go"), "DataPointCategory")

	listed := make(map[string]struct{}, len(hmenum.AllDataPointCategories))
	for _, c := range hmenum.AllDataPointCategories {
		listed[string(c)] = struct{}{}
	}

	var missing []string
	for goName, wire := range declared {
		// The undefined sentinel is deliberately absent: it classifies
		// nothing and is blocked from every north-bound plane.
		if wire == string(hmenum.DataPointCategoryUndefined) {
			continue
		}
		if _, ok := listed[wire]; !ok {
			missing = append(missing, goName)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Errorf("hmenum.AllDataPointCategories is missing %d declared category/categories: %v", len(missing), missing)
	}

	if err := hmenum.ValidateStartup(); err != nil {
		t.Errorf("ValidateStartup: %v", err)
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
