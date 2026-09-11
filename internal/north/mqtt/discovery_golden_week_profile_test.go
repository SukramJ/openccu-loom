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

var updateWeekProfileGolden = flag.Bool("update-week-profile-golden", false,
	"rewrite the pinned week-profile discovery payloads")

var weekProfileGoldenPath = filepath.Join("testdata", "discovery_golden_week_profile.json")

// wpGoldenDescriptor is the [WeekProfileDescriptor] the fixtures drive
// [DefaultDiscoveryBuilder.BuildWeekProfileDiscovery] with. It carries the
// two reads the builder actually makes — the profile list that becomes
// `options`, and the legacy identifier the object id is derived from — so a
// fixture can select a profile count or an identifier spelling without
// standing up a real weekprofile.ProfileDataPoint and the CCU paramset
// harvest behind it.
//
// UniqueID is spelled the way [weekprofile.ProfileDataPoint] spells it,
// "<central>:<channelAddress>:WEEKPROFILE", because the object id — and
// therefore the discovery topic and the entity-id seed — is a pure function
// of that string.
type wpGoldenDescriptor struct {
	uniqueID string
	profiles []string
}

func (d wpGoldenDescriptor) UniqueID() string            { return d.uniqueID }
func (d wpGoldenDescriptor) AvailableProfiles() []string { return d.profiles }
func (d wpGoldenDescriptor) CurrentProfile() string      { return "P1" }
func (d wpGoldenDescriptor) OnChange(func()) func()      { return func() {} }

// wpGoldenParent wraps a real device and additionally reports the multi-group
// structure that switches [deviceDescriptor] onto its sub-device branch on
// every other plane. It exists to pin that the branch does NOT switch here —
// see the "sub-device-enabled" fixture.
type wpGoldenParent struct {
	*device.Device
}

func (wpGoldenParent) HasSubDevices() bool { return true }

// wpGoldenThermostat is the HmIP wall thermostat as the model hands it to the
// discovery path: a real device object, so `model`, `name` and the
// manufacturer in the device block come from the same harvest production
// uses rather than from a literal in this file.
func wpGoldenThermostat() *device.Device {
	return device.New(device.Config{
		InterfaceID:  "HmIP-RF",
		Interface:    hmenum.InterfaceHmIPRF,
		Address:      "VCU1234567",
		Model:        "HmIP-eTRV-2",
		Name:         "Heizung Wohnzimmer",
		Manufacturer: hmenum.ManufacturerEQ3,
	})
}

// wpGoldenCase is one input to the week-profile builder, named so a diff says
// which shape moved rather than only which topic.
type wpGoldenCase struct {
	name string
	ev   WeekProfileEvent
}

