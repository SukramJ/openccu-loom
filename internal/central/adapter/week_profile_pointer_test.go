// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"encoding/json"
	"maps"
	"strconv"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// makeRootPointerDP builds the WEEK_PROGRAM_POINTER data point in the
// shape the classic RF wall thermostat declares it: an option type in
// the device-root MASTER paramset, MIN 0 / MAX 2 with the three
// "WEEK PROGRAM n" labels.
//
// Shape taken from the paramset description of HM-TC-IT-WM-W-EU
// (VCU0000341/MASTER) and from
// ../OpenCCU-Base/firmware/rftypes/rf_tc_it_wm-w-eu.xml, where
// WEEK_PROGRAM_POINTER sits inside the device-level MASTER paramset.
func makeRootPointerDP(deviceAddress string, maxVal int) *generic.Select {
	return generic.NewSelect(generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "BidCos-RF",
			ChannelAddress: deviceAddress,
			ParamsetKey:    hmenum.ParamsetKeyMaster,
			Parameter:      string(hmenum.ParameterWeekProgramPointer),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeEnum,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
			Min:        json.RawMessage("0"),
			Max:        json.RawMessage(strconv.Itoa(maxVal)),
			ValueList:  []string{"WEEK PROGRAM 1", "WEEK PROGRAM 2", "WEEK PROGRAM 3"},
		},
	})
}

// newRootPointerDevice builds an HM-TC-IT-WM-W-EU-shaped device: no
// WEEK_PROFILE channel, no CLIMATECONTROL_* channel, the week-program
// pointer and the schedule slots both on the device-root MASTER.
func newRootPointerDevice(deviceAddress string) *device.Device {
	dev := device.New(device.Config{
		InterfaceID: "BidCos-RF",
		Interface:   hmenum.InterfaceBidCosRF,
		Address:     deviceAddress,
		Model:       "HM-TC-IT-WM-W-EU",
	})
	dev.AddChannel(deviceAddress+":2", 2, "THERMALCONTROL_TRANSMIT", hmenum.ParamsetKeyValues)
	root := dev.EnsureRootChannel()
	root.PutMaster(makeRootPointerDP(deviceAddress, 2))
	return dev
}

// pointerPut records one PutParamset call in full — the existing
// schedule fake keeps only the values, and the paramset key is exactly
// what is under test here.
type pointerPut struct {
	address  string
	paramset hmenum.ParamsetKey
	values   map[string]any
}

// pointerPutRecorder collects every write the fixture backend receives.
type pointerPutRecorder struct {
	mu   sync.Mutex
	puts []pointerPut
}

func (r *pointerPutRecorder) all() []pointerPut {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]pointerPut(nil), r.puts...)
}

