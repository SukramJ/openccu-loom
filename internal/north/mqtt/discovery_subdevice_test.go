// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"reflect"
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
	desc := deviceDescriptor(ev, "", false, "Group {no}")
	ids := desc.Identifiers
	if len(ids) != 1 || ids[0] != "openccu-loom_abc0001" {
		t.Errorf("identifiers=%v, want [openccu-loom_abc0001]", desc.Identifiers)
	}
	if got := desc.ViaDevice; got != "openccu-loom_central_ccu-01" {
		t.Errorf("via_device=%q, want openccu-loom_central_ccu-01", got)
	}
	if got := desc.Name; got != "Wohnzimmer Jalousie" {
		t.Errorf("name=%q, want Wohnzimmer Jalousie", got)
	}
}

// TestDeviceDescriptorSubDevicesActive verifies the active sub-device
// path: parent reports HasSubDevices, channel reports IsInMultiGroup. The
// block stays the physical device's, central as via_device, and the channel
// group is its Home Assistant child device — under the identifier the group
// had as a device of its own, which is what Home Assistant converts the
// existing device by.
func TestDeviceDescriptorSubDevicesActive(t *testing.T) {
	ev := Event{
		Central:       "ccu-01",
		DeviceAddress: "ABC0001",
		DeviceName:    "Wohnzimmer Jalousie",
		Channel:       fakeSubDeviceChannel{groupNo: 2, multiGroup: true, subName: "Jalousie Ost"},
		Device:        fakeSubDeviceParent{hasSubs: true},
	}
	desc := deviceDescriptor(ev, "", true, "Group {no}")
	ids := desc.Identifiers
	if len(ids) != 1 || ids[0] != "openccu-loom_abc0001" {
		t.Errorf("identifiers=%v, want [openccu-loom_abc0001]", desc.Identifiers)
	}
	if got := desc.ViaDevice; got != "openccu-loom_central_ccu-01" {
		t.Errorf("via_device=%q, want openccu-loom_central_ccu-01", got)
	}
	if got := desc.Name; got != "Wohnzimmer Jalousie" {
		t.Errorf("name=%q, want the physical device's", got)
	}
	child := desc.ChildDevice
	if child == nil {
		t.Fatal("no child_device for a channel group")
	}
	if len(child.Identifiers) != 1 || child.Identifiers[0] != "openccu-loom_abc0001-2" {
		t.Errorf("child identifiers=%v, want [openccu-loom_abc0001-2]", child.Identifiers)
	}
	if child.Name != "Jalousie Ost" {
		t.Errorf("child name=%q, want Jalousie Ost", child.Name)
	}
}

// fakeLabelledSubDeviceChannel additionally satisfies subDeviceLabeller,
// as the production channel does.
type fakeLabelledSubDeviceChannel struct {
	fakeSubDeviceChannel
	label, number string
}

func (f fakeLabelledSubDeviceChannel) SubDeviceLabel() (name, number string) {
	return f.label, f.number
}

// TestDeviceDescriptorChildDeviceNameHasNoDevicePrefix: Home Assistant
// shows a child device under its parent and composes the two names, so a
// group without a name of its own is "Group <n>" in the daemon's language
// rather than "<device>-<n>", and a named group keeps its name.
func TestDeviceDescriptorChildDeviceNameHasNoDevicePrefix(t *testing.T) {
	cases := []struct {
		name          string
		label, number string
		groupLabel    string
		want          string
	}{
		{"named group", "Jalousie Ost", "", "Gruppe {no}", "Jalousie Ost"},
		{"numeric master name", "", "7", "Gruppe {no}", "Gruppe 7"},
		{"no number falls back to the group number", "", "", "Group {no}", "Group 2"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ev := Event{
				Central:       "ccu-01",
				DeviceAddress: "ABC0001",
				DeviceName:    "Wohnzimmer Jalousie",
				Channel: fakeLabelledSubDeviceChannel{
					fakeSubDeviceChannel: fakeSubDeviceChannel{groupNo: 2, multiGroup: true, subName: "Wohnzimmer Jalousie-2"},
					label:                tc.label, number: tc.number,
				},
				Device: fakeSubDeviceParent{hasSubs: true},
			}
			desc := deviceDescriptor(ev, "", true, tc.groupLabel)
			if desc.ChildDevice == nil || desc.ChildDevice.Name != tc.want {
				t.Errorf("child_device=%+v, want name %q", desc.ChildDevice, tc.want)
			}
		})
	}
}

