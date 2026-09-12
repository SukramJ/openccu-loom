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

var updateScheduleGolden = flag.Bool("update-schedule-golden", false,
	"rewrite the pinned schedule discovery payloads")

var scheduleGoldenPath = filepath.Join("testdata", "discovery_golden_schedule.json")

// scheduleGoldenBase is the topic base every fixture publishes under. Short
// on purpose, so a diff of a state / command / attrs topic stays readable.
const scheduleGoldenBase = "gh"

// scheduleGoldenDevice is the schedule-capable device as the model hands it
// to the discovery path: a real *device.Device, so `model`, `serial_number`
// and the manufacturer in the sub-device block come from the same harvest
// production uses rather than from literals in this file. The single room is
// set because it is the only source `suggested_area` has on this plane — the
// sub-device inherits it from the parent through [deviceWithRoom].
func scheduleGoldenDevice(addr, model, name string, iface hmenum.Interface, ifaceID string) *device.Device {
	return device.New(device.Config{
		InterfaceID:  ifaceID,
		Interface:    iface,
		Address:      addr,
		Model:        model,
		Name:         name,
		Manufacturer: hmenum.ManufacturerEQ3,
		Rooms:        []string{"Wohnzimmer"},
	})
}

// scheduleGoldenEntityCase is one input to the Zeitplan-sensor builder.
type scheduleGoldenEntityCase struct {
	name string
	ev   ScheduleEntityEvent
}

// scheduleGoldenSwitchCase is one input to the schedule-switch builder.
type scheduleGoldenSwitchCase struct {
	name string
	ev   ScheduleSwitchEvent
}

// scheduleGoldenEntityCases is the fixture matrix for the Zeitplan sensor —
// the first of the two shapes discovery_schedule.go emits.
//
// The plane emits exactly one sensor per schedule-carrying channel, so the
// matrix is not one case per component but one case per way that single
// shape resolves differently, plus every identity hazard the plane carries:
//
//   - The entity does NOT live on the physical device card. It lives on a
//     synthetic sub-device "<parent name> Schedule" whose identifier is the
//     parent's with a "_schedule" suffix, `via_device`-linked to the parent.
//     Three identifiers therefore have to agree across a move: the node id,
//     the sub-device identifier, and the via_device that points at the
//     parent. A sub-device identifier that changes strands the card; a
//     via_device that changes un-parents it.
//   - The node id is NOT the device identifier. It is [discoveryNodeID] —
//     a slugged central plus the address — while the device block carries
//     "openccu-loom_<addr>_schedule". The two are derived separately and can
//     drift apart in a move without either looking wrong on its own.
//   - The object id IS the unique_id here: the builder assigns the same
//     string to both. A change to one is a change to the other, which means
//     it both moves the retained topic and re-keys the HA entity registry.
func scheduleGoldenEntityCases() []scheduleGoldenEntityCase {
	// The production shape: a multi-IO device with a schedule channel above
	// zero, a globally unique hardware serial (so nothing is central-scoped)
	// and a device object to inherit model / serial / area from.
	base := ScheduleEntityEvent{
		Central:       "ccu-01",
		Interface:     "HmIP-RF",
		DeviceAddress: "0001D3C9",
		ChannelNo:     5,
		DeviceName:    "Bewässerung Garten",
		Model:         "HmIP-MIO16-PCB",
		Device: scheduleGoldenDevice("0001D3C9", "HmIP-MIO16-PCB",
			"Bewässerung Garten", hmenum.InterfaceHmIPRF, "HmIP-RF"),
	}

	// Hazard: no device object. ScheduleEntityEvent.Device is documented as
	// optional, and the sub-device block then assembles from the event's own
	// DeviceName instead of from the model harvest — no model, no
	// serial_number, no suggested_area. Pinned because the two paths must
	// agree on the identifiers: a sub-device that identifies differently
	// depending on whether the harvest was ready lands on a second card
	// beside the real one, and the entities split between them.
	noDevice := base
	noDevice.Device = nil

	// Hazard: a central-scoped address. CUxD hands out the SAME synthetic
	// address on every CCU it runs on, so [physicalDeviceIdentifier] folds
	// the central into the identifier — and therefore into the object id,
	// the unique_id, the sub-device identifier and the via_device. Both the
	// scoped case and the unscoped `base` above are pinned, because a move
	// that made the scoping unconditional would re-key every schedule entity
	// on every fleet, and one that dropped it would collapse two CCUs'
	// schedule cards into one.
	cuxd := ScheduleEntityEvent{
		Central:       "ccu-01",
		Interface:     "CUxD",
		DeviceAddress: "CUX2801001",
		ChannelNo:     1,
		DeviceName:    "Bewässerung CUxD",
		Model:         "CUxD-Switch",
		Device: scheduleGoldenDevice("CUX2801001", "CUxD-Switch",
			"Bewässerung CUxD", hmenum.InterfaceCUxD, "CUxD"),
	}

	// Hazard: the same central-scoped address on a second central. This is
	// the pair that proves the scoping actually separates two CCUs rather
	// than merely decorating one.
	cuxdSecondCentral := cuxd
	cuxdSecondCentral.Central = "ccu-02"

	// Hazard: a central named with capitals. The central name reaches the
	// payload three ways at once and each folds it differently — the node id
	// through the discovery slug, the object id / unique_id through
	// [safeLower], and the state / attrs topics verbatim. Every other
	// fixture uses a lower-case central and therefore cannot tell the three
	// apart; a pin that could not tell them apart would bless a move that
	// swapped one for another.
	mixedCase := cuxd
	mixedCase.Central = "CCU-Keller"

	return []scheduleGoldenEntityCase{
		{"sensor/hmip-multi-io", base},
		{"sensor/no-device-object", noDevice},
		{"sensor/cuxd-central-scoped", cuxd},
		{"sensor/cuxd-second-central", cuxdSecondCentral},
		{"sensor/mixed-case-central", mixedCase},
	}
}

