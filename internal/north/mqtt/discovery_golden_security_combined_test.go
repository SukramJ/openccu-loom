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

	hadiscovery "github.com/SukramJ/go-hamqtt/discovery"

	"github.com/SukramJ/openccu-loom/internal/i18n"
	"github.com/SukramJ/openccu-loom/internal/model/combined"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

var updateSecurityCombinedGolden = flag.Bool("update-security-combined-golden", false,
	"rewrite the pinned security and combined-projection discovery payloads")

var securityCombinedGoldenPath = filepath.Join("testdata", "discovery_golden_security_combined.json")

// secGoldenBase is the topic base every fixture publishes under, in the
// already-trimmed form [TopicBuilder.Base] hands the security publisher.
const secGoldenBase = "gh"

// secGoldenDevice / secGoldenConfigURL are what the reconciler passes as the
// device card's name and configuration URL. The URL is pinned with a value
// rather than empty because it is the one field of the card an operator can
// click, and an empty string and an absent key are different bodies.
const (
	secGoldenDevice    = "Security & Safety"
	secGoldenConfigURL = "http://ccu-01.local"
)

// secGoldenTranslator resolves the daemon catalogue the way the security
// publisher's tr8 does, at the default locale. The real catalogue is used
// rather than a fallback stub so the pinned `name` values are the ones an
// English installation actually receives; a locale change is out of scope
// and is called out in the scope note below.
func secGoldenTranslator(t *testing.T) func(key, fallback string) string {
	t.Helper()
	cat, err := i18n.NewCatalogs()
	if err != nil {
		t.Fatalf("load catalogues: %v", err)
	}
	return func(key, fallback string) string {
		if v := cat.T("en", key); v != "" && v != key {
			return v
		}
		return fallback
	}
}

// secGoldenCase is one built discovery item plus the name a diff reports.
type secGoldenCase struct {
	name string
	item DiscoveryItem
}

// securityGoldenCases is the fixture matrix for the Security & Safety plane:
// every shape security_entities.go hands [BuildSecurityDiscovery], plus the
// identity hazards the plane carries.
//
// The eight system entities are enumerated from the production list rather
// than re-declared here, so a shape added there appears in this pin as an
// unpinned name instead of quietly escaping it. They already span five of
// the six bodies the builder can emit — event, event+diagnostic, enum
// sensor, binary sensor with and without a JSON attribute source, timestamp
// sensor — and the class and zone aggregates below are the sixth and
// seventh, the only two that override the derived state topic.
//
// Identity hazards pinned here, all of them invisible in a body alone:
//
//   - the node id is the constant "security", not a device identifier, so
//     every entity of the plane shares one discovery node and a change to
//     that constant orphans the whole plane at once;
//   - the object id is the entity key, and the unique_id is that key with a
//     "loom_security_" prefix — two different derivations of one string;
//   - the class and zone entities' key and topic disagree on purpose
//     ("class_smoke" against security/class/smoke), which is exactly the
//     mismatch the topic override exists to prevent regressing;
//   - the zone slug travels verbatim into the key, the unique_id and the
//     entity-id seed.
func securityGoldenCases(t *testing.T) []secGoldenCase {
	t.Helper()
	tr := secGoldenTranslator(t)

	system := securitySystemEntities(tr)
	out := make([]secGoldenCase, 0, len(system)+5)
	for i := range system {
		out = append(out, secGoldenCase{
			name: "security/system/" + system[i].key,
			item: BuildSecurityDiscovery(secGoldenBase, secGoldenDevice, secGoldenConfigURL, system[i]),
		})
	}

	out = append(
		out,
		// A hazard class: smoke is a hazard rather than a diagnostic, so it
		// carries no entity_category while its class sibling below does. The
		// pair pins the [hmenum.SecurityClass.Diagnostic] split, which decides
		// whether a safety entity lands in an operator's main view or is filed
		// away under diagnostics.
		secGoldenCase{
			name: "security/class/smoke-hazard",
			item: BuildSecurityDiscovery(secGoldenBase, secGoldenDevice, secGoldenConfigURL,
				securityClassEntity(secGoldenBase, hmenum.SecurityClassSmoke, tr)),
		},
		// The diagnostic half of that split, and also the one class whose HA
		// device_class ("battery") is not a restatement of the class name.
		secGoldenCase{
			name: "security/class/battery-diagnostic",
			item: BuildSecurityDiscovery(secGoldenBase, secGoldenDevice, secGoldenConfigURL,
				securityClassEntity(secGoldenBase, hmenum.SecurityClassBattery, tr)),
		},
		// A zone with a display name. The slug is spelled the way the domain
		// mints one from an operator-typed room name — lower case with a
		// hyphen — because the slug is the key: it reaches the object id, the
		// unique_id and the entity-id seed unescaped, and a migration that
		// normalised the hyphen would re-key the entity silently.
		secGoldenCase{
			name: "security/zone/named",
			item: BuildSecurityDiscovery(secGoldenBase, secGoldenDevice, secGoldenConfigURL,
				securityZoneEntity(secGoldenBase, "erdgeschoss-flur", "Erdgeschoss Flur", tr)),
		},
		// The same zone without a display name: the label falls back to the
		// slug, so the entity's `name` changes while all three identifiers stay
		// put. Pinned as the counterpart, because a fallback that started
		// feeding the identifiers instead of only the label would move the
		// entity on every unnamed zone in one release.
		secGoldenCase{
			name: "security/zone/unnamed-falls-back-to-slug",
			item: BuildSecurityDiscovery(secGoldenBase, secGoldenDevice, secGoldenConfigURL,
				securityZoneEntity(secGoldenBase, "erdgeschoss-flur", "", tr)),
		},
		// No configuration URL. The reconciler passes whatever the wiring
		// resolved, and an unresolved URL is the normal case on a daemon
		// reachable only over MQTT. Pinned because the device card is shared
		// by every entity of the plane: a card that gained or lost a key
		// would rewrite all of them at once. The "state" entity stands in for
		// the rest — the card is byte-identical on every one.
		secGoldenCase{
			name: "security/system/state-without-config-url",
			item: BuildSecurityDiscovery(secGoldenBase, secGoldenDevice, "", system[2]),
		},
	)

	return out
}

