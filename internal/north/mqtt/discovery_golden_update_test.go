// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

var updateDeviceUpdateGolden = flag.Bool("update-device-update-golden", false,
	"rewrite the pinned per-device firmware-update discovery payloads")

var deviceUpdateGoldenPath = filepath.Join("testdata", "discovery_golden_update.json")

// duGoldenParent wraps a real device and additionally reports the multi-group
// structure that switches [deviceDescriptor] onto its sub-device branch on
// every other plane. It exists to pin that the branch does NOT switch here —
// see the "sub-devices-enabled" fixture.
type duGoldenParent struct {
	*device.Device
}

func (duGoldenParent) HasSubDevices() bool { return true }

// duGoldenDevice builds a device the way the model hands it to the discovery
// path, so `model`, `name`, the manufacturer and the firmware in the device
// block come from the same harvest production uses rather than from literals
// in this file.
func duGoldenDevice(ifaceID string, iface hmenum.Interface, address, model, name string, fw device.FirmwareInfo) *device.Device {
	return device.New(device.Config{
		InterfaceID:  ifaceID,
		Interface:    iface,
		Address:      address,
		Model:        model,
		Name:         name,
		Manufacturer: hmenum.ManufacturerEQ3,
		Updatable:    true,
		Firmware:     fw,
	})
}

// duGoldenCase is one input to the update builder, named so a diff says which
// shape moved rather than only which topic.
type duGoldenCase struct {
	name string
	ev   UpdateEvent
}

