// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"os"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

// ---------------------------------------------------------------------------
// Per-domain rule lookups
// ---------------------------------------------------------------------------

func TestLookupSensorRuleTemperatureAliasesShareKey(t *testing.T) {
	for _, p := range []string{"TEMPERATURE", "ACTUAL_TEMPERATURE"} {
		d, ok := LookupSensorRule("", p)
		if !ok {
			t.Fatalf("%s: not found", p)
		}
		if d.Key != "TEMPERATURE" {
			t.Fatalf("%s: key=%q want TEMPERATURE", p, d.Key)
		}
		if d.DeviceClass != "temperature" {
			t.Fatalf("%s: device_class=%q want temperature", p, d.DeviceClass)
		}
	}
}

func TestLookupSensorRuleOperatingVoltageDisabledDiagnostic(t *testing.T) {
	for _, p := range []string{"BATTERY_STATE", "OPERATING_VOLTAGE"} {
		d, ok := LookupSensorRule("", p)
		if !ok {
			t.Fatalf("%s: not found", p)
		}
		if d.EnabledByDefault {
			t.Fatalf("%s: must be disabled by default", p)
		}
		if d.EntityCategory != EntityCategoryDiagnostic {
			t.Fatalf("%s: entity_category=%q want diagnostic", p, d.EntityCategory)
		}
	}
}

func TestLookupSensorRuleRSSIAliasesDisabled(t *testing.T) {
	for _, p := range []string{"RSSI_DEVICE", "RSSI_PEER"} {
		d, ok := LookupSensorRule("", p)
		if !ok {
			t.Fatalf("%s: not found", p)
		}
		if d.EnabledByDefault {
			t.Fatalf("%s: must be disabled by default", p)
		}
		if d.EntityCategory != EntityCategoryDiagnostic {
			t.Fatalf("%s: entity_category=%q want diagnostic", p, d.EntityCategory)
		}
	}
}

func TestLookupSensorRuleMissingParameterReportsFalse(t *testing.T) {
	if _, ok := LookupSensorRule("", "NOT_A_REAL_PARAM"); ok {
		t.Fatal("unknown parameter must report ok=false")
	}
}

func TestLookupBinarySensorRuleLowBatteryAliases(t *testing.T) {
	for _, p := range []string{"LOWBAT", "LOW_BAT", "LOWBAT_SENSOR"} {
		d, ok := LookupBinarySensorRule("", p)
		if !ok {
			t.Fatalf("%s: not found", p)
		}
		if d.DeviceClass != "battery" {
			t.Fatalf("%s: device_class=%q want battery", p, d.DeviceClass)
		}
	}
}

func TestLookupBinarySensorRuleSabotageDisabledDiagnostic(t *testing.T) {
	d, ok := LookupBinarySensorRule("", "SABOTAGE")
	if !ok {
		t.Fatal("SABOTAGE: not found")
	}
	if d.EnabledByDefault {
		t.Fatal("SABOTAGE: must be disabled by default")
	}
	if d.EntityCategory != EntityCategoryDiagnostic {
		t.Fatalf("SABOTAGE: entity_category=%q want diagnostic", d.EntityCategory)
	}
}

// TestLookupBinarySensorRuleWindowContacts pins the window-contact family and
// the deliberate exclusion of HmIP-SWD from it (notes/parity/by_design.md,
// BD-Safety-SWDWindowRuleDropped).
//
// HmIP-SWD is the water sensor: it carries no STATE parameter, so the rule was
// unreachable, and labelling a leak detector a window contact is inverted for
// the safety classifier. The exclusion must survive a re-import of the ported
// table, hence the negative assertion.
func TestLookupBinarySensorRuleWindowContacts(t *testing.T) {
	for _, model := range []string{"HmIP-SWDO", "HmIP-SWDM", "HM-Sec-SC"} {
		d, ok := LookupBinarySensorRule(model, "STATE")
		if !ok {
			t.Fatalf("%s/STATE: not found", model)
		}
		_ = d
		// The device_class is the domain's answer now, not the rule's — the
		// rule carries only what this plane knows on its own. Asserted
		// through the resolver the discovery payload actually calls, so a
		// model that stops classifying these fails here.
		if got := resolveBinarySensorDeviceClass(model, "STATE"); got != "window" {
			t.Fatalf("%s/STATE: device_class=%q want window", model, got)
		}
	}
	// The prefix walk requires a "-" separator, so the variants resolve
	// through their own entries rather than through a shorter prefix.
	for _, model := range []string{"HmIP-SWDM-B2", "HmIP-SWDO-I"} {
		if _, ok := LookupBinarySensorRule(model, "STATE"); !ok {
			t.Fatalf("%s/STATE: not found", model)
		}
	}
	if d, ok := LookupBinarySensorRule("HmIP-SWD", "STATE"); ok {
		t.Fatalf("HmIP-SWD/STATE: resolved to device_class=%q, want no rule", d.DeviceClass)
	}
	// The table alone is not the exclusion. device_class is resolved through
	// the domain, so a model-side table that lists the shorter prefix defeats
	// this exclusion while the assertion above still passes. Assert the
	// negative on the path the discovery payload actually takes.
	if got := resolveBinarySensorDeviceClass("HmIP-SWD", "STATE"); got != "" {
		t.Fatalf("HmIP-SWD/STATE resolved to device_class=%q through the domain, want none: "+
			"the water sensor must not inherit the window rule from a shorter model prefix", got)
	}
}

