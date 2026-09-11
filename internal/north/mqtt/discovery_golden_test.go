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

var updateDiscoveryGolden = flag.Bool("update-discovery-golden", false,
	"rewrite the pinned discovery payloads")

var discoveryGoldenPath = filepath.Join("testdata", "discovery_golden.json")

// goldenEntry is one discovery publish, addressed the way the wire addresses
// it. The topic is pinned alongside the body on purpose: two of the three
// ways this migration can fail silently — a changed node id and a changed
// entity-id seed — are invisible in the body alone.
// The payload is stored decoded rather than as raw bytes so the pin stays
// reviewable — a human has to be able to read a diff and decide whether a
// change is wanted. Comparison is on the canonical re-encoding of both
// sides, which is exact for every key and value; only whitespace, which no
// consumer sees, is out of scope.
type goldenEntry struct {
	Topic   string         `json:"topic"`
	Payload map[string]any `json:"payload"`
}

// canonical is the form both sides are compared in.
func canonical(t *testing.T, body map[string]any) string {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(raw)
}

// goldenCase is one input to the discovery builder, named so a diff says
// which shape moved rather than only which topic.
type goldenCase struct {
	name string
	ev   Event
}

// goldenCases is the fixture matrix. It is deliberately small and
// deliberately spread: one case per discovery shape that resolves
// differently, plus the cases that carry an identity hazard.
//
// It is not a fleet. The 9,996 payloads a real CCU produces cannot be
// reconstructed in CI, and pretending otherwise would make this pin look
// like proof it is not — see the note on the second tier in
// TestDiscoveryPayloadsArePinned.
func goldenCases() []goldenCase {
	values := &payload.GenericConfig{Paramset: hmenum.ParamsetKeyValues}
	master := &payload.GenericConfig{Paramset: hmenum.ParamsetKeyMaster}

	base := func(addr, model string, ch int) Event {
		return Event{
			Central: "ccu-01", Interface: "HmIP-RF",
			DeviceAddress: addr, Model: model, ChannelNo: ch,
			ChannelAddress: addr + ":" + itoa(ch),
		}
	}
	with := func(ev Event, param string, cat hmenum.DataPointCategory,
		writable bool, desc payload.ConfigPayload,
	) Event {
		ev.Parameter, ev.Category, ev.Writable, ev.Descriptor = param, cat, writable, desc
		return ev
	}

	return []goldenCase{
		{"switch/values", with(base("AABBCCDD", "HmIP-BSM", 1),
			"STATE", hmenum.DataPointCategorySwitch, true, values)},
		// The MASTER paramset is what makes an entity a config entity, and
		// the entity_category it gains is a key an operator sees.
		{"switch/master", with(base("AABBCCDD", "HmIP-BSM", 1),
			"STATE", hmenum.DataPointCategorySwitch, true, master)},
		{"sensor/values", with(base("0001ABCD", "HmIP-BWTH", 1),
			"ACTUAL_TEMPERATURE", hmenum.DataPointCategorySensor, false, values)},
		{"binary_sensor/values", with(base("000B0001", "HmIP-SWDO", 1),
			"STATE", hmenum.DataPointCategoryBinarySensor, false, values)},
		{"number/master", with(base("0001ABCD", "HmIP-eTRV-2", 1),
			"LEVEL", hmenum.DataPointCategoryNumber, true, master)},
		{"event/press", with(base("000C0001", "HmIP-BRC2", 1),
			"PRESS_SHORT", hmenum.DataPointCategoryEvent, false, values)},
		// A CUxD address on two centrals. CUxD hands out the SAME synthetic
		// address on every CCU it runs on, so its unique_id must carry the
		// central — and a real device serial's must not, because the serial
		// is already globally unique. Both halves are pinned here because
		// getting either wrong collides two entities into one, and Home
		// Assistant keys on unique_id: the loser simply never appears.
		{"switch/cuxd-ccu-01", cuxdEvent("ccu-01")},
		{"switch/cuxd-ccu-02", cuxdEvent("ccu-02")},
		// The same device serial on a second central is NOT scoped, because
		// a serial cannot repeat. Pinned as the counterpart to the pair
		// above: if scoping ever became unconditional, every existing
		// entity on every fleet would be re-keyed at once.
		{"switch/serial-second-central", func() Event {
			ev := with(base("AABBCCDD", "HmIP-BSM", 1),
				"STATE", hmenum.DataPointCategorySwitch, true, values)
			ev.Central = "ccu-02"
			return ev
		}()},
		// A channel above zero, because the object id embeds it.
		{"sensor/high-channel", with(base("0001ABCD", "HmIP-BWTH", 7),
			"HUMIDITY", hmenum.DataPointCategorySensor, false, values)},
	}
}

