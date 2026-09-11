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

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

var updateAlarmGolden = flag.Bool("update-alarm-golden", false,
	"rewrite the pinned alarm discovery payloads")

var alarmGoldenPath = filepath.Join("testdata", "discovery_golden_alarm.json")

// alarmGoldenBase is the topic base every fixture publishes under. It is
// short on purpose so a diff of a state/command/availability topic is
// readable; production passes [TopicBuilder.Base], which is already
// trimmed, so a base needing normalisation is not a shape this plane can
// receive and is deliberately not pinned.
const alarmGoldenBase = "gh"

// alarmGoldenZone is a zone id in the form the REST layer mints them —
// uuid.NewString(). The hyphens matter: they travel into the object id,
// the unique_id and the `default_entity_id` seed, so a fixture using a
// tidy slug would hide what the real key looks like.
const alarmGoldenZone = "7f3a1c2e-9b4d-4e51-8a6f-2d0c5b8e1a33"

// alarmGoldenCase is one built discovery item plus the name a diff reports.
type alarmGoldenCase struct {
	name string
	item DiscoveryItem
}

// alarmGoldenCases is the fixture matrix for the alarm plane: one case per
// payload shape the three builders in alarm_discovery.go resolve
// differently, plus the cases that carry an identity hazard.
//
// The plane emits three entity kinds per zone — the panel, the
// "clear latched motion detectors" button, and the latched-detector count
// sensor — and the panel's body varies with the zone's mode set and its
// per-verb code policy. Those are the axes covered below.
func alarmGoldenCases() []alarmGoldenCase {
	allModes := []hmenum.AlarmMode{
		hmenum.AlarmModePerimeter, hmenum.AlarmModeFull,
		hmenum.AlarmModeNight, hmenum.AlarmModeVacation,
		hmenum.AlarmModeCustom,
	}
	return []alarmGoldenCase{
		// The baseline panel: every protection mode configured, no code
		// gate. Pins the supported-feature list in its canonical arm-button
		// order — HA renders the buttons in the order it is given, so a
		// reordering is visible to an operator even though no entity moves.
		{"panel/zone-all-modes", BuildAlarmPanelDiscovery(
			alarmGoldenBase, alarmGoldenZone, "Erdgeschoss", allModes, false, false, false,
		)},
		// One mode only. The feature list is derived, not stored, so a zone
		// that can only arm away must not advertise buttons that would send
		// a command the engine rejects.
		{"panel/zone-single-mode", BuildAlarmPanelDiscovery(
			alarmGoldenBase, alarmGoldenZone, "Erdgeschoss",
			[]hmenum.AlarmMode{hmenum.AlarmModeFull}, false, false, false,
		)},
		// Code on arm only. Asymmetric on purpose: it pins both booleans at
		// once with different values, and it pins that the command template
		// (which folds the entered code into the raw command JSON) appears
		// for the arm verb. Without the template HA sends the bare action
		// and the code never reaches loom's validator.
		{"panel/zone-code-arm-only", BuildAlarmPanelDiscovery(
			alarmGoldenBase, alarmGoldenZone, "Erdgeschoss", allModes, false, true, false,
		)},
		// Code on disarm only — the counterpart, because the template and
		// the REMOTE_CODE sentinel hang off either verb, not off arming.
		{"panel/zone-code-disarm-only", BuildAlarmPanelDiscovery(
			alarmGoldenBase, alarmGoldenZone, "Erdgeschoss", allModes, false, false, true,
		)},
		// Identity hazard: the aggregate panel. master=true forces the
		// topic/unique-id segment to the reserved "master" token and throws
		// the zone id away — a real zone id is passed here precisely so the
		// pin fails if that override is ever dropped and the aggregate
		// starts keying on a zone. It is also the one panel whose node id
		// is not derived from anything device-shaped: every alarm entity
		// sits under the fixed "alarm" node, not under a device identifier.
		{"panel/master-overrides-zone", BuildAlarmPanelDiscovery(
			alarmGoldenBase, alarmGoldenZone, "Alarm system", allModes, true, false, false,
		)},
		// The reset button. Its object id, unique_id and entity-id seed are
		// the panel's with a "_reset_motion" suffix, so it is a second
		// identity derived from the first: a change to PanelUniqueID moves
		// three entities, not one.
		{"button/zone-reset-motion", BuildAlarmMotionResetDiscovery(
			alarmGoldenBase, alarmGoldenZone, "Erdgeschoss", alarmResetMotionNameFallback, false,
		)},
		// The latched-detector count sensor — the third identity off the
		// same seed, and the only alarm entity that carries an
		// entity_category, a state_class and a unit.
		{"sensor/zone-triggered-motion", BuildAlarmTriggeredMotionDiscovery(
			alarmGoldenBase, alarmGoldenZone, "Erdgeschoss", alarmTriggeredMotionNameFallback, false,
		)},
		// Identity hazard, composed: the master override and the suffix
		// meet here. Each of the three builders repeats the override
		// itself, so pinning it on the panel alone would not catch a
		// derived builder losing it — and a derived entity keyed on a zone
		// while its panel is keyed on "master" is exactly the split HA
		// reports as nothing at all.
		{"button/master-reset-motion", BuildAlarmMotionResetDiscovery(
			alarmGoldenBase, alarmGoldenZone, "Alarm system", alarmResetMotionNameFallback, true,
		)},
		{"sensor/master-triggered-motion", BuildAlarmTriggeredMotionDiscovery(
			alarmGoldenBase, alarmGoldenZone, "Alarm system", alarmTriggeredMotionNameFallback, true,
		)},
	}
}

