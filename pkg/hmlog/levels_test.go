// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hmlog_test

import (
	"log/slog"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmlog"
)

// --------------------------------------------------------------------------
// Default resolve — no overrides
// --------------------------------------------------------------------------

func TestLevelRegistry_DefaultResolve(t *testing.T) {
	reg := hmlog.NewLevelRegistry(slog.LevelInfo)

	got := reg.Resolve("openccu-loom.client")
	if got != slog.LevelInfo {
		t.Errorf("Resolve without overrides: got %v, want %v", got, slog.LevelInfo)
	}
}

func TestLevelRegistry_SetDefaultAffectsResolve(t *testing.T) {
	reg := hmlog.NewLevelRegistry(slog.LevelInfo)
	reg.SetDefault(slog.LevelWarn)

	got := reg.Resolve("openccu-loom.client")
	if got != slog.LevelWarn {
		t.Errorf("after SetDefault(Warn): got %v, want %v", got, slog.LevelWarn)
	}
}

func TestLevelRegistry_Default_ReturnsCurrentDefault(t *testing.T) {
	reg := hmlog.NewLevelRegistry(slog.LevelError)
	if reg.Default() != slog.LevelError {
		t.Errorf("Default(): got %v, want %v", reg.Default(), slog.LevelError)
	}
	reg.SetDefault(slog.LevelDebug)
	if reg.Default() != slog.LevelDebug {
		t.Errorf("Default() after SetDefault(Debug): got %v, want %v", reg.Default(), slog.LevelDebug)
	}
}

// --------------------------------------------------------------------------
// Exact override
// --------------------------------------------------------------------------

func TestLevelRegistry_ExactOverride(t *testing.T) {
	reg := hmlog.NewLevelRegistry(slog.LevelInfo)
	reg.Set("openccu-loom.client", slog.LevelDebug, 0)

	got := reg.Resolve("openccu-loom.client")
	if got != slog.LevelDebug {
		t.Errorf("exact override: got %v, want Debug", got)
	}
}

// --------------------------------------------------------------------------
// Hierarchical resolution
// --------------------------------------------------------------------------

func TestLevelRegistry_HierarchicalResolve_DescendantInherits(t *testing.T) {
	reg := hmlog.NewLevelRegistry(slog.LevelInfo)
	reg.Set("openccu-loom.client", slog.LevelDebug, 0)

	got := reg.Resolve("openccu-loom.client.transport.xmlrpc")
	if got != slog.LevelDebug {
		t.Errorf("descendant should inherit ancestor override: got %v, want Debug", got)
	}
}

func TestLevelRegistry_HierarchicalResolve_SiblingUsesDefault(t *testing.T) {
	reg := hmlog.NewLevelRegistry(slog.LevelInfo)
	reg.Set("openccu-loom.client", slog.LevelDebug, 0)

	got := reg.Resolve("openccu-loom.north")
	if got != slog.LevelInfo {
		t.Errorf("unrelated sibling should use default: got %v, want Info", got)
	}
}

// --------------------------------------------------------------------------
// More specific override beats general
// --------------------------------------------------------------------------

func TestLevelRegistry_MoreSpecificOverride_Wins(t *testing.T) {
	reg := hmlog.NewLevelRegistry(slog.LevelInfo)
	reg.Set("openccu-loom.client", slog.LevelDebug, 0)
	reg.Set("openccu-loom.client.transport", slog.LevelWarn, 0)

	// Specific transport sub-path: the transport override wins.
	got := reg.Resolve("openccu-loom.client.transport.xmlrpc")
	if got != slog.LevelWarn {
		t.Errorf("transport override should win: got %v, want Warn", got)
	}
}

func TestLevelRegistry_MoreSpecificOverride_OtherChildUsesParent(t *testing.T) {
	reg := hmlog.NewLevelRegistry(slog.LevelInfo)
	reg.Set("openccu-loom.client", slog.LevelDebug, 0)
	reg.Set("openccu-loom.client.transport", slog.LevelWarn, 0)

	// A child that is NOT under transport falls back to the client override.
	got := reg.Resolve("openccu-loom.client.foo")
	if got != slog.LevelDebug {
		t.Errorf("non-transport child should use client override: got %v, want Debug", got)
	}
}

// --------------------------------------------------------------------------
// Reset
// --------------------------------------------------------------------------

