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

var updatePressButtonGolden = flag.Bool("update-press-button-golden", false,
	"rewrite the pinned press-button discovery payloads")

var pressButtonGoldenPath = filepath.Join("testdata", "discovery_golden_press_button.json")

// pressGoldenChannel is the channel inspector the press-button fixtures
// drive [pressButtonName] and [deviceDescriptor] with. It carries exactly
// the two optional extensions this plane reads — the CCU-operator channel
// name and the sub-device grouping — so a fixture can select a name shape
// or a sub-device split without standing up a real device.Channel graph.
type pressGoldenChannel struct {
	name       string
	groupNo    int
	multiGroup bool
	subName    string
}

func (c pressGoldenChannel) HasParameter(string) bool { return false }
func (c pressGoldenChannel) ChannelName() string      { return c.name }
func (c pressGoldenChannel) GroupNumber() int         { return c.groupNo }
func (c pressGoldenChannel) IsInMultiGroup() bool     { return c.multiGroup }
func (c pressGoldenChannel) SubDeviceName() string    { return c.subName }

// pressGoldenParent wraps a real device so the device block of the
// sub-device fixture is harvested from the same model the production path
// harvests it from, while still reporting the multi-group structure that
// switches [deviceDescriptor] onto its sub-device branch.
type pressGoldenParent struct {
	*device.Device
}

func (pressGoldenParent) HasSubDevices() bool { return true }

// pressGoldenDevice builds the device object the way the model hands it to
// the discovery path, so `model`, `name` and the manufacturer in the device
// block come from the same harvest production uses rather than from a
// literal in this file.
func pressGoldenDevice(address, model, name string) *device.Device {
	return device.New(device.Config{
		InterfaceID:  "HmIP-RF",
		Interface:    hmenum.InterfaceHmIPRF,
		Address:      address,
		Model:        model,
		Name:         name,
		Manufacturer: hmenum.ManufacturerEQ3,
	})
}

// pressButtonGoldenCase is one input to the press-button builder, named so
// a diff says which shape moved rather than only which topic.
type pressButtonGoldenCase struct {
	name string
	ev   Event
}