// scheduleGoldenSwitchCases is the fixture matrix for the schedule switch —
// the second shape, one entity per target channel the schedule governs.
//
// The switch repeats the sub-device block and the identifier derivation in
// its own builder rather than sharing the sensor's, so the hazards above are
// pinned a second time here deliberately: pinning them on the sensor alone
// would not catch the switch losing one, and a switch keyed differently from
// the sensor it belongs to splits one schedule card into two.
func scheduleGoldenSwitchCases() []scheduleGoldenSwitchCase {
	base := ScheduleSwitchEvent{
		Central:           "ccu-01",
		Interface:         "HmIP-RF",
		DeviceAddress:     "0001D3C9",
		ScheduleChannelNo: 5,
		DeviceName:        "Bewässerung Garten",
		Model:             "HmIP-MIO16-PCB",
		Device: scheduleGoldenDevice("0001D3C9", "HmIP-MIO16-PCB",
			"Bewässerung Garten", hmenum.InterfaceHmIPRF, "HmIP-RF"),
		Key:             "1_1",
		TargetChannelNo: 1,
		Label:           "Schedule channel 1",
	}

	// A second target channel on the same device, with a two-digit channel
	// in the key. The key travels verbatim into the object id, the unique_id
	// and the state / command topic segment, so the entities of one device
	// are told apart by nothing else: a change to how the key is spelled
	// renames every switch on every schedule device at once.
	secondKey := base
	secondKey.Key = "1_18"
	secondKey.TargetChannelNo = 18
	secondKey.Label = "Schedule channel 18"

	// Hazard: the central-scoped address again, on the switch builder.
	cuxd := ScheduleSwitchEvent{
		Central:           "ccu-01",
		Interface:         "CUxD",
		DeviceAddress:     "CUX2801001",
		ScheduleChannelNo: 1,
		DeviceName:        "Bewässerung CUxD",
		Model:             "CUxD-Switch",
		Device: scheduleGoldenDevice("CUX2801001", "CUxD-Switch",
			"Bewässerung CUxD", hmenum.InterfaceCUxD, "CUxD"),
		Key:             "1_1",
		TargetChannelNo: 1,
		Label:           "Schedule channel 1",
	}

	// Hazard: no device object, on the switch builder.
	noDevice := base
	noDevice.Device = nil

	return []scheduleGoldenSwitchCase{
		{"switch/target-channel-1", base},
		{"switch/target-channel-18", secondKey},
		{"switch/cuxd-central-scoped", cuxd},
		{"switch/no-device-object", noDevice},
	}
}

