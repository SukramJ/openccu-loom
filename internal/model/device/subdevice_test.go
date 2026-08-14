// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package device

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// Channel.SubDeviceName resolution chain:
//
//   1. master.Name is purely numeric → "<device.Name>-<master.Name>"
//   2. master.Name is a non-empty non-numeric string → master.Name
//   3. master.Name is empty → "<device.Name>-<group_no>"
//   4. group master cannot be located → empty string

func newSubDeviceFixture(t *testing.T, deviceName, masterName string) (dev *Device, master, member *Channel) {
	t.Helper()
	dev = New(Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "DEV0001",
		Name:        deviceName,
	})
	master = dev.AddChannel("DEV0001:5", 5, "TYPE_A", "")
	master.GroupNo = 5
	master.SetName(masterName)

	member = dev.AddChannel("DEV0001:6", 6, "TYPE_A", "")
	member.GroupNo = 5

	dev.AddChannelToGroup(5, 5)
	dev.AddChannelToGroup(5, 6)
	return dev, master, member
}

func TestChannelSubDeviceName_MasterNameWins(t *testing.T) {
	_, master, member := newSubDeviceFixture(t, "DeviceName", "Jalousie Ost")
	if got := master.SubDeviceName(); got != "Jalousie Ost" {
		t.Errorf("master.SubDeviceName()=%q, want Jalousie Ost", got)
	}
	if got := member.SubDeviceName(); got != "Jalousie Ost" {
		t.Errorf("member.SubDeviceName()=%q, want Jalousie Ost", got)
	}
}

func TestChannelSubDeviceName_NumericMasterCombines(t *testing.T) {
	_, master, member := newSubDeviceFixture(t, "DeviceName", "3")
	want := "DeviceName-3"
	if got := master.SubDeviceName(); got != want {
		t.Errorf("master.SubDeviceName()=%q, want %s", got, want)
	}
	if got := member.SubDeviceName(); got != want {
		t.Errorf("member.SubDeviceName()=%q, want %s", got, want)
	}
}

func TestChannelSubDeviceName_EmptyMasterFallsBackToGroupNo(t *testing.T) {
	_, _, member := newSubDeviceFixture(t, "DeviceName", "")
	want := "DeviceName-5"
	if got := member.SubDeviceName(); got != want {
		t.Errorf("member.SubDeviceName()=%q, want %s", got, want)
	}
}

func TestChannelSubDeviceName_NoGroupReturnsEmpty(t *testing.T) {
	d := New(Config{InterfaceID: "HmIP-RF", Address: "DEV0001", Name: "Foo"})
	ch := d.AddChannel("DEV0001:1", 1, "TYPE_A", "")
	if got := ch.SubDeviceName(); got != "" {
		t.Errorf("ungrouped channel: SubDeviceName()=%q, want empty", got)
	}
}

func TestChannelIsInMultiGroup(t *testing.T) {
	d, master, member := newSubDeviceFixture(t, "DeviceName", "Jalousie Ost")
	if !master.IsInMultiGroup() {
		t.Error("master in multi-channel group must report IsInMultiGroup=true")
	}
	if !member.IsInMultiGroup() {
		t.Error("member in multi-channel group must report IsInMultiGroup=true")
	}

	loner := d.AddChannel("DEV0001:7", 7, "TYPE_A", "")
	loner.GroupNo = 7
	d.AddChannelToGroup(7, 7)
	if loner.IsInMultiGroup() {
		t.Error("singleton group must NOT report IsInMultiGroup=true (single-DP sub-device adds no value)")
	}

	flat := d.AddChannel("DEV0001:8", 8, "TYPE_A", "")
	if flat.IsInMultiGroup() {
		t.Error("ungrouped channel must NOT report IsInMultiGroup=true")
	}
}

func TestChannelGroupNumber(t *testing.T) {
	d := New(Config{InterfaceID: "HmIP-RF", Address: "DEV0001"})
	ch := d.AddChannel("DEV0001:1", 1, "TYPE_A", "")
	if got := ch.GroupNumber(); got != 0 {
		t.Errorf("default GroupNumber()=%d, want 0", got)
	}
	ch.GroupNo = 4
	if got := ch.GroupNumber(); got != 4 {
		t.Errorf("set GroupNumber()=%d, want 4", got)
	}
}

func TestDeviceInfoPayloadHasSubDevicesField(t *testing.T) {
	d := New(Config{InterfaceID: "HmIP-RF", Address: "DEV0001", Name: "Flat"})
	info, ok := d.Info().(*payload.DeviceInfo)
	if !ok || info == nil {
		t.Fatal("InfoPayload missing has_sub_devices key")
	}
	if info.HasSubDevices {
		t.Errorf("flat device: has_sub_devices=%v, want false", info.HasSubDevices)
	}

	d.AddChannelToGroup(1, 1)
	d.AddChannelToGroup(1, 2)
	d.AddChannelToGroup(2, 3)
	d.AddChannelToGroup(2, 4)
	info, _ = d.Info().(*payload.DeviceInfo)
	if !info.HasSubDevices {
		t.Errorf("multi-group device: has_sub_devices=%v, want true", info.HasSubDevices)
	}
}

func TestChannelInfoPayloadSubDeviceFields(t *testing.T) {
	_, _, member := newSubDeviceFixture(t, "DeviceName", "Jalousie Ost")
	info, ok := member.Info().(*payload.ChannelInfo)
	if !ok || info == nil {
		t.Fatal("InfoPayload must return *payload.ChannelInfo")
	}
	if !info.IsInMultiGroup {
		t.Errorf("is_in_multi_group=%v, want true", info.IsInMultiGroup)
	}
	if info.SubDeviceName != "Jalousie Ost" {
		t.Errorf("sub_device_name=%v, want Jalousie Ost", info.SubDeviceName)
	}
}