// wpGoldenCases is the fixture matrix for the week-profile plane.
//
// The plane emits exactly one component — a `select` — per climate channel
// that reports a profile list, so the matrix is not one case per component
// but one case per way that single shape resolves differently: the `options`
// list, the device block, and the three identifiers, which this plane derives
// three DIFFERENT ways from the same event and therefore cannot be reasoned
// about from one fixture:
//
//   - unique_id comes from [DefaultDiscoveryBuilder.scopedUniqueID] over the
//     composed channel address, so it carries no central,
//   - node_id comes from [discoveryNodeID], so it carries a slugged central,
//   - object_id comes from the descriptor's own legacy identifier, so it
//     carries the central verbatim, lower-cased, with only ":" folded.
//
// It is not a fleet. A real CCU's ~9,996 retained configs cannot be
// reconstructed in CI; this pins what CI can reproduce, and the scope note on
// TestWeekProfilePayloadsArePinned says what it leaves out.
func wpGoldenCases() []wpGoldenCase {
	// The production shape: an HmIP thermostat, which exposes six profile
	// slots. The full `options` list is pinned rather than its length —
	// Home Assistant stores the selected option as the entity's state, so
	// dropping or renaming a key strands whatever state a user already has.
	hmip := WeekProfileEvent{
		Central:       "ccu-01",
		Interface:     "HmIP-RF",
		DeviceAddress: "VCU1234567",
		ChannelNo:     1,
		DeviceName:    "Heizung Wohnzimmer",
		Model:         "HmIP-eTRV-2",
		Device:        wpGoldenThermostat(),
		WP: wpGoldenDescriptor{
			uniqueID: "ccu-01:VCU1234567:1:WEEKPROFILE",
			profiles: []string{"P1", "P2", "P3", "P4", "P5", "P6"},
		},
	}

	// A BidCos-RF thermostat, which exposes three profile slots instead of
	// six, on a channel above one. Both move: the `options` list, and the
	// channel number, which appears in the state and command topics, in the
	// unique id and in the object id. Pinned as a second device family
	// because a change that normalised the profile count would be invisible
	// against the HmIP fixture alone.
	bidcos := WeekProfileEvent{
		Central:       "ccu-01",
		Interface:     "BidCos-RF",
		DeviceAddress: "LEQ0123456",
		ChannelNo:     4,
		DeviceName:    "Heizung Bad",
		Model:         "HM-CC-RT-DN",
		Device: device.New(device.Config{
			InterfaceID:  "BidCos-RF",
			Interface:    hmenum.InterfaceBidCosRF,
			Address:      "LEQ0123456",
			Model:        "HM-CC-RT-DN",
			Name:         "Heizung Bad",
			Manufacturer: hmenum.ManufacturerEQ3,
		}),
		WP: wpGoldenDescriptor{
			uniqueID: "ccu-01:LEQ0123456:4:WEEKPROFILE",
			profiles: []string{"P1", "P2", "P3"},
		},
	}

	// No device object. WeekProfileEvent.Device is documented as optional,
	// and the caller that omits it gets a device block assembled from the
	// event's own DeviceName / Model instead of from the model harvest.
	// Pinned because the two paths must agree: an entity whose device block
	// identifies differently from its siblings' lands on a second device
	// card beside the real one.
	noDevice := hmip
	noDevice.Device = nil

	// Sub-device split enabled. On every other plane a device that reports
	// sub-devices re-homes the entity onto "<parent>-<group>". It cannot
	// here: [deviceDescriptor] needs a channel inspector and WeekProfileEvent
	// carries none, so the device block stays the parent's. That outcome is
	// pinned deliberately — it is the plane's identity hazard, and a
	// migration that "fixed" it in passing would silently move every
	// week-profile entity on every multi-group thermostat to a different
	// device card.
	subDevice := hmip
	subDevice.Device = wpGoldenParent{Device: wpGoldenThermostat()}

	// The same device on a second central. Three identifiers, three
	// behaviours, all in one payload: the node id is central-scoped and MUST
	// change; the object id embeds the central verbatim and also changes; the
	// unique id is derived from the device address, which is globally unique,
	// and MUST NOT change. Home Assistant keys its entity registry on
	// unique_id, so scoping that one too would re-key every week-profile
	// entity on every fleet at once.
	secondCentral := hmip
	secondCentral.Central = "ccu-02"
	secondCentral.WP = wpGoldenDescriptor{
		uniqueID: "ccu-02:VCU1234567:1:WEEKPROFILE",
		profiles: []string{"P1", "P2", "P3", "P4", "P5", "P6"},
	}

	// A central named with capitals. [hmtypes.ValidateCentralName] holds every
	// central name to [A-Za-z0-9_-]+, so case is the only escaping question a
	// real name can raise — and it raises it three times over: the node id is
	// case-folded by [naming.DiscoverySlug], the object id is case-folded by
	// this plane's own strings.ToLower, and the state / command / availability
	// topics carry the name verbatim. Every other fixture here uses a
	// lower-case central and therefore cannot tell the three apart; a pin that
	// could not tell them apart would bless a migration that swapped one for
	// another.
	mixedCaseCentral := hmip
	mixedCaseCentral.Central = "CCU-Keller"
	mixedCaseCentral.WP = wpGoldenDescriptor{
		uniqueID: "CCU-Keller:VCU1234567:1:WEEKPROFILE",
		profiles: []string{"P1", "P2", "P3", "P4", "P5", "P6"},
	}

	return []wpGoldenCase{
		{"week_profile/hmip-six-profiles", hmip},
		{"week_profile/bidcos-three-profiles", bidcos},
		{"week_profile/no-device-object", noDevice},
		{"week_profile/sub-devices-enabled", subDevice},
		{"week_profile/second-central", secondCentral},
		{"week_profile/mixed-case-central", mixedCaseCentral},
	}
}