// scheduleGoldenBuilder is the builder every fixture is built through, wired
// the way the daemon wires it: a topic builder on the shared base and hub
// metadata for every central a fixture names, because `configuration_url` on
// the sub-device card is resolved per central.
func scheduleGoldenBuilder() *DefaultDiscoveryBuilder {
	db := NewDefaultDiscoveryBuilder(NewTopicBuilder(scheduleGoldenBase), "ccu-01")
	db.SetHubInfoFor("ccu-01", HubInfo{Serial: "3014F711A0001234", URL: "http://ccu-01.local"})
	db.SetHubInfoFor("ccu-02", HubInfo{Serial: "3014F711B0005678", URL: "http://ccu-02.local"})
	db.SetHubInfoFor("CCU-Keller", HubInfo{Serial: "3014F711C0009012", URL: "http://ccu-keller.local"})
	return db
}

// scheduleGoldenBuilt builds every fixture into the addressed form the wire
// carries, so the pin test and the schema check see exactly the same bytes.
func scheduleGoldenBuilt(t *testing.T) map[string]DiscoveryItem {
	t.Helper()
	db := scheduleGoldenBuilder()
	out := map[string]DiscoveryItem{}
	for _, c := range scheduleGoldenEntityCases() {
		item := db.BuildScheduleEntityDiscovery(c.ev.Central, c.ev)
		if !item.OK {
			t.Fatalf("%s: BuildScheduleEntityDiscovery returned OK=false — the fixture no longer produces an entity", c.name)
		}
		out[c.name] = item
	}
	switchCases := scheduleGoldenSwitchCases()
	for i := range switchCases {
		c := &switchCases[i]
		item := db.BuildScheduleSwitchDiscovery(c.ev.Central, c.ev)
		if !item.OK {
			t.Fatalf("%s: BuildScheduleSwitchDiscovery returned OK=false — the fixture no longer produces an entity", c.name)
		}
		out[c.name] = item
	}
	return out
}

