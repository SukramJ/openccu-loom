// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package sqlite

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

func newConfigSectionStore(t *testing.T) *ConfigSectionStore {
	t.Helper()
	return NewConfigSectionStore(openTestDB(t, "sections.db"))
}

// TestConfigSectionStorePutInsertsVersionOne verifies that the first
// Put on a section sets version=1.
func TestConfigSectionStorePutInsertsVersionOne(t *testing.T) {
	s := newConfigSectionStore(t)
	ctx := context.Background()

	payload := []byte(`{"broker":"mqtt.local"}`)
	row, err := s.Put(ctx, "north.mqtt", payload, "test")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if row.Version != 1 {
		t.Errorf("Version=%d want 1 on first Put", row.Version)
	}
}

// TestConfigSectionStorePutBumpsVersion verifies that a second Put on
// the same section increments version to 2.
func TestConfigSectionStorePutBumpsVersion(t *testing.T) {
	s := newConfigSectionStore(t)
	ctx := context.Background()

	payload1 := []byte(`{"broker":"mqtt.local"}`)
	if _, err := s.Put(ctx, "north.mqtt", payload1, "admin"); err != nil {
		t.Fatalf("first Put: %v", err)
	}

	payload2 := []byte(`{"broker":"mqtt2.local"}`)
	row2, err := s.Put(ctx, "north.mqtt", payload2, "admin")
	if err != nil {
		t.Fatalf("second Put: %v", err)
	}
	if row2.Version != 2 {
		t.Errorf("Version=%d want 2 on second Put", row2.Version)
	}
}

