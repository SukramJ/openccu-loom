// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"encoding/json"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/routingkey"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// cuxdSwitchDiscovery builds the discovery payload a real install emits
// for the CUxD switch at CUX2801001:1 — through the production builder,
// with the CCU serial registered the way the hub bring-up registers it,
// so the identity under test is the one that reaches the broker.
func cuxdSwitchDiscovery(t *testing.T) map[string]any {
	t.Helper()
	d := device.New(device.Config{
		InterfaceID:  "CUxD",
		Interface:    hmenum.InterfaceCUxD,
		Address:      "CUX2801001",
		Model:        "CUxD-Switch",
		Name:         "Sonos Schlafzimmer",
		Manufacturer: hmenum.ManufacturerEQ3,
	})
	db := NewDefaultDiscoveryBuilder(NewTopicBuilder("openccu-loom"), "ccu")
	db.SetHubInfoFor("ccu", HubInfo{Serial: "3014F711A0001234"})

	_, _, _, buf, ok := db.Build(Event{
		Central: "ccu", Interface: "CUxD", DeviceAddress: "CUX2801001", ChannelNo: 1,
		Parameter: "STATE", Category: hmenum.DataPointCategorySwitch, Device: d,
	})
	if !ok {
		t.Fatal("the builder withheld the CUxD switch entity")
	}
	var payload map[string]any
	if err := json.Unmarshal(buf, &payload); err != nil {
		t.Fatalf("unmarshal discovery payload: %v", err)
	}
	return payload
}

// TestCUxDDiscoveryIdentityIsCentralScoped pins the entity identity a real
// install publishes for a CUxD data point, on both sides of the change
// that introduced it.
//
// CUxD serials are "CUX" + a device type + a running number the operator
// picks per CCU, so two CCUs declare the identical id unless the CCU
// serial is prepended. Home Assistant keys its entity registry on that id
// and keeps whichever CCU arrived first; the discovery payload is
// retained, so the second CCU's entities stay missing until someone clears
// the topic by hand.
//
// The legacy id is asserted too, because the discovery TOPIC is built from
// the address and did not change with the id: an install upgrading across
// the change has the old id sitting in Home Assistant's registry while the
// broker carries the new one, and only the boot sweep
// ([carriesLegacyUnscopedCUxDID]) turns that into one clean entity instead
// of an orphan plus a duplicate.
func TestCUxDDiscoveryIdentityIsCentralScoped(t *testing.T) {
	t.Parallel()

	const (
		serialSuffix = "11a0001234"
		currentID    = "loom_" + serialSuffix + "_cux2801001_1_state"
		legacyID     = "loom_cux2801001_1_state"
	)

	payload := cuxdSwitchDiscovery(t)

	if got := payload["unique_id"]; got != currentID {
		t.Errorf("unique_id = %v, want %q — a second CCU's CUxD entities collide without the serial", got, currentID)
	}
	// The device block has to carry the same discriminator, or two CCUs'
	// CUxD devices merge into one Home Assistant device card.
	desc, _ := payload["device"].(map[string]any)
	ids, _ := desc["identifiers"].([]any)
	if len(ids) != 1 || ids[0] != "openccu-loom_ccu_cux2801001" {
		t.Errorf("device identifiers = %v, want the central-scoped one", ids)
	}

	// The id the previous build published for the very same topic, and the
	// one the Python reference still rebuilds: the folded address with no
	// central discriminator at all. Derived rather than spelled out,
	// because the channel-level key is unscoped for CUxD on both sides and
	// therefore carries exactly that spelling.
	if legacy := "loom_" + routingkey.GenerateChannelUniqueID("", "CUX2801001:1") + "_state"; legacy != legacyID {
		t.Fatalf("legacy id spelled %q, want %q; the sweep matcher below is pinned against the wrong string", legacy, legacyID)
	}
	if !carriesLegacyUnscopedCUxDID(legacyID) {
		t.Errorf("the sweep does not recognise %q as stale, so the retained config is overwritten in place "+
			"and Home Assistant keeps the old entity beside the new one", legacyID)
	}
	if carriesLegacyUnscopedCUxDID(currentID) {
		t.Errorf("the sweep classifies the current id %q as stale; it would clear a config it just published", currentID)
	}
}

// TestCUxDDiscoveryWithheldUntilSerialIsKnown pins that a CUxD entity is
// not published while the CCU serial is unresolved. Publishing without it
// would put the ambiguous id on a retained topic, which outlives the boot
// that produced it; skipping is recoverable, because the snapshot that
// follows the hub bring-up publishes what was skipped.
func TestCUxDDiscoveryWithheldUntilSerialIsKnown(t *testing.T) {
	t.Parallel()

	d := device.New(device.Config{
		InterfaceID: "CUxD", Interface: hmenum.InterfaceCUxD,
		Address: "CUX2801001", Model: "CUxD-Switch", Manufacturer: hmenum.ManufacturerEQ3,
	})
	db := NewDefaultDiscoveryBuilder(NewTopicBuilder("openccu-loom"), "ccu")

	if _, _, _, _, ok := db.Build(Event{
		Central: "ccu", Interface: "CUxD", DeviceAddress: "CUX2801001", ChannelNo: 1,
		Parameter: "STATE", Category: hmenum.DataPointCategorySwitch, Device: d,
	}); ok {
		t.Error("a CUxD entity was published before the CCU serial was known; its id is ambiguous across CCUs")
	}
}
