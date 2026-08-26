// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package sqlite

import (
	"context"
	"errors"
	"testing"
)

func newUserPreferencesStore(t *testing.T) *UserPreferencesStore {
	t.Helper()
	return NewUserPreferencesStore(openTestDB(t, "user_preferences.db"))
}

// TestPreferenceGetMissingKey verifies that Get returns ErrPreferenceNotFound
// when no row exists for the given (subject, key) pair.
func TestPreferenceGetMissingKey(t *testing.T) {
	t.Parallel()
	s := newUserPreferencesStore(t)
	_, err := s.Get(context.Background(), "alice", "favorites")
	if !errors.Is(err, ErrPreferenceNotFound) {
		t.Fatalf("Get missing key: want ErrPreferenceNotFound, got %v", err)
	}
}

// TestPreferenceSetThenGet verifies that a value stored via Set is returned
// verbatim by a subsequent Get.
func TestPreferenceSetThenGet(t *testing.T) {
	t.Parallel()
	s := newUserPreferencesStore(t)
	ctx := context.Background()
	const want = `["device-1","device-2"]`

	if err := s.Set(ctx, "alice", "favorites", want); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := s.Get(ctx, "alice", "favorites")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != want {
		t.Errorf("Get: got %q want %q", got, want)
	}
}

// TestPreferenceSetOverwritesExistingValue verifies upsert semantics: a second
// Set for the same (subject, key) pair replaces the previous value.
func TestPreferenceSetOverwritesExistingValue(t *testing.T) {
	t.Parallel()
	s := newUserPreferencesStore(t)
	ctx := context.Background()

	if err := s.Set(ctx, "alice", "theme", `"light"`); err != nil {
		t.Fatalf("first Set: %v", err)
	}
	if err := s.Set(ctx, "alice", "theme", `"dark"`); err != nil {
		t.Fatalf("second Set: %v", err)
	}
	got, err := s.Get(ctx, "alice", "theme")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got != `"dark"` {
		t.Errorf("Get after upsert: got %q want %q", got, `"dark"`)
	}
}

// TestPreferenceSubjectsAreIsolated verifies that different subjects cannot
// read each other's values for the same key.
func TestPreferenceSubjectsAreIsolated(t *testing.T) {
	t.Parallel()
	s := newUserPreferencesStore(t)
	ctx := context.Background()

	if err := s.Set(ctx, "alice", "favorites", `["a"]`); err != nil {
		t.Fatalf("Set alice: %v", err)
	}
	if err := s.Set(ctx, "bob", "favorites", `["b"]`); err != nil {
		t.Fatalf("Set bob: %v", err)
	}

	aliceVal, err := s.Get(ctx, "alice", "favorites")
	if err != nil {
		t.Fatalf("Get alice: %v", err)
	}
	if aliceVal != `["a"]` {
		t.Errorf("alice: got %q want %q", aliceVal, `["a"]`)
	}

	bobVal, err := s.Get(ctx, "bob", "favorites")
	if err != nil {
		t.Fatalf("Get bob: %v", err)
	}
	if bobVal != `["b"]` {
		t.Errorf("bob: got %q want %q", bobVal, `["b"]`)
	}
}

// TestPreferenceDeleteRemovesRow verifies that Delete removes the stored row
// and a subsequent Get returns ErrPreferenceNotFound.
func TestPreferenceDeleteRemovesRow(t *testing.T) {
	t.Parallel()
	s := newUserPreferencesStore(t)
	ctx := context.Background()

	if err := s.Set(ctx, "alice", "layout", `{}`); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := s.Delete(ctx, "alice", "layout"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, err := s.Get(ctx, "alice", "layout")
	if !errors.Is(err, ErrPreferenceNotFound) {
		t.Fatalf("Get after delete: want ErrPreferenceNotFound, got %v", err)
	}
}

// TestPreferenceDeleteMissingKeyIsNoError verifies that Delete on a row that
// does not exist returns nil.
func TestPreferenceDeleteMissingKeyIsNoError(t *testing.T) {
	t.Parallel()
	s := newUserPreferencesStore(t)
	if err := s.Delete(context.Background(), "alice", "nonexistent"); err != nil {
		t.Fatalf("Delete missing key: want nil, got %v", err)
	}
}