// pressButtonGoldenCases is the fixture matrix for the press-button plane.
//
// The plane emits exactly one component — an HA `button` — so the matrix is
// not one case per component but one case per way that single shape
// resolves differently. Three groups:
//
//   - the entity NAME, which is the seed Home Assistant derives the entity
//     id from. [pressButtonName] has four branches (descriptor label,
//     title-cased parameter, operator channel-name prefix, `ch<N>` prefix)
//     and each produces a different entity id for the very same button.
//   - the IDENTITY — node id and unique id. A virtual remote's address
//     repeats verbatim on every CCU, so its unique id is central-scoped; a
//     real device serial's must not be, because the serial is already
//     globally unique. Both halves are pinned, on two centrals each.
//   - the BODY keys the entity-description table sets: the press
//     parameters are disabled by default, and the reset buttons
//     additionally land in the `config` entity category.
//
// It is not a fleet. A real CCU's ~9,996 retained configs cannot be
// reconstructed in CI; this pins what CI can reproduce, and the scope note
// on TestPressButtonPayloadsArePinned says what it leaves out.
func pressButtonGoldenCases() []pressButtonGoldenCase {
	// A six-button wall remote: the ordinary source of press buttons.
	wrc := func(ch int, param string) Event {
		return Event{
			Central:        "ccu-01",
			Interface:      "HmIP-RF",
			DeviceAddress:  "0001D3C99C1234",
			DeviceName:     "Wandtaster Wohnzimmer",
			Model:          "HmIP-WRC6",
			ChannelNo:      ch,
			ChannelAddress: "0001D3C99C1234:" + itoa(ch),
			ChannelType:    "KEY",
			Parameter:      param,
			Category:       hmenum.DataPointCategoryButton,
			Usage:          hmenum.DataPointUsageDataPoint,
			Writable:       true,
			Device: pressGoldenDevice("0001D3C99C1234", "HmIP-WRC6",
				"Wandtaster Wohnzimmer"),
		}
	}

	// The operator named the channel on the CCU. The model, not this
	// package, owns that name; it reaches the payload verbatim as the name
	// prefix and is part of the entity-id seed, so it is pinned rather than
	// assumed inert.
	named := wrc(1, "PRESS_SHORT")
	named.Channel = pressGoldenChannel{name: "Taster Wohnzimmer oben links"}

	// No operator name: the prefix falls back to `ch<N>`. Pinned as the
	// counterpart above, because the fallback is what the overwhelming
	// majority of real channels take.
	unnamed := wrc(2, "PRESS_LONG")
	unnamed.Channel = pressGoldenChannel{}

	// The CCU reports the unlabelled `<base>:<channel_no>` form for a
	// channel the operator never renamed. Home Assistant slugifies that
	// down to the base alone, so every sibling channel would collapse onto
	// one entity id; the plane drops it to `ch<N>` instead. This is an
	// entity-id-seed hazard: treating the bare form as a real name re-seeds
	// every unnamed press channel at once.
	bareName := wrc(3, "PRESS_SHORT")
	bareName.Channel = pressGoldenChannel{name: "0001D3C99C1234:3"}

	// A descriptor-supplied, localised parameter label instead of the
	// title-cased wire parameter. Pinned because the label reaches the name
	// verbatim and is therefore part of the entity-id seed.
	labelled := wrc(4, "PRESS_SHORT")
	labelled.Channel = pressGoldenChannel{}
	labelled.Descriptor = &payload.GenericConfig{
		Paramset: hmenum.ParamsetKeyValues,
		Type:     hmenum.ParameterTypeAction,
		Label:    "Kurzer Tastendruck",
	}

	// The descriptor marks the parameter "primary", which everywhere else
	// means "render no entity name and let HA use the device name alone".
	// This plane deliberately refuses that: a nameless button would collide
	// with the device name and with every sibling press button. Pinned so
	// the refusal survives the move — honouring the omission here would
	// silently merge six buttons into one.
	omitted := wrc(5, "PRESS_SHORT")
	omitted.Channel = pressGoldenChannel{}
	omitted.Descriptor = &payload.GenericConfig{
		Paramset:     hmenum.ParamsetKeyValues,
		Type:         hmenum.ParameterTypeAction,
		LabelOmitted: true,
	}

	// Sub-device split. Only HALF the identity moves: the device block
	// starts identifying `<parent>-<group>` and hangs off the parent, while
	// the node id in the discovery topic stays the parent's address.
	// Confusing the two re-homes the button under a device card that does
	// not exist, and Home Assistant reports nothing.
	sub := Event{
		Central:        "ccu-01",
		Interface:      "HmIP-RF",
		DeviceAddress:  "0001D3C99C5678",
		DeviceName:     "Rollladenaktor Garage",
		Model:          "HmIP-MOD-RC8",
		ChannelNo:      3,
		ChannelAddress: "0001D3C99C5678:3",
		ChannelType:    "KEY",
		Parameter:      "PRESS_SHORT",
		Category:       hmenum.DataPointCategoryButton,
		Usage:          hmenum.DataPointUsageDataPoint,
		Writable:       true,
		Channel: pressGoldenChannel{
			groupNo: 2, multiGroup: true, subName: "Tor Links",
		},
		Device: pressGoldenParent{Device: pressGoldenDevice(
			"0001D3C99C5678", "HmIP-MOD-RC8", "Rollladenaktor Garage",
		)},
	}

	// A virtual remote. Its address is handed out identically on every CCU,
	// so the unique id MUST carry the central — and the virtual remote is
	// this plane's single largest population, because the model withholds
	// the button from every other event-only press. Both centrals are
	// pinned: if the scope ever dropped, two CCUs bridged into one Home
	// Assistant would declare the same unique_id and one CCU's buttons
	// would simply never appear.
	vr := func(central string) Event {
		return Event{
			Central:        central,
			Interface:      "HmIP-RF",
			DeviceAddress:  "HmIP-RCV-1",
			DeviceName:     "Virtuelle Fernbedienung",
			Model:          "HmIP-RCV-50",
			ChannelNo:      10,
			ChannelAddress: "HmIP-RCV-1:10",
			ChannelType:    "KEY_TRANSCEIVER",
			Parameter:      "PRESS_SHORT",
			Category:       hmenum.DataPointCategoryButton,
			Usage:          hmenum.DataPointUsageDataPoint,
			Writable:       true,
			Channel:        pressGoldenChannel{},
			Device: pressGoldenDevice("HmIP-RCV-1", "HmIP-RCV-50",
				"Virtuelle Fernbedienung"),
		}
	}

	// The same real device serial on a second central. The node id is
	// central-scoped and MUST change; the unique id is derived from the
	// serial, which cannot repeat across CCUs, and MUST NOT. Pinned as the
	// counterpart to the virtual-remote pair above: if scoping ever became
	// unconditional, every existing button on every fleet would be re-keyed
	// at once.
	secondCentral := wrc(1, "PRESS_SHORT")
	secondCentral.Central = "ccu-02"
	secondCentral.Channel = pressGoldenChannel{}

	// RESET_MOTION is not a press at all: it is a write-only ACTION the
	// model also types as a button with usage=data_point, which is the only
	// condition this plane gates on. It is in the matrix because it is the
	// one shape here that carries `entity_category: config`, and because it
	// is the shape whose topic the per-parameter [Build] path also claims —
	// see TestPressButtonPayloadsArePinned's note.
	resetMotion := Event{
		Central:        "ccu-01",
		Interface:      "HmIP-RF",
		DeviceAddress:  "0001D3C99CABCD",
		DeviceName:     "Bewegungsmelder Flur",
		Model:          "HmIP-SMI",
		ChannelNo:      1,
		ChannelAddress: "0001D3C99CABCD:1",
		ChannelType:    "MOTION_DETECTOR",
		Parameter:      "RESET_MOTION",
		Category:       hmenum.DataPointCategoryButton,
		Usage:          hmenum.DataPointUsageDataPoint,
		Writable:       true,
		Channel:        pressGoldenChannel{},
		Device: pressGoldenDevice("0001D3C99CABCD", "HmIP-SMI",
			"Bewegungsmelder Flur"),
	}

	return []pressButtonGoldenCase{
		{"button/operator-named-channel", named},
		{"button/channel-number-fallback", unnamed},
		{"button/bare-address-channel-name", bareName},
		{"button/descriptor-label", labelled},
		{"button/label-omitted", omitted},
		{"button/sub-device", sub},
		{"button/virtual-remote-ccu-01", vr("ccu-01")},
		{"button/virtual-remote-ccu-02", vr("ccu-02")},
		{"button/serial-second-central", secondCentral},
		{"button/reset-motion-config", resetMotion},
	}
}

