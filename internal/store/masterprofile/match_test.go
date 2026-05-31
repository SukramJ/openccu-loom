// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package masterprofile

import "testing"

// matchTestStore builds a Store backed by a hand-crafted profile set —
// avoids the embedded archive so tests pin the matching logic itself.
func matchTestStore(t *testing.T, profiles []Profile) *Store {
	t.Helper()
	s := New()
	s.cache["TYPE-A"] = map[string][]Profile{"KEY": profiles}
	return s
}

func TestMatchActiveProfile_NoMatchReturnsZero(t *testing.T) {
	t.Parallel()
	s := matchTestStore(t, []Profile{
		{ID: 0, Name: map[string]string{"en": "Expert"}},
		{ID: 1, Name: map[string]string{"en": "Eco"}, Params: map[string]ParamConstraint{
			"TEMP": {ConstraintType: "fixed", Value: 18.0},
		}},
	})
	got := s.MatchActiveProfile("TYPE-A", "KEY", map[string]any{
		"TEMP": 22.0, // does not match the Eco profile
	})
	if got != 0 {
		t.Fatalf("MatchActiveProfile = %d, want 0 (no match)", got)
	}
}

func TestMatchActiveProfile_FixedConstraintMatch(t *testing.T) {
	t.Parallel()
	s := matchTestStore(t, []Profile{
		{ID: 0},
		{ID: 1, Name: map[string]string{"en": "Eco"}, Params: map[string]ParamConstraint{
			"TEMP": {ConstraintType: "fixed", Value: 18.0},
			"MODE": {ConstraintType: "fixed", Value: "ECO"},
		}},
	})
	got := s.MatchActiveProfile("TYPE-A", "KEY", map[string]any{
		"TEMP": 18.0,
		"MODE": "ECO",
	})
	if got != 1 {
		t.Fatalf("MatchActiveProfile = %d, want 1", got)
	}
}

func TestMatchActiveProfile_FloatTolerance(t *testing.T) {
	t.Parallel()
	s := matchTestStore(t, []Profile{
		{ID: 0},
		{ID: 1, Params: map[string]ParamConstraint{
			"TEMP": {ConstraintType: "fixed", Value: 18.5},
		}},
	})
	got := s.MatchActiveProfile("TYPE-A", "KEY", map[string]any{
		"TEMP": 18.5000001, // within 1e-6 tolerance
	})
	if got != 1 {
		t.Fatalf("MatchActiveProfile = %d, want 1 (float tolerance)", got)
	}
}

func TestMatchActiveProfile_HighestScoreWins(t *testing.T) {
	t.Parallel()
	// Profile 1: 1 fixed constraint (TEMP).
	// Profile 2: 2 fixed constraints (TEMP + MODE).
	// Both match; profile 2 has the higher score (2 > 1).
	s := matchTestStore(t, []Profile{
		{ID: 0},
		{ID: 1, Params: map[string]ParamConstraint{
			"TEMP": {ConstraintType: "fixed", Value: 18.0},
		}},
		{ID: 2, Params: map[string]ParamConstraint{
			"TEMP": {ConstraintType: "fixed", Value: 18.0},
			"MODE": {ConstraintType: "fixed", Value: "ECO"},
		}},
	})
	got := s.MatchActiveProfile("TYPE-A", "KEY", map[string]any{
		"TEMP": 18.0,
		"MODE": "ECO",
	})
	if got != 2 {
		t.Fatalf("MatchActiveProfile = %d, want 2 (higher score)", got)
	}
}

func TestMatchActiveProfile_RangeConstraintAccepts(t *testing.T) {
	t.Parallel()
	s := matchTestStore(t, []Profile{
		{ID: 0},
		{ID: 1, Params: map[string]ParamConstraint{
			"TEMP": {ConstraintType: "range", Value: []any{15.0, 22.0}},
			"MODE": {ConstraintType: "fixed", Value: "ECO"},
		}},
	})
	if got := s.MatchActiveProfile("TYPE-A", "KEY", map[string]any{
		"TEMP": 18.0,
		"MODE": "ECO",
	}); got != 1 {
		t.Fatalf("range constraint match — got %d, want 1", got)
	}
}

func TestMatchActiveProfile_RangeConstraintRejects(t *testing.T) {
	t.Parallel()
	s := matchTestStore(t, []Profile{
		{ID: 0},
		{ID: 1, Params: map[string]ParamConstraint{
			"TEMP": {ConstraintType: "range", Value: []any{15.0, 22.0}},
		}},
	})
	if got := s.MatchActiveProfile("TYPE-A", "KEY", map[string]any{
		"TEMP": 30.0, // outside range
	}); got != 0 {
		t.Fatalf("out-of-range — got %d, want 0", got)
	}
}

func TestMatchActiveProfile_ListConstraint(t *testing.T) {
	t.Parallel()
	s := matchTestStore(t, []Profile{
		{ID: 0},
		{ID: 1, Params: map[string]ParamConstraint{
			"MODE": {ConstraintType: "list", Value: []any{"ECO", "AWAY"}},
		}},
	})
	if got := s.MatchActiveProfile("TYPE-A", "KEY", map[string]any{
		"MODE": "ECO",
	}); got != 1 {
		t.Fatal("list constraint must match ECO")
	}
	if got := s.MatchActiveProfile("TYPE-A", "KEY", map[string]any{
		"MODE": "BOOST",
	}); got != 0 {
		t.Fatal("list constraint must NOT match BOOST")
	}
}

func TestMatchActiveProfile_UnknownDeviceTypeReturnsZero(t *testing.T) {
	t.Parallel()
	s := New()
	got := s.MatchActiveProfile("UNKNOWN", "KEY", map[string]any{"TEMP": 18.0})
	if got != 0 {
		t.Fatalf("unknown device-type must yield 0, got %d", got)
	}
}

func TestMatchActiveProfile_MissingValueIsIgnored(t *testing.T) {
	t.Parallel()
	// A profile with one constraint, but the observed values miss it. The
	// non-Expert profile still wins with score=0 because best_score starts at
	// -1.
	s := matchTestStore(t, []Profile{
		{ID: 0},
		{ID: 1, Params: map[string]ParamConstraint{
			"TEMP": {ConstraintType: "fixed", Value: 18.0},
		}},
	})
	got := s.MatchActiveProfile("TYPE-A", "KEY", map[string]any{
		// no TEMP key
		"OTHER": 1,
	})
	if got != 1 {
		t.Fatalf("missing values yield score=0 → first non-Expert profile wins, got %d", got)
	}
}

func TestMatchActiveProfile_ViolatedConstraintDisqualifies(t *testing.T) {
	t.Parallel()
	// Profile 1 has TEMP=18 (fixed). Observed TEMP=22 — violates →
	// profile is disqualified (score=-1) and Expert (id=0) wins.
	s := matchTestStore(t, []Profile{
		{ID: 0},
		{ID: 1, Params: map[string]ParamConstraint{
			"TEMP": {ConstraintType: "fixed", Value: 18.0},
		}},
	})
	got := s.MatchActiveProfile("TYPE-A", "KEY", map[string]any{
		"TEMP": 22.0,
	})
	if got != 0 {
		t.Fatalf("violated constraint must disqualify the profile, got %d", got)
	}
}