// registryWithRootPointerDevice wires the fixture device into a central
// registry plus a value writer bound to a recording backend.
func registryWithRootPointerDevice(t *testing.T, deviceAddress string) (
	*central.Registry, *device.Device, *pointerPutRecorder, *client.ValueWriter,
) {
	t.Helper()
	c, err := central.New(central.Config{Name: "ccu-rf"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	dev := newRootPointerDevice(deviceAddress)
	c.ModelRegistry.Put(dev)

	rec := &pointerPutRecorder{}
	backend := &scheduleIOFakeBackend{}
	backend.putParamsetFn = func(_ context.Context, addr string, key hmenum.ParamsetKey, values map[string]any) error {
		cp := make(map[string]any, len(values))
		maps.Copy(cp, values)
		rec.mu.Lock()
		rec.puts = append(rec.puts, pointerPut{address: addr, paramset: key, values: cp})
		rec.mu.Unlock()
		return nil
	}
	w := client.NewValueWriter()
	w.Register("ccu-rf", "BidCos-RF", backend)
	return reg, dev, rec, w
}

// TestSetActiveProfileWritesTheRFPointerToTheDeviceRootMaster pins that a
// profile switch on a classic RF wall thermostat lands on the parameter
// the device actually declares: WEEK_PROGRAM_POINTER as an ENUM label in
// the device-root MASTER paramset, not ACTIVE_PROFILE in VALUES, which
// rfd rejects with "Unknown paramset" (-3).
func TestSetActiveProfileWritesTheRFPointerToTheDeviceRootMaster(t *testing.T) {
	t.Parallel()
	const addr = "RFTCIT001"
	reg, _, rec, w := registryWithRootPointerDevice(t, addr)
	domain := NewSchedulesDomain(reg, w)

	if err := domain.SetActiveProfile(t.Context(), addr, device.ChannelNumberDevice, "P2"); err != nil {
		t.Fatalf("SetActiveProfile: %v", err)
	}
	puts := rec.all()
	if len(puts) != 1 {
		t.Fatalf("puts: got %d, want 1 (%v)", len(puts), puts)
	}
	got := puts[0]
	if got.address != addr {
		t.Errorf("address: got %q, want %q", got.address, addr)
	}
	if _, unwanted := got.values["ACTIVE_PROFILE"]; unwanted {
		t.Errorf("ACTIVE_PROFILE written to a device that does not declare it: %v", got.values)
	}
	if got.paramset != hmenum.ParamsetKeyMaster {
		t.Errorf("paramset: got %q, want MASTER", got.paramset)
	}
	if v := got.values[string(hmenum.ParameterWeekProgramPointer)]; v != "WEEK PROGRAM 2" {
		t.Errorf("WEEK_PROGRAM_POINTER: got %#v, want %q", v, "WEEK PROGRAM 2")
	}
}

// TestSetActiveProfileRejectsAProfileBeyondTheRootPointerCap pins that
// the per-device cap is derived from the root-MASTER pointer descriptor
// (MAX 2 → P1..P3), not from the six-profile fallback.
func TestSetActiveProfileRejectsAProfileBeyondTheRootPointerCap(t *testing.T) {
	t.Parallel()
	const addr = "RFTCIT002"
	reg, _, rec, w := registryWithRootPointerDevice(t, addr)
	domain := NewSchedulesDomain(reg, w)

	if err := domain.SetActiveProfile(t.Context(), addr, device.ChannelNumberDevice, "P5"); err == nil {
		t.Fatalf("SetActiveProfile(P5) on a 3-program device: got nil, want an error")
	}
	if puts := rec.all(); len(puts) != 0 {
		t.Errorf("a rejected profile still reached the wire: %v", puts)
	}
}

// TestMaxProfilesForDeviceReadsTheDeviceRootPointer pins that the cap
// lookup finds a pointer declared in the device-root MASTER paramset.
func TestMaxProfilesForDeviceReadsTheDeviceRootPointer(t *testing.T) {
	t.Parallel()
	const addr = "RFTCIT003"
	reg, _, _, w := registryWithRootPointerDevice(t, addr)
	domain := NewSchedulesDomain(reg, w)

	got, err := domain.MaxProfilesForDevice(t.Context(), addr)
	if err != nil {
		t.Fatalf("MaxProfilesForDevice: %v", err)
	}
	if got != 3 {
		t.Errorf("cap: got %d, want 3 (WEEK_PROGRAM_POINTER MAX 2 in the root MASTER)", got)
	}
}

// TestDeriveWeekProfileMetadataReadsTheDeviceRootPointer pins that the
// advertised profile count of an RF wall thermostat comes from its
// root-MASTER pointer, so REST / MQTT / MCP do not offer P4..P6 on a
// three-program device.
func TestDeriveWeekProfileMetadataReadsTheDeviceRootPointer(t *testing.T) {
	t.Parallel()
	dev := newRootPointerDevice("RFTCIT004")

	meta := deriveWeekProfileMetadata(dev, dev.RootChannel())
	if meta.ProfileCount != 3 {
		t.Errorf("ProfileCount: got %d, want 3", meta.ProfileCount)
	}
}

// TestSubscribeProfilePointerSeedsFromTheDeviceRootPointer pins that
// CurrentProfile syncs from a pointer declared on the device root; it
// stayed empty for every classic RF thermostat before.
func TestSubscribeProfilePointerSeedsFromTheDeviceRootPointer(t *testing.T) {
	t.Parallel()
	dev := newRootPointerDevice("RFTCIT005")
	dp, ok := dev.RootChannel().MasterParameter(hmenum.ParameterWeekProgramPointer).(*generic.Select)
	if !ok {
		t.Fatalf("fixture: root pointer is not a *generic.Select")
	}
	dp.OnEvent(1) // 0-based → P2

	wp := weekprofile.NewProfileDataPoint(weekprofile.ProfileDataPointConfig{
		CentralName:    "ccu-rf",
		ChannelAddress: dev.Address,
		ScheduleType:   weekprofile.ScheduleTypeClimate,
		ProfileCount:   3,
	})
	subscribeProfilePointer(dev, wp)

	if got := wp.CurrentProfile(); got != "P2" {
		t.Errorf("CurrentProfile: got %q, want %q", got, "P2")
	}
}
