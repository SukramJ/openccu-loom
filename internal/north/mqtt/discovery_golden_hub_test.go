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

	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

var updateHubGolden = flag.Bool("update-hub-golden", false,
	"rewrite the pinned hub discovery payloads")

var hubGoldenPath = filepath.Join("testdata", "discovery_golden_hub.json")

// hubGoldenBase is the topic base every fixture publishes under. It is the
// same short base the other pins in this package use, so the files read
// against each other.
const hubGoldenBase = "gh"

// The two centrals the fixtures are built for, with the serials their hub
// unique_ids are scoped by. Two are needed because the hub plane's identity
// is central-scoped in two independent places — the node id (a slug of the
// central NAME) and the unique_id (the last ten characters of the central's
// SERIAL) — and a pin on a single central cannot tell a drift in one from a
// drift in the other.
const (
	hubGoldenCentral       = "ccu-01"
	hubGoldenCentralSerial = "3014F711A0001234"
	hubGoldenCentral2      = "ccu-02"
	hubGoldenCentral2Seria = "3014F711B0005678"
)

// hubGoldenCase is one built discovery item plus the name a diff reports.
type hubGoldenCase struct {
	name string
	item DiscoveryItem
}

// newHubGoldenBuilder is the builder the fixtures are built from: both
// centrals registered, because [DefaultDiscoveryBuilder.hubSerial] refuses
// to emit anything for a central whose serial it does not know, and the
// second-central fixtures would then silently pin nothing.
func newHubGoldenBuilder() *DefaultDiscoveryBuilder {
	db := NewDefaultDiscoveryBuilder(NewTopicBuilder(hubGoldenBase), hubGoldenCentral)
	db.SetHubInfoFor(hubGoldenCentral, HubInfo{
		Serial:  hubGoldenCentralSerial,
		Version: "3.79.6",
		URL:     "http://ccu-01.local",
	})
	db.SetHubInfoFor(hubGoldenCentral2, HubInfo{Serial: hubGoldenCentral2Seria})
	return db
}

func hubGoldenFloat(v float64) *float64 { return &v }