// TestAlarmDiscoveryPayloadsArePinned pins what the alarm plane puts on the
// wire, so moving this plane onto the shared model (ADR 0070) can be shown
// to change nothing an installed Home Assistant sees.
//
// Topic and payload are both pinned. The topic is not decoration here: the
// alarm plane's node id is a constant ("alarm") rather than a device
// identifier, and its object id is the zone — or the reserved master token
// — so a changed node id, a changed object id or a changed unique_id all
// orphan the installed entity and stand a new one beside it, with nothing
// in any log and nothing in Home Assistant's UI to say what happened.
//
// What this does NOT cover: the state, availability, event and command
// topics the panel names are pinned only as strings inside these payloads —
// whether anything publishes to them is the round-trip guard's job, not
// this test's. The localized master/button/sensor display names are pinned
// at their English fallbacks, so a translation change is out of scope. And
// this is a per-shape pin, not a fleet capture: the zone count of a real
// installation says nothing here.
//
// A diff is a regression until someone shows otherwise. Refresh with:
//
//	go test ./internal/north/mqtt/ -run TestAlarmDiscoveryPayloadsArePinned -update-alarm-golden
func TestAlarmDiscoveryPayloadsArePinned(t *testing.T) {
	tb := NewTopicBuilder(alarmGoldenBase)

	got := map[string]goldenEntry{}
	for _, c := range alarmGoldenCases() {
		if !c.item.OK {
			t.Fatalf("%s: builder returned OK=false — the fixture no longer produces an entity", c.name)
		}
		var body map[string]any
		if err := json.Unmarshal(c.item.Payload, &body); err != nil {
			t.Fatalf("%s: unmarshal: %v", c.name, err)
		}
		got[c.name] = goldenEntry{
			Topic:   tb.DiscoveryConfig(c.item.Component, c.item.NodeID, c.item.ObjectID),
			Payload: body,
		}
	}

	if *updateAlarmGolden {
		writeAlarmGolden(t, got)
		t.Logf("rewrote %s with %d payloads", alarmGoldenPath, len(got))
		return
	}

	want := readAlarmGolden(t)
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

// TestAlarmDiscoveryPinnedPayloadsAreValid reads the same fixtures back
// through the extracted Home Assistant MQTT schema, so a payload that is
// pinned is also a payload HA accepts. A pin on its own only says the bytes
// did not move; it cannot say they were right to begin with.
func TestAlarmDiscoveryPinnedPayloadsAreValid(t *testing.T) {
	for _, c := range alarmGoldenCases() {
		if err := ValidateDiscoveryBody(c.item.Component, c.item.Payload); err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
	}
}

func readAlarmGolden(t *testing.T) map[string]goldenEntry {
	t.Helper()
	raw, err := os.ReadFile(alarmGoldenPath)
	if err != nil {
		t.Fatalf("read %s: %v (create it with -update-alarm-golden)", alarmGoldenPath, err)
	}
	var out map[string]goldenEntry
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("parse %s: %v", alarmGoldenPath, err)
	}
	return out
}

func writeAlarmGolden(t *testing.T, entries map[string]goldenEntry) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(alarmGoldenPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	raw, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(alarmGoldenPath, append(raw, '\n'), 0o600); err != nil {
		t.Fatalf("write %s: %v", alarmGoldenPath, err)
	}
}
