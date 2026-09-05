// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package matterendpoint_test

import (
	"context"
	"errors"
	"testing"

	matterendpoint "github.com/SukramJ/openccu-loom/internal/store/matterendpoint"
)

// ---- GetExposure ----

func TestGetExposure_NotFound(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := matterendpoint.New(db)
	_, err := s.GetExposure(context.Background(), testKey("c1", "X:1", 1, matterendpoint.DPKindCustom, "K"))
	if !errors.Is(err, matterendpoint.ErrExposureNotFound) {
		t.Fatalf("expected ErrExposureNotFound, got %v", err)
	}
}

func TestGetExposure_Found(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := matterendpoint.New(db)
	ctx := context.Background()

	key := testKey("c1", "X:1", 1, matterendpoint.DPKindCustom, "K")
	rec := matterendpoint.ExposureRecord{Key: key, Enabled: true, FriendlyName: "My Lamp", Actor: "user"}
	if err := s.UpsertExposure(ctx, rec); err != nil {
		t.Fatalf("UpsertExposure: %v", err)
	}

	got, err := s.GetExposure(ctx, key)
	if err != nil {
		t.Fatalf("GetExposure: %v", err)
	}
	if !got.Enabled {
		t.Error("Enabled = false, want true")
	}
	if got.FriendlyName != "My Lamp" {
		t.Errorf("FriendlyName = %q, want %q", got.FriendlyName, "My Lamp")
	}
	if got.Actor != "user" {
		t.Errorf("Actor = %q, want %q", got.Actor, "user")
	}
}

// ---- IsExposed ----

func TestIsExposed_Missing_ReturnsFalse(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := matterendpoint.New(db)
	ok, err := s.IsExposed(context.Background(), testKey("c1", "Y:1", 1, matterendpoint.DPKindCustom, "K"))
	if err != nil {
		t.Fatalf("IsExposed: %v", err)
	}
	if ok {
		t.Error("IsExposed missing key = true, want false")
	}
}

func TestIsExposed_DisabledRow_ReturnsFalse(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := matterendpoint.New(db)
	ctx := context.Background()

	key := testKey("c1", "Y:2", 1, matterendpoint.DPKindCustom, "K")
	if err := s.UpsertExposure(ctx, matterendpoint.ExposureRecord{Key: key, Enabled: false, Actor: "sys"}); err != nil {
		t.Fatalf("UpsertExposure: %v", err)
	}
	ok, err := s.IsExposed(ctx, key)
	if err != nil {
		t.Fatalf("IsExposed: %v", err)
	}
	if ok {
		t.Error("IsExposed disabled = true, want false")
	}
}

func TestIsExposed_EnabledRow_ReturnsTrue(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := matterendpoint.New(db)
	ctx := context.Background()

	key := testKey("c1", "Y:3", 1, matterendpoint.DPKindCustom, "K")
	if err := s.UpsertExposure(ctx, matterendpoint.ExposureRecord{Key: key, Enabled: true, Actor: "sys"}); err != nil {
		t.Fatalf("UpsertExposure: %v", err)
	}
	ok, err := s.IsExposed(ctx, key)
	if err != nil {
		t.Fatalf("IsExposed: %v", err)
	}
	if !ok {
		t.Error("IsExposed enabled = false, want true")
	}
}

// ---- EnabledKeys ----

func TestEnabledKeys_Empty(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := matterendpoint.New(db)
	got, err := s.EnabledKeys(context.Background(), "")
	if err != nil {
		t.Fatalf("EnabledKeys: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len=%d, want 0", len(got))
	}
}

