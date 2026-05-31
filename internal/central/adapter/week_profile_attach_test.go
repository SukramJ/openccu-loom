// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestAttachWeekProfileToChannelSetsDescriptor verifies the helper
// installs a [weekprofile.ProfileDataPoint] on a fresh channel.
func TestAttachWeekProfileToChannelSetsDescriptor(t *testing.T) {
	t.Parallel()
	ch := &device.Channel{Address: "ABC:1"}
	if ch.HasWeekProfile() {
		t.Fatal("fresh channel must not have a week profile yet")
	}
	attachWeekProfileToChannel(ch, "TestCentral")
	if !ch.HasWeekProfile() {
		t.Fatal("attachWeekProfileToChannel did not set the descriptor")
	}
	wp := ch.WeekProfile()
	if wp == nil {
		t.Fatal("WeekProfile() returned nil after attach")
	}
	if wp.ScheduleType() != weekprofile.ScheduleTypeClimate {
		t.Errorf("ScheduleType = %v, want %v", wp.ScheduleType(), weekprofile.ScheduleTypeClimate)
	}
	if wp.ProfileCount() != 6 {
		t.Errorf("ProfileCount = %d, want 6 (CCU max)", wp.ProfileCount())
	}
}

// TestAttachWeekProfileToChannelIdempotent verifies repeated attach
// calls are no-ops once a descriptor is in place. Important because
// the pipeline hits attachWeekProfileToChannel once per filtered slot
// parameter — and a single channel has up to 84 slots.
func TestAttachWeekProfileToChannelIdempotent(t *testing.T) {
	t.Parallel()
	ch := &device.Channel{Address: "ABC:1"}
	attachWeekProfileToChannel(ch, "TestCentral")
	first := ch.WeekProfile()
	if first == nil {
		t.Fatal("first attach must produce a descriptor")
	}
	// Second + third call: same descriptor must be retained.
	attachWeekProfileToChannel(ch, "TestCentral")
	attachWeekProfileToChannel(ch, "TestCentral")
	if ch.WeekProfile() != first {
		t.Error("repeated attach replaced the descriptor — must be a no-op once one is attached")
	}
}

// TestAttachWeekProfileToChannelNilNoop verifies the helper is safe
// against a nil channel — the pipeline calls it from a hot loop and
// must not panic on edge fixtures.
func TestAttachWeekProfileToChannelNilNoop(t *testing.T) {
	t.Parallel()
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("attachWeekProfileToChannel(nil) panicked: %v", r)
		}
	}()
	attachWeekProfileToChannel(nil, "X")
}

// TestDeviceHasWeekProfileTracksAttachedDescriptor links the device-
// level gate to the channel-level descriptor: HasWeekProfile is true
// iff at least one channel has an attached descriptor, regardless of
// how many MASTER parameters the channel carries.
func TestDeviceHasWeekProfileTracksAttachedDescriptor(t *testing.T) {
	t.Parallel()
	d := device.New(device.Config{
		InterfaceID: "Test-HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "0001ABCD",
	})
	d.AddChannel("0001ABCD:1", 1, "CLIMATE_THERMOSTAT", hmenum.ParamsetKeyValues)
	if d.HasWeekProfile() {
		t.Fatal("fresh device must not report week profile")
	}
	attachWeekProfileToChannel(d.Channel("0001ABCD:1"), "Test")
	if !d.HasWeekProfile() {
		t.Fatal("HasWeekProfile must reflect channel-level attached descriptor")
	}
}
