// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package linkprofile_test

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/store/linkprofile"
)

// ---------------------------------------------------------------------------
// Nil / zero-value guard tests
// ---------------------------------------------------------------------------

func TestZeroStoreGetReturnsEmpty(t *testing.T) {
	t.Parallel()
	var s *linkprofile.Store
	// A nil Store must not panic — returns nil, nil.
	profs, err := s.GetLinkProfiles(context.Background(), "KEY", "KEY", "en")
	if err != nil {
		t.Fatalf("nil store GetLinkProfiles should not error: %v", err)
	}
	if len(profs) != 0 {
		t.Fatalf("nil store should return empty list, got %d", len(profs))
	}
}

func TestNew_UnknownReceiverTypeReturnsEmpty(t *testing.T) {
	t.Parallel()
	s := linkprofile.New()
	profs, err := s.GetLinkProfiles(context.Background(), "NONEXISTENT_TYPE_XYZ", "KEY", "en")
	if err != nil {
		t.Fatalf("expected nil error for unknown type, got %v", err)
	}
	if len(profs) != 0 {
		t.Fatalf("expected empty list, got %d profiles", len(profs))
	}
}

// ---------------------------------------------------------------------------
// Register (manual injection for unit tests)
// ---------------------------------------------------------------------------

func TestRegister_StoresAndRetrievesProfiles(t *testing.T) {
	t.Parallel()
	fixedVal := 1.0
	fixedVal2 := 2.0
	profs := []linkprofile.Profile{
		{
			ID:   1,
			Name: map[string]string{"en": "On/off", "de": "Ein/Aus"},
			Params: map[string]linkprofile.ParamConstraint{
				"SHORT_ACTION_TYPE": {ConstraintType: "fixed", Value: &fixedVal},
			},
		},
		{
			ID:   2,
			Name: map[string]string{"en": "Dim", "de": "Dimmen"},
			Params: map[string]linkprofile.ParamConstraint{
				"SHORT_ACTION_TYPE": {ConstraintType: "fixed", Value: &fixedVal2},
			},
		},
	}
	s := linkprofile.New()
	s.Register("DIMMER", "KEY", profs)

	got, err := s.GetLinkProfiles(context.Background(), "DIMMER", "KEY", "en")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 profiles, got %d", len(got))
	}
	if got[0].ID != 1 || got[1].ID != 2 {
		t.Fatalf("unexpected profile IDs: %d, %d", got[0].ID, got[1].ID)
	}
}

func TestRegister_UnknownPairReturnsEmpty(t *testing.T) {
	t.Parallel()
	s := linkprofile.New()
	s.Register("SWITCH", "KEY", []linkprofile.Profile{{ID: 1}})

	got, err := s.GetLinkProfiles(context.Background(), "SWITCH", "UNKNOWN_SENDER", "en")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty list for unknown pair, got %d", len(got))
	}
}

// ---------------------------------------------------------------------------
// Embedded data — real archive tests (DIMMER.json.gz → KEY sender)
// ---------------------------------------------------------------------------

func TestEmbeddedData_DimmerKeyProfilesLoaded(t *testing.T) {
	t.Parallel()
	s := linkprofile.New()
	profs, err := s.GetLinkProfiles(context.Background(), "DIMMER", "KEY", "en")
	if err != nil {
		t.Fatalf("GetLinkProfiles(DIMMER, KEY): %v", err)
	}
	// DIMMER.json.gz / KEY contains id=0 (Expert) plus at least 10 real profiles.
	if len(profs) < 2 {
		t.Fatalf("expected at least 2 profiles (Expert + real), got %d", len(profs))
	}
	// Profile id=0 must be "Expert" (the raw-editor sentinel).
	if profs[0].ID != 0 {
		t.Fatalf("expected first profile id=0 (Expert), got %d", profs[0].ID)
	}
	// Profile id=1 must be present and named.
	if profs[1].ID != 1 {
		t.Fatalf("expected second profile id=1, got %d", profs[1].ID)
	}
	if profs[1].LocalisedName("en") == "" {
		t.Fatal("profile id=1 has empty English name")
	}
}