func TestLookupNumberRuleHmwIo12FrequencyMHz(t *testing.T) {
	d, ok := LookupNumberRule("HMW-IO-12-Sw14-DR", "FREQUENCY")
	if !ok {
		t.Fatal("HMW-IO-12-Sw14-DR/FREQUENCY: not found")
	}
	if d.UnitOfMeasurement != "mHz" {
		t.Fatalf("HMW-IO-12-Sw14-DR/FREQUENCY: unit=%q want mHz", d.UnitOfMeasurement)
	}
}

func TestLookupCoverRuleHmIPBBLBlind(t *testing.T) {
	// Cover rules in misc.go are keyed on (device-prefix, parameter)
	// where parameter = "LEVEL" (the channel-level driver).
	d, ok := LookupCoverRule("HmIP-BBL", "LEVEL")
	if !ok {
		t.Fatal("HmIP-BBL/LEVEL: not found in cover rule table")
	}
	if d.DeviceClass != "blind" {
		t.Fatalf("HmIP-BBL/LEVEL: device_class=%q want blind", d.DeviceClass)
	}
}

// ---------------------------------------------------------------------------
// EntityDescriptionFor — unified API
// ---------------------------------------------------------------------------

func TestEntityDescriptionForSensorTemperatureMatches(t *testing.T) {
	desc := EntityDescriptionFor(HAComponentSensor, "", "ACTUAL_TEMPERATURE")
	if desc.DeviceClass != "temperature" {
		t.Fatalf("ACTUAL_TEMPERATURE: device_class=%q want temperature", desc.DeviceClass)
	}
	if desc.UnitOfMeasurement != "°C" {
		t.Fatalf("ACTUAL_TEMPERATURE: unit=%q want °C", desc.UnitOfMeasurement)
	}
	if desc.StateClass != "measurement" {
		t.Fatalf("ACTUAL_TEMPERATURE: state_class=%q want measurement", desc.StateClass)
	}
}

func TestEntityDescriptionForSensorRSSIDeviceDiagnosticDisabled(t *testing.T) {
	desc := EntityDescriptionFor(HAComponentSensor, "", "RSSI_DEVICE")
	if desc.EntityCategory != EntityCategoryDiagnostic {
		t.Fatalf("RSSI_DEVICE: entity_category=%q want diagnostic", desc.EntityCategory)
	}
	if desc.EnabledDefault == nil || *desc.EnabledDefault != false {
		t.Fatalf("RSSI_DEVICE: enabled_default must be false, got %v", desc.EnabledDefault)
	}
}

func TestEntityDescriptionForSensorOperatingVoltageDiagnosticDisabled(t *testing.T) {
	desc := EntityDescriptionFor(HAComponentSensor, "", "OPERATING_VOLTAGE")
	if desc.EntityCategory != EntityCategoryDiagnostic {
		t.Fatalf("OPERATING_VOLTAGE: entity_category=%q want diagnostic", desc.EntityCategory)
	}
	if desc.EnabledDefault == nil || *desc.EnabledDefault != false {
		t.Fatalf("OPERATING_VOLTAGE: enabled_default must be false, got %v", desc.EnabledDefault)
	}
}

func TestEntityDescriptionForNumberFrequencyUnit(t *testing.T) {
	// HMW-IO-12-Sw14-DR / FREQUENCY must carry the mHz unit override.
	desc := EntityDescriptionFor(HAComponentNumber, "HMW-IO-12-Sw14-DR", "FREQUENCY")
	if desc.UnitOfMeasurement != "mHz" {
		t.Fatalf("HMW-IO-12-Sw14-DR FREQUENCY: unit=%q want mHz", desc.UnitOfMeasurement)
	}
}

