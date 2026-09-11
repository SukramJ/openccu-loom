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
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

var updateNotifyGolden = flag.Bool("update-notify-golden", false,
	"rewrite the pinned text-display notify discovery payloads")

var notifyGoldenPath = filepath.Join("testdata", "discovery_golden_notify.json")

// notifyGoldenChannel is the channel inspector the notify fixtures drive
// [displayChannelName] and [deviceDescriptor] with. It carries exactly the
// three optional extensions the notify plane reads — the custom-DP naming
// classification, the model-composed display name, and the sub-device
// grouping — so a fixture can select a name shape or a sub-device split
// without standing up a real device.Channel graph.
type notifyGoldenChannel struct {
	primary     bool
	displayName string
	groupNo     int
	multiGroup  bool
	subName     string
}

func (c notifyGoldenChannel) HasParameter(string) bool         { return false }
func (c notifyGoldenChannel) IsCustomDPPrimaryChannel() bool   { return c.primary }
func (c notifyGoldenChannel) IsCustomDPSecondaryChannel() bool { return false }
func (c notifyGoldenChannel) HasSinglePrimaryCustomDP() bool   { return true }
func (c notifyGoldenChannel) CustomDPDisplayName() string      { return c.displayName }
func (c notifyGoldenChannel) GroupNumber() int                 { return c.groupNo }
func (c notifyGoldenChannel) IsInMultiGroup() bool             { return c.multiGroup }
func (c notifyGoldenChannel) SubDeviceName() string            { return c.subName }

// notifyGoldenParent wraps a real device so the device block of the
// sub-device fixture is harvested from the same model the production path
// harvests it from, while still reporting the multi-group structure that
// switches [deviceDescriptor] onto its sub-device branch.
type notifyGoldenParent struct {
	*device.Device
}

func (notifyGoldenParent) HasSubDevices() bool { return true }

// notifyGoldenDevice is the HmIP-WRCD as the model hands it to the
// discovery path: a real device object, so `model`, `name` and the
// manufacturer in the device block come from the same harvest production
// uses rather than from a literal in this file.
func notifyGoldenDevice() *device.Device {
	return device.New(device.Config{
		InterfaceID:  "HmIP-RF",
		Interface:    hmenum.InterfaceHmIPRF,
		Address:      "002A5D8989D5C3",
		Model:        "HmIP-WRCD",
		Name:         "Displayschalter Flur",
		Manufacturer: hmenum.ManufacturerEQ3,
	})
}

// notifyGoldenSource is the text-display custom-DP as the notify plane
// recognises it: a Source that classifies as DataPointCategoryTextDisplay
// and exposes the custom-DP topic slot the `write` service-method command
// topic is built from.
type notifyGoldenSource struct {
	stubSource
	slot payload.TopicSlot
}

func (s *notifyGoldenSource) Category() hmenum.DataPointCategory {
	return hmenum.DataPointCategoryTextDisplay
}

func (s *notifyGoldenSource) TopicSlot() payload.TopicSlot { return s.slot }

// notifyGoldenCase is one input to the notify builder, named so a diff
// says which shape moved rather than only which topic.
type notifyGoldenCase struct {
	name string
	ev   Event
}

// notifyGoldenCases is the fixture matrix for the text-display notify
// plane. The plane emits exactly one entity per text-display custom-DP,
// so the matrix is not one case per component but one case per way that
// single shape resolves differently: the entity name (the seed Home
// Assistant derives the entity id from), the device block, and the two
// identity fields — node id and unique id.
//
// It is not a fleet. A real CCU's ~9,996 retained configs cannot be
// reconstructed in CI; this pins what CI can reproduce, and the scope
// note on TestTextDisplayNotifyPayloadsArePinned says what it leaves out.
func notifyGoldenCases() []notifyGoldenCase {
	base := func() Event {
		return Event{
			Source: &notifyGoldenSource{
				slot: payload.TopicSlot{
					Address:   "002A5D8989D5C3",
					Channel:   3,
					Bucket:    payload.BucketCustom,
					Parameter: "text_display",
				},
			},
			Central:        "ccu-01",
			Interface:      "HmIP-RF",
			DeviceAddress:  "002A5D8989D5C3",
			DeviceName:     "Displayschalter Flur",
			Model:          "HmIP-WRCD",
			ChannelNo:      3,
			ChannelAddress: "002A5D8989D5C3:3",
			ChannelType:    "TEXT_DISPLAY_TRANSMITTER",
			Device:         notifyGoldenDevice(),
		}
	}

	// The production shape: the display is the device's only primary
	// custom-DP, so the model composes no entity name and the payload
	// carries `name: null` — Home Assistant's signal to render the device
	// name alone. An empty string here instead of null re-seeds the entity
	// id of every existing display.
	primary := base()
	primary.Channel = notifyGoldenChannel{primary: true}

	// The operator named the channel on the CCU. The model, not this
	// package, owns that name; it reaches the payload verbatim and is part
	// of the entity-id seed, so it is pinned rather than assumed inert.
	named := base()
	named.Channel = notifyGoldenChannel{primary: true, displayName: "Küche Display"}

	// No channel inspector at all — the fallback branch in
	// [displayChannelName], which names the entity after the bare channel
	// number. Pinned because it is where an event built without channel
	// context lands, and it yields a different entity-id seed than the
	// branch above for the very same device.
	noChannel := base()

	// Sub-device split. The identity hazard is that only HALF the identity
	// moves: the device block starts identifying `<parent>-<group>` and
	// hangs off the parent, while the node id in the discovery topic stays
	// the parent's address. Confusing the two re-homes the entity under a
	// device card that does not exist, and Home Assistant reports nothing.
	sub := base()
	sub.Device = notifyGoldenParent{Device: notifyGoldenDevice()}
	sub.Channel = notifyGoldenChannel{
		primary: true, groupNo: 2, multiGroup: true, subName: "Display Oben",
	}

	// The same device serial on a second central. The node id is
	// central-scoped and MUST change; the unique id is derived from the
	// device serial, which cannot repeat across CCUs, and MUST NOT. Both
	// halves are pinned together because scoping the unique id here too
	// would re-key every display on every fleet at once, and Home
	// Assistant keys on unique_id: the loser simply never appears.
	second := base()
	second.Central = "ccu-02"
	second.Channel = notifyGoldenChannel{primary: true}

	// A central whose name needs escaping. The node id in the discovery
	// topic and the central segment of the command topic are two DIFFERENT
	// escapes of the same operator-typed string, and with an ASCII-only
	// fixture the two are indistinguishable — a pin that could not tell
	// them apart would bless a migration that swapped one for the other.
	escaped := base()
	escaped.Central = "CCU Küche"
	escaped.Channel = notifyGoldenChannel{primary: true}

	return []notifyGoldenCase{
		{"notify/primary-channel", primary},
		{"notify/escaped-central", escaped},
		{"notify/operator-named-channel", named},
		{"notify/no-channel-context", noChannel},
		{"notify/sub-device", sub},
		{"notify/second-central", second},
	}
}