// --- combined projections ------------------------------------------------

// combinedGoldenCtx is the [payload.CombinedDiscoveryContext] the event
// bridge builds per channel, reduced to what the four projections read: the
// two topics, the daemon catalogue, and the CCU's own parameter label.
//
// paramLabel is settable per fixture because the timer projection's entity
// name depends on whether the OCCU catalogue knows the wire parameter on
// this channel type, and both outcomes reach production.
type combinedGoldenCtx struct {
	stateTopic   string
	commandTopic string
	translate    func(key string) string
	paramLabel   string
}

func (c combinedGoldenCtx) CombinedStateTopic() string   { return c.stateTopic }
func (c combinedGoldenCtx) CombinedCommandTopic() string { return c.commandTopic }
func (c combinedGoldenCtx) Translate(key string) string  { return c.translate(key) }

func (c combinedGoldenCtx) ParameterLabel(hmenum.Parameter) (string, bool) {
	if c.paramLabel == "" {
		return "", false
	}
	return c.paramLabel, true
}

// combinedGoldenParent wraps a real device and additionally reports the
// multi-group structure that re-homes an entity onto its sub-device card on
// the per-parameter planes. See the "sub-devices-enabled" fixture for why
// that outcome is pinned here rather than assumed.
type combinedGoldenParent struct {
	*device.Device
}

func (combinedGoldenParent) HasSubDevices() bool { return true }

// combinedGoldenSiren is the HmIP siren as the model hands it to discovery:
// a real device object, so the device block's name, model and manufacturer
// come from the same harvest production uses.
func combinedGoldenSiren() *device.Device {
	return device.New(device.Config{
		InterfaceID:  "HmIP-RF",
		Interface:    hmenum.InterfaceHmIPRF,
		Address:      "0001ABCD",
		Model:        "HmIP-ASIR",
		Name:         "Alarmsirene Flur",
		Manufacturer: hmenum.ManufacturerEQ3,
	})
}