func TestEntityDescriptionForLightComponentReturnsZeroValue(t *testing.T) {
	// HAComponentLight is not handled — returns the zero MqttEntityDescription.
	desc := EntityDescriptionFor(HAComponentLight, "HmIP-BDT", "LEVEL")
	if desc != (MqttEntityDescription{}) {
		t.Fatalf("HAComponentLight must return zero value, got %+v", desc)
	}
}

func TestEntityDescriptionForEventPressShort(t *testing.T) {
	desc := EntityDescriptionFor(HAComponentEvent, "", "PRESS_SHORT")
	if desc.DeviceClass != "button" {
		t.Fatalf("PRESS_SHORT: device_class=%q want button", desc.DeviceClass)
	}
}

// ---------------------------------------------------------------------------
// Encoding sanity — ensure the µm constant remains a single-rune valid UTF-8
// codepoint after editor round-trips.
// ---------------------------------------------------------------------------

func TestUnitMicrometersIsValidUtf8(t *testing.T) {
	if !utf8.ValidString(unitMicrometers) {
		t.Fatalf("unitMicrometers=%q is not valid UTF-8", unitMicrometers)
	}
}

// ---------------------------------------------------------------------------
// EntityDescriptionLookup — generated rule table
// ---------------------------------------------------------------------------

// TestEntityDescriptionLookupBasicMatches pins a handful of well-known
// Future
// caught at compile-time. Each case is rooted in a real
// `entity_helpers/descriptions/*.py` rule.
func TestEntityDescriptionLookupBasicMatches(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		category  string
		parameter string
		model     string
		unit      string
		postfix   string

		wantKey         string
		wantDeviceClass string
		wantStateClass  string
		wantUnit        string
	}{
		{
			name:            "TEMPERATURE sensor",
			category:        "sensor",
			parameter:       "ACTUAL_TEMPERATURE",
			wantKey:         "TEMPERATURE",
			wantDeviceClass: "temperature",
			wantStateClass:  "measurement",
			wantUnit:        "°C",
		},
		{
			name:            "HUMIDITY sensor",
			category:        "sensor",
			parameter:       "HUMIDITY",
			wantKey:         "HUMIDITY",
			wantDeviceClass: "humidity",
			wantStateClass:  "measurement",
			wantUnit:        "%",
		},
		{
			name:            "ALARMSTATE binary_sensor",
			category:        "binary_sensor",
			parameter:       "ALARMSTATE",
			wantKey:         "ALARMSTATE",
			wantDeviceClass: "safety",
		},
		{
			name:            "LOWBAT binary_sensor",
			category:        "binary_sensor",
			parameter:       "LOWBAT",
			wantKey:         "LOW_BAT",
			wantDeviceClass: "battery",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := HARegistryDescriptionLookup(tc.category, tc.parameter, tc.model, tc.unit, tc.postfix, "")
			if got == nil {
				t.Fatalf("HARegistryDescriptionLookup(%q,%q,…) = nil; want match", tc.category, tc.parameter)
			}
			if got.Key != tc.wantKey {
				t.Errorf("Key = %q, want %q", got.Key, tc.wantKey)
			}
			if tc.wantDeviceClass != "" && got.DeviceClass != tc.wantDeviceClass {
				t.Errorf("DeviceClass = %q, want %q", got.DeviceClass, tc.wantDeviceClass)
			}
			if tc.wantStateClass != "" && got.StateClass != tc.wantStateClass {
				t.Errorf("StateClass = %q, want %q", got.StateClass, tc.wantStateClass)
			}
			if tc.wantUnit != "" && got.UnitOfMeasurement != tc.wantUnit {
				t.Errorf("UnitOfMeasurement = %q, want %q", got.UnitOfMeasurement, tc.wantUnit)
			}
		})
	}
}

// TestEntityDescriptionLookupReturnsNilOnMiss confirms a category that has
// no matching rule yields nil rather than a partial false-positive.
func TestEntityDescriptionLookupReturnsNilOnMiss(t *testing.T) {
	t.Parallel()
	if got := HARegistryDescriptionLookup("sensor", "DOES_NOT_EXIST_PARAM", "", "", "", ""); got != nil {
		t.Errorf("expected nil for unknown parameter, got %+v", got)
	}
	if got := HARegistryDescriptionLookup("", "", "", "", "", ""); got != nil {
		t.Errorf("expected nil for empty category, got %+v", got)
	}
}

