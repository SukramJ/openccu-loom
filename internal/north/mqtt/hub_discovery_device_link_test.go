// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ─── Sysvar → physical-device linking ────────────────────────────────────────

// TestSysvarDiscoveryWithDeviceAddressLinksToPhysicalDevice pins that a
// sysvar carrying a DeviceAddress attaches its HA entity to the physical
// device's card (identifiers = openccu-loom_<lower(addr)>) instead of the
// synthetic central hub card, and stamps via_device pointing back at the
// central so HA can order the device even if it hasn't seen it yet.
func TestSysvarDiscoveryWithDeviceAddressLinksToPhysicalDevice(t *testing.T) {
	t.Parallel()
	db := newHubBuilder()
	item := db.BuildSysvarDiscovery("ccu-01", HubSysvarSpec{
		Name:          "svEnergy",
		ValueType:     hmenum.HubValueTypeFloat,
		Writable:      false,
		DeviceAddress: "0001ABCD",
	})
	m := jsonMap(t, item)

	dev, ok := m["device"].(map[string]any)
	if !ok {
		t.Fatalf("device block missing or wrong type: %v", m["device"])
	}

	ids, _ := dev["identifiers"].([]any)
	if len(ids) != 1 || ids[0] != "openccu-loom_0001abcd" {
		t.Fatalf("identifiers: got %v want [openccu-loom_0001abcd]", ids)
	}
	for _, id := range ids {
		if id == "openccu-loom_central_ccu-01" {
			t.Fatalf("device-linked sysvar must NOT carry the central hub identifier, got %v", ids)
		}
	}
	if dev["via_device"] != "openccu-loom_central_ccu-01" {
		t.Fatalf("via_device: got %v want %q", dev["via_device"], "openccu-loom_central_ccu-01")
	}
}

// TestSysvarDiscoveryWithoutDeviceAddressStaysOnHubCard verifies backward
// compatibility: an unlinked sysvar (DeviceAddress=="") keeps the unchanged
// synthetic central hub device block.
func TestSysvarDiscoveryWithoutDeviceAddressStaysOnHubCard(t *testing.T) {
	t.Parallel()
	db := newHubBuilder()
	item := db.BuildSysvarDiscovery("ccu-01", HubSysvarSpec{
		Name:      "svEnergy",
		ValueType: hmenum.HubValueTypeFloat,
		Writable:  false,
	})
	m := jsonMap(t, item)

	dev, ok := m["device"].(map[string]any)
	if !ok {
		t.Fatalf("device block missing or wrong type: %v", m["device"])
	}
	ids, _ := dev["identifiers"].([]any)
	if len(ids) != 1 || ids[0] != "openccu-loom_central_ccu-01" {
		t.Fatalf("identifiers: got %v want [openccu-loom_central_ccu-01]", ids)
	}
	if _, has := dev["via_device"]; has {
		t.Fatalf("hub-card device block must not carry via_device, got %v", dev["via_device"])
	}
}

// ─── Program → physical-device linking ───────────────────────────────────────

// TestProgramDiscoveryWithDeviceAddressLinksToPhysicalDevice mirrors the
// sysvar case for BuildProgramDiscovery's deviceAddress parameter.
func TestProgramDiscoveryWithDeviceAddressLinksToPhysicalDevice(t *testing.T) {
	t.Parallel()
	db := newHubBuilder()
	item := db.BuildProgramDiscovery("ccu-01", HubProgramSpec{ID: "PRG_42", Name: "Morning Lights", DeviceAddress: "0001ABCD"})
	m := jsonMap(t, item)

	dev, ok := m["device"].(map[string]any)
	if !ok {
		t.Fatalf("device block missing or wrong type: %v", m["device"])
	}
	ids, _ := dev["identifiers"].([]any)
	if len(ids) != 1 || ids[0] != "openccu-loom_0001abcd" {
		t.Fatalf("identifiers: got %v want [openccu-loom_0001abcd]", ids)
	}
	if dev["via_device"] != "openccu-loom_central_ccu-01" {
		t.Fatalf("via_device: got %v want %q", dev["via_device"], "openccu-loom_central_ccu-01")
	}
}

// TestProgramDiscoveryWithoutDeviceAddressStaysOnHubCard verifies backward
// compatibility for programs: an empty deviceAddress keeps the unchanged
// synthetic central hub device block.
func TestProgramDiscoveryWithoutDeviceAddressStaysOnHubCard(t *testing.T) {
	t.Parallel()
	db := newHubBuilder()
	item := db.BuildProgramDiscovery("ccu-01", HubProgramSpec{ID: "PRG_42", Name: "Morning Lights"})
	m := jsonMap(t, item)

	dev, ok := m["device"].(map[string]any)
	if !ok {
		t.Fatalf("device block missing or wrong type: %v", m["device"])
	}
	ids, _ := dev["identifiers"].([]any)
	if len(ids) != 1 || ids[0] != "openccu-loom_central_ccu-01" {
		t.Fatalf("identifiers: got %v want [openccu-loom_central_ccu-01]", ids)
	}
}

// ─── Identifier consistency with the per-device discovery path ──────────────