// combinedGoldenCase is one input to the combined builder.
type combinedGoldenCase struct {
	name string
	ev   CombinedEvent
}

// combinedGoldenCases is the fixture matrix for the combined projections.
//
// The four projections in the model layer — Timer (duration),
// LevelCombined, HSColor and EnumSelect — are invoked for real rather than
// transcribed, so the hand-written value templates
// ("{{ value_json.level }}", "{{ value_json.hue }}"), the number bounds and
// the select's option list are pinned as the source emits them. A
// transcription would pin this file against itself.
//
// Identity hazards pinned here:
//
//   - the node id and the object id are derived two different ways from the
//     same address — [discoveryNodeID] slugs the central and lower-cases the
//     address, [physicalDeviceIdentifier] prefixes "openccu-loom_" and adds
//     the central only for an address family that repeats across CCUs;
//   - the unique_id is the object id, so the topic and the registry key move
//     together — but only when the projection left UniqueID empty;
//   - there is NO `default_entity_id` on any combined entity. Every other
//     plane seeds it; this one does not, so Home Assistant derives the
//     entity_id from the device name and the entity name instead. That makes
//     the projection's `name` an identity key here, which is why the two
//     timer fixtures below differ only in their label.
func combinedGoldenCases(t *testing.T) []combinedGoldenCase {
	t.Helper()
	cat, err := i18n.NewCatalogs()
	if err != nil {
		t.Fatalf("load catalogues: %v", err)
	}
	translate := func(key string) string { return cat.T("en", key) }
	tb := NewTopicBuilder(secGoldenBase)

	ctxFor := func(central, iface, addr string, ch int, kind, paramLabel string) combinedGoldenCtx {
		return combinedGoldenCtx{
			stateTopic:   tb.CombinedState(central, iface, addr, ch, kind),
			commandTopic: tb.CombinedCommand(central, iface, addr, ch, kind),
			translate:    translate,
			paramLabel:   paramLabel,
		}
	}

	siren := func(comp hadiscovery.Component) CombinedEvent {
		return CombinedEvent{
			Central: "ccu-01", Interface: "HmIP-RF",
			DeviceAddress: "0001ABCD", ChannelNo: 3,
			DeviceName: "Alarmsirene Flur", Model: "HmIP-ASIR",
			Device:    combinedGoldenSiren(),
			Kind:      combined.KindDuration,
			Component: comp,
		}
	}

	timer := combined.NewTimer("0001ABCD:3", nil,
		hmenum.ParameterDurationValue, hmenum.ParameterDurationUnit)

	// The timer with the OCCU catalogue's own label for DURATION_VALUE.
	// The projection strips the leading "Wert " so Home Assistant's
	// entity_id derivation reads "…_zeitdauer" rather than
	// "…_wert_zeitdauer" — and since this plane seeds no entity id, that
	// strip IS the entity id. Pinned with the German label because that is
	// what the CCU catalogue carries; the strip is what is under test, not
	// the locale.
	withLabel := siren(timer.HACombinedDiscovery(
		ctxFor("ccu-01", "HmIP-RF", "0001ABCD", 3, combined.KindDuration, "Wert Zeitdauer"),
	))

	// The same timer where the CCU catalogue has no entry — the normal case
	// for a synthetic combined parameter — so the name comes from the
	// daemon catalogue instead. Two different names, one identical set of
	// identifiers: the pair is what shows that the label feeds the entity
	// id and nothing else.
	withoutLabel := siren(timer.HACombinedDiscovery(
		ctxFor("ccu-01", "HmIP-RF", "0001ABCD", 3, combined.KindDuration, ""),
	))

	// The blind's level+slats pair: a diagnostic sensor whose state is
	// extracted from the JSON body by a hand-written template.
	level := combined.NewLevelCombinedWithCentral("ccu-01", "0002BEEF:4",
		hmenum.ParameterLevel, hmenum.ParameterLevel2)
	blind := CombinedEvent{
		Central: "ccu-01", Interface: "HmIP-RF",
		DeviceAddress: "0002BEEF", ChannelNo: 4,
		DeviceName: "Jalousie Wohnzimmer", Model: "HmIP-BBL",
		Device: device.New(device.Config{
			InterfaceID: "HmIP-RF", Interface: hmenum.InterfaceHmIPRF,
			Address: "0002BEEF", Model: "HmIP-BBL",
			Name: "Jalousie Wohnzimmer", Manufacturer: hmenum.ManufacturerEQ3,
		}),
		Kind: combined.KindLevelCombined,
		Component: level.HACombinedDiscovery(
			ctxFor("ccu-01", "HmIP-RF", "0002BEEF", 4, combined.KindLevelCombined, ""),
		),
	}

	// The colour light's hue+saturation pair — the second template-driven
	// diagnostic sensor, and the reason both are pinned: they differ only
	// in the template and the name, so a shared refactor that swapped one
	// for the other would be invisible in either payload alone.
	hs := combined.NewHSColorWithCentral("ccu-01", "0003CAFE:1", nil,
		hmenum.ParameterHue, hmenum.ParameterSaturation)
	light := CombinedEvent{
		Central: "ccu-01", Interface: "HmIP-RF",
		DeviceAddress: "0003CAFE", ChannelNo: 1,
		DeviceName: "Stehlampe", Model: "HmIP-BSL",
		Device: device.New(device.Config{
			InterfaceID: "HmIP-RF", Interface: hmenum.InterfaceHmIPRF,
			Address: "0003CAFE", Model: "HmIP-BSL",
			Name: "Stehlampe", Manufacturer: hmenum.ManufacturerEQ3,
		}),
		Kind: combined.KindHSColor,
		Component: hs.HACombinedDiscovery(
			ctxFor("ccu-01", "HmIP-RF", "0003CAFE", 1, combined.KindHSColor, ""),
		),
	}

	// The garage door mode: the only writable projection of the four that
	// reaches a `select`, and the only one whose body carries an option
	// list. The options are the door's travel order, and Home Assistant
	// stores the selected option as the entity's state — renaming or
	// reordering one strands whatever state a user already has.
	doorMode := combined.NewEnumSelect(combined.EnumSelectConfig{
		Address:           "0004DOOR:1",
		CentralName:       "ccu-01",
		Kind:              "door_mode",
		LabelKey:          "discovery.garage_door_mode",
		CombinedParameter: "DOOR_MODE",
		StateParameter:    hmenum.ParameterDoorState,
		CommandParameter:  hmenum.ParameterDoorCommand,
		Modes: []combined.EnumSelectMode{
			{State: "CLOSED", Command: "CLOSE"},
			{State: "VENTILATION_POSITION", Command: "PARTIAL_OPEN"},
			{State: "OPEN", Command: "OPEN"},
		},
	})
	if doorMode == nil {
		t.Fatal("NewEnumSelect returned nil — the fixture no longer produces a projection")
	}
	garage := CombinedEvent{
		Central: "ccu-01", Interface: "HmIP-RF",
		DeviceAddress: "0004DOOR", ChannelNo: 1,
		DeviceName: "Garagentor", Model: "HmIP-MOD-TM",
		Device: device.New(device.Config{
			InterfaceID: "HmIP-RF", Interface: hmenum.InterfaceHmIPRF,
			Address: "0004DOOR", Model: "HmIP-MOD-TM",
			Name: "Garagentor", Manufacturer: hmenum.ManufacturerEQ3,
		}),
		Kind: "door_mode",
		Component: doorMode.HACombinedDiscovery(
			ctxFor("ccu-01", "HmIP-RF", "0004DOOR", 1, "door_mode", ""),
		),
	}

	// Identity hazard: a central-scoped address. An INT000* internal
	// address repeats verbatim on every CCU, so [physicalDeviceIdentifier]
	// folds the central into the object id — and therefore into the topic
	// AND the unique_id — while a hardware serial does not. Both halves of
	// that rule are pinned (every other fixture is the unscoped half),
	// because making the scope unconditional would re-key every combined
	// entity on every fleet at once.
	internalAddr := withoutLabel
	internalAddr.DeviceAddress = "INT0000001"
	internalAddr.DeviceName = "Systeminterner Kanal"
	internalAddr.Device = nil
	// The projection's own command topic has to be rebuilt for the new
	// address: the bridge fills the state topic but never rewrites what the
	// projection set, so a copied fixture would pin a body whose command
	// topic points at a different device — an inconsistency in the pin, not
	// in the plane.
	internalAddr.Component = timer.HACombinedDiscovery(
		ctxFor("ccu-01", "HmIP-RF", "INT0000001", 3, combined.KindDuration, ""),
	)

	// Identity hazard: no device object. CombinedEvent.Device is consulted
	// only for the harvested info map, and the bridge falls back to the
	// event's own DeviceName / Model. Pinned because an entity whose device
	// block identifies differently from its siblings' lands on a second
	// device card beside the real one.
	noDevice := withoutLabel
	noDevice.Device = nil

	// Identity hazard: a device that reports sub-devices, with the split
	// enabled on the builder. It cannot take effect here —
	// [deviceDescriptor] needs a channel inspector and CombinedEvent
	// carries none — so the combined entity stays on the parent card while
	// the per-parameter entities of the same channel move to the
	// sub-device's. That asymmetry is pinned deliberately: a migration that
	// "fixed" it in passing would move every combined entity on every
	// multi-group device to a different device card.
	subDevice := withoutLabel
	subDevice.Device = combinedGoldenParent{Device: combinedGoldenSiren()}

	// Identity hazard: a projection that sets its own unique_id. The
	// builder documents that it never overwrites a field the projection
	// filled, which means a projection can decouple the registry key from
	// the topic — the object id still comes from the address. Nothing in
	// the model layer does this today; it is pinned because the frame's
	// "projection first" rule is the seam the whole plane rests on.
	projectionUniqueID := withoutLabel
	projectionUniqueID.Component.UniqueID = "openccu-loom_projection_pinned"

	// A second central. The node id is central-scoped and MUST change; the
	// object id and the unique_id derive from a globally unique hardware
	// address and MUST NOT.
	secondCentral := withoutLabel
	secondCentral.Central = "ccu-02"
	secondCentral.Component = timer.HACombinedDiscovery(
		ctxFor("ccu-02", "HmIP-RF", "0001ABCD", 3, combined.KindDuration, ""),
	)

	return []combinedGoldenCase{
		{"combined/duration-ccu-label", withLabel},
		{"combined/duration-catalogue-label", withoutLabel},
		{"combined/level-combined", blind},
		{"combined/hs-color", light},
		{"combined/enum-select-door-mode", garage},
		{"combined/central-scoped-address", internalAddr},
		{"combined/no-device-object", noDevice},
		{"combined/sub-devices-enabled", subDevice},
		{"combined/projection-sets-unique-id", projectionUniqueID},
		{"combined/second-central", secondCentral},
	}
}

