// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package device

import "testing"

func TestDeviceCustomDPBehaviorDefaultsTrue(t *testing.T) {
	t.Parallel()
	d := New(Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})
	if !d.LightLastBrightness() {
		t.Error("LightLastBrightness() should default to true")
	}
	if !d.UseGroupChannelForCoverState() {
		t.Error("UseGroupChannelForCoverState() should default to true")
	}
}

func TestDeviceSetCustomDPBehaviorOverrides(t *testing.T) {
	t.Parallel()
	d := New(Config{InterfaceID: "HmIP-RF", Address: "ABC0001"})

	d.SetCustomDPBehavior(false, false)
	if d.LightLastBrightness() {
		t.Error("LightLastBrightness() should be false after override")
	}
	if d.UseGroupChannelForCoverState() {
		t.Error("UseGroupChannelForCoverState() should be false after override")
	}

	d.SetCustomDPBehavior(true, false)
	if !d.LightLastBrightness() {
		t.Error("LightLastBrightness() should be true after second override")
	}
	if d.UseGroupChannelForCoverState() {
		t.Error("UseGroupChannelForCoverState() should remain false")
	}
}
