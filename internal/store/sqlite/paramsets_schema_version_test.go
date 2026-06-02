// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sqlite

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// rawInsertParamset inserts a paramset row directly into the DB with an
// explicit schema_version, bypassing ParamsetStore.Upsert. This allows
// tests to seed rows with non-current versions.
func rawInsertParamset(t *testing.T, s *ParamsetStore, centralName, iface, ch string, psKey hmenum.ParamsetKey, schemaVersion int) {
	t.Helper()
	_, err := s.db.ExecContext(
		context.Background(),
		`INSERT INTO paramsets
		    (central_name, interface_id, channel_address, paramset_key, hash, paramset_json, schema_version, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, CURRENT_TIMESTAMP)`,
		centralName, iface, ch, string(psKey), "legacy-hash", `{"X":{"type":"BOOL"}}`, schemaVersion,
	)
	if err != nil {
		t.Fatalf("rawInsertParamset: %v", err)
	}
}

// TestParamsetWipeOutdatedRemovesPreVersioningRows verifies that a row
// written with schema_version = 0 (pre-migration legacy) is deleted by
// WipeOutdated and becomes invisible to Get.
func TestParamsetWipeOutdatedRemovesPreVersioningRows(t *testing.T) {
	t.Parallel()

	s := freshParamsetStore(t)
	ctx := context.Background()

	rawInsertParamset(t, s, "ccu1", "HmIP-RF", "WIPE:1", hmenum.ParamsetKeyValues, 0)

	n, err := s.WipeOutdated(ctx)
	if err != nil {
		t.Fatalf("WipeOutdated: %v", err)
	}
	if n != 1 {
		t.Errorf("WipeOutdated returned count=%d, want 1", n)
	}

	_, getErr := s.Get(ctx, "ccu1", "HmIP-RF", "WIPE:1", hmenum.ParamsetKeyValues)
	if !errors.Is(getErr, ErrParamsetNotFound) {
		t.Errorf("Get after wipe: got %v, want ErrParamsetNotFound", getErr)
	}
}