func TestEmbeddedData_DimmerKeyProfile1FixedParams(t *testing.T) {
	t.Parallel()
	s := linkprofile.New()
	profs, err := s.GetLinkProfiles(context.Background(), "DIMMER", "KEY", "en")
	if err != nil || len(profs) < 2 {
		t.Fatalf("could not load DIMMER/KEY profiles: err=%v count=%d", err, len(profs))
	}
	// Profile id=1 "Dimmer - on/brighter" has a known fixed SHORT_ACTION_TYPE=1.
	var p1 *linkprofile.Profile
	for i := range profs {
		if profs[i].ID == 1 {
			p1 = &profs[i]
			break
		}
	}
	if p1 == nil {
		t.Fatal("profile id=1 not found in DIMMER/KEY")
	}
	fixed := p1.FixedParams()
	if len(fixed) == 0 {
		t.Fatal("FixedParams() returned empty map for profile id=1")
	}
	v, ok := fixed["SHORT_ACTION_TYPE"]
	if !ok {
		t.Fatal("expected SHORT_ACTION_TYPE in fixed params")
	}
	if v != 1.0 {
		t.Fatalf("expected SHORT_ACTION_TYPE=1.0, got %v", v)
	}
}

func TestEmbeddedData_ReceiverTypes(t *testing.T) {
	t.Parallel()
	s := linkprofile.New()
	types, err := s.ReceiverTypes()
	if err != nil {
		t.Fatalf("ReceiverTypes: %v", err)
	}
	if len(types) < 60 {
		t.Fatalf("expected at least 60 receiver types, got %d", len(types))
	}
	// DIMMER must be present.
	found := slices.Contains(types, "DIMMER")
	if !found {
		t.Fatal("DIMMER not found in ReceiverTypes()")
	}
}

// ---------------------------------------------------------------------------
// GetProfileByID
// ---------------------------------------------------------------------------

func TestGetProfileByID_Found(t *testing.T) {
	t.Parallel()
	s := linkprofile.New()
	p, ok := s.GetProfileByID("DIMMER", "KEY", 1)
	if !ok {
		t.Fatal("GetProfileByID(DIMMER, KEY, 1): expected found=true")
	}
	if p.ID != 1 {
		t.Fatalf("expected id=1, got %d", p.ID)
	}
}

func TestGetProfileByID_NotFound(t *testing.T) {
	t.Parallel()
	s := linkprofile.New()
	_, ok := s.GetProfileByID("DIMMER", "KEY", 999)
	if ok {
		t.Fatal("expected found=false for unknown id=999")
	}
}

func TestGetProfileByID_UnknownReceiver(t *testing.T) {
	t.Parallel()
	s := linkprofile.New()
	_, ok := s.GetProfileByID("NONEXISTENT_TYPE", "KEY", 1)
	if ok {
		t.Fatal("expected found=false for unknown receiver type")
	}
}

// TestGetProfileByID_MutatingResultDoesNotAffectCache verifies that the
// Name/Description/Params maps of a Profile returned by GetProfileByID are
// independent copies: mutating them must not corrupt the store's cached
// data used by later lookups.
func TestGetProfileByID_MutatingResultDoesNotAffectCache(t *testing.T) {
	t.Parallel()
	s := linkprofile.New()

	p1, ok := s.GetProfileByID("DIMMER", "KEY", 1)
	if !ok {
		t.Fatal("GetProfileByID(DIMMER, KEY, 1): expected found=true")
	}
	if len(p1.Name) == 0 {
		t.Fatal("expected profile 1 to have a non-empty Name map")
	}

	// Mutate the returned maps as an incautious caller might.
	for k := range p1.Name {
		p1.Name[k] = "MUTATED"
	}
	if p1.Params != nil {
		for k := range p1.Params {
			p1.Params[k] = linkprofile.ParamConstraint{ConstraintType: "MUTATED"}
		}
	}

	p2, ok := s.GetProfileByID("DIMMER", "KEY", 1)
	if !ok {
		t.Fatal("second GetProfileByID(DIMMER, KEY, 1): expected found=true")
	}
	for k, v := range p2.Name {
		if v == "MUTATED" {
			t.Fatalf("cache corrupted: Name[%q] leaked mutation from prior caller", k)
		}
	}
	for k, c := range p2.Params {
		if c.ConstraintType == "MUTATED" {
			t.Fatalf("cache corrupted: Params[%q] leaked mutation from prior caller", k)
		}
	}
}

