// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// device_profile_catalogue_test.go holds the invariants of the
// hand-maintained device-profile catalogue (ADR 0063).
//
// It replaces the pins that compared the catalogue against the reference
// implementation it was generated from. Those asked "is our copy still
// their copy?" — a question that stopped having an answer the moment the
// catalogue became ours to correct. What stays checkable is that the
// catalogue is internally coherent: every profile resolves to a schema,
// every schema is reached, every mapping names a real parameter, and
// every device type is in the form the lookup normalises to.
//
// Each of these fails on a plausible hand edit. That is the bar a guard
// over hand-maintained data has to clear: a count pin does not, because
// the only way to make it fail is to add or remove an entry, which is
// exactly what maintaining the catalogue means.
package contract

import (
	"strconv"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// allRegisteredProfiles flattens the default registry into one slice.
// The registry exposes lookups rather than a dump, and adding an
// exported All() for a test alone would put an export into the tree
// that production never calls.
func allRegisteredProfiles() []custom.Profile {
	r := custom.DefaultRegistry()
	deviceTypes := r.DeviceTypes()
	out := make([]custom.Profile, 0, len(deviceTypes))
	for _, dt := range deviceTypes {
		out = append(out, r.ForDevice(dt)...)
	}
	return out
}

// TestEveryProfileResolvesToAChannelGroupSchema asserts that no profile
// registration carries a nil Config.
//
// A profile without a schema is skipped by the materializer without a
// word (`profile.Config == nil` → return nil in CreateCustomDataPoint),
// so every device carrying it silently gets no custom data point at all.
func TestEveryProfileResolvesToAChannelGroupSchema(t *testing.T) {
	t.Parallel()
	profiles := allRegisteredProfiles()
	if len(profiles) == 0 {
		t.Fatal("registry is empty — the walk is broken and this test would pass vacuously")
	}
	for _, p := range profiles {
		if p.Config == nil {
			t.Errorf("profile %s (device type %q) has no ProfileConfig — every device carrying it materialises nothing",
				p.Name, p.DeviceType)
		}
	}
}

// schemasWithoutAProfile names a channel-group schema that no profile
// registration points at, with the reason it is nevertheless kept.
//
// An entry is a claim that the schema is deliberately unused — not that
// nobody has looked. The guard reports a listed-but-referenced schema as
// an error too, so an entry cannot outlive its reason.
var schemasWithoutAProfile = map[hmenum.DeviceProfile]string{
	"RfSwitch": "inherited orphan: the reference implementation defines the same " +
		"schema and maps no device to it either. Classic RF switches surface their " +
		"STATE as a plain generic data point, so no custom data point composes this " +
		"schema. Kept because the constructor exists and a future classic switch that " +
		"does need composition would use it.",
	"IPSimpleFixedColorLight": "inherited orphan: only the wired variant " +
		"(IPSimpleFixedColorLightWired) has device registrations, in this catalogue and " +
		"in the reference implementation. Kept as the wireless counterpart of a shape " +
		"that is otherwise identical.",
}

// TestEveryChannelGroupSchemaIsReferenced asserts that no schema sits in
// the catalogue unreferenced without saying so.
//
// An orphan is dead weight at best. At worst it is a rename that landed
// on one side only — the profiles now point at a schema that still
// carries the old shape, and the intended one is never read.
func TestEveryChannelGroupSchemaIsReferenced(t *testing.T) {
	t.Parallel()
	referenced := make(map[hmenum.DeviceProfile]struct{})
	for _, p := range allRegisteredProfiles() {
		if p.Config != nil {
			referenced[p.Config.ProfileType] = struct{}{}
		}
	}
	if len(referenced) == 0 {
		t.Fatal("no schema is referenced by any profile — the walk is broken and this test would pass vacuously")
	}
	for name := range custom.ProfileConfigs {
		_, isReferenced := referenced[name]
		reason, declared := schemasWithoutAProfile[name]
		switch {
		case !isReferenced && !declared:
			t.Errorf("ProfileConfigs[%s] is referenced by no profile registration — point a "+
				"registration at it, drop it, or declare it in schemasWithoutAProfile with the reason", name)
		case isReferenced && declared:
			t.Errorf("ProfileConfigs[%s] is declared unused (%q) but a profile does reference it — "+
				"drop the entry so the list keeps meaning what it says", name, reason)
		}
	}
	for name := range schemasWithoutAProfile {
		if _, ok := custom.ProfileConfigs[name]; !ok {
			t.Errorf("schemasWithoutAProfile names %s, which is not in the catalogue any more — "+
				"a stale entry exempts nothing and hides the next real orphan", name)
		}
	}
}

// TestEveryFieldMappingNamesAParameter asserts that every field mapping
// in every schema carries a non-empty wire parameter.
//
// An empty parameter resolves to nothing on every device, and it reads
// in the source like a deliberate blank rather than the typo it is.
func TestEveryFieldMappingNamesAParameter(t *testing.T) {
	t.Parallel()
	checked := 0
	for name, cfg := range custom.ProfileConfigs {
		if cfg == nil {
			t.Errorf("ProfileConfigs[%s] is nil", name)
			continue
		}
		check := func(where string, fields map[hmenum.Field]custom.FieldValue) {
			for field, fv := range fields {
				checked++
				if parameter, _ := custom.ResolveFieldValue(fv); parameter == "" {
					t.Errorf("%s %s %s maps to an empty parameter", name, where, field)
				}
			}
		}
		check("Fields", cfg.ChannelGroup.Fields)
		for chNo, fields := range cfg.ChannelGroup.ChannelFields {
			check(channelLabel("ChannelFields", chNo), fields)
		}
		for chNo, fields := range cfg.ChannelGroup.FixedChannelFields {
			check(channelLabel("FixedChannelFields", chNo), fields)
		}
	}
	if checked == 0 {
		t.Fatal("no field mapping found — the walk is broken and this test would pass vacuously")
	}
	t.Logf("checked %d field mappings across %d schemas", checked, len(custom.ProfileConfigs))
}

// TestDeviceTypesAreNormalised asserts that every registration's device
// type is already in the form the registry normalises lookups to.
//
// `Registry.Register` normalises what it stores, so a mixed-case entry
// still resolves — but the catalogue is read by humans and diffed
// against the CCU's own model strings, and one entry in a different
// shape is what makes the next reader believe the shape does not matter.
func TestDeviceTypesAreNormalised(t *testing.T) {
	t.Parallel()
	for _, p := range allRegisteredProfiles() {
		if p.DeviceType != strings.ToLower(p.DeviceType) {
			t.Errorf("profile %s registers device type %q, which is not lower-cased", p.Name, p.DeviceType)
		}
		if strings.TrimSpace(p.DeviceType) != p.DeviceType {
			t.Errorf("profile %s registers device type %q with surrounding whitespace", p.Name, p.DeviceType)
		}
	}
}

// channelLabel renders a channel-keyed field map's key for a failure
// message, spelling the wildcard offset rather than its numeric value.
func channelLabel(prefix string, chNo int) string {
	if chNo == custom.AnyChannelOffset {
		return prefix + "[any]"
	}
	return prefix + "[" + strconv.Itoa(chNo) + "]"
}
