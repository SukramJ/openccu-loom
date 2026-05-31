// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"encoding/json"
	"testing"
)

// Raw doc that mirrors an excerpt.
// DIMMER_VIRTUAL_RECEIVER.json with two sender types and three
// profiles on the first sender (one Expert / id=0 and two real ones).
const fixtureProfileRaw = `{
  "KEY_TRANSCEIVER": {
    "profiles": [
      {"id": 0, "name": {"en": "Expert"}},
      {"id": 1, "name": {"en": "On/brighter"}, "params": {
        "LONG_CT_ON": {"constraint_type": "fixed", "value": 0},
        "LONG_DIM_MAX_LEVEL": {"constraint_type": "range", "min_value": 0, "max_value": 1.0, "default": 1.0},
        "LONG_COND_VALUE_LO": {"constraint_type": "list", "values": [50, 60]}
      }},
      {"id": 2, "name": {"en": "Off/darker"}, "params": {
        "LONG_CT_ON": {"constraint_type": "fixed", "value": 1},
        "LONG_DIM_MAX_LEVEL": {"constraint_type": "range", "min_value": 0, "max_value": 1.0}
      }}
    ]
  },
  "UNRELATED_SENDER": {
    "profiles": [
      {"id": 5, "name": {"en": "Unrelated"}, "params": {
        "X": {"constraint_type": "fixed", "value": 1}
      }}
    ]
  }
}`

func TestFilterProfileDocBySender(t *testing.T) {
	t.Parallel()
	filtered, defs, key, err := filterProfileDocBySender(json.RawMessage(fixtureProfileRaw), "KEY_TRANSCEIVER")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if key != "KEY_TRANSCEIVER" {
		t.Errorf("resolved key: got %q, want %q", key, "KEY_TRANSCEIVER")
	}
	// Filtered doc has only one sender type.
	var m map[string]any
	if err := json.Unmarshal(filtered, &m); err != nil {
		t.Fatalf("unmarshal filtered: %v", err)
	}
	if len(m) != 1 {
		t.Fatalf("expected 1 sender in filtered doc, got %d (%v)", len(m), m)
	}
	if _, ok := m["KEY_TRANSCEIVER"]; !ok {
		t.Fatalf("expected KEY_TRANSCEIVER in filtered doc")
	}
	if got := len(defs); got != 3 {
		t.Errorf("defs len: got %d, want 3", got)
	}
}

func TestFilterProfileDocBySenderMissingSender(t *testing.T) {
	t.Parallel()
	filtered, defs, key, err := filterProfileDocBySender(json.RawMessage(fixtureProfileRaw), "NOPE")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if filtered != nil || defs != nil || key != "" {
		t.Errorf("missing sender should return nil/nil/\"\", got %v / %v / %q", filtered, defs, key)
	}
}

func TestFilterProfileDocBySenderVirtualFallback(t *testing.T) {
	t.Parallel()
	// HmIP exposes *_VIRTUAL_TRANSCEIVER channel types that the
	// The filter must fall back
	// through the resolution chain: exact → alias → strip _VIRTUAL_
	// → alias of stripped form.
	_, defs, key, err := filterProfileDocBySender(json.RawMessage(fixtureProfileRaw), "KEY_VIRTUAL_TRANSCEIVER")
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	if key != "KEY_TRANSCEIVER" {
		t.Errorf("virtual fallback key: got %q, want %q", key, "KEY_TRANSCEIVER")
	}
	if len(defs) == 0 {
		t.Errorf("expected profiles, got none")
	}
}

func TestFilterProfileDocBySenderSemanticAlias(t *testing.T) {
	t.Parallel()
	// MOTIONDETECTOR_VIRTUAL_TRANSCEIVER (HmIP motion sensor) is
	// functionally equivalent to PRESENCEDETECTOR_TRANSCEIVER in
	// the archives. The alias table lifts that mapping.
	raw := `{"PRESENCEDETECTOR_TRANSCEIVER": {"profiles": [
	  {"id": 0, "name": {"en": "Expert"}},
	  {"id": 1, "name": {"en": "Light"}, "params": {
	    "SHORT_CT_ON": {"constraint_type": "fixed", "value": 0}
	  }}
	]}}`
	_, defs, key, err := filterProfileDocBySender(json.RawMessage(raw), "MOTIONDETECTOR_VIRTUAL_TRANSCEIVER")
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	if key != "PRESENCEDETECTOR_TRANSCEIVER" {
		t.Errorf("alias key: got %q, want PRESENCEDETECTOR_TRANSCEIVER", key)
	}
	if len(defs) != 2 {
		t.Errorf("expected 2 profiles, got %d", len(defs))
	}
}

func TestMatchActiveProfile(t *testing.T) {
	t.Parallel()
	_, defs, _, err := filterProfileDocBySender(json.RawMessage(fixtureProfileRaw), "KEY_TRANSCEIVER")
	if err != nil {
		t.Fatalf("filter: %v", err)
	}

	t.Run("matches fixed + range + list profile", func(t *testing.T) {
		t.Parallel()
		current := map[string]any{
			"LONG_CT_ON":         0.0,
			"LONG_DIM_MAX_LEVEL": 0.8,
			"LONG_COND_VALUE_LO": 50.0,
		}
		if got := matchActiveProfile(defs, current); got != 1 {
			t.Errorf("got %d, want 1", got)
		}
	})

	t.Run("picks different profile when fixed differs", func(t *testing.T) {
		t.Parallel()
		current := map[string]any{
			"LONG_CT_ON":         1.0,
			"LONG_DIM_MAX_LEVEL": 0.5,
		}
		if got := matchActiveProfile(defs, current); got != 2 {
			t.Errorf("got %d, want 2", got)
		}
	})

	t.Run("returns 0 when no profile matches", func(t *testing.T) {
		t.Parallel()
		current := map[string]any{"LONG_CT_ON": 42.0}
		if got := matchActiveProfile(defs, current); got != 0 {
			t.Errorf("got %d, want 0", got)
		}
	})

	t.Run("empty current values pick the least-loose profile", func(t *testing.T) {
		t.Parallel()
		// With empty values every profile vacuously matches; highest
		// specificity wins. Profile 1 has 1 fixed + 2 loose (score
		// -199), profile 2 has 1 fixed + 1 loose (score -99) → 2 wins.
		current := map[string]any{}
		if got := matchActiveProfile(defs, current); got != 2 {
			t.Errorf("got %d, want 2", got)
		}
	})

	t.Run("string values get coerced to float", func(t *testing.T) {
		t.Parallel()
		current := map[string]any{
			"LONG_CT_ON": "0",
		}
		if got := matchActiveProfile(defs, current); got != 1 {
			t.Errorf("got %d, want 1 (got from string→float coercion)", got)
		}
	})
}

func TestMatchActiveProfileSpecificity(t *testing.T) {
	t.Parallel()
	// Two profiles with overlapping fixed constraints — the more
	// specific one (more fixed) should win.
	raw := `{
	  "S": {"profiles": [
	    {"id": 1, "name": {"en": "Less"}, "params": {
	      "A": {"constraint_type": "fixed", "value": 1}
	    }},
	    {"id": 2, "name": {"en": "More"}, "params": {
	      "A": {"constraint_type": "fixed", "value": 1},
	      "B": {"constraint_type": "fixed", "value": 2}
	    }}
	  ]}
	}`
	_, defs, _, err := filterProfileDocBySender(json.RawMessage(raw), "S")
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	current := map[string]any{"A": 1.0, "B": 2.0}
	if got := matchActiveProfile(defs, current); got != 2 {
		t.Errorf("got %d, want 2", got)
	}
}