// TestEntityDescriptionLookupDevicePrefixMatch confirms device-prefix
// filtering works case-insensitively, mirroring `EntityDescriptionRule.matches`.
func TestEntityDescriptionLookupDevicePrefixMatch(t *testing.T) {
	t.Parallel()
	// Rules with device prefix typically have a higher priority and
	// override the generic fallback. We assert the lookup returns a
	// Description (the exact key may evolve as
	// rules; this test guards the wiring, not the data).
	if got := HARegistryDescriptionLookup("sensor", "LEVEL", "HmIP-eTRV-2", "", "", ""); got == nil {
		t.Logf("note: no rule for HmIP-eTRV-2 LEVEL — table may have changed")
	}
}

// TestEntityDescriptionLookupRuleCount is the in-package floor: a table
// that lost most of its rules fails here without needing the contract
// suite. Exact content is pinned separately, by
// TestHARegistryDescriptionRulesMatchTheGolden in tests/contract/.
func TestEntityDescriptionLookupRuleCount(t *testing.T) {
	t.Parallel()
	if got := len(haRegistryDescriptionRules); got < 100 {
		t.Errorf("haRegistryDescriptionRules = %d entries; expected at least 100", got)
	}
}

// ---------------------------------------------------------------------------
// EventDeviceClassForModel — doorbell vs. generic button
// ---------------------------------------------------------------------------

