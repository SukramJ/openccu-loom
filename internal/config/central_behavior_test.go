// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package config

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func boolPtr(b bool) *bool { return &b }

func TestCentralBehaviorScanAndFirmwareDefaults(t *testing.T) {
	t.Parallel()
	var b CentralBehavior // all nil → defaults
	if !b.EnableSysvarScanEnabled() {
		t.Error("EnableSysvarScanEnabled default should be true")
	}
	if !b.EnableProgramScanEnabled() {
		t.Error("EnableProgramScanEnabled default should be true")
	}
	if !b.IncludeInternalSysvarsEnabled() {
		t.Error("IncludeInternalSysvarsEnabled default should be true")
	}
	if b.IncludeInternalProgramsEnabled() {
		t.Error("IncludeInternalProgramsEnabled default should be false")
	}
	// Deliberate divergence from the reference stack: default true so
	// openccu-loom surfaces firmware-update entities out of the box.
	if !b.EnableDeviceFirmwareCheckEnabled() {
		t.Error("EnableDeviceFirmwareCheckEnabled default should be true")
	}
	if b.DelayNewDeviceCreationEnabled() {
		t.Error("DelayNewDeviceCreationEnabled default should be false")
	}
}

func TestParseCentralBehaviorFullBlock(t *testing.T) {
	t.Parallel()
	buf := []byte(minimalCentralYAML + `    behavior:
      enable_sysvar_scan: false
      enable_program_scan: false
      include_internal_sysvars: false
      include_internal_programs: true
      enable_device_firmware_check: false
      delay_new_device_creation: true
      sysvar_scan_interval: 90s
      sysvar_markers: [HAHM, MQTT]
      program_markers: [HX]
`)
	cfg, err := Parse(buf)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	b := cfg.Centrals[0].Behavior
	if b.EnableSysvarScanEnabled() || b.EnableProgramScanEnabled() {
		t.Error("scan toggles should decode to disabled")
	}
	if b.IncludeInternalSysvarsEnabled() || !b.IncludeInternalProgramsEnabled() {
		t.Error("internal-inclusion toggles decoded wrong")
	}
	if b.EnableDeviceFirmwareCheckEnabled() {
		t.Error("firmware check should decode to disabled")
	}
	if !b.DelayNewDeviceCreationEnabled() {
		t.Error("delay-new-device should decode to enabled")
	}
	if b.SysvarScanInterval.String() != "1m30s" {
		t.Errorf("sysvar_scan_interval = %v, want 1m30s", b.SysvarScanInterval)
	}
	if len(b.SysvarMarkers) != 2 || b.SysvarMarkers[0] != hmenum.DescriptionMarkerHAHM || b.SysvarMarkers[1] != hmenum.DescriptionMarkerMQTT {
		t.Errorf("sysvar_markers decoded wrong: %v", b.SysvarMarkers)
	}
	if len(b.ProgramMarkers) != 1 || b.ProgramMarkers[0] != hmenum.DescriptionMarkerHX {
		t.Errorf("program_markers decoded wrong: %v", b.ProgramMarkers)
	}
}

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
