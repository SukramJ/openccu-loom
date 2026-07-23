// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package history

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"

	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

func newOverlay(t *testing.T, include, exclude []string) (*RecordingOverrides, *sqlite.RecordingOverrideStore) {
	t.Helper()
	db := openStore(t).DB()
	os := sqlite.NewRecordingOverrideStore(db)
	return NewRecordingOverrides(os, include, exclude), os
}

func TestOverrides_DecideNilSafe(t *testing.T) {
	t.Parallel()
	var o *RecordingOverrides
	if !o.Decide("c", "i", "ch", "p", true) {
		t.Error("nil overlay must return the policy decision unchanged (true)")
	}
	if o.Decide("c", "i", "ch", "p", false) {
		t.Error("nil overlay must return the policy decision unchanged (false)")
	}
}

func TestOverrides_EffectiveAndPrecedence(t *testing.T) {
	t.Parallel()
	o, _ := newOverlay(t, nil, nil)
	ctx := context.Background()

	// No override yet → policy decides.
	if rec, src := o.Effective("c", "i", "ch", "TEMPERATURE"); !rec || src != "policy" {
		t.Errorf("no override: got (%v,%q), want (true,policy)", rec, src)
	}
	// Force off.
	if err := o.Set(ctx, "c", "i", "ch", "TEMPERATURE", false, "u"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if rec, src := o.Effective("c", "i", "ch", "TEMPERATURE"); rec || src != "override" {
		t.Errorf("after force-off: got (%v,%q), want (false,override)", rec, src)
	}
	// Decide honours the override over the policy.
	if o.Decide("c", "i", "ch", "TEMPERATURE", true) {
		t.Error("Decide must return the override (false) over the policy (true)")
	}
	// Clear → back to policy.
	if err := o.Clear(ctx, "c", "i", "ch", "TEMPERATURE"); err != nil {
		t.Fatalf("Clear: %v", err)
	}
	if rec, src := o.Effective("c", "i", "ch", "TEMPERATURE"); !rec || src != "policy" {
		t.Errorf("after clear: got (%v,%q), want (true,policy)", rec, src)
	}
}

func TestOverrides_LoadPopulatesFromStore(t *testing.T) {
	t.Parallel()
	o, store := newOverlay(t, nil, nil)
	ctx := context.Background()
	if err := store.Set(ctx, "c", "i", "ch", "P", false, "u"); err != nil {
		t.Fatalf("store.Set: %v", err)
	}
	if err := o.Load(ctx); err != nil {
		t.Fatalf("Load: %v", err)
	}
	if o.Decide("c", "i", "ch", "P", true) {
		t.Error("Load must surface the persisted force-off override")
	}
}

// TestRecorder_OverrideForceOffDropsIncludedDP: a DP the glob policy would
// record is dropped when an explicit force-off override is present.
func TestRecorder_OverrideForceOffDropsIncludedDP(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	reg, u, _, ch := centralWithDevice(t, "ccu-01", "DEV001", "DEV001:1")

	dp := floatDP("DEV001:1", "TEMPERATURE")
	ch.Put(dp)
	dp.OnEvent(21.5)

	overlay := NewRecordingOverrides(sqlite.NewRecordingOverrideStore(store.DB()), nil, nil)
	if err := overlay.Set(ctx, "ccu-01", "HmIP-RF", "DEV001:1", "TEMPERATURE", false, "u"); err != nil {
		t.Fatalf("Set override: %v", err)
	}
	r := New(store, Options{Overrides: overlay})
	stop := r.Wire(reg)
	publishValueEvent(u, "DEV001:1", "TEMPERATURE", hmenum.ParamsetKeyValues, hmtypes.FloatValue(21.5))
	stop()

	stats, _ := store.Stats(ctx)
	if stats.Rows != 0 {
		t.Errorf("Rows = %d, want 0 (force-off override must drop an otherwise-recorded DP)", stats.Rows)
	}
}

// TestRecorder_OverrideForceOnRecordsExcludedDP: a DP the glob policy would
// drop (matched by Exclude) is recorded when force-on is set.
func TestRecorder_OverrideForceOnRecordsExcludedDP(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	reg, u, _, ch := centralWithDevice(t, "ccu-01", "DEV002", "DEV002:1")

	dp := floatDP("DEV002:1", "TEMPERATURE")
	ch.Put(dp)
	dp.OnEvent(21.5)

	// Exclude TEMPERATURE so the glob policy would deny it.
	overlay := NewRecordingOverrides(sqlite.NewRecordingOverrideStore(store.DB()), nil, []string{"TEMPERATURE"})
	if err := overlay.Set(ctx, "ccu-01", "HmIP-RF", "DEV002:1", "TEMPERATURE", true, "u"); err != nil {
		t.Fatalf("Set override: %v", err)
	}
	r := New(store, Options{Overrides: overlay, Exclude: []string{"TEMPERATURE"}})
	stop := r.Wire(reg)
	publishValueEvent(u, "DEV002:1", "TEMPERATURE", hmenum.ParamsetKeyValues, hmtypes.FloatValue(21.5))
	stop()

	stats, _ := store.Stats(ctx)
	if stats.Rows != 1 {
		t.Errorf("Rows = %d, want 1 (force-on override must record an otherwise-excluded DP)", stats.Rows)
	}
}

// TestRecorder_OverrideCannotBeatProvenanceGuard: a force-on override does
// NOT override the numeric / live-provenance vetoes — a bool value is still
// not a measurement.
func TestRecorder_OverrideCannotBeatProvenanceGuard(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	reg, u, _, ch := centralWithDevice(t, "ccu-01", "DEV003", "DEV003:1")

	dp := floatDP("DEV003:1", "STATE")
	ch.Put(dp)
	dp.OnEvent(1.0)

	overlay := NewRecordingOverrides(sqlite.NewRecordingOverrideStore(store.DB()), nil, nil)
	_ = overlay.Set(ctx, "ccu-01", "HmIP-RF", "DEV003:1", "STATE", true, "u")
	r := New(store, Options{Overrides: overlay})
	stop := r.Wire(reg)
	// A boolean value is never a measurement, override notwithstanding.
	publishValueEvent(u, "DEV003:1", "STATE", hmenum.ParamsetKeyValues, hmtypes.BoolValue(true))
	stop()

	stats, _ := store.Stats(ctx)
	if stats.Rows != 0 {
		t.Errorf("Rows = %d, want 0 (a force-on override must not defeat the numeric guard)", stats.Rows)
	}
}