func TestEnabledKeys_FilterByCentral(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := matterendpoint.New(db)
	ctx := context.Background()

	for i, centralName := range []string{"aa", "bb"} {
		key := testKey(centralName, "Z:"+string(rune('1'+i)), 1, matterendpoint.DPKindGeneric, "V")
		if err := s.UpsertExposure(ctx, matterendpoint.ExposureRecord{Key: key, Enabled: true, Actor: "sys"}); err != nil {
			t.Fatalf("UpsertExposure: %v", err)
		}
	}

	got, err := s.EnabledKeys(ctx, "aa")
	if err != nil {
		t.Fatalf("EnabledKeys: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("len=%d, want 1", len(got))
	}
}

func TestEnabledKeys_NoFilter_ReturnsAll(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := matterendpoint.New(db)
	ctx := context.Background()

	for i, centralName := range []string{"cc", "dd"} {
		key := testKey(centralName, "W:"+string(rune('1'+i)), 1, matterendpoint.DPKindGeneric, "V")
		if err := s.UpsertExposure(ctx, matterendpoint.ExposureRecord{Key: key, Enabled: true, Actor: "sys"}); err != nil {
			t.Fatalf("UpsertExposure: %v", err)
		}
	}

	got, err := s.EnabledKeys(ctx, "")
	if err != nil {
		t.Fatalf("EnabledKeys: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("len=%d, want 2", len(got))
	}
}

// ---- ListExposures ----

func TestListExposures_Empty(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := matterendpoint.New(db)
	got, err := s.ListExposures(context.Background(), "")
	if err != nil {
		t.Fatalf("ListExposures: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len=%d, want 0", len(got))
	}
}

func TestListExposures_FilterByCentral(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := matterendpoint.New(db)
	ctx := context.Background()

	for i, centralName := range []string{"ee", "ff", "ee"} {
		key := testKey(centralName, "Q:1", i+1, matterendpoint.DPKindCustom, "K")
		if err := s.UpsertExposure(ctx, matterendpoint.ExposureRecord{Key: key, Enabled: true, Actor: "u"}); err != nil {
			t.Fatalf("UpsertExposure: %v", err)
		}
	}

	got, err := s.ListExposures(ctx, "ee")
	if err != nil {
		t.Fatalf("ListExposures: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("len=%d, want 2", len(got))
	}
}

func TestListExposures_NoFilter(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := matterendpoint.New(db)
	ctx := context.Background()

	for i, centralName := range []string{"gg", "hh"} {
		key := testKey(centralName, "P:1", i+1, matterendpoint.DPKindCustom, "K")
		if err := s.UpsertExposure(ctx, matterendpoint.ExposureRecord{Key: key, Enabled: false, Actor: "u"}); err != nil {
			t.Fatalf("UpsertExposure: %v", err)
		}
	}

	got, err := s.ListExposures(ctx, "")
	if err != nil {
		t.Fatalf("ListExposures: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("len=%d, want 2", len(got))
	}
}

// ---- UpsertExposure update ----

func TestUpsertExposure_UpdateEnabled(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := matterendpoint.New(db)
	ctx := context.Background()

	key := testKey("c1", "R:1", 1, matterendpoint.DPKindCustom, "T")
	if err := s.UpsertExposure(ctx, matterendpoint.ExposureRecord{Key: key, Enabled: false, Actor: "u"}); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if err := s.UpsertExposure(ctx, matterendpoint.ExposureRecord{Key: key, Enabled: true, Actor: "u"}); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err := s.GetExposure(ctx, key)
	if err != nil {
		t.Fatalf("GetExposure: %v", err)
	}
	if !got.Enabled {
		t.Error("Enabled after update = false, want true")
	}
}

// ---- DeleteExposure ----

func TestDeleteExposure_Existing(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := matterendpoint.New(db)
	ctx := context.Background()

	key := testKey("c1", "S:1", 1, matterendpoint.DPKindCustom, "D")
	if err := s.UpsertExposure(ctx, matterendpoint.ExposureRecord{Key: key, Enabled: true, Actor: "u"}); err != nil {
		t.Fatalf("UpsertExposure: %v", err)
	}
	if err := s.DeleteExposure(ctx, key); err != nil {
		t.Fatalf("DeleteExposure: %v", err)
	}
	_, err := s.GetExposure(ctx, key)
	if !errors.Is(err, matterendpoint.ErrExposureNotFound) {
		t.Errorf("expected ErrExposureNotFound after delete, got %v", err)
	}
}

func TestDeleteExposure_Missing_NoError(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := matterendpoint.New(db)
	key := testKey("c1", "T:1", 1, matterendpoint.DPKindCustom, "E")
	if err := s.DeleteExposure(context.Background(), key); err != nil {
		t.Fatalf("DeleteExposure on missing: %v", err)
	}
}

// TestDeleteForCentral_RemovesOnlyThatCentralsRows is the regression guard
// for a removed central leaving its Matter exposure allowlist rows behind:
// GET /api/v1/matter/status's enabled_count is computed from these
// persisted rows (not the live model), so an orphaned row keeps counting
// an endpoint that can never exist again.
func TestDeleteForCentral_RemovesOnlyThatCentralsRows(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := matterendpoint.New(db)
	ctx := context.Background()

	removedKey := testKey("removed", "S:1", 1, matterendpoint.DPKindCustom, "D")
	survivorKey := testKey("survivor", "S:1", 1, matterendpoint.DPKindCustom, "D")
	if err := s.UpsertExposure(ctx, matterendpoint.ExposureRecord{Key: removedKey, Enabled: true, Actor: "u"}); err != nil {
		t.Fatalf("UpsertExposure(removed): %v", err)
	}
	if err := s.UpsertExposure(ctx, matterendpoint.ExposureRecord{Key: survivorKey, Enabled: true, Actor: "u"}); err != nil {
		t.Fatalf("UpsertExposure(survivor): %v", err)
	}

	if err := s.DeleteForCentral(ctx, "removed"); err != nil {
		t.Fatalf("DeleteForCentral: %v", err)
	}

	if _, err := s.GetExposure(ctx, removedKey); !errors.Is(err, matterendpoint.ErrExposureNotFound) {
		t.Errorf("removed central's row survived: err=%v, want ErrExposureNotFound", err)
	}
	if _, err := s.GetExposure(ctx, survivorKey); err != nil {
		t.Errorf("survivor central's row was deleted: %v", err)
	}
}

func TestDeleteForCentral_NoRowsForCentral_NoError(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := matterendpoint.New(db)
	if err := s.DeleteForCentral(context.Background(), "never-seen"); err != nil {
		t.Fatalf("DeleteForCentral on absent central: %v", err)
	}
}

// ---- CountEnabled ----

func TestCountEnabled_Zero(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := matterendpoint.New(db)
	n, err := s.CountEnabled(context.Background(), "")
	if err != nil {
		t.Fatalf("CountEnabled: %v", err)
	}
	if n != 0 {
		t.Errorf("n=%d, want 0", n)
	}
}

func TestCountEnabled_FilterByCentral(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := matterendpoint.New(db)
	ctx := context.Background()

	for i, centralName := range []string{"m1", "m2", "m1"} {
		key := testKey(centralName, "U:1", i+1, matterendpoint.DPKindCustom, "K")
		if err := s.UpsertExposure(ctx, matterendpoint.ExposureRecord{Key: key, Enabled: true, Actor: "u"}); err != nil {
			t.Fatalf("UpsertExposure: %v", err)
		}
	}

	n, err := s.CountEnabled(ctx, "m1")
	if err != nil {
		t.Fatalf("CountEnabled: %v", err)
	}
	if n != 2 {
		t.Errorf("n=%d, want 2", n)
	}
}

// TestGetExposure_DisabledRow verifies that a disabled row is returned (not
// ErrExposureNotFound) with Enabled=false.
func TestGetExposure_DisabledRow(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := matterendpoint.New(db)
	ctx := context.Background()

	key := testKey("c1", "XX:1", 1, matterendpoint.DPKindCustom, "DISABLED")
	if err := s.UpsertExposure(ctx, matterendpoint.ExposureRecord{Key: key, Enabled: false, Actor: "sys"}); err != nil {
		t.Fatalf("UpsertExposure: %v", err)
	}
	got, err := s.GetExposure(ctx, key)
	if err != nil {
		t.Fatalf("GetExposure disabled row: %v", err)
	}
	if got.Enabled {
		t.Error("Enabled = true for a disabled row, want false")
	}
}

// TestListExposures_Mixed verifies that rows with different Enabled values
// are all returned by ListExposures.
func TestListExposures_Mixed(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := matterendpoint.New(db)
	ctx := context.Background()

	keys := []matterendpoint.SourceKey{
		testKey("mix", "M:1", 1, matterendpoint.DPKindCustom, "A"),
		testKey("mix", "M:2", 2, matterendpoint.DPKindCustom, "B"),
	}
	for i, k := range keys {
		if err := s.UpsertExposure(ctx, matterendpoint.ExposureRecord{Key: k, Enabled: i%2 == 0, Actor: "u"}); err != nil {
			t.Fatalf("UpsertExposure[%d]: %v", i, err)
		}
	}
	got, err := s.ListExposures(ctx, "mix")
	if err != nil {
		t.Fatalf("ListExposures: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("len=%d, want 2", len(got))
	}
	// Verify the Enabled field is set correctly.
	enabled := 0
	for _, r := range got {
		if r.Enabled {
			enabled++
		}
	}
	if enabled != 1 {
		t.Errorf("enabled count=%d, want 1", enabled)
	}
}

func TestCountEnabled_Global(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := matterendpoint.New(db)
	ctx := context.Background()

	for i, centralName := range []string{"n1", "n2"} {
		key := testKey(centralName, "V:1", i+1, matterendpoint.DPKindCustom, "K")
		if err := s.UpsertExposure(ctx, matterendpoint.ExposureRecord{Key: key, Enabled: true, Actor: "u"}); err != nil {
			t.Fatalf("UpsertExposure: %v", err)
		}
	}
	// Add one disabled row — must not count.
	disKey := testKey("n1", "V:3", 3, matterendpoint.DPKindCustom, "K")
	if err := s.UpsertExposure(ctx, matterendpoint.ExposureRecord{Key: disKey, Enabled: false, Actor: "u"}); err != nil {
		t.Fatalf("UpsertExposure disabled: %v", err)
	}

	n, err := s.CountEnabled(ctx, "")
	if err != nil {
		t.Fatalf("CountEnabled: %v", err)
	}
	if n != 2 {
		t.Errorf("n=%d, want 2", n)
	}
}

// TestExposures_EnabledKeys_OnlyEnabled verifies that EnabledKeys returns only
// rows where enabled=true, filtering out disabled rows.
func TestExposures_EnabledKeys_OnlyEnabled(t *testing.T) {
	t.Parallel()
	db := openTestDB(t)
	s := matterendpoint.New(db)
	ctx := context.Background()

	// Add 2 enabled, 1 disabled.
	for i, enabled := range []bool{true, false, true} {
		key := testKey("ek2", "A:1", i+2, matterendpoint.DPKindCustom, "K")
		if err := s.UpsertExposure(ctx, matterendpoint.ExposureRecord{Key: key, Enabled: enabled, Actor: "u"}); err != nil {
			t.Fatalf("UpsertExposure[%d]: %v", i, err)
		}
	}

	got, err := s.EnabledKeys(ctx, "ek2")
	if err != nil {
		t.Fatalf("EnabledKeys: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("EnabledKeys len=%d, want 2 (only enabled rows)", len(got))
	}
}

// TestExposures_GetMissReturnsNotFound verifies GetExposure returns
// ErrExposureNotFound for an absent key.
func TestExposures_GetMissReturnsNotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := matterendpoint.New(openTestDB(t))

	key := testKey("miss", "X:1", 1, matterendpoint.DPKindCustom, "MISSING")
	_, err := s.GetExposure(ctx, key)
	if !errors.Is(err, matterendpoint.ErrExposureNotFound) {
		t.Errorf("GetExposure missing: want ErrExposureNotFound, got %v", err)
	}
}

// TestExposures_IsExposedMissReturnsFalse verifies IsExposed returns false for
// an absent key.
func TestExposures_IsExposedMissReturnsFalse(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := matterendpoint.New(openTestDB(t))

	key := testKey("iexp", "Y:2", 3, matterendpoint.DPKindGeneric, "STATE")
	ok, err := s.IsExposed(ctx, key)
	if err != nil {
		t.Fatalf("IsExposed: %v", err)
	}
	if ok {
		t.Error("IsExposed for missing key: want false, got true")
	}
}

// TestExposures_IsExposedDisabledReturnsFalse verifies IsExposed returns false
// when the row exists but enabled=false.
func TestExposures_IsExposedDisabledReturnsFalse(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := matterendpoint.New(openTestDB(t))

	key := testKey("dis", "D:1", 1, matterendpoint.DPKindCustom, "K")
	if err := s.UpsertExposure(ctx, matterendpoint.ExposureRecord{Key: key, Enabled: false, Actor: "test"}); err != nil {
		t.Fatalf("UpsertExposure: %v", err)
	}
	ok, err := s.IsExposed(ctx, key)
	if err != nil {
		t.Fatalf("IsExposed: %v", err)
	}
	if ok {
		t.Error("IsExposed disabled: want false, got true")
	}
}

// TestExposures_DeleteIdempotent verifies that DeleteExposure for a missing key
// does not error.
func TestExposures_DeleteIdempotent(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := matterendpoint.New(openTestDB(t))

	key := testKey("del", "Z:1", 2, matterendpoint.DPKindCalculated, "V")
	// Delete before insert — must not error.
	if err := s.DeleteExposure(ctx, key); err != nil {
		t.Fatalf("DeleteExposure missing: %v", err)
	}
}

// TestExposures_CountEnabledWithFilter verifies CountEnabled with central filter
// and without filter.
func TestExposures_CountEnabledWithFilter(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := matterendpoint.New(openTestDB(t))

	// Insert 2 enabled for "cntA", 1 enabled for "cntB", 1 disabled for "cntA".
	for i, row := range []struct {
		centralName string
		enabled     bool
	}{
		{"cntA", true},
		{"cntA", false},
		{"cntA", true},
		{"cntB", true},
	} {
		key := testKey(row.centralName, "M:1", i+1, matterendpoint.DPKindCustom, "P")
		if err := s.UpsertExposure(ctx, matterendpoint.ExposureRecord{Key: key, Enabled: row.enabled, Actor: "u"}); err != nil {
			t.Fatalf("UpsertExposure[%d]: %v", i, err)
		}
	}

	n, err := s.CountEnabled(ctx, "cntA")
	if err != nil {
		t.Fatalf("CountEnabled cntA: %v", err)
	}
	if n != 2 {
		t.Errorf("CountEnabled(cntA)=%d want 2", n)
	}

	total, err := s.CountEnabled(ctx, "")
	if err != nil {
		t.Fatalf("CountEnabled all: %v", err)
	}
	if total != 3 {
		t.Errorf("CountEnabled(all)=%d want 3", total)
	}
}

// TestExposures_ListNoFilter verifies ListExposures with empty central returns
// all rows.
func TestExposures_ListNoFilter(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	s := matterendpoint.New(openTestDB(t))

	for i := range 4 {
		key := testKey("lall", "A:1", i+1, matterendpoint.DPKindCustom, "K")
		if err := s.UpsertExposure(ctx, matterendpoint.ExposureRecord{Key: key, Enabled: i%2 == 0, Actor: "t"}); err != nil {
			t.Fatalf("UpsertExposure[%d]: %v", i, err)
		}
	}

	list, err := s.ListExposures(ctx, "")
	if err != nil {
		t.Fatalf("ListExposures: %v", err)
	}
	if len(list) != 4 {
		t.Errorf("len=%d want 4", len(list))
	}
}
