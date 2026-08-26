// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"testing"
)

// fakeSubDeviceChannel satisfies SubDeviceInspector + ChannelInspector for
// the deviceDescriptor sub-device test fixtures.
type fakeSubDeviceChannel struct {
	groupNo    int
	multiGroup bool
	subName    string
}

func (f fakeSubDeviceChannel) HasParameter(string) bool { return false }
func (f fakeSubDeviceChannel) GroupNumber() int         { return f.groupNo }
func (f fakeSubDeviceChannel) IsInMultiGroup() bool     { return f.multiGroup }
func (f fakeSubDeviceChannel) SubDeviceName() string    { return f.subName }

// fakeSubDeviceParent satisfies deviceWithSubDevices.
type fakeSubDeviceParent struct {
	hasSubs bool
}

func (f fakeSubDeviceParent) HasSubDevices() bool { return f.hasSubs }

// TestDeviceDescriptorSubDevicesDisabled pins the legacy behaviour: with
// the feature flag off the descriptor identifies the physical device and
// references the central as via_device — regardless of channel grouping.
func TestDeviceDescriptorSubDevicesDisabled(t *testing.T) {
	ev := Event{
		Central:       "ccu-01",
		DeviceAddress: "ABC0001",
		DeviceName:    "Wohnzimmer Jalousie",
		Channel:       fakeSubDeviceChannel{groupNo: 2, multiGroup: true, subName: "Jalousie Ost"},
		Device:        fakeSubDeviceParent{hasSubs: true},
	}
	desc := deviceDescriptor(ev, "", false)
	ids, _ := desc["identifiers"].([]string)
	if len(ids) != 1 || ids[0] != "openccu-loom_abc0001" {
		t.Errorf("identifiers=%v, want [openccu-loom_abc0001]", desc["identifiers"])
	}
	if got, _ := desc["via_device"].(string); got != "openccu-loom_central_ccu-01" {
		t.Errorf("via_device=%q, want openccu-loom_central_ccu-01", got)
	}
	if got, _ := desc["name"].(string); got != "Wohnzimmer Jalousie" {
		t.Errorf("name=%q, want Wohnzimmer Jalousie", got)
	}
}

// TestDeviceDescriptorSubDevicesActive verifies the active sub-device
// path: parent reports HasSubDevices, channel reports IsInMultiGroup,
// the descriptor identifies the sub-device with the parent as
// via_device and the sub-device name as `name`.
func TestDeviceDescriptorSubDevicesActive(t *testing.T) {
	ev := Event{
		Central:       "ccu-01",
		DeviceAddress: "ABC0001",
		DeviceName:    "Wohnzimmer Jalousie",
		Channel:       fakeSubDeviceChannel{groupNo: 2, multiGroup: true, subName: "Jalousie Ost"},
		Device:        fakeSubDeviceParent{hasSubs: true},
	}
	desc := deviceDescriptor(ev, "", true)
	ids, _ := desc["identifiers"].([]string)
	if len(ids) != 1 || ids[0] != "openccu-loom_abc0001-2" {
		t.Errorf("identifiers=%v, want [openccu-loom_abc0001-2]", desc["identifiers"])
	}
	if got, _ := desc["via_device"].(string); got != "openccu-loom_abc0001" {
		t.Errorf("via_device=%q, want openccu-loom_abc0001", got)
	}
	if got, _ := desc["name"].(string); got != "Jalousie Ost" {
		t.Errorf("name=%q, want Jalousie Ost", got)
	}
}

// TestDeviceDescriptorSubDevicesInactiveParent ensures the parent-device
// path stays untouched when the channel lives outside any multi-group
// (group_no = 0 or single-member group) — even with the feature enabled.
func TestDeviceDescriptorSubDevicesInactiveParent(t *testing.T) {
	ev := Event{
		Central:       "ccu-01",
		DeviceAddress: "ABC0001",
		DeviceName:    "Wohnzimmer Jalousie",
		Channel:       fakeSubDeviceChannel{groupNo: 0, multiGroup: false},
		Device:        fakeSubDeviceParent{hasSubs: true},
	}
	desc := deviceDescriptor(ev, "", true)
	ids, _ := desc["identifiers"].([]string)
	if len(ids) != 1 || ids[0] != "openccu-loom_abc0001" {
		t.Errorf("identifiers=%v, want [openccu-loom_abc0001]", desc["identifiers"])
	}
	if got, _ := desc["via_device"].(string); got != "openccu-loom_central_ccu-01" {
		t.Errorf("via_device=%q, want openccu-loom_central_ccu-01", got)
	}
}

// TestDeviceDescriptorSubDevicesParentFlat ensures the parent-device
// path stays untouched when the parent device does NOT report
// HasSubDevices (single-group / singleton-groups). The active flag has
// no effect on flat devices.
func TestDeviceDescriptorSubDevicesParentFlat(t *testing.T) {
	ev := Event{
		Central:       "ccu-01",
		DeviceAddress: "ABC0001",
		DeviceName:    "Schalter Küche",
		Channel:       fakeSubDeviceChannel{groupNo: 1, multiGroup: true, subName: "irrelevant"},
		Device:        fakeSubDeviceParent{hasSubs: false},
	}
	desc := deviceDescriptor(ev, "", true)
	ids, _ := desc["identifiers"].([]string)
	if len(ids) != 1 || ids[0] != "openccu-loom_abc0001" {
		t.Errorf("identifiers=%v, want [openccu-loom_abc0001]", desc["identifiers"])
	}
	if got, _ := desc["via_device"].(string); got != "openccu-loom_central_ccu-01" {
		t.Errorf("via_device=%q, want openccu-loom_central_ccu-01", got)
	}
}