// TestConfigSectionStoreGetRoundTrips verifies that Get returns the
// same JSON payload written by Put, byte-for-byte.
func TestConfigSectionStoreGetRoundTrips(t *testing.T) {
	s := newConfigSectionStore(t)
	ctx := context.Background()

	payload := []byte(`{"broker":"mqtt.example.com","port":1883}`)
	if _, err := s.Put(ctx, "north.mqtt", payload, "alice"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	got, err := s.Get(ctx, "north.mqtt")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !bytes.Equal(got.ValueJSON, payload) {
		t.Errorf("ValueJSON=%q want %q", got.ValueJSON, payload)
	}
	if got.Section != "north.mqtt" {
		t.Errorf("Section=%q want north.mqtt", got.Section)
	}
	if got.Version != 1 {
		t.Errorf("Version=%d want 1", got.Version)
	}
	if got.UpdatedBy != "alice" {
		t.Errorf("UpdatedBy=%q want alice", got.UpdatedBy)
	}
	if got.UpdatedAt.IsZero() {
		t.Error("UpdatedAt is zero")
	}
}

// TestConfigSectionStoreGetUnknown verifies ErrSectionNotFound for a
// section that was never written.
func TestConfigSectionStoreGetUnknown(t *testing.T) {
	s := newConfigSectionStore(t)
	ctx := context.Background()

	_, err := s.Get(ctx, "nonexistent.section")
	if !errors.Is(err, ErrSectionNotFound) {
		t.Errorf("Get unknown: want ErrSectionNotFound, got %v", err)
	}
}

// TestConfigSectionStoreDelete verifies happy-path deletion.
func TestConfigSectionStoreDelete(t *testing.T) {
	s := newConfigSectionStore(t)
	ctx := context.Background()

	if _, err := s.Put(ctx, "callback", []byte(`{}`), "admin"); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := s.Delete(ctx, "callback"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := s.Get(ctx, "callback"); !errors.Is(err, ErrSectionNotFound) {
		t.Errorf("Get after Delete: want ErrSectionNotFound, got %v", err)
	}
}

// TestConfigSectionStoreDeleteUnknown verifies ErrSectionNotFound when
// deleting a section that does not exist.
func TestConfigSectionStoreDeleteUnknown(t *testing.T) {
	s := newConfigSectionStore(t)
	ctx := context.Background()

	err := s.Delete(ctx, "does.not.exist")
	if !errors.Is(err, ErrSectionNotFound) {
		t.Errorf("Delete unknown: want ErrSectionNotFound, got %v", err)
	}
}

// TestConfigSectionStoreListSortedBySection verifies ORDER BY section.
func TestConfigSectionStoreListSortedBySection(t *testing.T) {
	s := newConfigSectionStore(t)
	ctx := context.Background()

	for _, sec := range []string{"reliability", "callback", "north.mqtt"} {
		if _, err := s.Put(ctx, sec, []byte(`{}`), "admin"); err != nil {
			t.Fatalf("Put %s: %v", sec, err)
		}
	}
	rows, err := s.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("List len=%d want 3", len(rows))
	}
	want := []string{"callback", "north.mqtt", "reliability"}
	for i, w := range want {
		if rows[i].Section != w {
			t.Errorf("rows[%d].Section=%q want %q", i, rows[i].Section, w)
		}
	}
}

// TestConfigSectionStorePutEmptySection verifies that an empty section
// name is rejected.
func TestConfigSectionStorePutEmptySection(t *testing.T) {
	s := newConfigSectionStore(t)
	ctx := context.Background()

	_, err := s.Put(ctx, "", []byte(`{}`), "admin")
	if err == nil {
		t.Error("Put with empty section: expected error, got nil")
	}
}

// TestConfigSectionStorePutEmptyValue verifies that empty JSON is
// rejected.
func TestConfigSectionStorePutEmptyValue(t *testing.T) {
	s := newConfigSectionStore(t)
	ctx := context.Background()

	_, err := s.Put(ctx, "north.mqtt", nil, "admin")
	if err == nil {
		t.Error("Put with nil valueJSON: expected error, got nil")
	}
	_, err = s.Put(ctx, "north.mqtt", []byte{}, "admin")
	if err == nil {
		t.Error("Put with empty valueJSON: expected error, got nil")
	}
}

// TestConfigSectionStoreVersionAccumulates verifies that repeated Puts
// accumulate version numbers monotonically.
func TestConfigSectionStoreVersionAccumulates(t *testing.T) {
	s := newConfigSectionStore(t)
	ctx := context.Background()

	for i := 1; i <= 5; i++ {
		row, err := s.Put(ctx, "persistence", []byte(`{"v":1}`), "admin")
		if err != nil {
			t.Fatalf("Put %d: %v", i, err)
		}
		if row.Version != i {
			t.Errorf("Put %d: Version=%d want %d", i, row.Version, i)
		}
	}
}

// TestConfigSectionStorePutStampsSchemaVersion verifies that Put
// stamps the current ConfigSectionSchemaVersion on each write and
// that Get reads it back correctly.
func TestConfigSectionStorePutStampsSchemaVersion(t *testing.T) {
	t.Parallel()
	s := newConfigSectionStore(t)
	ctx := context.Background()

	row, err := s.Put(ctx, "north.mqtt", []byte(`{"broker":"test"}`), "admin")
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if row.SchemaVersion != ConfigSectionSchemaVersion {
		t.Errorf("Put SchemaVersion=%d want %d", row.SchemaVersion, ConfigSectionSchemaVersion)
	}

	got, err := s.Get(ctx, "north.mqtt")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.SchemaVersion != ConfigSectionSchemaVersion {
		t.Errorf("Get SchemaVersion=%d want %d", got.SchemaVersion, ConfigSectionSchemaVersion)
	}
}

// TestConfigSectionStoreWipeOutdatedSections verifies that WipeOutdatedSections
// removes rows whose schema_version does not match the current version while
// leaving current-version rows intact.
func TestConfigSectionStoreWipeOutdatedSections(t *testing.T) {
	t.Parallel()
	db := openTestDB(t, "sections_wipe.db")
	s := NewConfigSectionStore(db)
	ctx := context.Background()

	// Insert a current-schema section via Put (stamps ConfigSectionSchemaVersion).
	if _, err := s.Put(ctx, "north.mqtt", []byte(`{"broker":"good"}`), "admin"); err != nil {
		t.Fatalf("Put current: %v", err)
	}

	// Back-door: insert a stale row with schema_version=0 directly.
	_, err := db.ExecContext(ctx,
		`INSERT INTO config_sections (section, value_json, version, schema_version, updated_at, updated_by)
		 VALUES ('stale.section', '{}', 1, 0, CURRENT_TIMESTAMP, 'test')`)
	if err != nil {
		t.Fatalf("insert stale: %v", err)
	}

	n, err := s.WipeOutdatedSections(ctx)
	if err != nil {
		t.Fatalf("WipeOutdatedSections: %v", err)
	}
	if n != 1 {
		t.Errorf("wiped %d rows want 1", n)
	}

	// The current-schema row must still exist.
	if _, err := s.Get(ctx, "north.mqtt"); err != nil {
		t.Errorf("current row removed unexpectedly: %v", err)
	}
	// The stale row must be gone.
	if _, err := s.Get(ctx, "stale.section"); !errors.Is(err, ErrSectionNotFound) {
		t.Errorf("stale row still present: %v", err)
	}
}