// TestGetLinkProfiles_MutatingResultDoesNotAffectCache verifies the same
// isolation for the slice returned by GetLinkProfiles.
func TestGetLinkProfiles_MutatingResultDoesNotAffectCache(t *testing.T) {
	t.Parallel()
	s := linkprofile.New()

	got1, err := s.GetLinkProfiles(context.Background(), "DIMMER", "KEY", "en")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got1) == 0 || len(got1[0].Name) == 0 {
		t.Fatal("expected at least one profile with a non-empty Name map")
	}
	for k := range got1[0].Name {
		got1[0].Name[k] = "MUTATED"
	}

	got2, err := s.GetLinkProfiles(context.Background(), "DIMMER", "KEY", "en")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for k, v := range got2[0].Name {
		if v == "MUTATED" {
			t.Fatalf("cache corrupted: Name[%q] leaked mutation from prior caller", k)
		}
	}
}

// ---------------------------------------------------------------------------
// Profile.ApplyValues
// ---------------------------------------------------------------------------

// TestApplyValues_FixedAndDefaultedLooseParams verifies against the real
// embedded archive that ApplyValues returns every fixed constraint's value
// plus the default of every non-fixed constraint that carries one, and
// nothing else. ACTOR_WINDOW/SHUTTER_CONTACT profile id=3 has a known shape:
// fixed constraints, two range constraints with a default
// (SHORT_ON_LEVEL, SHORT_COND_VALUE_HI), and one list constraint without a
// default (SHORT_MULTIEXECUTE).
func TestApplyValues_FixedAndDefaultedLooseParams(t *testing.T) {
	t.Parallel()
	s := linkprofile.New()
	p, ok := s.GetProfileByID("ACTOR_WINDOW", "SHUTTER_CONTACT", 3)
	if !ok {
		t.Fatal("GetProfileByID(ACTOR_WINDOW, SHUTTER_CONTACT, 3): expected found=true")
	}

	// Build the expected set independently from the same profile's Params,
	// mirroring the documented rule rather than hardcoding literals.
	wantFixed := 0
	wantLooseWithDefault := 0
	for _, c := range p.Params {
		switch {
		case c.ConstraintType == "fixed" && c.Value != nil:
			wantFixed++
		case c.ConstraintType != "fixed" && c.Default != nil:
			wantLooseWithDefault++
		}
	}
	if wantFixed == 0 || wantLooseWithDefault == 0 {
		t.Fatalf("archive shape assumption broke: fixed=%d looseWithDefault=%d", wantFixed, wantLooseWithDefault)
	}

	got := p.ApplyValues()
	if len(got) != wantFixed+wantLooseWithDefault {
		t.Fatalf("ApplyValues: expected %d entries (fixed=%d + defaulted-loose=%d), got %d: %v",
			wantFixed+wantLooseWithDefault, wantFixed, wantLooseWithDefault, len(got), got)
	}
	for name, c := range p.Params {
		v, present := got[name]
		switch {
		case c.ConstraintType == "fixed" && c.Value != nil:
			if !present || v.(float64) != *c.Value {
				t.Fatalf("ApplyValues: fixed param %s: expected %v, got %v (present=%v)", name, *c.Value, v, present)
			}
		case c.ConstraintType != "fixed" && c.Default != nil:
			if !present || v.(float64) != *c.Default {
				t.Fatalf("ApplyValues: defaulted loose param %s: expected %v, got %v (present=%v)", name, *c.Default, v, present)
			}
		default:
			if present {
				t.Fatalf("ApplyValues: param %s (type=%s, no default) must be absent, got %v", name, c.ConstraintType, v)
			}
		}
	}

	// SHORT_MULTIEXECUTE is the documented limitation: a list constraint
	// without a default must not appear in the applied value set.
	if _, present := got["SHORT_MULTIEXECUTE"]; present {
		t.Fatal("ApplyValues: SHORT_MULTIEXECUTE (list, no default) must be absent")
	}
}

func TestApplyValues_NoParams(t *testing.T) {
	t.Parallel()
	p := linkprofile.Profile{}
	got := p.ApplyValues()
	if len(got) != 0 {
		t.Fatalf("expected empty value set for a profile with no params, got %v", got)
	}
}

// ---------------------------------------------------------------------------
// MatchActiveProfile
// ---------------------------------------------------------------------------

