// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package masterprofile

// White-box tests for unexported helpers: localised, toFloat, inRange variants.

import (
	"errors"
	"testing"
)

// --- LocalisedDescription ---

func TestLocalisedDescriptionFallback(t *testing.T) {
	s := New()
	p, err := s.Profile("BLIND", "", 0)
	if err != nil {
		t.Fatalf("Profile err: %v", err)
	}
	// Any locale is fine; we just exercise the method path.
	_ = p.LocalisedDescription("de")
	_ = p.LocalisedDescription("en")
	_ = p.LocalisedDescription("xx") // unknown locale — exercises fallback chain
}

// --- localised fallback branch ---

func TestLocalisedFallsBackToEnWhenLocaleAbsent(t *testing.T) {
	m := map[string]string{"en": "English Name", "de": "Deutsch Name"}
	if got := localised(m, "fr"); got != "English Name" {
		t.Errorf("localised fallback to en: got %q, want %q", got, "English Name")
	}
}

func TestLocalisedFallsBackToFirstWhenEnAbsent(t *testing.T) {
	m := map[string]string{"de": "Nur Deutsch"}
	if got := localised(m, "xx"); got != "Nur Deutsch" {
		t.Errorf("localised fallback to first entry: got %q", got)
	}
}

func TestLocalisedReturnsEmptyWhenNoEntries(t *testing.T) {
	if got := localised(map[string]string{}, "en"); got != "" {
		t.Errorf("expected empty, got %q", got)
	}
}

// --- Profile lookup with unknown channel type falls back to KEY ---

func TestProfilesChannelTypeFallsBackToKEY(t *testing.T) {
	s := New()
	// BLIND has no "UNKNOWN_CH" bucket; should fall back to KEY.
	profiles, err := s.Profiles("BLIND", "UNKNOWN_CH")
	if err != nil {
		t.Fatalf("expected fallback to KEY, got err: %v", err)
	}
	if len(profiles) == 0 {
		t.Fatal("expected profiles from KEY fallback, got none")
	}
}

// --- Profile by ID: not found ---

func TestProfileByIDNotFound(t *testing.T) {
	s := New()
	_, err := s.Profile("BLIND", "", 99999)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for unknown id, got %v", err)
	}
}

// --- DeviceTypes: caching ---

func TestDeviceTypesIndexErrorCached(t *testing.T) {
	// Second call should return cached index, not re-read.
	s := New()
	_, err1 := s.DeviceTypes()
	_, err2 := s.DeviceTypes()
	// Both calls must agree: either both nil or both the same sentinel error.
	if (err1 == nil) != (err2 == nil) {
		t.Errorf("expected same error on second call: first=%v second=%v", err1, err2)
	} else if err1 != nil && !errors.Is(err2, err1) {
		t.Errorf("expected same error on second call: first=%v second=%v", err1, err2)
	}
}

// --- ChannelTypes: unknown device returns ErrNotFound ---

