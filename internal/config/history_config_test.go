// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package config

import (
	"testing"
	"time"
)

// TestConfig_Validate_ClampsHistoryRetention verifies finding #5: an
// explicit history.retention below the hourly-rollup lag is clamped up to
// HistoryRetentionFloor at config load (so the purge can never delete raw
// rows before the hourly fold folds them), while zero (use-default) and
// any value at or above the floor pass through unchanged.
func TestConfig_Validate_ClampsHistoryRetention(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		in   time.Duration
		want time.Duration
	}{
		{"below floor is clamped", 30 * time.Minute, HistoryRetentionFloor},
		{"zero stays zero (use default)", 0, 0},
		{"at floor unchanged", HistoryRetentionFloor, HistoryRetentionFloor},
		{"above floor unchanged", 720 * time.Hour, 720 * time.Hour},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := Default()
			c.Persistence.History.Retention = tc.in
			if err := c.Validate(); err != nil {
				t.Fatalf("Validate: %v", err)
			}
			if got := c.Persistence.History.Retention; got != tc.want {
				t.Errorf("retention after Validate = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestHistoryConfig_NilEnabled(t *testing.T) {
	t.Parallel()
	c := HistoryConfig{}
	if c.HistoryFeatureEnabled() {
		t.Error("HistoryFeatureEnabled() = true, want false when Enabled is nil")
	}
	if c.HistoryEnabled("x") {
		t.Error("HistoryEnabled(\"x\") = true, want false when Enabled is nil")
	}
}

func TestHistoryConfig_ExplicitFalse(t *testing.T) {
	t.Parallel()
	boolPtr := func(b bool) *bool { return &b }
	c := HistoryConfig{Enabled: boolPtr(false)}
	if c.HistoryFeatureEnabled() {
		t.Error("HistoryFeatureEnabled() = true, want false when Enabled is *false")
	}
	if c.HistoryEnabled("x") {
		t.Error("HistoryEnabled(\"x\") = true, want false when Enabled is *false")
	}
}

func TestHistoryConfig_EnabledNoCentralsRestriction(t *testing.T) {
	t.Parallel()
	boolPtr := func(b bool) *bool { return &b }
	c := HistoryConfig{Enabled: boolPtr(true)}
	if !c.HistoryFeatureEnabled() {
		t.Error("HistoryFeatureEnabled() = false, want true when Enabled is *true")
	}
	if !c.HistoryEnabled("x") {
		t.Error("HistoryEnabled(\"x\") = false, want true when Enabled is *true and no DisabledCentrals")
	}
}

func TestHistoryConfig_DisabledCentralExcludesIt(t *testing.T) {
	t.Parallel()
	boolPtr := func(b bool) *bool { return &b }
	c := HistoryConfig{
		Enabled:          boolPtr(true),
		DisabledCentrals: []string{"b"},
	}
	if !c.HistoryEnabled("a") {
		t.Error("HistoryEnabled(\"a\") = false, want true when \"a\" is not in DisabledCentrals")
	}
	if c.HistoryEnabled("b") {
		t.Error("HistoryEnabled(\"b\") = true, want false when \"b\" is in DisabledCentrals")
	}
}
