// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// device_admin_channel_assignment_test.go covers DeviceAdminDomain's
// per-channel room/function assignment surface: SetChannelRooms,
// SetChannelFunctions, setChannelAssignment and unionChannelAssignments.

package adapter

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// recordingRoomMutator is a [hub.RoomMutator] fake that records the last
// address + rooms it was called with, so tests can assert the write goes
// out against the channel address rather than the device address.
type recordingRoomMutator struct {
	err         error
	lastAddress string
	lastRooms   []string
	calls       int
}

func (m *recordingRoomMutator) SetDeviceRooms(_ context.Context, address string, rooms []string) error {
	m.calls++
	m.lastAddress = address
	m.lastRooms = rooms
	return m.err
}

// recordingFunctionMutator mirrors [recordingRoomMutator] for functions.
type recordingFunctionMutator struct {
	err           error
	lastAddress   string
	lastFunctions []string
	calls         int
}

func (m *recordingFunctionMutator) SetDeviceFunctions(_ context.Context, address string, functions []string) error {
	m.calls++
	m.lastAddress = address
	m.lastFunctions = functions
	return m.err
}

// channelAssignmentFixture builds a central.Unit registered in a fresh
// registry, holding one device with two channels. Channel 2 starts with a
// pre-existing room/function so the union-across-channels recomputation in
// SetChannelRooms/SetChannelFunctions has more than one channel to fold
// over.
func channelAssignmentFixture(t *testing.T, centralName, deviceAddr string) (reg *central.Registry, dev *device.Device, ch1 *device.Channel) {
	t.Helper()
	c, err := central.New(central.Config{Name: centralName})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg = central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	dev = device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     deviceAddr,
		Model:       "HmIP-PS",
	})
	ch1 = dev.AddChannel(deviceAddr+":1", 1, "SWITCH", hmenum.ParamsetKeyValues)
	ch2 := dev.AddChannel(deviceAddr+":2", 2, "SWITCH", hmenum.ParamsetKeyValues)
	ch2.SetRooms([]string{"Küche"})
	ch2.SetFunctions([]string{"Heizung"})
	c.ModelRegistry.Put(dev)
	return reg, dev, ch1
}

// ---------------------------------------------------------------------------
// SetChannelRooms
// ---------------------------------------------------------------------------

func TestDeviceAdminDomain_SetChannelRooms_HappyPath(t *testing.T) {
	t.Parallel()
	reg, dev, ch1 := channelAssignmentFixture(t, "ccu-chrooms-1", "DEV020")
	mutator := &recordingRoomMutator{}
	firstUnit := reg.List()[0]
	firstUnit.HubModel.RoomMutator = mutator

	admin := NewDeviceAdminDomain(reg, nil)
	err := admin.SetChannelRooms(context.Background(), "DEV020", 1, []string{"Wohnzimmer"})
	if err != nil {
		t.Fatalf("SetChannelRooms: %v", err)
	}

	if mutator.calls != 1 {
		t.Fatalf("expected 1 call to the wired RoomMutator, got %d", mutator.calls)
	}
	if mutator.lastAddress != "DEV020:1" {
		t.Fatalf("expected the CCU write to target the channel address %q, got %q", "DEV020:1", mutator.lastAddress)
	}
	if len(ch1.Rooms()) != 1 || ch1.Rooms()[0] != "Wohnzimmer" {
		t.Fatalf("expected channel Rooms to be stamped to [Wohnzimmer], got %v", ch1.Rooms())
	}
	// dev.Rooms recomputes as the sorted union over every channel: channel 1
	// now carries "Wohnzimmer", channel 2 still carries "Küche".
	want := []string{"Küche", "Wohnzimmer"}
	if len(dev.Rooms) != len(want) {
		t.Fatalf("dev.Rooms = %v, want %v", dev.Rooms, want)
	}
	for i, w := range want {
		if dev.Rooms[i] != w {
			t.Fatalf("dev.Rooms = %v, want %v", dev.Rooms, want)
		}
	}
}

func TestDeviceAdminDomain_SetChannelRooms_UnknownDevice_ReturnsErr(t *testing.T) {
	t.Parallel()
	reg, _, _ := channelAssignmentFixture(t, "ccu-chrooms-2", "DEV021")
	admin := NewDeviceAdminDomain(reg, nil)

	err := admin.SetChannelRooms(context.Background(), "NOSUCHDEV", 1, []string{"Wohnzimmer"})
	if err == nil {
		t.Fatal("expected an error for an unknown device")
	}
}