// TestParamsetWipeOutdatedKeepsCurrentVersionRows verifies that a row written
// by the store (schema_version = ParamsetCacheSchemaVersion) is not removed by
// WipeOutdated and remains readable via Get.
func TestParamsetWipeOutdatedKeepsCurrentVersionRows(t *testing.T) {
	t.Parallel()

	s := freshParamsetStore(t)
	ctx := context.Background()

	if err := s.Upsert(ctx, ParamsetRecord{
		CentralName:    "ccu1",
		InterfaceID:    "HmIP-RF",
		ChannelAddress: "KEEP:1",
		ParamsetKey:    hmenum.ParamsetKeyValues,
		Hash:           "keep-hash",
		Paramset:       hmproto.Paramset{"P": {Type: hmenum.ParameterTypeBool}},
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	n, err := s.WipeOutdated(ctx)
	if err != nil {
		t.Fatalf("WipeOutdated: %v", err)
	}
	if n != 0 {
		t.Errorf("WipeOutdated count=%d, want 0 (current-version row must be kept)", n)
	}

	got, err := s.Get(ctx, "ccu1", "HmIP-RF", "KEEP:1", hmenum.ParamsetKeyValues)
	if err != nil {
		t.Fatalf("Get after wipe: %v", err)
	}
	if got.Hash != "keep-hash" {
		t.Errorf("Hash=%q, want keep-hash", got.Hash)
	}
}

// TestParamsetGetIgnoresOutdatedRowEvenWithoutWipe verifies that Get's WHERE
// clause filters on schema_version = ParamsetCacheSchemaVersion, so a row
// with schema_version = 0 is invisible even before WipeOutdated is called.
func TestParamsetGetIgnoresOutdatedRowEvenWithoutWipe(t *testing.T) {
	t.Parallel()

	s := freshParamsetStore(t)
	ctx := context.Background()

	rawInsertParamset(t, s, "ccu1", "HmIP-RF", "HIDDEN:1", hmenum.ParamsetKeyValues, 0)

	_, err := s.Get(ctx, "ccu1", "HmIP-RF", "HIDDEN:1", hmenum.ParamsetKeyValues)
	if !errors.Is(err, ErrParamsetNotFound) {
		t.Errorf("Get before wipe: got %v, want ErrParamsetNotFound (outdated row must be filtered by WHERE)", err)
	}
}

// TestParamsetUpsertOverwritesOutdatedRowToCurrentVersion verifies that
// calling Upsert on a composite key that already exists with a non-current
// schema_version (here: 99) promotes the row to ParamsetCacheSchemaVersion
// via the ON CONFLICT path.
func TestParamsetUpsertOverwritesOutdatedRowToCurrentVersion(t *testing.T) {
	t.Parallel()

	s := freshParamsetStore(t)
	ctx := context.Background()

	const (
		centralName = "ccu1"
		iface       = "HmIP-RF"
		ch          = "PROMOTE:1"
	)

	// Seed a row with a "future" schema version (99) via raw SQL.
	rawInsertParamset(t, s, centralName, iface, ch, hmenum.ParamsetKeyValues, 99)

	// Upsert the same key through the store — must promote to current version.
	if err := s.Upsert(ctx, ParamsetRecord{
		CentralName:    centralName,
		InterfaceID:    iface,
		ChannelAddress: ch,
		ParamsetKey:    hmenum.ParamsetKeyValues,
		Hash:           "promoted-hash",
		Paramset:       hmproto.Paramset{"Q": {Type: hmenum.ParameterTypeFloat}},
	}); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	// Confirm via raw SELECT that schema_version is now the current version.
	var gotVersion int
	err := s.db.QueryRowContext(
		ctx,
		`SELECT schema_version FROM paramsets
		 WHERE central_name = ? AND interface_id = ? AND channel_address = ? AND paramset_key = ?`,
		centralName, iface, ch, string(hmenum.ParamsetKeyValues),
	).Scan(&gotVersion)
	if err != nil {
		t.Fatalf("raw SELECT schema_version: %v", err)
	}
	if gotVersion != ParamsetCacheSchemaVersion {
		t.Errorf("schema_version=%d after Upsert, want %d", gotVersion, ParamsetCacheSchemaVersion)
	}
}

// TestOpenAutomaticallyWipesOutdatedRows verifies that Open calls
// WipeOutdated as part of its startup sequence, so rows with an outdated
// schema_version that were present in the database are removed before the
// caller ever accesses the store.
//
// This test uses a real file-backed database (via t.TempDir) so the same
// on-disk file can be opened a second time to simulate a daemon restart.
func TestOpenAutomaticallyWipesOutdatedRows(t *testing.T) {
	// Not marked t.Parallel() because openMu is a package-level mutex shared
	// across all parallel tests; holding it for two sequential Open calls
	// here while other parallel tests also contend would deadlock. Serial
	// execution avoids the issue without modifying production code.

	dir := t.TempDir()
	dsn := "file:" + filepath.Join(dir, "wipe-on-open.db") + "?_pragma=journal_mode(WAL)"
	ctx := context.Background()

	// First open: run migrations and get a handle so we can insert a legacy row.
	openMu.Lock()
	db1, err := Open(ctx, dsn)
	openMu.Unlock()
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}

	s1 := NewParamsetStore(db1)
	rawInsertParamset(t, s1, "ccu1", "HmIP-RF", "STALE:1", hmenum.ParamsetKeyValues, 0)

	// Verify the row was actually inserted before we close.
	var countBefore int
	if err := db1.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM paramsets WHERE schema_version = 0`).Scan(&countBefore); err != nil {
		t.Fatalf("count before close: %v", err)
	}
	if countBefore != 1 {
		t.Fatalf("expected 1 legacy row before close, got %d", countBefore)
	}
	if err := db1.Close(); err != nil {
		t.Fatalf("close db1: %v", err)
	}

	// Verify the WAL is checkpointed so the legacy row is on disk.
	// (closing the last connection to a WAL db checkpoints automatically
	// in modernc/sqlite, but we assert the file is non-empty just to be sure.)
	fi, err := os.Stat(filepath.Join(dir, "wipe-on-open.db"))
	if err != nil {
		t.Fatalf("stat db file: %v", err)
	}
	if fi.Size() == 0 {
		t.Fatal("db file is empty after close — WAL checkpoint did not flush")
	}

	// Second open: simulates daemon restart. Open must run WipeOutdated.
	openMu.Lock()
	db2, err := Open(ctx, dsn)
	openMu.Unlock()
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	t.Cleanup(func() { _ = db2.Close() })

	// The legacy row must be gone.
	s2 := NewParamsetStore(db2)
	_, getErr := s2.Get(ctx, "ccu1", "HmIP-RF", "STALE:1", hmenum.ParamsetKeyValues)
	if !errors.Is(getErr, ErrParamsetNotFound) {
		t.Errorf("Get after re-open: got %v, want ErrParamsetNotFound (Open must have wiped the legacy row)", getErr)
	}
}
