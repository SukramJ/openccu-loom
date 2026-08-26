// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package custom

import (
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// =====================================================================
// Cluster E — normalizeModel
// =====================================================================

// TestNormalizeModelLowercase verifies that normalizeModel converts any
// mixed-case model string to its all-lowercase form.
func TestNormalizeModelLowercase(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want string
	}{
		{"HmIP-PS", "hmip-ps"},
		{"HM-LC-Dim1TPBU-FM", "hm-lc-dim1tpbu-fm"},
		{"hmip-broll", "hmip-broll"},
		{"HMIP-BROLL", "hmip-broll"},
	}
	for _, tc := range cases {
		if got := normalizeModel(tc.in); got != tc.want {
			t.Errorf("normalizeModel(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestNormalizeModelHbToHm verifies the HomeBrew-to-Homematic substitution:
// the "hb-" prefix (after lowercasing) is replaced with "hm-".
func TestNormalizeModelHbToHm(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want string
	}{
		{"HB-UNI-Sensor1", "hm-uni-sensor1"},
		{"hb-uni-sensor1", "hm-uni-sensor1"},
		{"HB-SCI-3-FM", "hm-sci-3-fm"},
		// Non-hb- prefix must not be touched.
		{"HmIP-HB-Foo", "hmip-hb-foo"},
		// Only the first occurrence is replaced.
		{"hb-hb-double", "hm-hb-double"},
	}
	for _, tc := range cases {
		if got := normalizeModel(tc.in); got != tc.want {
			t.Errorf("normalizeModel(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// =====================================================================
// Cluster F — Blacklist API
// =====================================================================

// TestBlacklistExactMatch verifies that a model added verbatim to the
// blacklist causes IsBlacklisted to return true for the exact same model.
func TestBlacklistExactMatch(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	r.Blacklist("HmIP-SPECIAL")
	if !r.IsBlacklisted("HmIP-SPECIAL") {
		t.Error("IsBlacklisted(HmIP-SPECIAL) = false, want true")
	}
	// Lowercase variant should also match (normalization applied on both sides).
	if !r.IsBlacklisted("hmip-special") {
		t.Error("IsBlacklisted(hmip-special) = false, want true (normalized)")
	}
}

// TestBlacklistPrefixMatch verifies that the blacklist uses prefix
// semantics: a blacklisted "hmip-bwth" entry blocks "HmIP-BWTH-1" and
// "HmIP-BWTH-2" but not "HmIP-BWTHx" if the prefix doesn't match.
func TestBlacklistPrefixMatch(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	r.Blacklist("hmip-bwth")
	// Variants with suffix must be blocked.
	if !r.IsBlacklisted("HmIP-BWTH") {
		t.Error("exact model should be blocked by own prefix entry")
	}
	if !r.IsBlacklisted("HmIP-BWTH-1") {
		t.Error("HmIP-BWTH-1 should be blocked by prefix hmip-bwth")
	}
	if !r.IsBlacklisted("HmIP-BWTH-2") {
		t.Error("HmIP-BWTH-2 should be blocked by prefix hmip-bwth")
	}
	// A model that merely shares the start of the prefix string must not
	// be blocked if it doesn't start with the blacklist entry.
	if r.IsBlacklisted("HmIP-BWS") {
		t.Error("HmIP-BWS must NOT be blocked by prefix hmip-bwth")
	}
}

// TestBlacklistHbNormalized verifies that adding an "HB-" model to the
// blacklist normalizes it to "hm-" and therefore blocks the
// corresponding "HM-" models.
func TestBlacklistHbNormalized(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	r.Blacklist("HB-UNI-Sensor1")
	// The stored entry is "hm-uni-sensor1"; the HM- query must match.
	if !r.IsBlacklisted("HM-UNI-Sensor1") {
		t.Error("IsBlacklisted(HM-UNI-Sensor1) = false; HB- should normalize to HM-")
	}
}

// TestGetBlacklistReturnsSortedCopy verifies that GetBlacklist returns a
// lexicographically sorted slice and that mutations to the returned slice
// do not affect the registry's internal state.
func TestGetBlacklistReturnsSortedCopy(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	r.Blacklist("hmip-zzz", "hmip-aaa", "hmip-mmm")
	bl := r.GetBlacklist()
	if len(bl) != 3 {
		t.Fatalf("GetBlacklist len=%d, want 3", len(bl))
	}
	for i := 1; i < len(bl); i++ {
		if bl[i] < bl[i-1] {
			t.Errorf("GetBlacklist not sorted at index %d: %v", i, bl)
		}
	}
	// Mutate the returned slice.
	bl[0] = "mutated"
	// Re-fetch: must be unchanged.
	bl2 := r.GetBlacklist()
	if bl2[0] == "mutated" {
		t.Error("GetBlacklist returned a non-copy; external mutation visible")
	}
}

// TestBlacklistDeduplicates verifies that calling Blacklist with the
// same model multiple times does not create duplicate entries.
func TestBlacklistDeduplicates(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	r.Blacklist("hmip-ps", "HmIP-PS", "HMIP-PS")
	bl := r.GetBlacklist()
	if len(bl) != 1 {
		t.Errorf("GetBlacklist after triple identical blacklist: len=%d, want 1; entries: %v", len(bl), bl)
	}
}

// =====================================================================
// Cluster G — GetConfigs
// =====================================================================

// TestGetConfigsExactMatch verifies that GetConfigs returns the profile
// whose DeviceType equals (after normalization) the queried model.
func TestGetConfigsExactMatch(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	p := makeProfile("IPSwitch", hmenum.DataPointCategorySwitch, "hmip-ps")
	if err := r.Register(p); err != nil {
		t.Fatal(err)
	}
	got := r.GetConfigs("HmIP-PS") // mixed case — should be normalized
	if len(got) != 1 {
		t.Fatalf("GetConfigs(HmIP-PS): len=%d, want 1", len(got))
	}
	if got[0].Name != "IPSwitch" {
		t.Errorf("GetConfigs name=%q, want IPSwitch", got[0].Name)
	}
}

// TestGetConfigsPrefixMatch verifies that when no exact match exists,
// GetConfigs falls back to prefix matching: a registered "hmip-bwth"
// entry satisfies a query for "HmIP-BWTH-1".
func TestGetConfigsPrefixMatch(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	// Register the base prefix; the variant "hmip-bwth-1" is not registered.
	p := makeProfile("IPThermostat", hmenum.DataPointCategoryClimate, "hmip-bwth")
	if err := r.Register(p); err != nil {
		t.Fatal(err)
	}
	got := r.GetConfigs("HmIP-BWTH-1")
	if len(got) != 1 {
		t.Fatalf("GetConfigs(HmIP-BWTH-1) prefix: len=%d, want 1", len(got))
	}
	if got[0].Name != "IPThermostat" {
		t.Errorf("GetConfigs name=%q, want IPThermostat", got[0].Name)
	}
}

// TestGetConfigsExactBeatsPrefix registers both an exact-match and a
// shorter-prefix entry in the same category; the exact match must win.
func TestGetConfigsExactBeatsPrefix(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	// Prefix entry.
	if err := r.Register(makeProfile("IPThermostatGeneric", hmenum.DataPointCategoryClimate, "hmip-bwth")); err != nil {
		t.Fatal(err)
	}
	// Exact-match entry for the specific variant.
	if err := r.Register(makeProfile("IPThermostatSpecific", hmenum.DataPointCategoryClimate, "hmip-bwth-1")); err != nil {
		t.Fatal(err)
	}
	got := r.GetConfigs("HmIP-BWTH-1")
	if len(got) != 1 {
		t.Fatalf("GetConfigs: len=%d, want 1", len(got))
	}
	if got[0].Name != "IPThermostatSpecific" {
		t.Errorf("exact match should win; got name=%q", got[0].Name)
	}
}

// TestGetConfigsAggregatesAcrossCategories verifies that a device with
// profiles in two distinct categories has both returned by GetConfigs,
// sorted by category.
func TestGetConfigsAggregatesAcrossCategories(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	const dt = "hmip-bwth"
	if err := r.Register(makeProfile("IPThermostat", hmenum.DataPointCategoryClimate, dt)); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(makeProfile("IPLock", hmenum.DataPointCategoryLock, dt)); err != nil {
		t.Fatal(err)
	}
	got := r.GetConfigs("HmIP-BWTH")
	if len(got) != 2 {
		t.Fatalf("GetConfigs: len=%d, want 2; profiles: %v", len(got), got)
	}
	// Must be sorted by category.
	if got[0].Category > got[1].Category {
		t.Errorf("GetConfigs not sorted by category: [%v, %v]", got[0].Category, got[1].Category)
	}
}

// TestGetConfigsBlacklistedReturnsEmpty verifies that a blacklisted
// model produces an empty (nil) slice from GetConfigs, even when a
// matching profile is registered.
func TestGetConfigsBlacklistedReturnsEmpty(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	if err := r.Register(makeProfile("IPSwitch", hmenum.DataPointCategorySwitch, "hmip-ps")); err != nil {
		t.Fatal(err)
	}
	r.Blacklist("hmip-ps")
	got := r.GetConfigs("HmIP-PS")
	if len(got) != 0 {
		t.Errorf("GetConfigs for blacklisted model: len=%d, want 0; profiles: %v", len(got), got)
	}
}

// TestGetConfigsNoMatchReturnsEmpty verifies that an unknown model
// returns an empty (nil) slice without an error.
func TestGetConfigsNoMatchReturnsEmpty(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	if err := r.Register(makeProfile("IPSwitch", hmenum.DataPointCategorySwitch, "hmip-ps")); err != nil {
		t.Fatal(err)
	}
	got := r.GetConfigs("totally-unknown-xyz")
	if len(got) != 0 {
		t.Errorf("GetConfigs for unknown model: len=%d, want 0", len(got))
	}
}

// =====================================================================
// Cluster H — RegisterMultiple
// =====================================================================

// TestRegisterMultipleSuccess verifies the happy path: a batch of
// non-conflicting profiles is inserted atomically and every profile is
// reachable afterwards.
func TestRegisterMultipleSuccess(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	profiles := []Profile{
		makeProfile("IPSwitch", hmenum.DataPointCategorySwitch, "hmip-ps"),
		makeProfile("IPCover", hmenum.DataPointCategoryCover, "hmip-broll"),
		makeProfile("IPDimmer", hmenum.DataPointCategoryLight, "hmip-bdt"),
	}
	if err := r.RegisterMultiple(profiles); err != nil {
		t.Fatalf("RegisterMultiple: unexpected error %v", err)
	}
	if r.Len() != 3 {
		t.Errorf("Len after RegisterMultiple=%d, want 3", r.Len())
	}
	if _, err := r.Get(hmenum.DataPointCategorySwitch, "hmip-ps"); err != nil {
		t.Errorf("Get(switch, hmip-ps): %v", err)
	}
}

// TestRegisterMultipleAtomicRollback verifies that if the batch
// contains a conflict (one profile collides with an existing entry) the
// entire batch is rejected: the non-conflicting profiles in the batch
// are NOT inserted (atomic rollback).
func TestRegisterMultipleAtomicRollback(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	// Pre-register a profile that will collide with the second item in
	// the batch below.
	existing := makeProfile("IPCover", hmenum.DataPointCategoryCover, "hmip-broll")
	if err := r.Register(existing); err != nil {
		t.Fatal(err)
	}
	initialLen := r.Len()

	batch := []Profile{
		makeProfile("IPSwitch", hmenum.DataPointCategorySwitch, "hmip-ps"),
		existing, // conflict
		makeProfile("IPDimmer", hmenum.DataPointCategoryLight, "hmip-bdt"),
	}
	err := r.RegisterMultiple(batch)
	if !errors.Is(err, ErrProfileConflict) {
		t.Fatalf("RegisterMultiple with conflict: expected ErrProfileConflict, got %v", err)
	}
	// Registry must not have grown — atomic rollback.
	if got := r.Len(); got != initialLen {
		t.Errorf("Len after failed RegisterMultiple=%d, want %d (no partial insert)", got, initialLen)
	}
	// Specifically, the first item must NOT have been inserted.
	if r.Has(hmenum.DataPointCategorySwitch, "hmip-ps") {
		t.Error("hmip-ps was partially inserted before the conflict; atomic rollback failed")
	}
}

// TestRegisterMultipleEmptyBatchIsNoop verifies that an empty slice
// produces no error and leaves the registry unchanged.
func TestRegisterMultipleEmptyBatchIsNoop(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	if err := r.RegisterMultiple(nil); err != nil {
		t.Fatalf("RegisterMultiple(nil): unexpected error %v", err)
	}
	if err := r.RegisterMultiple([]Profile{}); err != nil {
		t.Fatalf("RegisterMultiple([]): unexpected error %v", err)
	}
	if r.Len() != 0 {
		t.Errorf("Len after noop RegisterMultiple=%d, want 0", r.Len())
	}
}

// =====================================================================
// Cluster I — Normalization of existing API
// =====================================================================

// TestExistingAPINormalizesDeviceType verifies that Get, ForCategory,
// ForDevice, and Has all accept mixed-case device types and route to
// the same profile as its normalized form.
func TestExistingAPINormalizesDeviceType(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	// Register with explicit lowercase (what the generator produces).
	if err := r.Register(makeProfile("IPSwitch", hmenum.DataPointCategorySwitch, "hmip-ps")); err != nil {
		t.Fatal(err)
	}

	// Get with original mixed case.
	if _, err := r.Get(hmenum.DataPointCategorySwitch, "HmIP-PS"); err != nil {
		t.Errorf("Get(switch, HmIP-PS): %v", err)
	}
	// ForCategory with mixed case.
	if got := r.ForCategory(hmenum.DataPointCategorySwitch, "HmIP-PS"); len(got) != 1 {
		t.Errorf("ForCategory(switch, HmIP-PS): len=%d, want 1", len(got))
	}
	// ForDevice with mixed case.
	if got := r.ForDevice("HmIP-PS"); len(got) != 1 {
		t.Errorf("ForDevice(HmIP-PS): len=%d, want 1", len(got))
	}
	// Has with mixed case.
	if !r.Has(hmenum.DataPointCategorySwitch, "HmIP-PS") {
		t.Error("Has(switch, HmIP-PS) = false, want true")
	}
}

// TestRegisterHbPrefixNormalized verifies that registering with an
// "HB-" device type results in a profile reachable under the "HM-"
// equivalent (because both are stored/queried in normalized form).
func TestRegisterHbPrefixNormalized(t *testing.T) {
	t.Parallel()
	r := NewRegistry()
	if err := r.Register(makeProfile("HbSwitch", hmenum.DataPointCategorySwitch, "HB-UNI-Sensor1")); err != nil {
		t.Fatal(err)
	}
	// Must be reachable under the HM- form.
	if _, err := r.Get(hmenum.DataPointCategorySwitch, "HM-UNI-Sensor1"); err != nil {
		t.Errorf("Get(switch, HM-UNI-Sensor1) after HB- register: %v", err)
	}
	if !r.Has(hmenum.DataPointCategorySwitch, "hm-uni-sensor1") {
		t.Error("Has(switch, hm-uni-sensor1) = false after registering HB- variant")
	}
	// The DeviceType on the returned profile is the normalized form.
	p, _ := r.Get(hmenum.DataPointCategorySwitch, "hm-uni-sensor1")
	if p.DeviceType != "hm-uni-sensor1" {
		t.Errorf("stored DeviceType=%q, want hm-uni-sensor1 (normalized)", p.DeviceType)
	}
}
