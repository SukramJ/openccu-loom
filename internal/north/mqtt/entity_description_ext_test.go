// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import "testing"

// ---------------------------------------------------------------------------
// Task #47 — EntityDescriptionExtRule matching (unit/postfix/var_name)
// ---------------------------------------------------------------------------

func TestMatchesExtParameterCaseInsensitive(t *testing.T) {
	r := EntityDescriptionExtRule{
		Parameter:   "TEMPERATURE",
		Description: EntityDescription{Key: "temp"},
	}
	if !r.MatchesExt("", "temperature", "", "", "") {
		t.Fatal("lowercase parameter should match")
	}
	if !r.MatchesExt("", "TEMPERATURE", "", "", "") {
		t.Fatal("uppercase parameter should match")
	}
	if r.MatchesExt("", "HUMIDITY", "", "", "") {
		t.Fatal("different parameter must not match")
	}
}

func TestMatchesExtUnitExact(t *testing.T) {
	r := EntityDescriptionExtRule{
		Unit:        "mHz",
		Description: EntityDescription{Key: "freq"},
	}
	if !r.MatchesExt("", "FREQUENCY", "mHz", "", "") {
		t.Fatal("matching unit must produce a hit")
	}
	if r.MatchesExt("", "FREQUENCY", "Hz", "", "") {
		t.Fatal("non-matching unit must not produce a hit")
	}
}

func TestMatchesExtPostfixCaseInsensitive(t *testing.T) {
	r := EntityDescriptionExtRule{
		Postfix:     "_2",
		Description: EntityDescription{Key: "level_2"},
	}
	if !r.MatchesExt("", "LEVEL_2", "", "_2", "") {
		t.Fatal("postfix _2 should match")
	}
	if r.MatchesExt("", "LEVEL_3", "", "_3", "") {
		t.Fatal("postfix _3 must not match rule for _2")
	}
}

func TestMatchesExtVarNameContainsSubstring(t *testing.T) {
	r := EntityDescriptionExtRule{
		VarNameContains: "temperature",
		Description:     EntityDescription{Key: "temp"},
	}
	if !r.MatchesExt("", "", "", "", "ACTUAL_TEMPERATURE") {
		t.Fatal("var name containing substring should match")
	}
	if r.MatchesExt("", "", "", "", "HUMIDITY") {
		t.Fatal("var name not containing substring must not match")
	}
}

func TestMatchesExtDevicePrefixBoundary(t *testing.T) {
	r := EntityDescriptionExtRule{
		DevicePrefix: "HmIP-eTRV",
		Parameter:    "LEVEL",
		Description:  EntityDescription{Key: "level"},
	}
	// Exact match
	if !r.MatchesExt("HmIP-eTRV", "LEVEL", "", "", "") {
		t.Fatal("exact device match must succeed")
	}
	// Prefix match: HmIP-eTRV-2 starts with HmIP-eTRV-
	if !r.MatchesExt("HmIP-eTRV-2", "LEVEL", "", "", "") {
		t.Fatal("prefix device match (dash boundary) must succeed")
	}
	// Unrelated device must not match
	if r.MatchesExt("HmIP-STHD", "LEVEL", "", "", "") {
		t.Fatal("unrelated device must not match")
	}
}

func TestMatchesExtAllCriteriaAnd(t *testing.T) {
	r := EntityDescriptionExtRule{
		DevicePrefix: "HmIP-BS",
		Parameter:    "ACTUAL_TEMPERATURE",
		Unit:         "°C",
		Description:  EntityDescription{Key: "at"},
	}
	// All criteria satisfied. HmIP-BS-X has the dash boundary after the prefix.
	if !r.MatchesExt("HmIP-BS-X", "ACTUAL_TEMPERATURE", "°C", "", "") {
		t.Fatal("all criteria matching: should hit")
	}
	// Exact device match (no suffix) must also work.
	if !r.MatchesExt("HmIP-BS", "ACTUAL_TEMPERATURE", "°C", "", "") {
		t.Fatal("exact device match: should hit")
	}
	// Unit mismatch
	if r.MatchesExt("HmIP-BS-X", "ACTUAL_TEMPERATURE", "K", "", "") {
		t.Fatal("unit mismatch: should miss")
	}
	// Parameter mismatch
	if r.MatchesExt("HmIP-BS-X", "TEMPERATURE", "°C", "", "") {
		t.Fatal("parameter mismatch: should miss")
	}
}

