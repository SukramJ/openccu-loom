// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package device_test

import (
	"slices"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/device"
)

// TestDeviceAssignmentsSurviveConcurrentWriteAndRead reproduces the race
// between the device-admin write path (a REST/WS handler goroutine calling
// SetRooms / SetFunctions after the CCU accepted the change) and any
// north-bound reader assembling the device payload at the same time. Both
// are ordinary concurrent HTTP handlers in one daemon, so the slice header
// must never be written while another goroutine reads it.
func TestDeviceAssignmentsSurviveConcurrentWriteAndRead(t *testing.T) {
	t.Parallel()
	d := device.New(device.Config{
		Address:   "VCU0000001",
		Rooms:     []string{"Kitchen"},
		Functions: []string{"Lighting"},
	})

	const rounds = 500
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := range rounds {
			if i%2 == 0 {
				d.SetRooms([]string{"Kitchen", "Hallway"})
				d.SetFunctions([]string{"Lighting", "Security"})
			} else {
				d.SetRooms([]string{"Kitchen"})
				d.SetFunctions([]string{"Lighting"})
			}
		}
	}()
	go func() {
		defer wg.Done()
		for range rounds {
			info := d.Info()
			if info == nil {
				t.Error("Info returned nil")
				return
			}
			_ = d.Rooms()
			_ = d.Functions()
			_ = d.Room()
			_ = d.Function()
		}
	}()
	wg.Wait()
}

// TestDeviceNameSurvivesConcurrentWriteAndRead reproduces the race between
// the device-admin rename path (Unit.RenameDevice calling SetName from a
// REST/WS handler goroutine) and any north-bound reader assembling the
// device payload at the same time. Name is a string header, so an
// unsynchronised concurrent write/read is a data race the same way the
// room/function slice headers were before assignmentMu covered them.
func TestDeviceNameSurvivesConcurrentWriteAndRead(t *testing.T) {
	t.Parallel()
	d := device.New(device.Config{
		Address: "VCU0000002",
		Name:    "Living Room Lamp",
	})

	const rounds = 500
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := range rounds {
			if i%2 == 0 {
				d.SetName("Kitchen Lamp")
			} else {
				d.SetName("Living Room Lamp")
			}
		}
	}()
	go func() {
		defer wg.Done()
		for range rounds {
			info := d.Info()
			if info == nil {
				t.Error("Info returned nil")
				return
			}
			_ = d.Name()
		}
	}()
	wg.Wait()
}

// TestDeviceAssignmentAccessorsCopyOnReadAndWrite pins that neither the
// caller of the setter nor the caller of the getter shares storage with the
// device: the alarm candidate scan and the snapshot exporter keep the
// returned slice well past the call and the snapshot exporter even
// tokenises it in place, which used to write straight through into the
// live model.
func TestDeviceAssignmentAccessorsCopyOnReadAndWrite(t *testing.T) {
	t.Parallel()
	d := device.New(device.Config{Address: "VCU0000002"})

	rooms := []string{"Kitchen"}
	functions := []string{"Lighting"}
	d.SetRooms(rooms)
	d.SetFunctions(functions)
	rooms[0] = "mutated"
	functions[0] = "mutated"
	if got := d.Rooms(); !slices.Equal(got, []string{"Kitchen"}) {
		t.Errorf("Rooms() = %v, want [Kitchen] — the setter must copy its input", got)
	}
	if got := d.Functions(); !slices.Equal(got, []string{"Lighting"}) {
		t.Errorf("Functions() = %v, want [Lighting] — the setter must copy its input", got)
	}

	read := d.Rooms()
	read[0] = "mutated"
	if got := d.Rooms(); !slices.Equal(got, []string{"Kitchen"}) {
		t.Errorf("Rooms() = %v, want [Kitchen] — the getter must hand out a copy", got)
	}
	readFn := d.Functions()
	readFn[0] = "mutated"
	if got := d.Functions(); !slices.Equal(got, []string{"Lighting"}) {
		t.Errorf("Functions() = %v, want [Lighting] — the getter must hand out a copy", got)
	}
}

// TestDeviceSingularAssignmentsFollowTheSet pins that the singular room /
// function stay derived from the current assignment. They feed MQTT
// Discovery's suggested_area, so a stale value keeps sending Home Assistant
// the room the device left.
func TestDeviceSingularAssignmentsFollowTheSet(t *testing.T) {
	t.Parallel()
	d := device.New(device.Config{
		Address:   "VCU0000003",
		Rooms:     []string{"Kitchen"},
		Functions: []string{"Lighting"},
	})
	if got := d.Room(); got != "Kitchen" {
		t.Fatalf("Room() = %q, want Kitchen", got)
	}

	d.SetRooms([]string{"Hallway"})
	d.SetFunctions([]string{"Security"})
	if got := d.Room(); got != "Hallway" {
		t.Errorf("Room() = %q, want Hallway after the reassignment", got)
	}
	if got := d.Function(); got != "Security" {
		t.Errorf("Function() = %q, want Security after the reassignment", got)
	}

	// Ambiguous assignments collapse to the empty string: Home Assistant
	// accepts a single area only, and picking one entry of a multi-room
	// set would mis-attribute the device.
	d.SetRooms([]string{"Hallway", "Kitchen"})
	d.SetFunctions(nil)
	if got := d.Room(); got != "" {
		t.Errorf("Room() = %q, want empty for a multi-room device", got)
	}
	if got := d.Function(); got != "" {
		t.Errorf("Function() = %q, want empty for an unassigned device", got)
	}
}
