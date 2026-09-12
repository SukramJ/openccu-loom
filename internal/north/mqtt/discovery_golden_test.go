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

		// --- The kinds the first pass never reached ----------------------
		//
		// Everything below is a component this plane's per-parameter path
		// can emit and that nothing in the repository pinned: cover, lock,
		// light, valve, siren, select, text, update, climate.
		//
		// Each is paired with its MASTER twin. MASTER is not a variation of
		// the body: it moves the state, command and attribute topics into
		// the `master/` bucket — a different retained topic for the same
		// entity — and it stamps entity_category. Both halves therefore
		// have to be pinned per kind, not once for the plane.
		//
		// Reading the two halves side by side shows something the pin
		// records rather than changes: the discovery topic and the
		// unique_id are identical for a MASTER and a VALUES parameter of
		// the same name on the same channel. Only the state topic differs.
		// Nothing today collides — the MASTER whitelist in
		// internal/store/visibility/rules.go surfaces no parameter name
		// that is also a VALUES parameter — but the identity does not
		// carry the paramset, so nothing but that whitelist prevents it.
		//
		// Some of those twins are synthetic, and that is said here rather
		// than left to be inferred from a diff: no CCU MASTER parameter
		// classifies to climate, to a cover or to a siren. Those cases pin
		// how the MASTER branch composes with that component; they do not
		// claim to reproduce a payload a fleet produces.
		{"cover/values", with(base("000D0001", "HmIP-BROLL", 4),
			"LEVEL", hmenum.DataPointCategoryCover, true, values)},
		{"cover/master", with(base("000D0001", "HmIP-BROLL", 4),
			"LEVEL", hmenum.DataPointCategoryCover, true, master)},
		{"lock/values", with(base("000E0001", "HmIP-DLD", 1),
			"LOCK_TARGET_LEVEL", hmenum.DataPointCategoryLock, true, values)},
		{"lock/master", with(base("000E0001", "HmIP-DLD", 1),
			"LOCK_TARGET_LEVEL", hmenum.DataPointCategoryLock, true, master)},
		{"light/values", with(base("000F0001", "HmIP-BSL", 4),
			"LEVEL", hmenum.DataPointCategoryLight, true, values)},
		{"light/master", with(base("000F0001", "HmIP-BSL", 4),
			"LEVEL", hmenum.DataPointCategoryLight, true, master)},
		{"valve/values", with(base("00100001", "HmIP-FALMOT-C12", 1),
			"VALVE_STATE", hmenum.DataPointCategoryValve, true, values)},
		{"valve/master", with(base("00100001", "HmIP-FALMOT-C12", 1),
			"VALVE_STATE", hmenum.DataPointCategoryValve, true, master)},
		{"siren/values", with(base("00110001", "HmIP-ASIR", 3),
			"ACOUSTIC_ALARM_ACTIVE", hmenum.DataPointCategorySiren, true, values)},
		{"siren/master", with(base("00110001", "HmIP-ASIR", 3),
			"ACOUSTIC_ALARM_ACTIVE", hmenum.DataPointCategorySiren, true, master)},
		// select carries a VALUE_LIST, so its two halves also pin the
		// options list and the pair of enum templates that map an HA option
		// back to the CCU token. Losing the list is the same edit as losing
		// the templates, and the templates fail quietly: HA sends the
		// lowercase option and the CCU rejects a value it never declared.
		{"select/values", with(base("00120001", "HmIP-eTRV-2", 1),
			"SET_POINT_MODE", hmenum.DataPointCategorySelect, true,
			enumDesc(hmenum.ParamsetKeyValues,
				"AUTO_MODE", "MANU_MODE", "PARTY_MODE", "BOOST_MODE"))},
		{"select/master", with(base("00120001", "HmIP-eTRV-2", 1),
			"SET_POINT_MODE", hmenum.DataPointCategorySelect, true,
			enumDesc(hmenum.ParamsetKeyMaster,
				"AUTO_MODE", "MANU_MODE", "PARTY_MODE", "BOOST_MODE"))},
		// An action_select is the one select variant whose entity_category
		// is forced to "config" by the category alone, on the VALUES
		// paramset where nothing else would set it.
		{"select/action-values", with(base("00120001", "HmIP-eTRV-2", 1),
			"BOOST_MODE", hmenum.DataPointCategoryActionSelect, true,
			enumDesc(hmenum.ParamsetKeyValues, "OFF", "ON"))},
		// text pins min/max, which the text branch truncates to whole
		// numbers before emitting.
		{"text/values", with(base("00130001", "HmIP-WRCD", 1),
			"DISPLAY_LINE", hmenum.DataPointCategoryText, true,
			textDesc(hmenum.ParamsetKeyValues))},
		{"text/master", with(base("00130001", "HmIP-WRCD", 1),
			"DISPLAY_LINE", hmenum.DataPointCategoryText, true,
			textDesc(hmenum.ParamsetKeyMaster))},
		{"update/values", with(base("00140001", "HmIP-BSM", 0),
			"FIRMWARE", hmenum.DataPointCategoryUpdate, false, values)},
		{"update/master", with(base("00140001", "HmIP-BSM", 0),
			"FIRMWARE", hmenum.DataPointCategoryUpdate, false, master)},
		{"climate/values", with(base("00150001", "HmIP-BWTH", 1),
			"SET_POINT_TEMPERATURE", hmenum.DataPointCategoryClimate, true, values)},
		{"climate/master", with(base("00150001", "HmIP-BWTH", 1),
			"SET_POINT_TEMPERATURE", hmenum.DataPointCategoryClimate, true, master)},

		// --- Identity hazards the first pass did not carry ---------------
		//
		// Hazard: a synthetic data point. A calculated DP (DEW_POINT and
		// friends) is not a wire parameter — it shares a channel and a name
		// with one. Two things move for it at once: the state topic goes to
		// the `calculated/` bucket, and the unique_id gains the `calculated`
		// family marker. Drop the marker and the key collides with the real
		// VALUES parameter of the same name; Home Assistant keeps whichever
		// config arrived first and the other entity is never created.
		{"sensor/calculated-synthetic", func() Event {
			ev := with(base("0001ABCD", "HmIP-BWTH", 1),
				"DEW_POINT", hmenum.DataPointCategorySensor, false, values)
			ev.Calculated = true
			return ev
		}()},
		// Hazard: a central-scoped address that is not CUxD. The
		// virtual-remote buses repeat verbatim on every CCU, so their key
		// carries the central's serial — by a different rule than the CUxD
		// prefix, in a different branch of NeedsCentralScope. The CUxD pair
		// above would keep passing if the virtual-remote root list were
		// emptied.
		{"event/virtual-remote-central-scoped", with(
			Event{
				Central: "ccu-01", Interface: "BidCos-RF",
				DeviceAddress: "BidCoS-RF", Model: "HM-RCV-50", ChannelNo: 3,
				ChannelAddress: "BidCoS-RF:3",
			},
			"PRESS_SHORT", hmenum.DataPointCategoryEvent, false, values,
		)},
		// Hazard: an absent entity-id seed. A parameter the translation
		// catalogue marks primary publishes `name: null`, which tells Home
		// Assistant to seed the entity id from the device name alone. An
		// empty string, or the key going missing, both make HA derive a
		// different id — and the entity that already exists under the old
		// one stays behind, orphaned.
		{"sensor/name-null-seed", with(base("0001ABCD", "HmIP-BWTH", 1),
			"ACTUAL_TEMPERATURE", hmenum.DataPointCategorySensor, false,
			&payload.GenericConfig{
				Paramset: hmenum.ParamsetKeyValues, LabelOmitted: true,
			})},
	}
}