// duGoldenCases is the fixture matrix for the per-device firmware-update
// plane.
//
// The plane emits exactly one component — an `update` — per updatable device,
// so the matrix is not one case per component but one case per way that
// single shape resolves differently: the device block, the `title` the
// platform fields carry, and the three identifiers, which this plane derives
// three DIFFERENT ways from the same event and therefore cannot be reasoned
// about from one fixture:
//
//   - unique_id comes from [DefaultDiscoveryBuilder.scopedUniqueID] over the
//     device address, so it carries no central,
//   - node_id comes from [discoveryNodeID], so it carries a slugged central,
//   - object_id is the unique id again, and also seeds `default_entity_id`.
//
// It is not a fleet. A real CCU's retained configs cannot be reconstructed in
// CI; this pins what CI can reproduce, and the scope note on
// TestDeviceUpdatePayloadsArePinned says what it leaves out.
func duGoldenCases() []duGoldenCase {
	// The production shape: an HmIP thermostat, the interface whose update
	// lifecycle is the richest (it is the only one with a meaningful
	// in-progress phase).
	hmipDev := duGoldenDevice("HmIP-RF", hmenum.InterfaceHmIPRF, "VCU1234567", "HmIP-eTRV-2", "Heizung Wohnzimmer",
		device.FirmwareInfo{Current: "1.4.2", Available: "1.6.0"})
	hmip := UpdateEvent{
		Central:       "ccu-01",
		Interface:     "HmIP-RF",
		DeviceAddress: "VCU1234567",
		DeviceName:    "Heizung Wohnzimmer",
		Model:         "HmIP-eTRV-2",
		Device:        hmipDev,
		Update:        device.NewUpdate(hmipDev, nil, nil),
	}

	// A BidCos-RF device. Both the model — which the payload's `title` embeds
	// verbatim — and the device block move. Pinned as a second device family
	// because the `title` is assembled from the model string rather than
	// translated, so a migration that routed it through the translator would
	// be invisible against a single fixture.
	bidcosDev := duGoldenDevice("BidCos-RF", hmenum.InterfaceBidCosRF, "LEQ0123456", "HM-CC-RT-DN", "Heizung Bad",
		device.FirmwareInfo{Current: "1.4", Available: "1.4"})
	bidcos := UpdateEvent{
		Central:       "ccu-01",
		Interface:     "BidCos-RF",
		DeviceAddress: "LEQ0123456",
		DeviceName:    "Heizung Bad",
		Model:         "HM-CC-RT-DN",
		Device:        bidcosDev,
		Update:        device.NewUpdate(bidcosDev, nil, nil),
	}

	// No device object. UpdateEvent.Device is documented as optional, and the
	// caller that omits it gets a device block assembled from the event's own
	// DeviceName / Model instead of from the model harvest. Pinned because the
	// two paths must agree: an entity whose device block identifies
	// differently from its siblings' lands on a second device card beside the
	// real one.
	noDevice := hmip
	noDevice.Device = nil

	// Sub-device split enabled. On every other plane a device that reports
	// sub-devices re-homes the entity onto "<parent>-<group>". It cannot here:
	// [deviceDescriptor] needs a channel inspector and UpdateEvent carries
	// none — the update entity is device-level and has no channel number at
	// all. That outcome is pinned deliberately: a migration that "fixed" it in
	// passing would silently move every firmware-update entity on every
	// multi-group device to a different device card.
	subDevice := hmip
	subDevice.Device = duGoldenParent{Device: hmipDev}

	// The same device on a second central. Three identifiers, three
	// behaviours, all in one payload: the node id is central-scoped and MUST
	// change; the unique id is derived from the device address, which is
	// globally unique, and MUST NOT change — and because the object id is the
	// unique id, neither may the entity-id seed. Home Assistant keys its
	// entity registry on unique_id, so scoping that one too would re-key every
	// firmware-update entity on every fleet at once.
	secondCentral := hmip
	secondCentral.Central = "ccu-02"

	// A central named with capitals. [hmtypes.ValidateCentralName] holds every
	// central name to [A-Za-z0-9_-]+, so case is the only escaping question a
	// real name can raise — and it raises it twice: the node id is case-folded
	// by [naming.DiscoverySlug], while the state and availability topics carry
	// the name verbatim. Every other fixture here uses a lower-case central and
	// therefore cannot tell the two apart.
	mixedCaseCentral := hmip
	mixedCaseCentral.Central = "CCU-Keller"

	// A device the CCU has not reported a model for. The `title` is the model
	// string with " Firmware" appended, so an empty model produces a leading
	// space — pinned as-is rather than quietly trimmed, because trimming it is
	// a payload change like any other and belongs in its own commit.
	blankModelDev := duGoldenDevice("HmIP-RF", hmenum.InterfaceHmIPRF, "VCU7654321", "", "Unbekannt",
		device.FirmwareInfo{Current: "1.0.0", Available: "1.0.0"})
	blankModel := UpdateEvent{
		Central:       "ccu-01",
		Interface:     "HmIP-RF",
		DeviceAddress: "VCU7654321",
		DeviceName:    "Unbekannt",
		Device:        blankModelDev,
		Update:        device.NewUpdate(blankModelDev, nil, nil),
	}

	return []duGoldenCase{
		{"update/hmip", hmip},
		{"update/bidcos", bidcos},
		{"update/no-device-object", noDevice},
		{"update/sub-devices-enabled", subDevice},
		{"update/second-central", secondCentral},
		{"update/mixed-case-central", mixedCaseCentral},
		{"update/blank-model", blankModel},
	}
}