// TestTextDisplayNotifyPayloadsArePinned pins the topic and the payload of
// every entity shape discovery_notify.go produces, so the move of this
// daemon's discovery layer onto the shared model (ADR 0070) can be checked
// against the one criterion that matters: the bytes do not change.
//
// The notify entity is the SOLE Home Assistant surface of a text-display
// custom-DP (HmIP-WRCD) — the aggregate text entity is suppressed — so a
// silent change here loses the display's only control outright.
//
// The topic is pinned alongside the body deliberately. Two of the three
// ways this move can break are invisible in the body: a changed node id
// and a changed object id both publish the new config on a new topic.
// Home Assistant reports none of the three — the old entity is orphaned
// and a new one appears beside it, with nothing in any log.
//
// The payload is stored decoded so the file stays reviewable; comparison
// is on the canonical re-encoding of both sides, exact for every key and
// value, with only whitespace — which no consumer sees — out of scope.
//
// Scope. This pins what CI can reproduce: one case per way the single
// notify shape resolves differently. It does NOT reproduce a fleet — the
// real device catalogue and its ~9,996 retained configs live on a CCU, and
// a pin that looked complete would be worse than one that admits it is
// not. It also does not pin the cases that emit nothing (a non-text-display
// source, an unresolvable unique id, a missing command topic); those are
// covered in text_display_notify_test.go.
//
// A diff here is a regression until someone shows otherwise. Refresh with:
//
//	go test ./internal/north/mqtt/ -run TestTextDisplayNotifyPayloadsArePinned -update-notify-golden
func TestTextDisplayNotifyPayloadsArePinned(t *testing.T) {
	tb := NewTopicBuilder("gh")
	db := NewDefaultDiscoveryBuilder(tb, "ccu-01")
	// Both centrals have to be known before the builder can answer for
	// either: the unique-id scope decision reads the owning central's
	// serial.
	db.SetHubInfoFor("ccu-01", HubInfo{Serial: "3014F711A0001234"})
	db.SetHubInfoFor("ccu-02", HubInfo{Serial: "3014F711B0005678"})
	db.SetHubInfoFor("CCU Küche", HubInfo{Serial: "3014F711C0009012"})
	db.SubDevicesEnabled = true

	got := map[string]goldenEntry{}
	for _, c := range notifyGoldenCases() {
		item := db.BuildTextDisplayNotify(c.ev)
		if !item.OK {
			t.Fatalf("%s: BuildTextDisplayNotify returned OK=false — the fixture no longer produces an entity", c.name)
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

	if *updateNotifyGolden {
		writeNotifyGolden(t, got)
		t.Logf("rewrote %s with %d payloads", notifyGoldenPath, len(got))
		return
	}

	want := readNotifyGolden(t)
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

func readNotifyGolden(t *testing.T) map[string]goldenEntry {
	t.Helper()
	raw, err := os.ReadFile(notifyGoldenPath)
	if err != nil {
		t.Fatalf("read %s: %v (create it with -update-notify-golden)", notifyGoldenPath, err)
	}
	var out map[string]goldenEntry
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("parse %s: %v", notifyGoldenPath, err)
	}
	return out
}

func writeNotifyGolden(t *testing.T, entries map[string]goldenEntry) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(notifyGoldenPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	raw, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(notifyGoldenPath, append(raw, '\n'), 0o600); err != nil {
		t.Fatalf("write %s: %v", notifyGoldenPath, err)
	}
}