// hubGoldenSysvarCases pins the sysvar builder, whose component selection is
// a two-axis switch — the CCU value type crossed with the extended-and-
// writable marker — plus two post-processing passes (the auto-counter
// classification and the number min/max fallback). Every branch that
// resolves to a different payload gets exactly one case.
//
//nolint:funlen // one fixture per sysvar branch; splitting it hides the matrix
func hubGoldenSysvarCases(db *DefaultDiscoveryBuilder) []hubGoldenCase {
	sv := func(name string, vid int, vt hmenum.HubValueType) HubSysvarSpec {
		return HubSysvarSpec{Name: name, Vid: vid, ValueType: vt, EnabledDefault: true}
	}
	extended := func(s HubSysvarSpec) HubSysvarSpec {
		s.IsExtended, s.Writable = true, true
		return s
	}
	build := func(s HubSysvarSpec) DiscoveryItem {
		return db.BuildSysvarDiscovery(hubGoldenCentral, s)
	}

	listValues := []string{"Aus", "Anwesend", "Abwesend"}

	return []hubGoldenCase{
		// LOGIC, both halves of the editable gate. The gate is keyed on the
		// extended marker, not on writability, and the two halves are
		// different HA platforms — so the component segment of the topic
		// moves with it, not just a key in the body.
		{"sysvar/logic-switch", build(extended(sv("Anwesenheit", 4711, hmenum.HubValueTypeLogic)))},
		{"sysvar/logic-binary-sensor", build(sv("Fenster offen", 4712, hmenum.HubValueTypeLogic))},
		// ALARM read-only is the one branch that adds a device_class.
		{"sysvar/alarm-binary-sensor", build(sv("Alarmzone", 4713, hmenum.HubValueTypeAlarm))},
		// LIST: select when extended AND non-empty, sensor with enum options
		// otherwise. The empty-list case is pinned too because it is the
		// branch where "extended" does not win.
		{"sysvar/list-select", func() DiscoveryItem {
			s := extended(sv("Betriebsart", 4714, hmenum.HubValueTypeList))
			s.ValueList = listValues
			return build(s)
		}()},
		{"sysvar/list-sensor-enum", func() DiscoveryItem {
			s := sv("Betriebsart Anzeige", 4715, hmenum.HubValueTypeList)
			s.ValueList = listValues
			return build(s)
		}()},
		{"sysvar/list-extended-without-options", build(extended(sv("Leere Liste", 4716, hmenum.HubValueTypeList)))},
		{"sysvar/string-text", build(extended(sv("Notiz", 4717, hmenum.HubValueTypeString)))},
		{"sysvar/string-sensor", build(sv("AlleServicemeldungen", 4718, hmenum.HubValueTypeString))},
		// FLOAT extended with no declared bounds: pins the ±1e9 fallback
		// range, which exists because HA silently defaults a number entity
		// to 0–100 and rejects every real reading above it.
		{"sysvar/float-number-fallback-range", func() DiscoveryItem {
			s := extended(sv("Sollwert", 4719, hmenum.HubValueTypeFloat))
			s.Unit = "°C"
			return build(s)
		}()},
		// INTEGER extended with declared bounds: the other number shape
		// (mode box, step 1) and the branch where the declared min/max win.
		{"sysvar/integer-number-bounded", func() DiscoveryItem {
			s := extended(sv("Helligkeit", 4720, hmenum.HubValueTypeInteger))
			s.Min, s.Max, s.Unit = hubGoldenFloat(0), hubGoldenFloat(255), "lx"
			return build(s)
		}()},
		{"sysvar/number-sensor", func() DiscoveryItem {
			s := sv("Aussentemperatur", 4721, hmenum.HubValueTypeNumber)
			s.Unit = "°C"
			s.Description = "Außentemperatur"
			return build(s)
		}()},
		// An unrecognised value type falls through to a plain sensor rather
		// than dropping the entity.
		{"sysvar/unknown-type-sensor", build(sv("Unbekannt", 4722, hmenum.HubValueType("WHATEVER")))},
		// A CCU-auto-generated counter: the name is a machine token, and the
		// classification pass overwrites name, state_class, device_class and
		// unit while leaving the unique_id alone. That split is the whole
		// point — the friendly name may move, the identity may not.
		{"sysvar/auto-energy-counter", build(sv("svEnergyCounter_1234_ABC0123456:6", 4723, hmenum.HubValueTypeFloat))},

		// ---- identity hazards ----

		// Absent entity-id seed: Vid == 0 is a sysvar whose CCU id has not
		// been resolved, and the unique_id then falls back to a slug of the
		// NAME. That fallback is a rename hazard by construction, so it is
		// pinned rather than left to be discovered after a fleet moves.
		{"sysvar/hazard-unresolved-vid", build(sv("Nicht aufgelöste Variable", 0, hmenum.HubValueTypeLogic))},
		// Two names that differ only in punctuation — the exact pair
		// [sysvarUniqueID] names as the collision it was written to fix.
		// The unique_ids are indeed distinct now, because each carries its
		// own Vid. The object ids are NOT: both slug to "alarm_kueche", so
		// the two configs land on one retained discovery topic. The pin
		// records that as the current fact rather than asserting it is
		// right; changing it is a behaviour change and belongs in its own
		// commit.
		{"sysvar/hazard-punctuation-twin-a", build(sv("Alarm: Küche", 4730, hmenum.HubValueTypeLogic))},
		{"sysvar/hazard-punctuation-twin-b", build(sv("Alarm Küche", 4731, hmenum.HubValueTypeLogic))},
		// A node id that is NOT a device identifier, made visible: this
		// sysvar's device block points at a physical device card, while its
		// node id stays the central-scoped "ccu-01_sysvars". The two are
		// independent, and a migration that "helpfully" aligned the node id
		// with the device would move the retained config topic of every
		// device-linked sysvar at once.
		{"sysvar/hazard-linked-to-device", func() DiscoveryItem {
			s := sv("Wohnzimmer Heizung Soll", 4732, hmenum.HubValueTypeFloat)
			s.DeviceAddress = "ABC0123456"
			return build(s)
		}()},
		// The same link, but to a CHANNEL address — the sub-device-shaped
		// case. Production cannot currently reach it: the model's
		// DeviceAddress() truncates at the colon, so this builder only ever
		// sees a device address. It is pinned as the boundary, because the
		// builder itself does NOT truncate: the colon survives verbatim into
		// the HA device identifier, which matches no card the per-device
		// plane publishes, so the entity would land on a nameless phantom
		// device. If the upstream truncation is ever lost in the move, this
		// fixture is where it shows rather than on a fleet.
		{"sysvar/hazard-linked-to-channel", func() DiscoveryItem {
			s := sv("Wohnzimmer Heizung Ventil", 4733, hmenum.HubValueTypeFloat)
			s.DeviceAddress = "ABC0123456:4"
			return build(s)
		}()},
		// A central-scoped address: INT0000001 repeats verbatim on every
		// CCU, so its device identifier must carry the central slug while a
		// real serial's must not. Both halves matter — see the
		// second-central fixtures for the counterpart.
		{"sysvar/hazard-linked-to-internal-device", func() DiscoveryItem {
			s := sv("Interner Zähler", 4734, hmenum.HubValueTypeInteger)
			s.DeviceAddress = "INT0000001"
			return build(s)
		}()},
		// Same sysvar, second central. The node id follows the central NAME
		// and the unique_id follows the central SERIAL; this fixture is the
		// only thing that would catch one of the two quietly dropping out.
		{"sysvar/hazard-second-central", db.BuildSysvarDiscovery(hubGoldenCentral2,
			sv("Anwesenheit", 4711, hmenum.HubValueTypeLogic))},
	}
}