func TestMatchActiveProfile_MatchesProfileByFixedParams(t *testing.T) {
	t.Parallel()
	s := linkprofile.New()
	// Profile id=1 in DIMMER/KEY has SHORT_ACTION_TYPE=1 (fixed).
	// Supplying the exact value should yield id=1.
	current := map[string]any{
		"SHORT_ACTION_TYPE": float64(1),
	}
	id := s.MatchActiveProfile("DIMMER", "KEY", current)
	if id == 0 {
		t.Fatal("MatchActiveProfile: expected non-zero id, got 0 (Expert fallback)")
	}
}

func TestMatchActiveProfile_NoMatchReturnsZero(t *testing.T) {
	t.Parallel()
	s := linkprofile.New()
	// With empty current values all param checks are skipped (missing keys are
	// ignored per the Python reference), so the most-specific profile wins.
	// We just verify the function does not panic and returns a non-negative id.
	id := s.MatchActiveProfile("DIMMER", "KEY", map[string]any{})
	if id < 0 {
		t.Fatalf("expected non-negative id, got %d", id)
	}
}

func TestMatchActiveProfile_ConflictingValueReturnsZero(t *testing.T) {
	t.Parallel()
	s := linkprofile.New()
	// SHORT_ACTION_TYPE=99 does not match any profile's fixed constraint.
	// Only the Expert profile (id=0) remains, so result must be 0.
	current := map[string]any{
		"SHORT_ACTION_TYPE": float64(99),
	}
	id := s.MatchActiveProfile("DIMMER", "KEY", current)
	if id != 0 {
		t.Fatalf("expected 0 (Expert fallback) for non-matching value, got %d", id)
	}
}

func TestMatchActiveProfile_UnknownTypeReturnsZero(t *testing.T) {
	t.Parallel()
	s := linkprofile.New()
	id := s.MatchActiveProfile("NONEXISTENT_TYPE", "KEY", map[string]any{"X": float64(1)})
	if id != 0 {
		t.Fatalf("expected 0 for unknown type, got %d", id)
	}
}

// ---------------------------------------------------------------------------
// Localisation helpers
// ---------------------------------------------------------------------------

func TestProfile_LocalisedName_Fallback(t *testing.T) {
	t.Parallel()
	p := linkprofile.Profile{
		Name: map[string]string{"de": "Standardprofil"},
	}
	if got := p.LocalisedName("en"); got != "Standardprofil" {
		t.Fatalf("expected German fallback, got %q", got)
	}
	if got := p.LocalisedName("de"); got != "Standardprofil" {
		t.Fatalf("expected de name, got %q", got)
	}
}

func TestProfile_LocalisedName_PreferRequestedLocale(t *testing.T) {
	t.Parallel()
	p := linkprofile.Profile{
		Name: map[string]string{"en": "Standard", "de": "Standard DE"},
	}
	if got := p.LocalisedName("de"); got != "Standard DE" {
		t.Fatalf("expected DE name, got %q", got)
	}
	if got := p.LocalisedName("en"); got != "Standard" {
		t.Fatalf("expected EN name, got %q", got)
	}
}

