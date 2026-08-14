// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"testing"
)

// fakeRoomDevice satisfies deviceWithRoom, the read-side contract the
// device block uses for Home Assistant's suggested_area.
type fakeRoomDevice struct {
	room string
}

func (f fakeRoomDevice) Room() string { return f.room }

// TestDeviceDescriptorSuggestedAreaFollowsTheDeviceRoom pins that the HA
// device block carries the device's resolved single room, and only that
// one: the model collapses a multi-room or unassigned device to the empty
// string because HA accepts a single area and picking an arbitrary entry
// would mis-attribute a device that spans rooms.
func TestDeviceDescriptorSuggestedAreaFollowsTheDeviceRoom(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		room string
		want string
	}{
		{name: "single room", room: "Wohnzimmer", want: "Wohnzimmer"},
		{name: "ambiguous or unassigned", room: "", want: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ev := Event{
				Central:       "ccu-01",
				DeviceAddress: "ABC0001",
				DeviceName:    "Stehlampe",
				Device:        fakeRoomDevice{room: tc.room},
			}
			desc := deviceDescriptor(ev, "", false)
			got, _ := desc["suggested_area"].(string)
			if got != tc.want {
				t.Errorf("suggested_area = %q, want %q", got, tc.want)
			}
			if tc.want == "" {
				if _, present := desc["suggested_area"]; present {
					t.Errorf("suggested_area must be absent for an ambiguous room, got %v", desc["suggested_area"])
				}
			}
		})
	}
}

// TestScheduleSubDeviceDescriptorInheritsTheParentRoom pins that the
// schedule sub-device lands in the same HA area as the device it hangs
// off — it is the same physical unit and an unassigned schedule card
// shows up in HA's "no area" bucket.
func TestScheduleSubDeviceDescriptorInheritsTheParentRoom(t *testing.T) {
	t.Parallel()
	ev := Event{
		Central:       "ccu-01",
		DeviceAddress: "ABC0002",
		DeviceName:    "Heizung",
		Device:        fakeRoomDevice{room: "Bad"},
	}
	desc := scheduleSubDeviceDescriptor(ev, "", "Zeitprogramm")
	if got, _ := desc["suggested_area"].(string); got != "Bad" {
		t.Errorf("suggested_area = %q, want Bad", got)
	}
}