// hubGoldenProgramCases pins the program builders. A program is two
// controls — the activity switch and the execute button — and the roles are
// declared by the model, not by this package, so the fixtures ask the model
// for them rather than restating them.
func hubGoldenProgramCases(db *DefaultDiscoveryBuilder) []hubGoldenCase {
	spec := HubProgramSpec{ID: "3721", Name: "Gute Nacht", EnabledDefault: true}
	roles := hub.NewProgram(hubGoldenCentral, spec.ID, spec.Name, "", false, nil).
		MQTTRoles(hubGoldenBase, hubGoldenCentral)
	built := db.BuildProgramDiscoveryRoles(hubGoldenCentral, spec, roles)
	out := make([]hubGoldenCase, 0, len(built)+3)
	// Role order is the model's, and it is part of the pin: the principal
	// role is the one that keeps the identity the program always had, so a
	// reordering that made the button principal would re-key both entities.
	for i, item := range built {
		name := "program/role-principal-switch"
		if i > 0 {
			name = "program/role-execute-button"
		}
		out = append(out, hubGoldenCase{name, item})
	}
	return append(
		out,
		// The no-roles fallback, still reachable for any source that declares
		// none. It is a third payload shape, not a duplicate of the switch
		// role: it names the trigger topic as its command topic.
		hubGoldenCase{
			"program/legacy-single-switch",
			db.BuildProgramDiscovery(hubGoldenCentral, spec),
		},
		// Absent display name: the ID stands in. An operator sees the raw CCU
		// id as the entity name, and — because default_entity_id is derived
		// from the unique_id, not the name — the entity id does NOT follow
		// it. Pinned so that separation stays visible.
		hubGoldenCase{
			"program/hazard-unnamed",
			db.BuildProgramDiscovery(hubGoldenCentral, HubProgramSpec{ID: "3722"}),
		},
		// Second central: same program id, different serial, different node id.
		hubGoldenCase{
			"program/hazard-second-central",
			db.BuildProgramDiscovery(hubGoldenCentral2, spec),
		},
	)
}

