// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ccudata

import "testing"

// TestDoorbellModelsCuratedSet pins the curated doorbell-model set
// loaded from embedded/device_semantics.json: exactly the three models
// whose press/ring channel is a doorbell rather than a generic button.
func TestDoorbellModelsCuratedSet(t *testing.T) {
	t.Parallel()

	got := DoorbellModels()
	want := []string{"HM-Sen-DB-PCB", "HmIP-DBB", "HmIP-DSD-PCB"}

	if len(got) != len(want) {
		t.Fatalf("DoorbellModels() has %d entries, want %d: got=%v", len(got), len(want), got)
	}
	for _, m := range want {
		if _, ok := got[m]; !ok {
			t.Errorf("DoorbellModels() missing curated model %q; got=%v", m, got)
		}
	}
}

// TestDoorbellModelsRejectsNonCuratedModel confirms the set does not
// classify an arbitrary non-doorbell model as a doorbell.
func TestDoorbellModelsRejectsNonCuratedModel(t *testing.T) {
	t.Parallel()

	if _, ok := DoorbellModels()["HmIP-WRC2"]; ok {
		t.Error("DoorbellModels() must not contain the generic remote HmIP-WRC2")
	}
}

// TestDoorbellModelsStableAcrossCalls verifies the sync.Once-guarded
// loader returns the same populated set on repeated calls (the
// once.Do body only runs once per process).
func TestDoorbellModelsStableAcrossCalls(t *testing.T) {
	t.Parallel()

	first := DoorbellModels()
	second := DoorbellModels()

	if len(first) == 0 {
		t.Fatal("DoorbellModels() returned an empty set on first call")
	}
	if len(first) != len(second) {
		t.Fatalf("DoorbellModels() set size changed across calls: first=%d second=%d", len(first), len(second))
	}
	for m := range first {
		if _, ok := second[m]; !ok {
			t.Errorf("DoorbellModels() second call missing %q present in first call", m)
		}
	}
}