func TestLevelRegistry_Reset_RemovesExactPath(t *testing.T) {
	reg := hmlog.NewLevelRegistry(slog.LevelInfo)
	reg.Set("openccu-loom.client", slog.LevelDebug, 0)
	reg.Set("openccu-loom.client.transport", slog.LevelWarn, 0)

	removed := reg.Reset("openccu-loom.client.transport")
	if !removed {
		t.Fatal("Reset should return true when an override existed")
	}

	// After removal the client override should apply to the transport subtree.
	got := reg.Resolve("openccu-loom.client.transport.xmlrpc")
	if got != slog.LevelDebug {
		t.Errorf("after Reset(transport): descendant should fall through to client override; got %v", got)
	}
}

func TestLevelRegistry_Reset_ReturnsFalseForUnknownPath(t *testing.T) {
	reg := hmlog.NewLevelRegistry(slog.LevelInfo)

	removed := reg.Reset("openccu-loom.nonexistent")
	if removed {
		t.Error("Reset of non-existing path should return false")
	}
}

// --------------------------------------------------------------------------
// Path normalisation
// --------------------------------------------------------------------------

func TestLevelRegistry_PathNormalisation_CaseInsensitive(t *testing.T) {
	reg := hmlog.NewLevelRegistry(slog.LevelInfo)
	reg.Set("OpenCCU-Loom.Client", slog.LevelDebug, 0)

	got := reg.Resolve("openccu-loom.client")
	if got != slog.LevelDebug {
		t.Errorf("case-insensitive path: got %v, want Debug", got)
	}
}

func TestLevelRegistry_PathNormalisation_EmptyPathIgnored(t *testing.T) {
	reg := hmlog.NewLevelRegistry(slog.LevelInfo)
	// Set with an empty path must be a no-op; registry stays at default.
	reg.Set("", slog.LevelDebug, 0)
	if len(reg.Snapshot()) != 0 {
		t.Error("Set with empty path should not install an override")
	}
}

// --------------------------------------------------------------------------
// TTL / expiry (deterministic via fake clock)
// --------------------------------------------------------------------------

func TestLevelRegistry_TTL_ResolveBeforeExpiry(t *testing.T) {
	epoch := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := epoch
	reg := hmlog.NewLevelRegistry(slog.LevelInfo)
	reg.SetNowFunc(func() time.Time { return now })

	reg.Set("a.b", slog.LevelDebug, 100*time.Millisecond)

	now = epoch.Add(50 * time.Millisecond)
	got := reg.Resolve("a.b")
	if got != slog.LevelDebug {
		t.Errorf("before expiry: got %v, want Debug", got)
	}
}

func TestLevelRegistry_TTL_ResolveAfterExpiry(t *testing.T) {
	epoch := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := epoch
	reg := hmlog.NewLevelRegistry(slog.LevelInfo)
	reg.SetNowFunc(func() time.Time { return now })

	reg.Set("a.b", slog.LevelDebug, 100*time.Millisecond)

	now = epoch.Add(200 * time.Millisecond)
	got := reg.Resolve("a.b")
	if got != slog.LevelInfo {
		t.Errorf("after expiry: got %v, want Info (default)", got)
	}
}

func TestLevelRegistry_TTL_SnapshotBeforeExpiry(t *testing.T) {
	epoch := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := epoch
	reg := hmlog.NewLevelRegistry(slog.LevelInfo)
	reg.SetNowFunc(func() time.Time { return now })

	reg.Set("a.b", slog.LevelDebug, 100*time.Millisecond)

	now = epoch.Add(50 * time.Millisecond)
	snaps := reg.Snapshot()
	if len(snaps) != 1 {
		t.Errorf("Snapshot before expiry: want 1 entry, got %d", len(snaps))
	}
}

func TestLevelRegistry_TTL_SnapshotAfterExpiry(t *testing.T) {
	epoch := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := epoch
	reg := hmlog.NewLevelRegistry(slog.LevelInfo)
	reg.SetNowFunc(func() time.Time { return now })

	reg.Set("a.b", slog.LevelDebug, 100*time.Millisecond)

	now = epoch.Add(200 * time.Millisecond)
	snaps := reg.Snapshot()
	if len(snaps) != 0 {
		t.Errorf("Snapshot after expiry: want 0 entries, got %d", len(snaps))
	}
}

func TestLevelRegistry_Sweep_RemovesExpired(t *testing.T) {
	epoch := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := epoch
	reg := hmlog.NewLevelRegistry(slog.LevelInfo)
	reg.SetNowFunc(func() time.Time { return now })

	reg.Set("a.b", slog.LevelDebug, 100*time.Millisecond)

	now = epoch.Add(200 * time.Millisecond)
	removed := reg.Sweep()
	if removed != 1 {
		t.Errorf("Sweep after expiry: want 1 removed, got %d", removed)
	}
}