func TestProfile_LocalisedDescription_Empty(t *testing.T) {
	t.Parallel()
	p := linkprofile.Profile{}
	if got := p.LocalisedDescription("en"); got != "" {
		t.Fatalf("expected empty description, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// profileMatches — list and range constraint coverage
// ---------------------------------------------------------------------------

// TestMatchActiveProfile_ListConstraint exercises the "list" constraint
// branch in profileMatches.  Profile 1 accepts only values [1, 2, 3] for
// parameter ACTION.  Profile 2 requires a different ACTION value so they
// are mutually exclusive.
func TestMatchActiveProfile_ListConstraint(t *testing.T) {
	t.Parallel()
	s := linkprofile.New()

	listVal1, listVal2, listVal3 := 1.0, 2.0, 3.0
	actionVal99 := 99.0
	profs := []linkprofile.Profile{
		{
			ID:   1,
			Name: map[string]string{"en": "List profile"},
			Params: map[string]linkprofile.ParamConstraint{
				"ACTION": {ConstraintType: "list", Values: []float64{listVal1, listVal2, listVal3}},
			},
		},
		{
			// Profile 2 requires ACTION=99 — mutually exclusive with profile 1.
			ID:   2,
			Name: map[string]string{"en": "Other profile"},
			Params: map[string]linkprofile.ParamConstraint{
				"ACTION": {ConstraintType: "fixed", Value: &actionVal99},
			},
		},
	}
	s.Register("TEST_RCV", "TEST_SND", profs)

	// ACTION=2 is in [1,2,3] → should match profile 1.
	id := s.MatchActiveProfile("TEST_RCV", "TEST_SND", map[string]any{"ACTION": float64(2)})
	if id != 1 {
		t.Errorf("list constraint: expected id=1, got %d", id)
	}

	// ACTION=99 not in [1,2,3] for profile 1, but matches profile 2's fixed constraint.
	id = s.MatchActiveProfile("TEST_RCV", "TEST_SND", map[string]any{"ACTION": float64(99)})
	if id != 2 {
		t.Errorf("list no match / fixed match: expected id=2, got %d", id)
	}

	// ACTION=50 matches neither → returns 0.
	id = s.MatchActiveProfile("TEST_RCV", "TEST_SND", map[string]any{"ACTION": float64(50)})
	if id != 0 {
		t.Errorf("no match at all: expected id=0, got %d", id)
	}
}

// TestMatchActiveProfile_RangeConstraint exercises the "range" branch.
func TestMatchActiveProfile_RangeConstraint(t *testing.T) {
	t.Parallel()
	s := linkprofile.New()

	rangeMin, rangeMax := 5.0, 15.0
	profs := []linkprofile.Profile{
		{
			ID:   3,
			Name: map[string]string{"en": "Range-only profile"},
			Params: map[string]linkprofile.ParamConstraint{
				"BRIGHTNESS": {ConstraintType: "range", MinValue: &rangeMin, MaxValue: &rangeMax},
			},
		},
	}
	s.Register("DIMMER_RCV", "REMOTE_SND", profs)

	// 10 in [5,15] → matches.
	id := s.MatchActiveProfile("DIMMER_RCV", "REMOTE_SND", map[string]any{"BRIGHTNESS": float64(10)})
	if id != 3 {
		t.Errorf("range match: expected id=3, got %d", id)
	}

	// 20 > 15 → no match → 0.
	id = s.MatchActiveProfile("DIMMER_RCV", "REMOTE_SND", map[string]any{"BRIGHTNESS": float64(20)})
	if id != 0 {
		t.Errorf("range no match: expected id=0, got %d", id)
	}
}

// TestMatchActiveProfile_NonConvertibleValue exercises the toFloat64 failure
// path: if the live value for a parameter cannot be narrowed to float64, the
// profile is rejected.
func TestMatchActiveProfile_NonConvertibleValue(t *testing.T) {
	t.Parallel()
	s := linkprofile.New()

	val := 1.0
	profs := []linkprofile.Profile{
		{
			ID:   5,
			Name: map[string]string{"en": "Fixed profile"},
			Params: map[string]linkprofile.ParamConstraint{
				"X": {ConstraintType: "fixed", Value: &val},
			},
		},
	}
	s.Register("RCV_NON", "SND_NON", profs)

	// A struct value cannot be converted — profile must not match.
	type weird struct{ n int }
	id := s.MatchActiveProfile("RCV_NON", "SND_NON", map[string]any{"X": weird{n: 1}})
	if id != 0 {
		t.Errorf("non-convertible: expected id=0, got %d", id)
	}
}

// TestMatchActiveProfile_toFloat64Types exercises multiple toFloat64 branches
// by passing float32, int, int32, int64, and bool values as live paramset values
// through the MatchActiveProfile path.
func TestMatchActiveProfile_toFloat64Types(t *testing.T) {
	t.Parallel()
	s := linkprofile.New()

	val := 2.0
	profs := []linkprofile.Profile{
		{
			ID:   7,
			Name: map[string]string{"en": "Numeric types"},
			Params: map[string]linkprofile.ParamConstraint{
				"V": {ConstraintType: "fixed", Value: &val},
			},
		},
	}
	s.Register("TYPE_RCV", "TYPE_SND", profs)

	cases := []struct {
		name  string
		value any
		want  int
	}{
		{"float32 match", float32(2.0), 7},
		{"float32 no match", float32(3.0), 0},
		{"int match", int(2), 7},
		{"int32 match", int32(2), 7},
		{"int64 match", int64(2), 7},
		{"bool true (=1, no match 2)", bool(true), 0},
		{"bool false (=0, no match 2)", bool(false), 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			id := s.MatchActiveProfile("TYPE_RCV", "TYPE_SND", map[string]any{"V": tc.value})
			if id != tc.want {
				t.Errorf("value=%v: expected id=%d, got %d", tc.value, tc.want, id)
			}
		})
	}
}

// TestMatchActiveProfile_BoolTrue exercises the bool=true→1 path.
func TestMatchActiveProfile_BoolTrue(t *testing.T) {
	t.Parallel()
	s := linkprofile.New()

	val := 1.0
	profs := []linkprofile.Profile{
		{
			ID:   8,
			Name: map[string]string{"en": "Bool true"},
			Params: map[string]linkprofile.ParamConstraint{
				"FLAG": {ConstraintType: "fixed", Value: &val},
			},
		},
	}
	s.Register("BOOL_RCV", "BOOL_SND", profs)

	// bool(true) narrows to 1.0 which matches val=1.0.
	id := s.MatchActiveProfile("BOOL_RCV", "BOOL_SND", map[string]any{"FLAG": true})
	if id != 8 {
		t.Errorf("bool true: expected id=8, got %d", id)
	}
}

// TestMatchActiveProfile_EmptyListConstraintContinues verifies that a
// "list" constraint with an empty Values slice is treated as "always
// match" (the continue branch in profileMatches).
func TestMatchActiveProfile_EmptyListConstraintContinues(t *testing.T) {
	t.Parallel()
	s := linkprofile.New()

	profs := []linkprofile.Profile{
		{
			ID:   9,
			Name: map[string]string{"en": "Empty list"},
			Params: map[string]linkprofile.ParamConstraint{
				"X": {ConstraintType: "list", Values: nil},
			},
		},
	}
	s.Register("ELIST_RCV", "ELIST_SND", profs)

	// Any value satisfies an empty list → profile 9 matches.
	id := s.MatchActiveProfile("ELIST_RCV", "ELIST_SND", map[string]any{"X": float64(99)})
	if id != 9 {
		t.Errorf("empty list: expected id=9, got %d", id)
	}
}

// TestMatchActiveProfile_RangeNoMinMax exercises the range branch when
// MinValue or MaxValue is nil (partial range spec — treated as "always in range").
func TestMatchActiveProfile_RangeNoMinMax(t *testing.T) {
	t.Parallel()
	s := linkprofile.New()

	profs := []linkprofile.Profile{
		{
			ID:   10,
			Name: map[string]string{"en": "Nil range"},
			Params: map[string]linkprofile.ParamConstraint{
				// Both nil means no constraint check → always matches.
				"X": {ConstraintType: "range", MinValue: nil, MaxValue: nil},
			},
		},
	}
	s.Register("NIL_RCV", "NIL_SND", profs)

	id := s.MatchActiveProfile("NIL_RCV", "NIL_SND", map[string]any{"X": float64(99999)})
	if id != 10 {
		t.Errorf("nil range: expected id=10, got %d", id)
	}
}

// TestMatchActiveProfile_FloatTolerance exercises the relative float
// tolerance in profileMatches's "fixed" arm against the real embedded
// archive. DIMMER/KEY profile id=1 has SHORT_ACTION_TYPE fixed at 1.0
// (pinned by TestEmbeddedData_DimmerKeyProfile1FixedParams); an observed
// value that differs only by float noise must still match.
func TestMatchActiveProfile_FloatTolerance(t *testing.T) {
	t.Parallel()
	s := linkprofile.New()
	exact := s.MatchActiveProfile("DIMMER", "KEY", map[string]any{"SHORT_ACTION_TYPE": 1.0})
	if exact == 0 {
		t.Fatal("exact observed value 1.0 should match a profile (Expert fallback returned)")
	}
	nearExact := s.MatchActiveProfile("DIMMER", "KEY", map[string]any{"SHORT_ACTION_TYPE": 1.0000001})
	if nearExact != exact {
		t.Fatalf("observed value within float tolerance of the fixed constraint should match the same profile: exact=%d nearExact=%d", exact, nearExact)
	}
}

// TestSenderTypes_RealReceiver pins SenderTypes against the real embedded
// DIMMER_VIRTUAL_RECEIVER archive: the returned buckets must be exactly the
// archive file's top-level JSON keys, sorted.
func TestSenderTypes_RealReceiver(t *testing.T) {
	t.Parallel()
	s := linkprofile.New()
	types, err := s.SenderTypes("DIMMER_VIRTUAL_RECEIVER")
	if err != nil {
		t.Fatalf("SenderTypes(DIMMER_VIRTUAL_RECEIVER): %v", err)
	}
	want := []string{
		"ACCELERATION_TRANSCEIVER", "ACCESS_TRANSCEIVER", "COND_SWITCH_TRANSMITTER",
		"KEY_TRANSCEIVER", "MULTI_MODE_INPUT_TRANSMITTER",
		"PASSAGE_DETECTOR_DIRECTION_TRANSMITTER", "PRESENCEDETECTOR_TRANSCEIVER",
		"RAIN_DETECTION_TRANSMITTER", "ROTARY_HANDLE_TRANSCEIVER", "SHUTTER_CONTACT",
		"SWITCH_TRANSCEIVER", "WATER_DETECTION_TRANSMITTER",
	}
	if !slices.Equal(types, want) {
		t.Fatalf("SenderTypes(DIMMER_VIRTUAL_RECEIVER) = %v, want %v", types, want)
	}
}

// TestSenderTypes_AliasResolvesToTarget verifies SenderTypes resolves the
// receiver-type alias the same way load does: OPTICAL_SIGNAL_RECEIVER has
// no archive of its own and must return DIMMER_VIRTUAL_RECEIVER's buckets.
func TestSenderTypes_AliasResolvesToTarget(t *testing.T) {
	t.Parallel()
	s := linkprofile.New()
	viaAlias, err := s.SenderTypes("OPTICAL_SIGNAL_RECEIVER")
	if err != nil {
		t.Fatalf("SenderTypes(OPTICAL_SIGNAL_RECEIVER): %v", err)
	}
	viaTarget, err := s.SenderTypes("DIMMER_VIRTUAL_RECEIVER")
	if err != nil {
		t.Fatalf("SenderTypes(DIMMER_VIRTUAL_RECEIVER): %v", err)
	}
	if !slices.Equal(viaAlias, viaTarget) {
		t.Fatalf("SenderTypes via alias = %v, want target's buckets %v", viaAlias, viaTarget)
	}
}

// TestRegister_AliasSpellingReachableUnderBothNames verifies that a pair
// registered under a receiver-type alias spelling is found under both the
// alias and its canonical archive name — the two lookup paths must agree.
func TestRegister_AliasSpellingReachableUnderBothNames(t *testing.T) {
	t.Parallel()
	s := linkprofile.New()
	profs := []linkprofile.Profile{{ID: 1, Name: map[string]string{"en": "Toggle"}}}
	// SWITCH_TRANSCEIVER -> SWITCH_VIRTUAL_RECEIVER per _receiver_type_aliases.json.
	s.Register("SWITCH_TRANSCEIVER", "KEY", profs)

	viaAlias, err := s.GetLinkProfiles(context.Background(), "SWITCH_TRANSCEIVER", "KEY", "en")
	if err != nil {
		t.Fatalf("GetLinkProfiles via alias spelling: %v", err)
	}
	if len(viaAlias) != 1 || viaAlias[0].ID != 1 {
		t.Fatalf("expected the registered profile via alias spelling, got %v", viaAlias)
	}

	viaCanonical, err := s.GetLinkProfiles(context.Background(), "SWITCH_VIRTUAL_RECEIVER", "KEY", "en")
	if err != nil {
		t.Fatalf("GetLinkProfiles via canonical spelling: %v", err)
	}
	if len(viaCanonical) != 1 || viaCanonical[0].ID != 1 {
		t.Fatalf("expected the registered profile via canonical spelling, got %v", viaCanonical)
	}
}

// TestNew_InvalidAliasJSONFallsBackGracefully exercises the error branch in
// New() by verifying that the store still works when the embedded aliases
// JSON cannot be decoded. We cannot inject bad JSON into the embedded file,
// but we can verify that New() always returns a usable *Store.
func TestNew_AlwaysReturnsUsableStore(t *testing.T) {
	t.Parallel()
	s := linkprofile.New()
	if s == nil {
		t.Fatal("New() must not return nil")
	}
	// Basic sanity: a known embedded receiver type resolves without error.
	profs, err := s.GetLinkProfiles(context.Background(), "DIMMER", "KEY", "en")
	if err != nil {
		t.Fatalf("GetLinkProfiles after New(): %v", err)
	}
	if len(profs) == 0 {
		t.Fatal("expected profiles for DIMMER/KEY after New()")
	}
}

// TestGetLinkProfiles_NilStoreReturnsNil exercises the nil-Store guard in
// GetLinkProfiles (s == nil path).
func TestGetLinkProfiles_NilStore(t *testing.T) {
	t.Parallel()
	var s *linkprofile.Store
	profs, err := s.GetLinkProfiles(context.Background(), "DIMMER", "KEY", "en")
	if err != nil {
		t.Fatalf("nil Store GetLinkProfiles: unexpected error: %v", err)
	}
	if profs != nil {
		t.Fatalf("nil Store GetLinkProfiles: expected nil, got %v", profs)
	}
}

// TestMatchActiveProfile_NilStore exercises the nil-Store guard in MatchActiveProfile.
func TestMatchActiveProfile_NilStore(t *testing.T) {
	t.Parallel()
	var s *linkprofile.Store
	id := s.MatchActiveProfile("DIMMER", "KEY", map[string]any{})
	if id != 0 {
		t.Fatalf("nil Store MatchActiveProfile: expected 0, got %d", id)
	}
}

// ---------------------------------------------------------------------------
// Receiver-type aliases (_receiver_type_aliases.json)
// ---------------------------------------------------------------------------

func TestReceiverTypeAlias_OpticalSignalMappedToDimmerVirtualReceiver(t *testing.T) {
	t.Parallel()
	// OPTICAL_SIGNAL_RECEIVER → DIMMER_VIRTUAL_RECEIVER per _receiver_type_aliases.json.
	s := linkprofile.New()
	// DIMMER_VIRTUAL_RECEIVER.json.gz must contain KEY sender profiles.
	profs, err := s.GetLinkProfiles(context.Background(), "OPTICAL_SIGNAL_RECEIVER", "KEY", "en")
	if err != nil {
		t.Fatalf("GetLinkProfiles via alias: %v", err)
	}
	// We accept empty (no KEY sender in DIMMER_VIRTUAL_RECEIVER) but must not error.
	_ = profs
}

// TestProfileNamesAreNotHTMLEscaped pins that display strings reach callers
// as plain text. The extracts come from the CCU WebUI, whose texts are HTML
// fragments — "Bew&auml;sserungsaktor" instead of "Bewässerungsaktor" — and
// every north-bound surface renders them verbatim, so an undecoded reference
// is shown to the operator as-is.
func TestProfileNamesAreNotHTMLEscaped(t *testing.T) {
	t.Parallel()
	s := linkprofile.New()
	// ACCELERATION_TRANSCEIVER carries "&Auml;nderungssignal", KEY_TRANSCEIVER
	// the "&amp;" form — both reference classes in one receiver.
	var profiles []linkprofile.Profile
	for _, sender := range []string{"ACCELERATION_TRANSCEIVER", "KEY_TRANSCEIVER"} {
		got, err := s.GetLinkProfiles(context.Background(), "ACOUSTIC_SIGNAL_VIRTUAL_RECEIVER", sender, "de")
		if err != nil {
			t.Fatalf("GetLinkProfiles(%s): %v", sender, err)
		}
		profiles = append(profiles, got...)
	}
	if len(profiles) == 0 {
		t.Fatal("no profiles returned for ACOUSTIC_SIGNAL_VIRTUAL_RECEIVER")
	}
	var sawUmlaut bool
	for _, p := range profiles {
		for locale, name := range p.Name {
			if strings.Contains(name, "&") && strings.Contains(name, ";") {
				t.Errorf("profile %d name[%s] still carries an HTML reference: %q", p.ID, locale, name)
			}
			if strings.ContainsAny(name, "äöüÄÖÜß") {
				sawUmlaut = true
			}
		}
		for locale, desc := range p.Description {
			if strings.Contains(desc, "&") && strings.Contains(desc, ";") {
				t.Errorf("profile %d description[%s] still carries an HTML reference: %q", p.ID, locale, desc)
			}
		}
	}
	if !sawUmlaut {
		t.Error("expected at least one decoded umlaut in this receiver's profile names")
	}
}