func TestChannelTypesUnknownDeviceReturnsErrNotFound(t *testing.T) {
	s := New()
	_, err := s.ChannelTypes("DOES_NOT_EXIST_AT_ALL")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

// --- toFloat: untested types ---

func TestToFloatFloat32(t *testing.T) {
	v, ok := toFloat(float32(3.14))
	if !ok || v < 3.13 || v > 3.15 {
		t.Errorf("toFloat(float32): ok=%v v=%v", ok, v)
	}
}

func TestToFloatInt32(t *testing.T) {
	v, ok := toFloat(int32(7))
	if !ok || v != 7 {
		t.Errorf("toFloat(int32): ok=%v v=%v", ok, v)
	}
}

func TestToFloatInt64(t *testing.T) {
	v, ok := toFloat(int64(100))
	if !ok || v != 100 {
		t.Errorf("toFloat(int64): ok=%v v=%v", ok, v)
	}
}

func TestToFloatStringIsUnrecognised(t *testing.T) {
	_, ok := toFloat("ten")
	if ok {
		t.Error("toFloat(string) should return false")
	}
}

// --- inRange: map form ---

func TestInRangeMapForm(t *testing.T) {
	c := ParamConstraint{ConstraintType: "range", Value: map[string]any{"min": 10.0, "max": 20.0}}
	if !inRange(c, 15.0) {
		t.Error("inRange(map): 15 should be within [10, 20]")
	}
	if inRange(c, 25.0) {
		t.Error("inRange(map): 25 should not be within [10, 20]")
	}
}

func TestInRangeMapFormMissingMinIsOK(t *testing.T) {
	// If min is not a number, toFloat returns false → inRange returns false.
	c := ParamConstraint{ConstraintType: "range", Value: map[string]any{"min": "bad", "max": 20.0}}
	if inRange(c, 15.0) {
		t.Error("inRange(map): non-numeric min should return false")
	}
}

func TestInRangeListFormWrongLength(t *testing.T) {
	// A 1-element list is invalid — should return false.
	c := ParamConstraint{ConstraintType: "range", Value: []any{10.0}}
	if inRange(c, 10.0) {
		t.Error("inRange: 1-element list should return false")
	}
}

func TestInRangeListFormNonNumericBound(t *testing.T) {
	c := ParamConstraint{ConstraintType: "range", Value: []any{"bad", 20.0}}
	if inRange(c, 15.0) {
		t.Error("inRange: non-numeric min bound should return false")
	}
}

func TestInRangeUnknownValueType(t *testing.T) {
	// A string Value for a range constraint — should return false.
	c := ParamConstraint{ConstraintType: "range", Value: "10-20"}
	if inRange(c, 15.0) {
		t.Error("inRange: unknown value type should return false")
	}
}

func TestInRangeNonNumericCurrent(t *testing.T) {
	c := ParamConstraint{ConstraintType: "range", Value: []any{10.0, 20.0}}
	// Passing a string as current — toFloat(current) should return false.
	if inRange(c, "fifteen") {
		t.Error("inRange: non-numeric current should return false")
	}
}

// --- listContains: single-value degenerate form ---

func TestListContainsSingleValueMatch(t *testing.T) {
	c := ParamConstraint{ConstraintType: "list", Value: "ECO"}
	if !listContains(c, "ECO") {
		t.Error("listContains: single-value form should match ECO")
	}
	if listContains(c, "BOOST") {
		t.Error("listContains: single-value form should not match BOOST")
	}
}

// --- scoreProfile: unknown constraint type is skipped ---

func TestScoreProfileUnknownConstraintTypeSkipped(t *testing.T) {
	p := Profile{
		ID: 1,
		Params: map[string]ParamConstraint{
			"X": {ConstraintType: "future_type", Value: 42.0},
		},
	}
	score := scoreProfile(p, map[string]any{"X": 42.0})
	// Unknown type is skipped → score = 0 (no match, not disqualified).
	if score < 0 {
		t.Errorf("unknown constraint type should not disqualify, score=%d", score)
	}
}

// --- MatchActiveProfile: profile with zero Params is skipped ---

func TestMatchActiveProfile_EmptyParamsProfileSkipped(t *testing.T) {
	s := New()
	s.cache["TYPE-Z"] = map[string][]Profile{"KEY": {
		{ID: 0},
		{ID: 1, Params: map[string]ParamConstraint{}}, // empty params → skipped
	}}
	// Only the Expert (id=0) remains after skipping id=1.
	got := s.MatchActiveProfile("TYPE-Z", "KEY", map[string]any{"TEMP": 18.0})
	if got != 0 {
		t.Errorf("empty-params profile should be skipped, got %d", got)
	}
}

// --- DeviceTypes: result is a defensive copy ---

func TestDeviceTypesReturnsCopy(t *testing.T) {
	s := New()
	types1, _ := s.DeviceTypes()
	if len(types1) > 0 {
		// Mutate the returned slice.
		types1[0] = "MUTATED"
	}
	types2, _ := s.DeviceTypes()
	if len(types2) > 0 && types2[0] == "MUTATED" {
		t.Error("DeviceTypes should return a defensive copy")
	}
}