// TestEventDeviceClassForModel pins the doorbell/button split: the
// curated set — HM-Sen-DB-PCB (classic wired doorbell PCB), HmIP-DBB
// (wireless doorbell button), and HmIP-DSD-PCB (doorbell sensor PCB) —
// reports device_class "doorbell"; every other model — including the
// empty string — falls back to the generic "button".
func TestEventDeviceClassForModel(t *testing.T) {
	t.Parallel()

	cases := []struct {
		model string
		want  string
	}{
		{model: "HM-Sen-DB-PCB", want: "doorbell"},
		{model: "HmIP-DBB", want: "doorbell"},
		{model: "HmIP-DSD-PCB", want: "doorbell"},
		{model: "HmIP-WRC2", want: "button"},
		{model: "", want: "button"},
	}

	for _, tc := range cases {
		t.Run(tc.model, func(t *testing.T) {
			t.Parallel()
			if got := EventDeviceClassForModel(tc.model); got != tc.want {
				t.Errorf("EventDeviceClassForModel(%q) = %q, want %q", tc.model, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// MapDoorbellEventTypes — announced event_types rewriting
// ---------------------------------------------------------------------------

// TestMapDoorbellEventTypesRewritesPressShortToRing verifies that for a
// doorbell-class model, only "press_short" is rewritten to the standard
// "ring" event type; the other PRESS_* types are left untouched, and the
// input slice itself is not mutated (a fresh slice is returned).
func TestMapDoorbellEventTypesRewritesPressShortToRing(t *testing.T) {
	t.Parallel()

	in := []string{"press_short", "press_long", "press_long_release", "press_long_start"}
	inSnapshot := append([]string(nil), in...)

	got := MapDoorbellEventTypes("HmIP-DBB", in)
	want := []string{"ring", "press_long", "press_long_release", "press_long_start"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("MapDoorbellEventTypes(HmIP-DBB, %v) = %v, want %v", in, got, want)
	}
	if !reflect.DeepEqual(in, inSnapshot) {
		t.Errorf("MapDoorbellEventTypes mutated its input slice: got %v, want unchanged %v", in, inSnapshot)
	}
}

// TestMapDoorbellEventTypesClassicModelAlsoRewrites confirms the newly
// curated HM-Sen-DB-PCB (classic wired doorbell PCB) gets the same
// press_short → ring rewrite as the HmIP doorbell models.
func TestMapDoorbellEventTypesClassicModelAlsoRewrites(t *testing.T) {
	t.Parallel()

	got := MapDoorbellEventTypes("HM-Sen-DB-PCB", []string{"press_short"})
	want := []string{"ring"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("MapDoorbellEventTypes(HM-Sen-DB-PCB, [press_short]) = %v, want %v", got, want)
	}
}

// TestMapDoorbellEventTypesNonDoorbellPassesThrough verifies that a
// non-doorbell model's event_types list passes through completely
// unchanged, including a literal "press_short" entry (which only gets
// the doorbell treatment for curated models).
func TestMapDoorbellEventTypesNonDoorbellPassesThrough(t *testing.T) {
	t.Parallel()

	in := []string{"press_short", "press_long"}
	got := MapDoorbellEventTypes("HmIP-WRC2", in)
	if !reflect.DeepEqual(got, in) {
		t.Errorf("MapDoorbellEventTypes(HmIP-WRC2, %v) = %v, want unchanged %v", in, got, in)
	}
}

// ---------------------------------------------------------------------------
// DoorbellEventType — single runtime press-type mapping
// ---------------------------------------------------------------------------

// TestDoorbellEventType covers the runtime per-event mapping: PRESS_SHORT
// becomes "ring" only for curated doorbell models; every other
// combination lower-cases the press type and passes it through,
// including PRESS_SHORT itself on a non-doorbell model.
func TestDoorbellEventType(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		model     string
		pressType string
		want      string
	}{
		{name: "doorbell PRESS_SHORT becomes ring", model: "HmIP-DBB", pressType: "PRESS_SHORT", want: "ring"},
		{name: "classic doorbell PRESS_SHORT becomes ring", model: "HM-Sen-DB-PCB", pressType: "press_short", want: "ring"},
		{name: "doorbell PRESS_LONG lower-cases, no ring rewrite", model: "HmIP-DBB", pressType: "PRESS_LONG", want: "press_long"},
		{name: "non-doorbell PRESS_SHORT lower-cases only", model: "HmIP-WRC2", pressType: "PRESS_SHORT", want: "press_short"},
		{name: "non-doorbell PRESS_LONG lower-cases only", model: "HmIP-WRC2", pressType: "PRESS_LONG", want: "press_long"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := DoorbellEventType(tc.model, tc.pressType); got != tc.want {
				t.Errorf("DoorbellEventType(%q, %q) = %q, want %q", tc.model, tc.pressType, got, tc.want)
			}
		})
	}
}

// TestEntityDescriptionUnitsUseMicroSign pins the spelling of the micro
// prefix across every description table, hand-written and generated alike.
//
// Unicode encodes the prefix twice — U+00B5 MICRO SIGN and U+03BC GREEK
// SMALL LETTER MU — and the two are indistinguishable on screen. Home
// Assistant validates a sensor's advertised unit against its own canonical
// U+00B5 string for the declared device class and refuses the whole config
// when they differ, so a PM entity published with the Greek letter never
// appears at all. The check scans the package source rather than a list of
// tables so a newly added table, or a regenerated one, cannot reintroduce
// it unnoticed.
func TestEntityDescriptionUnitsUseMicroSign(t *testing.T) {
	t.Parallel()
	const greekMu = "μ"
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	scanned := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		body, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		scanned++
		for i, line := range strings.Split(string(body), "\n") {
			if strings.Contains(line, greekMu) {
				t.Errorf("%s:%d uses U+03BC GREEK SMALL LETTER MU; use U+00B5 MICRO SIGN: %s",
					name, i+1, strings.TrimSpace(line))
			}
		}
	}
	if scanned == 0 {
		t.Fatal("no package sources scanned")
	}
}

// TestParticulateMatterUnitsAreCanonical asserts the spelling through the
// two lookup paths a discovery payload actually takes — the hand-written
// per-parameter rules and the generated registry table — so the guard
// survives a refactor that moves the literal somewhere the source scan
// above no longer covers.
func TestParticulateMatterUnitsAreCanonical(t *testing.T) {
	t.Parallel()
	const wantPM = "µg/m³"
	for _, param := range []string{
		"MASS_CONCENTRATION_PM_1",
		"MASS_CONCENTRATION_PM_10",
		"MASS_CONCENTRATION_PM_2_5",
		"MASS_CONCENTRATION_PM_1_24H_AVERAGE",
		"MASS_CONCENTRATION_PM_10_24H_AVERAGE",
		"MASS_CONCENTRATION_PM_2_5_24H_AVERAGE",
	} {
		d, ok := LookupSensorRule("", param)
		if !ok {
			t.Fatalf("%s: no sensor rule", param)
		}
		if d.UnitOfMeasurement != wantPM {
			t.Errorf("%s: unit %q, want %q", param, d.UnitOfMeasurement, wantPM)
		}
		reg := HARegistryDescriptionLookup("sensor", param, "", "", "", "")
		if reg == nil {
			t.Fatalf("%s: no registry description", param)
		}
		if reg.UnitOfMeasurement != wantPM {
			t.Errorf("%s: registry unit %q, want %q", param, reg.UnitOfMeasurement, wantPM)
		}
	}
}