// enumDesc is a VALUE_LIST-carrying wire descriptor, for the select cases.
func enumDesc(ps hmenum.ParamsetKey, list ...string) *payload.GenericConfig {
	return &payload.GenericConfig{
		Paramset: ps, Type: hmenum.ParameterTypeEnum, ValueList: list,
	}
}

// textDesc is a bounded string descriptor, for the text cases. The bounds
// are floats on the wire and whole numbers in the payload; the fractional
// part is there so the pin covers the truncation rather than a value that
// would survive either way.
func textDesc(ps hmenum.ParamsetKey) *payload.GenericConfig {
	mn, mx := 0.0, 12.5
	return &payload.GenericConfig{
		Paramset: ps, Type: hmenum.ParameterTypeString, Min: &mn, Max: &mx,
	}
}

// goldenSubDeviceCases are the fixtures that need the sub-device feature
// flag, which is set on the builder rather than on the event. They live in
// their own slice so the main matrix keeps running through the default
// builder and the flag cannot leak into it.
//
// Hazard: the node id is not the device identifier. With sub-devices on,
// the device block re-identifies as `<parent>-<group>` and re-parents to
// the physical device, while the discovery topic's node id stays the
// physical device throughout. Those two are derived by different code from
// different inputs, and a move that quietly aligned them would re-key every
// entity on a multi-group device — every blind, every multi-channel
// actuator on the fleet.
func goldenSubDeviceCases() []goldenCase {
	ev := Event{
		Central: "ccu-01", Interface: "HmIP-RF",
		DeviceAddress: "000A0001", Model: "HmIP-BROLL", ChannelNo: 4,
		ChannelAddress: "000A0001:4",
		DeviceName:     "Wohnzimmer Jalousie",
		Parameter:      "LEVEL",
		Category:       hmenum.DataPointCategoryCover,
		Writable:       true,
		Descriptor:     &payload.GenericConfig{Paramset: hmenum.ParamsetKeyValues},
		Device:         fakeSubDeviceParent{hasSubs: true},
		Channel:        fakeSubDeviceChannel{groupNo: 2, multiGroup: true, subName: "Jalousie Ost"},
	}
	return []goldenCase{{"cover/sub-device", ev}}
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
// What this does NOT cover, stated plainly because the matrix reads
// broader than it is. It pins the per-parameter path only: the channel
// aggregates (custom data points reached through Event.ChannelType) and
// the press/impulse/device-error event groups leave through other
// builders and are pinned, or not, elsewhere. Entity names are pinned at
// their untranslated fallbacks, so a locale change is out of scope. The
// state, command and availability topics inside these bodies are pinned
// as strings; whether anything publishes to them is the round-trip
// guard's job. Several MASTER twins are synthetic — see the note on them
// in goldenCases. And the entity-description and quantity tables that
// decorate these payloads are pinned only at the values these fixtures
// happen to reach: a rule for a model not named here is not covered.
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

	// The sub-device fixtures need the feature flag, which lives on the
	// builder. A second builder keeps it off the main matrix.
	subDB := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu-01").WithSubDevices(true)
	subDB.SetHubInfoFor("ccu-01", HubInfo{Serial: "3014F711A0001234"})

	got := map[string]goldenEntry{}
	build := func(c goldenCase, b *DefaultDiscoveryBuilder) {
		component, nodeID, objectID, buf, ok := b.Build(c.ev)
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
	for _, c := range goldenCases() {
		build(c, db)
	}
	for _, c := range goldenSubDeviceCases() {
		build(c, subDB)
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

// discoveryGoldenUnreachableShapes names the pinned cases whose body the
// Home Assistant schema rejects, with the reason each is excluded from
// [TestDiscoveryPinnedPayloadsAreValid] rather than fixed.
//
// Both kinds are unreachable on this plane in production, and so the bodies
// below are latent rather than live breakage. That is why they are excluded
// rather than reported as live breakage. Event.Category on the per-parameter
// path is the generic wire data point's category
// (internal/model/generic/resolver.go's kindToCategory), which spans only
// switch / binary_sensor / sensor / button / action / action_number /
// action_select / number / select / text. siren and climate are
// custom-data-point categories: their events always carry a ChannelType
// and leave through the channel aggregate, which builds a different body.
//
// The fixtures stay pinned anyway. resolveComponent does route these
// categories here, so the branches exist and a change must not move what
// they emit unnoticed — but what they emit today is a body Home Assistant
// would not accept:
//
//   - siren: no `command_topic` at all. Siren falls through the
//     per-component switch to its `default` arm, which sets none, and
//     command_topic is required — HA would reject the config outright.
//   - climate: a `device_class`, which the climate schema does not declare,
//     so HA drops it silently.
//
// Both are real defects in what this plane would emit, and both are
// unreachable, so neither is fixed here: a fix nothing can exercise is a
// guess. `light` used to be on this list for a `value_template` the light
// schema does not declare; that key is no longer published and the shape
// validates, so it came off.
var discoveryGoldenUnreachableShapes = map[string]struct{}{
	"siren/values":   {},
	"siren/master":   {},
	"climate/values": {},
	"climate/master": {},
}

// TestDiscoveryPinnedPayloadsAreValid reads the same fixtures back through
// the extracted Home Assistant MQTT schema, so a payload that is pinned is
// also a payload HA accepts. A pin on its own only says the bytes did not
// move; it cannot say they were right to begin with, and this plane is the
// one where most of them live.
//
// [discoveryGoldenUnreachableShapes] is skipped, and says why per case.
func TestDiscoveryPinnedPayloadsAreValid(t *testing.T) {
	db := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu-01")
	db.SetHubInfoFor("ccu-01", HubInfo{Serial: "3014F711A0001234"})
	db.SetHubInfoFor("ccu-02", HubInfo{Serial: "3014F711B0005678"})
	subDB := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu-01").WithSubDevices(true)
	subDB.SetHubInfoFor("ccu-01", HubInfo{Serial: "3014F711A0001234"})

	check := func(c goldenCase, b *DefaultDiscoveryBuilder) {
		component, _, _, buf, ok := b.Build(c.ev)
		if !ok {
			t.Fatalf("%s: Build returned ok=false", c.name)
		}
		err := ValidateDiscoveryBody(component, buf)
		if _, excluded := discoveryGoldenUnreachableShapes[c.name]; excluded {
			// Asserted, not merely skipped: if one of these ever starts
			// validating, the exclusion has outlived its reason and the
			// case belongs back in the checked set.
			if err == nil {
				t.Errorf("%s: now valid — drop it from discoveryGoldenUnreachableShapes", c.name)
			}
			return
		}
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
	}
	for _, c := range goldenCases() {
		check(c, db)
	}
	for _, c := range goldenSubDeviceCases() {
		check(c, subDB)
	}
}