func TestLookupExtRuleInSlicePriorityOrder(t *testing.T) {
	// Higher priority rule must win even if lower priority rule also matches.
	rules := []EntityDescriptionExtRule{
		{Priority: 10, Parameter: "X", Description: EntityDescription{Key: "high"}},
		{Priority: 0, Parameter: "X", Description: EntityDescription{Key: "low"}},
	}
	d, ok := LookupExtRuleInSlice(rules, "", "X", "", "", "")
	if !ok {
		t.Fatal("expected a match")
	}
	if d.Key != "high" {
		t.Fatalf("expected high-priority description, got key=%q", d.Key)
	}
}

func TestLookupExtRuleInSliceNoMatch(t *testing.T) {
	rules := []EntityDescriptionExtRule{
		{Parameter: "FOO", Description: EntityDescription{Key: "foo"}},
	}
	_, ok := LookupExtRuleInSlice(rules, "", "BAR", "", "", "")
	if ok {
		t.Fatal("non-matching parameter must return ok=false")
	}
}

func TestLookupExtRuleForComponentUnknownComponentReturnsFalse(t *testing.T) {
	// HAComponentClimate has no extended rules registered.
	_, ok := LookupExtRuleForComponent(HAComponentClimate, "HmIP-eTRV", "SET_POINT_TEMPERATURE", "", "", "")
	if ok {
		t.Fatal("component with no ext rules must return ok=false")
	}
}

func TestEntityDescriptionForExtFallsThroughToExtRules(t *testing.T) {
	// Temporarily inject a sensor ext rule that matches on unit "mHz".
	orig := sensorExtRules
	defer func() { sensorExtRules = orig }()
	sensorExtRules = []EntityDescriptionExtRule{
		{
			Parameter:   "CUSTOM_FREQ",
			Unit:        "mHz",
			Priority:    5,
			Description: EntityDescription{Key: "custom_freq", UnitOfMeasurement: "mHz", EnabledByDefault: true, SuggestedDisplayPrecision: -1},
		},
	}

	desc := EntityDescriptionForExt(HAComponentSensor, "SomeDevice", "CUSTOM_FREQ", "mHz", "", "")
	if desc.UnitOfMeasurement != "mHz" {
		t.Fatalf("ext rule should have produced mHz unit, got %q", desc.UnitOfMeasurement)
	}
}

func TestEntityDescriptionForExtStaticMapWinsOverExtRule(t *testing.T) {
	// Inject a sensor ext rule for a parameter that already has a static entry.
	// The static map (Tier 1) must win.
	orig := sensorExtRules
	defer func() { sensorExtRules = orig }()
	sensorExtRules = []EntityDescriptionExtRule{
		{
			Parameter:   "ACTUAL_TEMPERATURE",
			Priority:    100, // high priority, but still below static map tier 1
			Description: EntityDescription{Key: "ext_override", EnabledByDefault: true, SuggestedDisplayPrecision: -1},
		},
	}

	desc := EntityDescriptionForExt(HAComponentSensor, "", "ACTUAL_TEMPERATURE", "", "", "")
	// Static map key for ACTUAL_TEMPERATURE is "TEMPERATURE".
	if desc.DeviceClass != "temperature" {
		t.Fatalf("static map tier must win: expected device_class=temperature, got %q", desc.DeviceClass)
	}
}

// ---------------------------------------------------------------------------
// Task #48 — ValidateEntityDescriptionRules
// ---------------------------------------------------------------------------

func TestValidateEntityDescriptionRulesCleanState(t *testing.T) {
	// Default state (prod rule tables) must be conflict-free.
	if err := ValidateEntityDescriptionRules(); err != nil {
		t.Fatalf("prod rule tables must not have conflicts: %v", err)
	}
}

func TestValidateEntityDescriptionRulesDetectsExtConflict(t *testing.T) {
	// Inject two ext rules with the same criteria and same priority.
	orig := sensorExtRules
	defer func() { sensorExtRules = orig }()
	sensorExtRules = []EntityDescriptionExtRule{
		{Priority: 5, Parameter: "DUPE", Description: EntityDescription{Key: "a"}},
		{Priority: 5, Parameter: "DUPE", Description: EntityDescription{Key: "b"}},
	}

	err := ValidateEntityDescriptionRules()
	if err == nil {
		t.Fatal("duplicate ext rules must be flagged")
	}
}

func TestValidateEntityDescriptionRulesDifferentPriorityNotConflict(t *testing.T) {
	// Two ext rules for the same parameter at different priorities are
	// valid overrides — not a conflict.
	orig := sensorExtRules
	defer func() { sensorExtRules = orig }()
	sensorExtRules = []EntityDescriptionExtRule{
		{Priority: 10, Parameter: "X", Description: EntityDescription{Key: "high"}},
		{Priority: 0, Parameter: "X", Description: EntityDescription{Key: "low"}},
	}

	if err := ValidateEntityDescriptionRules(); err != nil {
		t.Fatalf("different priorities must not flag a conflict: %v", err)
	}
}