// hubGoldenSingletonCases pins the one-per-central entities: the three
// message aggregates, the per-interface install-mode pair and connectivity,
// the daemon status, the three system metrics and the hub update.
func hubGoldenSingletonCases(db *DefaultDiscoveryBuilder) []hubGoldenCase {
	return []hubGoldenCase{
		{"aggregate/alarm-messages", db.BuildAlarmMessagesDiscovery(hubGoldenCentral)},
		{"aggregate/service-messages", db.BuildServiceMessagesDiscovery(hubGoldenCentral)},
		{"aggregate/inbox", db.BuildInboxDiscovery(hubGoldenCentral)},

		// Install mode is one sensor plus one button per interface, and the
		// interface reaches the identity through a SUFFIX table rather than
		// verbatim: HmIP-RF becomes "hmip", BidCos-RF becomes "bidcos", and
		// anything else is slugged. All three arms are pinned because the
		// table is what keeps two interfaces from sharing one unique_id.
		{"install-mode/sensor-hmip", db.BuildInstallModeSensorDiscovery(hubGoldenCentral, "HmIP-RF")},
		{"install-mode/button-hmip", db.BuildInstallModeButtonDiscovery(hubGoldenCentral, "HmIP-RF")},
		{"install-mode/sensor-bidcos", db.BuildInstallModeSensorDiscovery(hubGoldenCentral, "BidCos-RF")},
		{"install-mode/button-bidcos", db.BuildInstallModeButtonDiscovery(hubGoldenCentral, "BidCos-RF")},
		// The default arm of that table. BidCos-Wired is a real pairing-
		// capable interface with no short label, so it must slug to its own
		// slot instead of falling into the "bidcos" bucket — if it ever did,
		// the two interfaces would share one retained config topic and one
		// unique_id, and only the payload published last would survive.
		{"install-mode/sensor-bidcos-wired", db.BuildInstallModeSensorDiscovery(hubGoldenCentral, "BidCos-Wired")},
		{"install-mode/button-bidcos-wired", db.BuildInstallModeButtonDiscovery(hubGoldenCentral, "BidCos-Wired")},

		{"connectivity/hmip", db.BuildConnectivityDiscovery(hubGoldenCentral, "HmIP-RF")},
		{"connectivity/bidcos", db.BuildConnectivityDiscovery(hubGoldenCentral, "BidCos-RF")},

		// The daemon-status sensor is the one hub entity that deliberately
		// carries NO availability block, because its state topic IS the
		// availability topic. The absence is the feature, and an absence is
		// exactly what a body-only review overlooks.
		{"system/daemon-status", db.BuildDaemonStatusDiscovery(hubGoldenCentral)},
		{"system/health", db.BuildSystemHealthDiscovery(hubGoldenCentral)},
		{"system/connection-latency", db.BuildConnectionLatencyDiscovery(hubGoldenCentral)},
		{"system/last-event-age", db.BuildLastEventAgeDiscovery(hubGoldenCentral)},
		{"system/hub-update", db.BuildHubUpdateDiscovery(hubGoldenCentral)},

		// Second central, across the three node ids the singletons live
		// under ("messages", "central", "system") plus connectivity. The hub
		// device block differs too: ccu-02 registered a serial and nothing
		// else, so sw_version and configuration_url must be absent rather
		// than empty strings — HA renders an empty string, and "Unknown" is
		// the honest reading.
		{"aggregate/hazard-second-central-inbox", db.BuildInboxDiscovery(hubGoldenCentral2)},
		{"install-mode/hazard-second-central-sensor", db.BuildInstallModeSensorDiscovery(hubGoldenCentral2, "HmIP-RF")},
		{"connectivity/hazard-second-central", db.BuildConnectivityDiscovery(hubGoldenCentral2, "HmIP-RF")},
		{"system/hazard-second-central-health", db.BuildSystemHealthDiscovery(hubGoldenCentral2)},
	}
}

// hubGoldenCases is the fixture matrix for the hub plane: one case per
// payload shape the builders in hub_discovery.go resolve differently, plus
// one case per identity hazard the plane carries.
//
// It is not a fleet. A real installation has hundreds of sysvars and
// programs; the claim here is only that each SHAPE and each identity rule
// is represented once.
func hubGoldenCases() []hubGoldenCase {
	db := newHubGoldenBuilder()
	out := hubGoldenSysvarCases(db)
	out = append(out, hubGoldenProgramCases(db)...)
	out = append(out, hubGoldenSingletonCases(db)...)
	return out
}