// TestSubDeviceGroupLabelIsTranslated: the label the builder hands to
// [deviceDescriptor] comes from the catalogue in both shipped languages and
// carries the placeholder the descriptor fills.
func TestSubDeviceGroupLabelIsTranslated(t *testing.T) {
	for locale, want := range map[string]string{"de": "Gruppe {no}", "en": "Group {no}"} {
		d := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu-01")
		d.Locale = locale
		if got := d.tr(subDeviceGroupLabelKey); got != want {
			t.Errorf("%s: %q, want %q", locale, got, want)
		}
	}
}

// TestScheduleDescriptorAsChildDevice: with sub-devices on, the schedule
// device is a child of the physical device under its old identifier and the
// bare label; the block is the physical device's own, so it matches what
// every other entity of the device publishes.
func TestScheduleDescriptorAsChildDevice(t *testing.T) {
	ev := Event{
		Central:       "ccu-01",
		DeviceAddress: "ABC0001",
		DeviceName:    "Heizung",
		// A schedule on a grouped channel still hangs on the physical
		// device: a child device cannot have a child.
		Channel: fakeSubDeviceChannel{groupNo: 2, multiGroup: true, subName: "Heizung-2"},
		Device:  fakeSubDeviceParent{hasSubs: true},
	}
	desc := scheduleSubDeviceDescriptor(ev, "http://ccu", "Zeitplan", true)
	parent := deviceDescriptor(Event{
		Central: ev.Central, DeviceAddress: ev.DeviceAddress, DeviceName: ev.DeviceName, Device: ev.Device,
	}, "http://ccu", true, "Group {no}")

	child := desc.ChildDevice
	if child == nil {
		t.Fatal("no child_device for the schedule")
	}
	if len(child.Identifiers) != 1 || child.Identifiers[0] != "openccu-loom_abc0001_schedule" {
		t.Errorf("child identifiers=%v, want [openccu-loom_abc0001_schedule]", child.Identifiers)
	}
	if child.Name != "Zeitplan" {
		t.Errorf("child name=%q, want the bare label", child.Name)
	}
	desc.ChildDevice = nil
	if !reflect.DeepEqual(desc, parent) {
		t.Errorf("schedule block\n got %+v\nwant the physical device's %+v", desc, parent)
	}

	// Off, the schedule stays a device of its own linked with via_device.
	legacy := scheduleSubDeviceDescriptor(ev, "", "Zeitplan", false)
	if legacy.ChildDevice != nil || legacy.ViaDevice != "openccu-loom_abc0001" ||
		len(legacy.Identifiers) != 1 || legacy.Identifiers[0] != "openccu-loom_abc0001_schedule" ||
		legacy.Name != "Heizung Zeitplan" {
		t.Errorf("legacy schedule block = %+v", legacy)
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
	desc := deviceDescriptor(ev, "", true, "Group {no}")
	ids := desc.Identifiers
	if len(ids) != 1 || ids[0] != "openccu-loom_abc0001" {
		t.Errorf("identifiers=%v, want [openccu-loom_abc0001]", desc.Identifiers)
	}
	if got := desc.ViaDevice; got != "openccu-loom_central_ccu-01" {
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
	desc := deviceDescriptor(ev, "", true, "Group {no}")
	ids := desc.Identifiers
	if len(ids) != 1 || ids[0] != "openccu-loom_abc0001" {
		t.Errorf("identifiers=%v, want [openccu-loom_abc0001]", desc.Identifiers)
	}
	if got := desc.ViaDevice; got != "openccu-loom_central_ccu-01" {
		t.Errorf("via_device=%q, want openccu-loom_central_ccu-01", got)
	}
}