func TestLevelRegistry_Sweep_NothingRemovedBeforeExpiry(t *testing.T) {
	epoch := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := epoch
	reg := hmlog.NewLevelRegistry(slog.LevelInfo)
	reg.SetNowFunc(func() time.Time { return now })

	reg.Set("a.b", slog.LevelDebug, 100*time.Millisecond)

	now = epoch.Add(50 * time.Millisecond)
	removed := reg.Sweep()
	if removed != 0 {
		t.Errorf("Sweep before expiry: want 0 removed, got %d", removed)
	}
}

func TestLevelRegistry_Sweep_IdempotentAfterRemoval(t *testing.T) {
	epoch := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := epoch
	reg := hmlog.NewLevelRegistry(slog.LevelInfo)
	reg.SetNowFunc(func() time.Time { return now })

	reg.Set("a.b", slog.LevelDebug, 100*time.Millisecond)

	now = epoch.Add(200 * time.Millisecond)
	reg.Sweep()
	removed := reg.Sweep()
	if removed != 0 {
		t.Errorf("second Sweep should remove nothing; got %d", removed)
	}
}

// --------------------------------------------------------------------------
// Permanent override (no TTL)
// --------------------------------------------------------------------------

func TestLevelRegistry_Permanent_SurvivesSweep(t *testing.T) {
	reg := hmlog.NewLevelRegistry(slog.LevelInfo)
	reg.Set("a", slog.LevelDebug, 0)

	for i := range 3 {
		removed := reg.Sweep()
		if removed != 0 {
			t.Errorf("Sweep iteration %d removed a permanent override", i)
		}
	}

	if reg.Resolve("a") != slog.LevelDebug {
		t.Error("permanent override must survive repeated Sweeps")
	}
}

func TestLevelRegistry_Permanent_SnapshotInfo(t *testing.T) {
	reg := hmlog.NewLevelRegistry(slog.LevelInfo)
	reg.Set("a", slog.LevelDebug, 0)

	snaps := reg.Snapshot()
	if len(snaps) != 1 {
		t.Fatalf("want 1 snapshot entry, got %d", len(snaps))
	}
	info := snaps[0]
	if !info.Permanent {
		t.Error("Snapshot.Permanent should be true for a no-TTL override")
	}
	if info.RemainingMS != 0 {
		t.Errorf("Snapshot.RemainingMS should be 0 for permanent override; got %d", info.RemainingMS)
	}
}

// --------------------------------------------------------------------------
// Leveler is live
// --------------------------------------------------------------------------

func TestLevelRegistry_Leveler_IsLive(t *testing.T) {
	reg := hmlog.NewLevelRegistry(slog.LevelInfo)
	l := reg.Leveler("a.b")

	if l.Level() != slog.LevelInfo {
		t.Errorf("Leveler before Set: got %v, want Info", l.Level())
	}

	reg.Set("a.b", slog.LevelDebug, 0)
	if l.Level() != slog.LevelDebug {
		t.Errorf("Leveler after Set: got %v, want Debug", l.Level())
	}

	reg.Reset("a.b")
	if l.Level() != slog.LevelInfo {
		t.Errorf("Leveler after Reset: got %v, want Info", l.Level())
	}
}

// --------------------------------------------------------------------------
// ApplyConfig
// --------------------------------------------------------------------------

func TestLevelRegistry_ApplyConfig_ReplacesPermanentOverrides(t *testing.T) {
	epoch := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	now := epoch
	reg := hmlog.NewLevelRegistry(slog.LevelInfo)
	reg.SetNowFunc(func() time.Time { return now })

	// Install one permanent and one TTL override.
	reg.Set("old.permanent", slog.LevelError, 0)
	reg.Set("ttl.path", slog.LevelDebug, 10*time.Minute)

	err := reg.ApplyConfig(map[string]string{"x.y": "warn"})
	if err != nil {
		t.Fatalf("ApplyConfig returned unexpected error: %v", err)
	}

	// Old permanent override must be gone.
	if reg.Resolve("old.permanent") != slog.LevelInfo {
		t.Error("old permanent override should have been replaced by ApplyConfig")
	}

	// New permanent override from config must be active.
	if reg.Resolve("x.y") != slog.LevelWarn {
		t.Errorf("new config override: got %v, want Warn", reg.Resolve("x.y"))
	}

	// TTL override must still be active.
	if reg.Resolve("ttl.path") != slog.LevelDebug {
		t.Errorf("TTL override should survive ApplyConfig; got %v", reg.Resolve("ttl.path"))
	}
}