// TestPressButtonPayloadsArePinned pins the topic and the payload of every
// entity shape discovery_press_button.go produces, so the move of this
// daemon's discovery layer onto the shared model (ADR 0070) can be checked
// against the one criterion that matters: the bytes do not change.
//
// The topic is pinned alongside the body deliberately. Two of the three
// ways this move can break are invisible in the body: a changed node id and
// a changed object id both publish the new config on a new topic. Home
// Assistant reports none of the three — the old entity is orphaned and a
// new one appears beside it, with nothing in any log. The entity name is
// the third, and it is in the body: Home Assistant seeds the entity id from
// it, so a renamed button is a re-keyed button.
//
// The payload is stored decoded so the file stays reviewable; comparison is
// on the canonical re-encoding of both sides, exact for every key and
// value, with only whitespace — which no consumer sees — out of scope.
//
// Note on button/reset-motion-config: the per-parameter [Build] path emits
// its own `button` for that same event, on the same node id and object id,
// with a different body. The pin records what THIS plane produces; it does
// not assert which of the two wins the retained topic.
//
// Scope. This pins what CI can reproduce: one case per way the single
// button shape resolves differently. It does NOT reproduce a fleet — the
// real device catalogue and its ~9,996 retained configs live on a CCU, and
// a pin that looked complete would be worse than one that admits it is not.
// It also does not pin the cases that emit nothing (a press whose usage is
// event rather than data_point, a virtual remote on a central with no
// registered serial); those are covered in parity_round1_test.go and
// virtual_remote_naming_test.go. Nor does it pin the bridge wiring that
// decides WHEN this builder is called — only what it returns.
//
// A diff here is a regression until someone shows otherwise. Refresh with:
//
//	go test ./internal/north/mqtt/ -run TestPressButtonPayloadsArePinned -update-press-button-golden
func TestPressButtonPayloadsArePinned(t *testing.T) {
	tb := NewTopicBuilder("gh")
	db := NewDefaultDiscoveryBuilder(tb, "ccu-01")
	// Both centrals have to be known before the builder can answer for
	// either: the unique-id scope decision reads the owning central's
	// serial, and a virtual remote without one is declined outright.
	db.SetHubInfoFor("ccu-01", HubInfo{Serial: "3014F711A0001234"})
	db.SetHubInfoFor("ccu-02", HubInfo{Serial: "3014F711B0005678"})
	db.SubDevicesEnabled = true

	got := map[string]goldenEntry{}
	for _, c := range pressButtonGoldenCases() {
		item := db.BuildPressButton(c.ev)
		if !item.OK {
			t.Fatalf("%s: BuildPressButton returned OK=false — the fixture no longer produces an entity", c.name)
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

	if *updatePressButtonGolden {
		writePressButtonGolden(t, got)
		t.Logf("rewrote %s with %d payloads", pressButtonGoldenPath, len(got))
		return
	}

	want := readPressButtonGolden(t)
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

func readPressButtonGolden(t *testing.T) map[string]goldenEntry {
	t.Helper()
	raw, err := os.ReadFile(pressButtonGoldenPath)
	if err != nil {
		t.Fatalf("read %s: %v (create it with -update-press-button-golden)", pressButtonGoldenPath, err)
	}
	var out map[string]goldenEntry
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("parse %s: %v", pressButtonGoldenPath, err)
	}
	return out
}

func writePressButtonGolden(t *testing.T, entries map[string]goldenEntry) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(pressButtonGoldenPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	raw, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(pressButtonGoldenPath, append(raw, '\n'), 0o600); err != nil {
		t.Fatalf("write %s: %v", pressButtonGoldenPath, err)
	}
}