// TestDeviceUpdatePayloadsArePinned pins the topic and the payload of every
// entity shape discovery_update.go produces, so the move of this daemon's
// discovery layer onto the shared model (ADR 0070) can be checked against the
// one criterion that matters: the bytes do not change.
//
// The per-device `update` entity is the only Home Assistant surface on which a
// device's firmware level and the version an install would target are visible
// at all, so a silent change here does not degrade a control — it removes the
// fleet's only view of which devices are behind.
//
// The topic is pinned alongside the body deliberately. Two of the three ways
// this move can break are invisible in the body: a changed node id and a
// changed object id both publish the new config on a new topic. Home Assistant
// reports none of the three — the old entity is orphaned and a new one appears
// beside it, with nothing in any log.
//
// The payload is stored decoded so the file stays reviewable; comparison is on
// the canonical re-encoding of both sides, exact for every key and value, with
// only whitespace — which no consumer sees — out of scope.
//
// Scope. This pins what the builder produces, not what reaches a broker:
// retain flags, QoS, publish ordering and the orphan sweep that removes a
// superseded config are all outside it. It pins the payload's keys and values,
// not Home Assistant's acceptance of them. It does not pin the state traffic on
// the topics the payload names, only the topic strings themselves — the state
// body is [Bridge.PublishUpdateState]'s, covered elsewhere. And it does not
// pin the cases that emit nothing (a nil Update source, a source whose
// component carries no platform, an unresolvable unique id); those are covered
// in the plane's own tests.
//
// A diff here is a regression until someone shows otherwise. Refresh with:
//
//	go test ./internal/north/mqtt/ -run TestDeviceUpdatePayloadsArePinned -update-device-update-golden
func TestDeviceUpdatePayloadsArePinned(t *testing.T) {
	tb := NewTopicBuilder("gh")
	db := NewDefaultDiscoveryBuilder(tb, "ccu-01")
	// Every central has to be known before the builder can answer for any of
	// them: the unique-id scope decision reads the owning central's serial.
	db.SetHubInfoFor("ccu-01", HubInfo{Serial: "3014F711A0001234"})
	db.SetHubInfoFor("ccu-02", HubInfo{Serial: "3014F711B0005678"})
	db.SetHubInfoFor("CCU-Keller", HubInfo{Serial: "3014F711C0009012"})
	// On, so the sub-device fixture exercises the branch rather than being
	// short-circuited by the flag before it reaches the interesting check.
	db.SubDevicesEnabled = true

	got := map[string]goldenEntry{}
	for _, c := range duGoldenCases() {
		item := db.BuildUpdateDiscovery(c.ev.Central, c.ev)
		if !item.OK {
			t.Fatalf("%s: BuildUpdateDiscovery returned OK=false — the fixture no longer produces an entity", c.name)
		}
		// Decoded to a map so the pin is stable against field order in the
		// builder and unstable against a changed key or value — exactly the
		// sensitivity wanted.
		var body map[string]any
		if err := json.Unmarshal(item.Payload, &body); err != nil {
			t.Fatalf("%s: unmarshal: %v", c.name, err)
		}
		got[c.name] = goldenEntry{
			Topic:   tb.DiscoveryConfig(item.Component, item.NodeID, item.ObjectID),
			Payload: body,
		}
	}

	if *updateDeviceUpdateGolden {
		writeDeviceUpdateGolden(t, got)
		t.Logf("rewrote %s with %d payloads", deviceUpdateGoldenPath, len(got))
		return
	}

	want := readDeviceUpdateGolden(t)
	names := make([]string, 0, len(got))
	for name := range got {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		w, pinned := want[name]
		if !pinned {
			t.Errorf("%s: not in the pin — a new shape appeared; refresh the pin deliberately", name)
			continue
		}
		if got[name].Topic != w.Topic {
			t.Errorf("%s: topic\n got %s\nwant %s\n(a changed node id or object id orphans every entity under it, silently)",
				name, got[name].Topic, w.Topic)
		}
		if g, wantBody := canonical(t, got[name].Payload), canonical(t, w.Payload); g != wantBody {
			t.Errorf("%s: payload\n got %s\nwant %s", name, g, wantBody)
		}
	}
	for name := range want {
		if _, still := got[name]; !still {
			t.Errorf("%s: in the pin but no longer produced — an entity disappeared", name)
		}
	}
}

func readDeviceUpdateGolden(t *testing.T) map[string]goldenEntry {
	t.Helper()
	raw, err := os.ReadFile(deviceUpdateGoldenPath)
	if err != nil {
		t.Fatalf("read %s: %v (create it with -update-device-update-golden)", deviceUpdateGoldenPath, err)
	}
	var out map[string]goldenEntry
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("parse %s: %v", deviceUpdateGoldenPath, err)
	}
	return out
}

func writeDeviceUpdateGolden(t *testing.T, entries map[string]goldenEntry) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(deviceUpdateGoldenPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	raw, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(deviceUpdateGoldenPath, append(raw, '\n'), 0o600); err != nil {
		t.Fatalf("write %s: %v", deviceUpdateGoldenPath, err)
	}
}