// cuxdEvent is the CUxD switch as the production builder wants it: a real
// device object rather than a paramset descriptor, which is the shape the
// CUxD path recognises.
func cuxdEvent(central string) Event {
	return Event{
		Central: central, Interface: "CUxD",
		DeviceAddress: "CUX2801001", ChannelNo: 1,
		Parameter: "STATE", Category: hmenum.DataPointCategorySwitch,
		Device: device.New(device.Config{
			InterfaceID: "CUxD", Interface: hmenum.InterfaceCUxD,
			Address: "CUX2801001", Model: "CUxD-Switch",
			Name: "Sonos Schlafzimmer", Manufacturer: hmenum.ManufacturerEQ3,
		}),
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// TestDiscoveryPayloadsArePinned is the acceptance criterion for moving this
// daemon's discovery layer onto the shared model (ADR 0070), written down
// before the first plane moves.
//
// The move's whole promise is that the bytes do not change. Three of
// the ways it can break — a changed `default_entity_id`, a changed
// `unique_id`, a changed node id — fail SILENTLY in Home Assistant: the old
// entity is orphaned and a new one appears beside it, with nothing in any
// log. Nothing in this repository would have caught that, which is why this
// pin exists before the work rather than after it.
//
// This is the first of two tiers, and the weaker one. It pins what CI can
// reproduce: one case per discovery shape that resolves differently. The
// second tier is an operator capture of a real fleet's retained configs
// before and after — see notes/parity/ — because ~9,996 payloads over a real
// device catalogue cannot be reconstructed here, and a pin that looked
// complete would be worse than one that admits its scope.
//
// A diff here is a regression until someone shows otherwise. Refresh with:
//
//	go test ./internal/north/mqtt/ -run TestDiscoveryPayloadsArePinned -update-discovery-golden
func TestDiscoveryPayloadsArePinned(t *testing.T) {
	db := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu-01")
	// The CUxD identity is derived from the owning central's serial, so the
	// builder has to know both centrals before it can answer for either.
	db.SetHubInfoFor("ccu-01", HubInfo{Serial: "3014F711A0001234"})
	db.SetHubInfoFor("ccu-02", HubInfo{Serial: "3014F711B0005678"})

	got := map[string]goldenEntry{}
	for _, c := range goldenCases() {
		component, nodeID, objectID, buf, ok := db.Build(c.ev)
		if !ok {
			t.Fatalf("%s: Build returned ok=false — the fixture no longer produces an entity", c.name)
		}
		// Decoded to a map so the pin is stable against field order in the
		// builder and unstable against a changed key or value — which is
		// exactly the sensitivity wanted.
		var body map[string]any
		if err := json.Unmarshal(buf, &body); err != nil {
			t.Fatalf("%s: unmarshal: %v", c.name, err)
		}
		got[c.name] = goldenEntry{
			Topic:   NewTopicBuilder("gh").DiscoveryConfig(component, nodeID, objectID),
			Payload: body,
		}
	}

	if *updateDiscoveryGolden {
		writeGolden(t, got)
		t.Logf("rewrote %s with %d payloads", discoveryGoldenPath, len(got))
		return
	}

	want := readGolden(t)
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
		if g, want := canonical(t, got[name].Payload), canonical(t, w.Payload); g != want {
			t.Errorf("%s: payload\n got %s\nwant %s", name, g, want)
		}
	}
	for name := range want {
		if _, still := got[name]; !still {
			t.Errorf("%s: in the pin but no longer produced — an entity disappeared", name)
		}
	}
}

func readGolden(t *testing.T) map[string]goldenEntry {
	t.Helper()
	raw, err := os.ReadFile(discoveryGoldenPath)
	if err != nil {
		t.Fatalf("read %s: %v (create it with -update-discovery-golden)", discoveryGoldenPath, err)
	}
	var out map[string]goldenEntry
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("parse %s: %v", discoveryGoldenPath, err)
	}
	return out
}

func writeGolden(t *testing.T, entries map[string]goldenEntry) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(discoveryGoldenPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	raw, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(discoveryGoldenPath, append(raw, '\n'), 0o600); err != nil {
		t.Fatalf("write %s: %v", discoveryGoldenPath, err)
	}
}