// TestSecurityAndCombinedDiscoveryPayloadsArePinned pins the topic and the
// payload of every entity shape security_discovery.go and
// discovery_combined.go produce, so moving this daemon's discovery layer
// onto the shared model (ADR 0070) can be checked against the one criterion
// that matters: the bytes do not change.
//
// Two planes share one file because they are small and because they fail
// the same way. Three of the ways a move breaks them — a changed unique_id,
// a changed entity-id seed, a changed node id — are silent in Home
// Assistant: the installed entity is orphaned, a new one appears beside it,
// and nothing is written to any log. Two of the three are invisible in the
// body, which is why the topic is pinned alongside it.
//
// The payload is stored decoded so the file stays reviewable; comparison is
// on the canonical re-encoding of both sides, exact for every key and
// value, with only whitespace — which no consumer sees — out of scope.
//
// What this does NOT cover:
//
//   - It is not a fleet. A real CCU's ~9,996 retained configs cannot be
//     reconstructed in CI; this pins one case per shape that resolves
//     differently, and a pin that looked complete would be worse than one
//     that admits it is not.
//   - Names are pinned at the default locale. A catalogue edit in another
//     locale, or a locale change, is out of scope — but note that the
//     combined plane seeds no `default_entity_id`, so for those entities a
//     name change does move the entity id.
//   - Only the discovery configs are pinned, not the state, command or
//     availability traffic on the topics they name. That the plane actually
//     publishes there is the round-trip guards' job.
//   - The declines are not pinned (an empty security key, a combined event
//     with no kind, address or platform); they are covered in
//     security_discovery_test.go and discovery_combined_test.go.
//   - The security plane's class and zone sets follow the installation, so
//     which classes and zones exist is a property of a CCU, not of this
//     pin — one class of each kind and one zone stand in for all of them.
//
// A diff here is a regression until someone shows otherwise. Refresh with:
//
//	go test ./internal/north/mqtt/ -run TestSecurityAndCombinedDiscoveryPayloadsArePinned -update-security-combined-golden
func TestSecurityAndCombinedDiscoveryPayloadsArePinned(t *testing.T) {
	tb := NewTopicBuilder(secGoldenBase)
	db := NewDefaultDiscoveryBuilder(tb, "ccu-01")
	db.SetHubInfoFor("ccu-01", HubInfo{Serial: "3014F711A0001234"})
	db.SetHubInfoFor("ccu-02", HubInfo{Serial: "3014F711B0005678"})
	// On, so the sub-device fixture exercises the branch rather than being
	// short-circuited by the flag before it reaches the interesting check.
	db.SubDevicesEnabled = true

	got := map[string]goldenEntry{}
	add := func(name string, item DiscoveryItem) {
		if !item.OK {
			t.Fatalf("%s: builder returned OK=false — the fixture no longer produces an entity", name)
		}
		var body map[string]any
		if err := json.Unmarshal(item.Payload, &body); err != nil {
			t.Fatalf("%s: unmarshal: %v", name, err)
		}
		got[name] = goldenEntry{
			Topic:   tb.DiscoveryConfig(item.Component, item.NodeID, item.ObjectID),
			Payload: body,
		}
	}

	for _, c := range securityGoldenCases(t) {
		add(c.name, c.item)
	}
	for _, c := range combinedGoldenCases(t) {
		add(c.name, db.BuildCombinedDiscovery(c.ev.Central, c.ev))
	}

	if *updateSecurityCombinedGolden {
		writeSecurityCombinedGolden(t, got)
		t.Logf("rewrote %s with %d payloads", securityCombinedGoldenPath, len(got))
		return
	}

	want := readSecurityCombinedGolden(t)
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

// TestSecurityAndCombinedPinnedPayloadsAreValid reads the same fixtures back
// through the extracted Home Assistant MQTT schema, so a payload that is
// pinned is also a payload HA accepts. A pin on its own only says the bytes
// did not move; it cannot say they were right to begin with.
func TestSecurityAndCombinedPinnedPayloadsAreValid(t *testing.T) {
	tb := NewTopicBuilder(secGoldenBase)
	db := NewDefaultDiscoveryBuilder(tb, "ccu-01")
	db.SetHubInfoFor("ccu-01", HubInfo{Serial: "3014F711A0001234"})
	db.SetHubInfoFor("ccu-02", HubInfo{Serial: "3014F711B0005678"})
	db.SubDevicesEnabled = true

	for _, c := range securityGoldenCases(t) {
		if err := ValidateDiscoveryBody(c.item.Component, c.item.Payload); err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	for _, c := range combinedGoldenCases(t) {
		item := db.BuildCombinedDiscovery(c.ev.Central, c.ev)
		if err := ValidateDiscoveryBody(item.Component, item.Payload); err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
	}
}

func readSecurityCombinedGolden(t *testing.T) map[string]goldenEntry {
	t.Helper()
	raw, err := os.ReadFile(securityCombinedGoldenPath)
	if err != nil {
		t.Fatalf("read %s: %v (create it with -update-security-combined-golden)",
			securityCombinedGoldenPath, err)
	}
	var out map[string]goldenEntry
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("parse %s: %v", securityCombinedGoldenPath, err)
	}
	return out
}

func writeSecurityCombinedGolden(t *testing.T, entries map[string]goldenEntry) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(securityCombinedGoldenPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	raw, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(securityCombinedGoldenPath, append(raw, '\n'), 0o600); err != nil {
		t.Fatalf("write %s: %v", securityCombinedGoldenPath, err)
	}
}