// TestHubEntityDeviceIdentifierMatchesPerDeviceDiscovery is the load-bearing
// contract test: HA only merges an entity into a device card when the
// `identifiers` value matches byte-for-byte. This pins that the identifier
// [hubEntityDeviceBlock] emits for a device-linked sysvar/program is
// IDENTICAL to the identifier [deviceDescriptor] emits for a regular
// per-DP discovery of the very same physical device.
func TestHubEntityDeviceIdentifierMatchesPerDeviceDiscovery(t *testing.T) {
	t.Parallel()

	const addr = "0001ABCD"
	ev := Event{
		Central:       "ccu-01",
		DeviceAddress: addr,
	}
	perDeviceDesc := deviceDescriptor(ev, "", false)
	perDeviceIDs, _ := perDeviceDesc["identifiers"].([]string)
	if len(perDeviceIDs) != 1 {
		t.Fatalf("deviceDescriptor identifiers: got %v want a single-element slice", perDeviceDesc["identifiers"])
	}

	db := newHubBuilder()
	item := db.BuildSysvarDiscovery("ccu-01", HubSysvarSpec{
		Name:          "svEnergy",
		ValueType:     hmenum.HubValueTypeFloat,
		DeviceAddress: addr,
	})
	m := jsonMap(t, item)
	dev, ok := m["device"].(map[string]any)
	if !ok {
		t.Fatalf("device block missing or wrong type: %v", m["device"])
	}
	hubIDs, _ := dev["identifiers"].([]any)
	if len(hubIDs) != 1 {
		t.Fatalf("hub identifiers: got %v want a single-element slice", dev["identifiers"])
	}

	if hubIDs[0] != perDeviceIDs[0] {
		t.Fatalf("identifier mismatch: hub-entity %q vs per-device %q — HA would not merge these into one card", hubIDs[0], perDeviceIDs[0])
	}
	// Also pin against the shared helper directly.
	if want := physicalDeviceIdentifier(addr); hubIDs[0] != want {
		t.Fatalf("hub identifier %q != physicalDeviceIdentifier(%q) = %q", hubIDs[0], addr, want)
	}
}

// ─── Case-insensitivity ──────────────────────────────────────────────────────

// TestHubEntityDeviceIdentifierIsLowercased verifies that an upper-case
// DeviceAddress (the CCU's native casing) still produces a lower-cased
// identifier, matching the per-device discovery path's normalisation.
func TestHubEntityDeviceIdentifierIsLowercased(t *testing.T) {
	t.Parallel()
	db := newHubBuilder()
	item := db.BuildSysvarDiscovery("ccu-01", HubSysvarSpec{
		Name:          "svEnergy",
		ValueType:     hmenum.HubValueTypeFloat,
		DeviceAddress: "0001ABCD",
	})
	m := jsonMap(t, item)
	dev, ok := m["device"].(map[string]any)
	if !ok {
		t.Fatalf("device block missing or wrong type: %v", m["device"])
	}
	ids, _ := dev["identifiers"].([]any)
	if len(ids) != 1 {
		t.Fatalf("identifiers: got %v want a single-element slice", dev["identifiers"])
	}
	id, _ := ids[0].(string)
	for _, r := range id {
		if r >= 'A' && r <= 'Z' {
			t.Fatalf("identifier %q contains an upper-case rune", id)
		}
	}
	if id != "openccu-loom_0001abcd" {
		t.Fatalf("identifier: got %q want %q", id, "openccu-loom_0001abcd")
	}
}

// TestDeviceViaDeviceMatchesTheHubCardIdentifier asserts the two producers of
// the central's device identifier agree byte-for-byte.
//
// Home Assistant resolves a parent only on an exact identifiers match, so a
// per-device card whose `via_device` spells the central differently from the
// hub card's `identifiers` loses the whole hierarchy: every physical device
// floats at the top level instead of nesting under the CCU, and the sysvar /
// program entities that ride the device-linked block lose their link too.
//
// The assertion compares the two producers rather than a literal, because a
// literal only pins today's spelling — it is the disagreement that breaks HA.
func TestDeviceViaDeviceMatchesTheHubCardIdentifier(t *testing.T) {
	t.Parallel()

	// A name the escaping actually rewrites: with a bare ASCII slug both
	// spellings coincide and the comparison would be vacuous.
	const central = "Haus CCÜ"

	hubIDs, ok := hubDeviceBlock(central, HubInfo{})["identifiers"].([]string)
	if !ok || len(hubIDs) != 1 {
		t.Fatalf("hub card identifiers = %v, want exactly one", hubIDs)
	}

	desc := deviceDescriptor(Event{Central: central, DeviceAddress: "0001ABCD"}, "", false)
	if got := desc["via_device"]; got != hubIDs[0] {
		t.Fatalf("per-device via_device = %v, hub card identifier = %q — HA cannot resolve the parent", got, hubIDs[0])
	}

	linked := hubEntityDeviceBlock(central, "0001ABCD", HubInfo{})
	if got := linked["via_device"]; got != hubIDs[0] {
		t.Fatalf("device-linked hub entity via_device = %v, hub card identifier = %q", got, hubIDs[0])
	}
}