// TestWeekProfilePayloadsArePinned pins the topic and the payload of every
// entity shape discovery_week_profile.go produces, so the move of this
// daemon's discovery layer onto the shared model (ADR 0070) can be checked
// against the one criterion that matters: the bytes do not change.
//
// The week-profile `select` is the only Home Assistant surface through which
// a thermostat's active weekly program can be read or switched, so a silent
// change here does not degrade a control — it removes it.
//
// One caveat belongs up front, because it bounds what this pin is worth
// today: nothing outside this package calls [Bridge.PublishWeekProfileDiscovery]
// or [DefaultDiscoveryBuilder.BuildWeekProfileDiscovery]. No fleet has these
// bytes retained yet, so a diff here is not (yet) an orphaned entity on a real
// CCU. It is still the contract the plane will be wired to, and pinning it
// before the move costs nothing and later cannot be reconstructed.
//
// The topic is pinned alongside the body deliberately. Two of the three ways
// this move can break are invisible in the body: a changed node id and a
// changed object id both publish the new config on a new topic. Home
// Assistant reports none of the three — the old entity is orphaned and a new
// one appears beside it, with nothing in any log. This plane is unusually
// exposed to that, because it derives its three identifiers three different
// ways from the same event (see the note on wpGoldenCases).
//
// The payload is stored decoded so the file stays reviewable; comparison is
// on the canonical re-encoding of both sides, exact for every key and value,
// with only whitespace — which no consumer sees — out of scope.
//
// Scope. This pins what CI can reproduce: one case per way the single
// week-profile shape resolves differently. It does NOT reproduce a fleet —
// the real device catalogue and its ~9,996 retained configs live on a CCU,
// and a pin that looked complete would be worse than one that admits it is
// not. It does not pin the cases that emit nothing (a nil descriptor, an
// empty profile list on a non-climate channel, an unresolvable unique id);
// those are covered in discovery_week_profile_test.go. It does not pin the
// state or command traffic on the topics the payload names, only the two
// topic strings themselves. And it does not pin the defensive branch that
// leaves the object id alone when the descriptor's identifier already starts
// with "openccu-loom_": no production identifier does, and an invented one
// would pin a payload no CCU emits.
//
// A diff here is a regression until someone shows otherwise. Refresh with:
//
//	go test ./internal/north/mqtt/ -run TestWeekProfilePayloadsArePinned -update-week-profile-golden
func TestWeekProfilePayloadsArePinned(t *testing.T) {
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
	for _, c := range wpGoldenCases() {
		item := db.BuildWeekProfileDiscovery(c.ev.Central, c.ev)
		if !item.OK {
			t.Fatalf("%s: BuildWeekProfileDiscovery returned OK=false — the fixture no longer produces an entity", c.name)
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

	if *updateWeekProfileGolden {
		writeWeekProfileGolden(t, got)
		t.Logf("rewrote %s with %d payloads", weekProfileGoldenPath, len(got))
		return
	}

	want := readWeekProfileGolden(t)
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

func readWeekProfileGolden(t *testing.T) map[string]goldenEntry {
	t.Helper()
	raw, err := os.ReadFile(weekProfileGoldenPath)
	if err != nil {
		t.Fatalf("read %s: %v (create it with -update-week-profile-golden)", weekProfileGoldenPath, err)
	}
	var out map[string]goldenEntry
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("parse %s: %v", weekProfileGoldenPath, err)
	}
	return out
}

func writeWeekProfileGolden(t *testing.T, entries map[string]goldenEntry) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(weekProfileGoldenPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	raw, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(weekProfileGoldenPath, append(raw, '\n'), 0o600); err != nil {
		t.Fatalf("write %s: %v", weekProfileGoldenPath, err)
	}
}