// TestHubDiscoveryPayloadsArePinned pins what the hub plane puts on the
// wire, so moving it onto the shared model (ADR 0070) can be shown to
// change nothing an installed Home Assistant sees.
//
// Topic and payload are both pinned, and the topic is not decoration here.
// This plane's node ids are central-scoped tokens — `<central>_sysvars`,
// `<central>_programs`, `<central>_messages`, `<central>_central`,
// `<central>_system`, `<central>_connectivity` — and NOT device
// identifiers, even for the entities whose `device` block points at a real
// CCU device. Several of these entities hang off a synthetic device
// (`openccu-loom_central_<central>`) that no hardware corresponds to. So a
// changed node id, a changed object id, a changed `unique_id` and a changed
// `default_entity_id` are four separate ways to orphan an installed entity,
// three of them invisible in the body, all four silent in Home Assistant:
// the old retained config stays where it was, a second entity appears
// beside it, and nothing is logged.
//
// Scope, plainly. This pins what the builders produce, not what reaches a
// broker: retain flags, QoS, publish ordering, the orphan sweep and the
// gate that skips a central whose serial is not yet known are all outside
// it. The localized names are pinned in the catalogue's default locale, so
// a translation change is out of scope — as is the origin block's version,
// which other tests in this package set and restore. And it is a per-shape
// pin, not a fleet capture: the sysvar and program counts of a real
// installation say nothing here. The operator-side capture of a real
// fleet's retained configs before and after remains the stronger tier.
//
// A diff is a regression until someone shows otherwise. Refresh with:
//
//	go test ./internal/north/mqtt/ -run TestHubDiscoveryPayloadsArePinned -update-hub-golden
func TestHubDiscoveryPayloadsArePinned(t *testing.T) {
	// Not parallel: every payload carries an `origin` block read from the
	// package-global origin version, which other tests set and restore.
	tb := NewTopicBuilder(hubGoldenBase)

	got := map[string]goldenEntry{}
	for _, c := range hubGoldenCases() {
		if !c.item.OK {
			t.Fatalf("%s: builder returned OK=false — the fixture no longer produces an entity", c.name)
		}
		if _, dup := got[c.name]; dup {
			t.Fatalf("%s: duplicate fixture name — one case would hide the other", c.name)
		}
		// Decoded to a map so the pin is stable against field order in the
		// builder and unstable against a changed key or value — which is
		// exactly the sensitivity wanted.
		var body map[string]any
		if err := json.Unmarshal(c.item.Payload, &body); err != nil {
			t.Fatalf("%s: unmarshal: %v", c.name, err)
		}
		got[c.name] = goldenEntry{
			Topic:   tb.DiscoveryConfig(c.item.Component, c.item.NodeID, c.item.ObjectID),
			Payload: body,
		}
	}

	if *updateHubGolden {
		writeHubGolden(t, got)
		t.Logf("rewrote %s with %d payloads", hubGoldenPath, len(got))
		return
	}

	want := readHubGolden(t)
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

// TestHubDiscoveryPinnedPayloadsAreValid reads the same fixtures back
// through the extracted Home Assistant MQTT schema, so a payload that is
// pinned is also a payload HA accepts. A pin on its own only says the bytes
// did not move; it cannot say they were right to begin with.
func TestHubDiscoveryPinnedPayloadsAreValid(t *testing.T) {
	for _, c := range hubGoldenCases() {
		if err := ValidateDiscoveryBody(c.item.Component, c.item.Payload); err != nil {
			t.Errorf("%s: %v", c.name, err)
		}
	}
}

func readHubGolden(t *testing.T) map[string]goldenEntry {
	t.Helper()
	raw, err := os.ReadFile(hubGoldenPath)
	if err != nil {
		t.Fatalf("read %s: %v (create it with -update-hub-golden)", hubGoldenPath, err)
	}
	var out map[string]goldenEntry
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("parse %s: %v", hubGoldenPath, err)
	}
	return out
}

func writeHubGolden(t *testing.T, entries map[string]goldenEntry) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(hubGoldenPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	raw, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(hubGoldenPath, append(raw, '\n'), 0o600); err != nil {
		t.Fatalf("write %s: %v", hubGoldenPath, err)
	}
}
