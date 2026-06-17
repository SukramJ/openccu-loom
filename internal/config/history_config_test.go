// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package config

import "testing"

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
