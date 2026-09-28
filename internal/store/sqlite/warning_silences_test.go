// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package sqlite

import (
	"context"
	"testing"
	"time"
)

func freshWarningSilenceStore(t *testing.T) *WarningSilenceStore {
	t.Helper()
	return NewWarningSilenceStore(openTestDB(t, "warning_silences.db"))
}

func TestWarningSilenceStoreRoundTrip(t *testing.T) {
	s := freshWarningSilenceStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	until := now.Add(time.Hour)

	if err := s.Set(ctx, "alice", "warn-1", until, now); err != nil {
		t.Fatalf("silence: %v", err)
	}

	got, err := s.Silences(ctx, "alice")
	if err != nil {
		t.Fatalf("silences: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d want 1", len(got))
	}
	gotUntil, ok := got["warn-1"]
	if !ok {
		t.Fatal("warn-1 missing")
	}
	if !gotUntil.Equal(until) {
		t.Errorf("until=%v want %v", gotUntil, until)
	}
}

func TestWarningSilenceStoreSilenceUpsertOverwritesUntil(t *testing.T) {
	s := freshWarningSilenceStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	if err := s.Set(ctx, "alice", "warn-1", now.Add(time.Hour), now); err != nil {
		t.Fatalf("silence 1: %v", err)
	}
	newUntil := now.Add(2 * time.Hour)
	if err := s.Set(ctx, "alice", "warn-1", newUntil, now); err != nil {
		t.Fatalf("silence 2: %v", err)
	}

	got, err := s.Silences(ctx, "alice")
	if err != nil {
		t.Fatalf("silences: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d want 1 (upsert, not a second row)", len(got))
	}
	if !got["warn-1"].Equal(newUntil) {
		t.Errorf("until=%v want %v", got["warn-1"], newUntil)
	}
}

func TestWarningSilenceStoreUnsilence(t *testing.T) {
	s := freshWarningSilenceStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	if err := s.Set(ctx, "alice", "warn-1", now.Add(time.Hour), now); err != nil {
		t.Fatalf("silence: %v", err)
	}
	if err := s.Delete(ctx, "alice", "warn-1"); err != nil {
		t.Fatalf("unsilence: %v", err)
	}
	got, err := s.Silences(ctx, "alice")
	if err != nil {
		t.Fatalf("silences: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("len=%d want 0", len(got))
	}

	// Deleting a non-existent row is not an error.
	if err := s.Delete(ctx, "alice", "does-not-exist"); err != nil {
		t.Fatalf("unsilence missing: %v", err)
	}
}

func TestWarningSilenceStoreDeleteExpired(t *testing.T) {
	s := freshWarningSilenceStore(t)
	ctx := context.Background()
	now := time.Now().UTC()

	if err := s.Set(ctx, "alice", "expired", now.Add(-time.Hour), now); err != nil {
		t.Fatalf("silence expired: %v", err)
	}
	if err := s.Set(ctx, "alice", "active", now.Add(time.Hour), now); err != nil {
		t.Fatalf("silence active: %v", err)
	}

	if err := s.DeleteExpired(ctx, now); err != nil {
		t.Fatalf("delete expired: %v", err)
	}

	got, err := s.Silences(ctx, "alice")
	if err != nil {
		t.Fatalf("silences: %v", err)
	}
	if _, ok := got["expired"]; ok {
		t.Error("expired silence survived DeleteExpired")
	}
	if _, ok := got["active"]; !ok {
		t.Error("active silence was removed by DeleteExpired")
	}
}

func TestWarningSilenceStoreDeleteOtherThanKeepsOnlyActive(t *testing.T) {
	s := freshWarningSilenceStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	until := now.Add(time.Hour)

	for _, id := range []string{"warn-1", "warn-2", "warn-3"} {
		if err := s.Set(ctx, "alice", id, until, now); err != nil {
			t.Fatalf("silence %s: %v", id, err)
		}
	}

	if err := s.DeleteOtherThan(ctx, "alice", []string{"warn-2"}); err != nil {
		t.Fatalf("delete other than: %v", err)
	}

	got, err := s.Silences(ctx, "alice")
	if err != nil {
		t.Fatalf("silences: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len=%d want 1", len(got))
	}
	if _, ok := got["warn-2"]; !ok {
		t.Error("warn-2 (active) was removed")
	}
}

func TestWarningSilenceStoreDeleteOtherThanEmptyWipesUser(t *testing.T) {
	s := freshWarningSilenceStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	until := now.Add(time.Hour)

	for _, id := range []string{"warn-1", "warn-2"} {
		if err := s.Set(ctx, "alice", id, until, now); err != nil {
			t.Fatalf("silence %s: %v", id, err)
		}
	}

	if err := s.DeleteOtherThan(ctx, "alice", nil); err != nil {
		t.Fatalf("delete other than (empty): %v", err)
	}

	got, err := s.Silences(ctx, "alice")
	if err != nil {
		t.Fatalf("silences: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("len=%d want 0", len(got))
	}
}

func TestWarningSilenceStoreIsolatesUsers(t *testing.T) {
	s := freshWarningSilenceStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	until := now.Add(time.Hour)

	if err := s.Set(ctx, "alice", "warn-1", until, now); err != nil {
		t.Fatalf("silence alice: %v", err)
	}
	if err := s.Set(ctx, "bob", "warn-1", until, now); err != nil {
		t.Fatalf("silence bob: %v", err)
	}

	if err := s.Delete(ctx, "alice", "warn-1"); err != nil {
		t.Fatalf("unsilence alice: %v", err)
	}

	aliceGot, err := s.Silences(ctx, "alice")
	if err != nil {
		t.Fatalf("silences alice: %v", err)
	}
	if len(aliceGot) != 0 {
		t.Fatalf("alice len=%d want 0", len(aliceGot))
	}

	bobGot, err := s.Silences(ctx, "bob")
	if err != nil {
		t.Fatalf("silences bob: %v", err)
	}
	if len(bobGot) != 1 {
		t.Fatalf("bob len=%d want 1 (unaffected by alice's unsilence)", len(bobGot))
	}
}