func TestDeviceAdminDomain_SetChannelRooms_UnknownChannel_ReturnsErrChannelNotFound(t *testing.T) {
	t.Parallel()
	reg, _, _ := channelAssignmentFixture(t, "ccu-chrooms-3", "DEV022")
	admin := NewDeviceAdminDomain(reg, nil)

	err := admin.SetChannelRooms(context.Background(), "DEV022", 9, []string{"Wohnzimmer"})
	if !errors.Is(err, interfaces.ErrChannelNotFound) {
		t.Fatalf("expected errors.Is(err, ErrChannelNotFound), got %v", err)
	}
}

func TestDeviceAdminDomain_SetChannelRooms_NilRegistry_ReturnsErr(t *testing.T) {
	t.Parallel()
	admin := NewDeviceAdminDomain(nil, nil)

	err := admin.SetChannelRooms(context.Background(), "DEV023", 1, []string{"Wohnzimmer"})
	if !errors.Is(err, ErrNoDeviceBackend) {
		t.Fatalf("expected errors.Is(err, ErrNoDeviceBackend), got %v", err)
	}
}

// ---------------------------------------------------------------------------
// SetChannelFunctions
// ---------------------------------------------------------------------------

func TestDeviceAdminDomain_SetChannelFunctions_HappyPath(t *testing.T) {
	t.Parallel()
	reg, dev, ch1 := channelAssignmentFixture(t, "ccu-chfuncs-1", "DEV030")
	mutator := &recordingFunctionMutator{}
	firstUnit := reg.List()[0]
	firstUnit.HubModel.FunctionMutator = mutator

	admin := NewDeviceAdminDomain(reg, nil)
	err := admin.SetChannelFunctions(context.Background(), "DEV030", 1, []string{"Licht"})
	if err != nil {
		t.Fatalf("SetChannelFunctions: %v", err)
	}

	if mutator.calls != 1 {
		t.Fatalf("expected 1 call to the wired FunctionMutator, got %d", mutator.calls)
	}
	if mutator.lastAddress != "DEV030:1" {
		t.Fatalf("expected the CCU write to target the channel address %q, got %q", "DEV030:1", mutator.lastAddress)
	}
	if len(ch1.Functions()) != 1 || ch1.Functions()[0] != "Licht" {
		t.Fatalf("expected channel Functions to be stamped to [Licht], got %v", ch1.Functions())
	}
	want := []string{"Heizung", "Licht"}
	if len(dev.Functions) != len(want) {
		t.Fatalf("dev.Functions = %v, want %v", dev.Functions, want)
	}
	for i, w := range want {
		if dev.Functions[i] != w {
			t.Fatalf("dev.Functions = %v, want %v", dev.Functions, want)
		}
	}
}

func TestDeviceAdminDomain_SetChannelFunctions_UnknownDevice_ReturnsErr(t *testing.T) {
	t.Parallel()
	reg, _, _ := channelAssignmentFixture(t, "ccu-chfuncs-2", "DEV031")
	admin := NewDeviceAdminDomain(reg, nil)

	err := admin.SetChannelFunctions(context.Background(), "NOSUCHDEV", 1, []string{"Licht"})
	if err == nil {
		t.Fatal("expected an error for an unknown device")
	}
}

func TestDeviceAdminDomain_SetChannelFunctions_UnknownChannel_ReturnsErrChannelNotFound(t *testing.T) {
	t.Parallel()
	reg, _, _ := channelAssignmentFixture(t, "ccu-chfuncs-3", "DEV032")
	admin := NewDeviceAdminDomain(reg, nil)

	err := admin.SetChannelFunctions(context.Background(), "DEV032", 9, []string{"Licht"})
	if !errors.Is(err, interfaces.ErrChannelNotFound) {
		t.Fatalf("expected errors.Is(err, ErrChannelNotFound), got %v", err)
	}
}

func TestDeviceAdminDomain_SetChannelFunctions_NilRegistry_ReturnsErr(t *testing.T) {
	t.Parallel()
	admin := NewDeviceAdminDomain(nil, nil)

	err := admin.SetChannelFunctions(context.Background(), "DEV033", 1, []string{"Licht"})
	if !errors.Is(err, ErrNoDeviceBackend) {
		t.Fatalf("expected errors.Is(err, ErrNoDeviceBackend), got %v", err)
	}
}