// TestSchedulePayloadsArePinned pins the topic and the payload of both entity
// shapes discovery_schedule.go produces, so moving this daemon's discovery
// layer onto the shared model (ADR 0070) can be checked against the one
// criterion that matters: the bytes do not change.
//
// The topic is pinned alongside the body deliberately. Two of the three ways
// the move can break are invisible in the body: a changed node id and a
// changed object id both publish the config on a new topic. Home Assistant
// reports none of the three — the old entity is orphaned and a new one
// appears beside it, with nothing in any log and nothing in the UI to say
// what happened. This plane is unusually exposed, because everything it
// publishes hangs off a synthetic sub-device: a drifted sub-device
// identifier or via_device moves the whole card, not one entity.
//
// The payload is stored decoded so the file stays reviewable; comparison is
// on the canonical re-encoding of both sides, exact for every key and value,
// with only whitespace — which no consumer sees — out of scope.
//
// Scope. This pins what CI can reproduce: one case per way the two schedule
// shapes resolve differently, plus each identity hazard once per builder. It
// does NOT reproduce a fleet — the real device catalogue and its ~9,996
// retained configs live on a CCU, and a pin that looked complete would be
// worse than one that admits it is not. It does not pin the declining
// branches (an empty device address, an empty switch key), which
// discovery_schedule_test.go covers. It does not pin the state, attrs or
// command traffic on the topics these payloads name — only the topic strings
// themselves; the round-trip guards own the traffic. The sensor's display
// name is pinned at its English i18n fallback, so a translation change is
// out of scope, and the switch's label is a caller-supplied string, pinned
// only as the literal the fixture passes.
//
// A diff here is a regression until someone shows otherwise. Refresh with:
//
//	go test ./internal/north/mqtt/ -run TestSchedulePayloadsArePinned -update-schedule-golden
func TestSchedulePayloadsArePinned(t *testing.T) {
	tb := NewTopicBuilder(scheduleGoldenBase)

	got := map[string]goldenEntry{}
	for name, item := range scheduleGoldenBuilt(t) {
		// Decoded to a map so the pin is stable against field order in the
		// builder and unstable against a changed key or value — exactly the
		// sensitivity wanted.
		var body map[string]any
		if err := json.Unmarshal(item.Payload, &body); err != nil {
			t.Fatalf("%s: unmarshal: %v", name, err)
		}
		got[name] = goldenEntry{
			Topic:   tb.DiscoveryConfig(item.Component, item.NodeID, item.ObjectID),
			Payload: body,
		}
	}

	if *updateScheduleGolden {
		writeScheduleGolden(t, got)
		t.Logf("rewrote %s with %d payloads", scheduleGoldenPath, len(got))
		return
	}

	want := readScheduleGolden(t)
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

// TestScheduleDiscoveryPublishesNoEntityIDSeed pins an ABSENCE, which the
// payload pin above cannot: a golden file records the keys that are there
// and says nothing about a key that is not.
//
// This plane publishes no entity-id seed today. Home Assistant therefore
// derives every schedule entity_id from the entity name and its device, and
// whatever an operator has renamed since stays theirs. A move that started
// emitting `default_entity_id` — which the shared model does emit on planes
// that ask for it — would hand Home Assistant a seed for entities that were
// registered without one, renaming every schedule entity on every fleet in
// one reconnect, silently.
//
// `object_id` is checked with it: it is the older spelling of the same seed
// and is not a legal MQTT discovery key, so its appearance would be both a
// rename and a dropped key.
func TestScheduleDiscoveryPublishesNoEntityIDSeed(t *testing.T) {
	for name, item := range scheduleGoldenBuilt(t) {
		var body map[string]any
		if err := json.Unmarshal(item.Payload, &body); err != nil {
			t.Fatalf("%s: unmarshal: %v", name, err)
		}
		for _, key := range []string{"default_entity_id", "object_id"} {
			if v, present := body[key]; present {
				t.Errorf("%s: %s=%v appeared — this plane published no entity-id seed, "+
					"and adding one renames every installed schedule entity", name, key, v)
			}
		}
	}
}

// TestSchedulePinnedPayloadsAreValid reads the same fixtures back through the
// extracted Home Assistant MQTT schema, so a payload that is pinned is also a
// payload HA accepts. A pin on its own only says the bytes did not move; it
// cannot say they were right to begin with.
func TestSchedulePinnedPayloadsAreValid(t *testing.T) {
	for name, item := range scheduleGoldenBuilt(t) {
		if err := ValidateDiscoveryBody(item.Component, item.Payload); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func readScheduleGolden(t *testing.T) map[string]goldenEntry {
	t.Helper()
	raw, err := os.ReadFile(scheduleGoldenPath)
	if err != nil {
		t.Fatalf("read %s: %v (create it with -update-schedule-golden)", scheduleGoldenPath, err)
	}
	var out map[string]goldenEntry
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("parse %s: %v", scheduleGoldenPath, err)
	}
	return out
}

func writeScheduleGolden(t *testing.T, entries map[string]goldenEntry) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(scheduleGoldenPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	raw, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(scheduleGoldenPath, append(raw, '\n'), 0o600); err != nil {
		t.Fatalf("write %s: %v", scheduleGoldenPath, err)
	}
}
