// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package config

import "testing"

func boolPtr(b bool) *bool { return &b }

func TestCentralBehaviorAccessorsDefaultTrue(t *testing.T) {
	t.Parallel()
	var b CentralBehavior // both pointers nil → defaults
	if !b.LightLastBrightnessEnabled() {
		t.Error("LightLastBrightnessEnabled() should default to true")
	}
	if !b.UseGroupChannelForCoverStateEnabled() {
		t.Error("UseGroupChannelForCoverStateEnabled() should default to true")
	}
}

func TestCentralBehaviorAccessorsRespectExplicitValues(t *testing.T) {
	t.Parallel()
	b := CentralBehavior{
		LightLastBrightness:          boolPtr(false),
		UseGroupChannelForCoverState: boolPtr(false),
	}
	if b.LightLastBrightnessEnabled() {
		t.Error("explicit false must disable light last-brightness")
	}
	if b.UseGroupChannelForCoverStateEnabled() {
		t.Error("explicit false must disable cover group-channel state")
	}

	b = CentralBehavior{
		LightLastBrightness:          boolPtr(true),
		UseGroupChannelForCoverState: boolPtr(true),
	}
	if !b.LightLastBrightnessEnabled() || !b.UseGroupChannelForCoverStateEnabled() {
		t.Error("explicit true must enable both")
	}
}

func TestParseCentralBehaviorFromYAML(t *testing.T) {
	t.Parallel()
	buf := []byte(minimalCentralYAML + `    behavior:
      light_last_brightness: false
      use_group_channel_for_cover_state: false
`)
	cfg, err := Parse(buf)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(cfg.Centrals) != 1 {
		t.Fatalf("expected 1 central, got %d", len(cfg.Centrals))
	}
	b := cfg.Centrals[0].Behavior
	if b.LightLastBrightnessEnabled() {
		t.Error("YAML light_last_brightness: false should decode to disabled")
	}
	if b.UseGroupChannelForCoverStateEnabled() {
		t.Error("YAML use_group_channel_for_cover_state: false should decode to disabled")
	}
}

// A central with no behavior block keeps both toggles enabled.
func TestParseCentralBehaviorDefaultsWhenAbsent(t *testing.T) {
	t.Parallel()
	cfg, err := Parse([]byte(minimalCentralYAML))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	b := cfg.Centrals[0].Behavior
	if !b.LightLastBrightnessEnabled() || !b.UseGroupChannelForCoverStateEnabled() {
		t.Error("absent behavior block must leave both toggles enabled")
	}
}