func TestLevelRegistry_ApplyConfig_InvalidLevelReturnsError(t *testing.T) {
	reg := hmlog.NewLevelRegistry(slog.LevelInfo)
	reg.Set("existing", slog.LevelError, 0)

	err := reg.ApplyConfig(map[string]string{"x.y": "trace"})
	if err == nil {
		t.Fatal("ApplyConfig with invalid level should return an error")
	}

	// Registry must remain unchanged on error.
	if reg.Resolve("existing") != slog.LevelError {
		t.Error("registry must be untouched when ApplyConfig returns an error")
	}
}

// --------------------------------------------------------------------------
// ParseLevel table
// --------------------------------------------------------------------------

func TestParseLevel_Table(t *testing.T) {
	cases := []struct {
		input   string
		want    slog.Level
		wantErr bool
	}{
		{"debug", slog.LevelDebug, false},
		{"DEBUG", slog.LevelDebug, false},
		{"Debug", slog.LevelDebug, false},
		{" debug ", slog.LevelDebug, false},
		{"info", slog.LevelInfo, false},
		{"INFO", slog.LevelInfo, false},
		{"warn", slog.LevelWarn, false},
		{"Warn", slog.LevelWarn, false},
		{"warning", slog.LevelWarn, false},
		{"WARNING", slog.LevelWarn, false},
		{"error", slog.LevelError, false},
		{"ERROR", slog.LevelError, false},
		{"", slog.LevelInfo, false},
		{"trace", slog.LevelInfo, true},
		{"CRITICAL", slog.LevelInfo, true},
		{"verbose", slog.LevelInfo, true},
	}

	for _, tc := range cases {
		t.Run("input="+tc.input, func(t *testing.T) {
			got, err := hmlog.ParseLevel(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Errorf("ParseLevel(%q): expected error, got level %v", tc.input, got)
				}
				return
			}
			if err != nil {
				t.Errorf("ParseLevel(%q): unexpected error: %v", tc.input, err)
				return
			}
			if got != tc.want {
				t.Errorf("ParseLevel(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

// --------------------------------------------------------------------------
// FormatLevel roundtrip
// --------------------------------------------------------------------------

func TestFormatLevel_Roundtrip(t *testing.T) {
	levels := []slog.Level{
		slog.LevelDebug,
		slog.LevelInfo,
		slog.LevelWarn,
		slog.LevelError,
	}

	for _, lvl := range levels {
		s := hmlog.FormatLevel(lvl)
		if s == "" {
			t.Errorf("FormatLevel(%v) returned empty string", lvl)
			continue
		}
		parsed, err := hmlog.ParseLevel(s)
		if err != nil {
			t.Errorf("ParseLevel(FormatLevel(%v)=%q) returned error: %v", lvl, s, err)
			continue
		}
		if parsed != lvl {
			t.Errorf("roundtrip %v → %q → %v: mismatch", lvl, s, parsed)
		}
	}
}

func TestFormatLevel_KnownStrings(t *testing.T) {
	cases := []struct {
		level slog.Level
		want  string
	}{
		{slog.LevelDebug, "debug"},
		{slog.LevelInfo, "info"},
		{slog.LevelWarn, "warn"},
		{slog.LevelError, "error"},
	}
	for _, tc := range cases {
		got := hmlog.FormatLevel(tc.level)
		if got != tc.want {
			t.Errorf("FormatLevel(%v) = %q, want %q", tc.level, got, tc.want)
		}
	}
}

// --------------------------------------------------------------------------
// Snapshot is sorted
// --------------------------------------------------------------------------

func TestLevelRegistry_Snapshot_SortedByPath(t *testing.T) {
	reg := hmlog.NewLevelRegistry(slog.LevelInfo)
	reg.Set("z.path", slog.LevelError, 0)
	reg.Set("a.path", slog.LevelDebug, 0)
	reg.Set("m.path", slog.LevelWarn, 0)

	snaps := reg.Snapshot()
	if len(snaps) != 3 {
		t.Fatalf("want 3 snapshot entries, got %d", len(snaps))
	}

	paths := make([]string, len(snaps))
	for i, s := range snaps {
		paths[i] = s.Path
	}

	if !sort.StringsAreSorted(paths) {
		t.Errorf("Snapshot entries are not sorted; got %v", paths)
	}
}

// --------------------------------------------------------------------------
// Concurrent access — race detector
// --------------------------------------------------------------------------

func TestLevelRegistry_ConcurrentSetResolve_NoRace(t *testing.T) {
	reg := hmlog.NewLevelRegistry(slog.LevelInfo)

	var wg sync.WaitGroup
	const n = 100

	for i := range n {
		wg.Go(func() {
			if i%2 == 0 {
				reg.Set("concurrent.path", slog.LevelDebug, 0)
			} else {
				_ = reg.Resolve("concurrent.path")
			}
		})
	}

	wg.Wait()
}
