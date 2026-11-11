// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package measurements

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"

	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

func newOverlay(t *testing.T, include, exclude []string) (*RecordingOverrides, *sqlite.RecordingOverrideStore) {
	t.Helper()
	db := openMainDB(t)
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

// TestOverrides_DeleteCentralNilSafe pins the nil-receiver contract every
// other mutator on RecordingOverrides shares.
func TestOverrides_DeleteCentralNilSafe(t *testing.T) {
	t.Parallel()
	var o *RecordingOverrides
	if n := o.DeleteCentral("c"); n != 0 {
		t.Errorf("nil overlay DeleteCentral = %d, want 0", n)
	}
}

// TestOverrides_DeleteCentralDropsOnlyThatCentralsOverrides pins the fix for
// a removed-and-re-adopted central serving stale overrides: DeleteCentral
// must clear every key for the given central from the in-memory overlay —
// which Decide/Effective consult on the hot path — while leaving every
// other central's overrides untouched.
func TestOverrides_DeleteCentralDropsOnlyThatCentralsOverrides(t *testing.T) {
	t.Parallel()
	o, _ := newOverlay(t, nil, nil)
	ctx := context.Background()

	if err := o.Set(ctx, "haus", "i", "ch", "TEMPERATURE", false, "u"); err != nil {
		t.Fatalf("Set(haus): %v", err)
	}
	if err := o.Set(ctx, "haus", "i", "ch2", "STATE", true, "u"); err != nil {
		t.Fatalf("Set(haus/ch2): %v", err)
	}
	if err := o.Set(ctx, "other", "i", "ch", "TEMPERATURE", false, "u"); err != nil {
		t.Fatalf("Set(other): %v", err)
	}

	if n := o.DeleteCentral("haus"); n != 2 {
		t.Errorf("DeleteCentral(haus) removed %d keys, want 2", n)
	}

	// The removed central's overrides are gone: Decide falls back to the
	// policy argument instead of returning the stale forced verdict.
	if !o.Decide("haus", "i", "ch", "TEMPERATURE", true) {
		t.Error("haus override survived DeleteCentral: Decide still returns the forced false")
	}
	if o.Decide("haus", "i", "ch2", "STATE", false) {
		t.Error("haus override survived DeleteCentral: Decide still returns the forced true")
	}

	// A different central's override must be untouched.
	if o.Decide("other", "i", "ch", "TEMPERATURE", true) {
		t.Error("DeleteCentral(haus) must not remove an unrelated central's override")
	}

	// Idempotent: a second call finds nothing left to remove.
	if n := o.DeleteCentral("haus"); n != 0 {
		t.Errorf("second DeleteCentral(haus) removed %d keys, want 0", n)
	}
}

// TestForwarder_OverrideForceOffDropsIncludedDP: a DP the glob policy would
// forward is dropped when an explicit force-off override is present.
func TestForwarder_OverrideForceOffDropsIncludedDP(t *testing.T) {
	t.Parallel()
	exp := &fakeExporter{}
	db := openMainDB(t)
	ctx := context.Background()
	reg, u, _, ch := centralWithDevice(t, "ccu-01", "DEV001", "DEV001:1")

	dp := floatDP("DEV001:1", "TEMPERATURE")
	ch.Put(dp)
	dp.OnEvent(21.5)

	overlay := NewRecordingOverrides(sqlite.NewRecordingOverrideStore(db), nil, nil)
	if err := overlay.Set(ctx, "ccu-01", "HmIP-RF", "DEV001:1", "TEMPERATURE", false, "u"); err != nil {
		t.Fatalf("Set override: %v", err)
	}
	r := New(exp, Options{Overrides: overlay})
	stop := r.Wire(reg)
	publishValueEvent(u, "DEV001:1", "TEMPERATURE", hmenum.ParamsetKeyValues, hmtypes.FloatValue(21.5))
	stop()

	if got := exp.got(); len(got) != 0 {
		t.Errorf("samples = %d, want 0 (force-off override must drop an otherwise-forwarded DP)", len(got))
	}
}

// TestForwarder_OverrideForceOnRecordsExcludedDP: a DP the glob policy would
// drop (matched by Exclude) is forwarded when force-on is set.
func TestForwarder_OverrideForceOnRecordsExcludedDP(t *testing.T) {
	t.Parallel()
	exp := &fakeExporter{}
	db := openMainDB(t)
	ctx := context.Background()
	reg, u, _, ch := centralWithDevice(t, "ccu-01", "DEV002", "DEV002:1")

	dp := floatDP("DEV002:1", "TEMPERATURE")
	ch.Put(dp)
	dp.OnEvent(21.5)

	// Exclude TEMPERATURE so the glob policy would deny it.
	overlay := NewRecordingOverrides(sqlite.NewRecordingOverrideStore(db), nil, []string{"TEMPERATURE"})
	if err := overlay.Set(ctx, "ccu-01", "HmIP-RF", "DEV002:1", "TEMPERATURE", true, "u"); err != nil {
		t.Fatalf("Set override: %v", err)
	}
	r := New(exp, Options{Overrides: overlay, Exclude: []string{"TEMPERATURE"}})
	stop := r.Wire(reg)
	publishValueEvent(u, "DEV002:1", "TEMPERATURE", hmenum.ParamsetKeyValues, hmtypes.FloatValue(21.5))
	stop()

	if got := exp.got(); len(got) != 1 {
		t.Errorf("samples = %d, want 1 (force-on override must forward an otherwise-excluded DP)", len(got))
	}
}

// TestForwarder_OverrideCannotBeatProvenanceGuard: a force-on override does
// NOT override the numeric / live-provenance vetoes — a bool value is still
// not a measurement.
func TestForwarder_OverrideCannotBeatProvenanceGuard(t *testing.T) {
	t.Parallel()
	exp := &fakeExporter{}
	db := openMainDB(t)
	ctx := context.Background()
	reg, u, _, ch := centralWithDevice(t, "ccu-01", "DEV003", "DEV003:1")

	dp := floatDP("DEV003:1", "STATE")
	ch.Put(dp)
	dp.OnEvent(1.0)

	overlay := NewRecordingOverrides(sqlite.NewRecordingOverrideStore(db), nil, nil)
	_ = overlay.Set(ctx, "ccu-01", "HmIP-RF", "DEV003:1", "STATE", true, "u")
	r := New(exp, Options{Overrides: overlay})
	stop := r.Wire(reg)
	// A boolean value is never a measurement, override notwithstanding.
	publishValueEvent(u, "DEV003:1", "STATE", hmenum.ParamsetKeyValues, hmtypes.BoolValue(true))
	stop()

	if got := exp.got(); len(got) != 0 {
		t.Errorf("samples = %d, want 0 (a force-on override must not defeat the numeric guard)", len(got))
	}
}

// TestOverrides_DeleteDeviceDropsStoreAndOverlay pins that a device removal
// reaches both halves: the overlay is loaded once, so a key left in the map
// would keep deciding for a device re-paired at the same address.
func TestOverrides_DeleteDeviceDropsStoreAndOverlay(t *testing.T) {
	t.Parallel()
	o, store := newOverlay(t, nil, nil)
	ctx := context.Background()

	type key struct{ iface, channel string }
	removed := []key{{"HmIP-RF", "DEV:1"}, {"HmIP-RF", "DEV:2"}}
	kept := []key{{"HmIP-RF", "DEV2:1"}, {"BidCos-RF", "DEV:1"}}
	for _, k := range append(append([]key(nil), removed...), kept...) {
		if err := o.Set(ctx, "ccu1", k.iface, k.channel, "POWER", false, "u"); err != nil {
			t.Fatalf("Set(%s %s): %v", k.iface, k.channel, err)
		}
	}

	if err := o.DeleteDevice(ctx, "ccu1", "HmIP-RF", "DEV"); err != nil {
		t.Fatalf("DeleteDevice: %v", err)
	}

	for _, k := range removed {
		if _, src := o.Effective("ccu1", k.iface, k.channel, "POWER"); src != "policy" {
			t.Errorf("overlay still holds an override for %s %s after DeleteDevice", k.iface, k.channel)
		}
	}
	for _, k := range kept {
		if rec, src := o.Effective("ccu1", k.iface, k.channel, "POWER"); src != "override" || rec {
			t.Errorf("overlay lost the override for %s %s: (%v,%q), want (false,override)", k.iface, k.channel, rec, src)
		}
	}

	rows, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	left := map[key]bool{}
	for _, r := range rows {
		left[key{r.InterfaceID, r.ChannelAddress}] = true
	}
	for _, k := range removed {
		if left[k] {
			t.Errorf("store still holds an override for %s %s after DeleteDevice", k.iface, k.channel)
		}
	}
	for _, k := range kept {
		if !left[k] {
			t.Errorf("store lost the override for %s %s", k.iface, k.channel)
		}
	}
}

// TestOverrides_DeleteDeviceNilSafe pins the nil-receiver contract.
func TestOverrides_DeleteDeviceNilSafe(t *testing.T) {
	t.Parallel()
	var o *RecordingOverrides
	if err := o.DeleteDevice(context.Background(), "c", "i", "DEV"); err != nil {
		t.Errorf("nil overlay DeleteDevice = %v, want nil", err)
	}
}
