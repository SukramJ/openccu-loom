// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// scheduleEnabledFakeOps embeds paramsetFakeOps and captures SetValue
// calls so tests can assert the correct COMBINED_PARAMETER was written.
type scheduleEnabledFakeOps struct {
	paramsetFakeOps

	// setValueCalls records (address, parameter, value) for each SetValue call.
	setValueCalls []setValueCall
}

type setValueCall struct {
	address   string
	parameter hmenum.Parameter
	value     any
}

func (f *scheduleEnabledFakeOps) SetValue(
	_ context.Context,
	address string,
	parameter hmenum.Parameter,
	value any,
	_ hmenum.CommandPriority,
	_ hmenum.CommandRxMode,
) error {
	f.setValueCalls = append(f.setValueCalls, setValueCall{
		address:   address,
		parameter: parameter,
		value:     value,
	})
	return nil
}

// scheduleEnabledParamsetFakeOps also overrides GetParamset to return schedule keys
// so that FindScheduleChannel identifies the channel as a schedule channel.
func (f *scheduleEnabledFakeOps) GetParamset(_ context.Context, addr string, key hmenum.ParamsetKey) (map[string]any, error) {
	if key == hmenum.ParamsetKeyMaster && addr != "" {
		// Return one climate schedule slot key to satisfy hasScheduleParams.
		return map[string]any{"P1_ENDTIME_MONDAY_1": 480}, nil
	}
	return map[string]any{}, nil
}

// TestSetScheduleEnabled_NilRegistry verifies ErrNoScheduleBackend is returned
// when neither registry nor writer is configured.
func TestSetScheduleEnabled_NilRegistry(t *testing.T) {
	t.Parallel()
	domain := NewSchedulesDomain(nil, nil)
	err := domain.SetScheduleEnabled(context.Background(), "VCU0001", true, "1_1")
	if err == nil {
		t.Fatal("expected error when registry is nil, got nil")
	}
}

// TestSetScheduleEnabled_DeviceNotFound verifies an error is returned when the
// device cannot be found in the registry.
func TestSetScheduleEnabled_DeviceNotFound(t *testing.T) {
	t.Parallel()
	c, _ := central.New(central.Config{Name: "ccu-01"})
	reg := central.NewRegistry()
	_ = reg.Register(c)
	domain := NewSchedulesDomain(reg, client.NewValueWriter())
	err := domain.SetScheduleEnabled(context.Background(), "UNKNOWN0001", true, "1_1")
	if err == nil {
		t.Fatal("expected error for unknown device, got nil")
	}
}

// TestSetScheduleEnabled_WritesEnabledCombinedParameter verifies that enabling
// a schedule writes COMBINED_PARAMETER with WPTCLS=1,WPTCL=2 for key "1_1".
func TestSetScheduleEnabled_WritesEnabledCombinedParameter(t *testing.T) {
	t.Parallel()
	const (
		centralName   = "ccu-01"
		interfaceID   = "HmIP-RF"
		deviceAddress = "VCU0001"
		channelKey    = "1_1"
	)

	fakeOps := &scheduleEnabledFakeOps{}

	c, _ := central.New(central.Config{Name: centralName})
	reg := central.NewRegistry()
	_ = reg.Register(c)

	d := device.New(device.Config{
		Address:     deviceAddress,
		Interface:   hmenum.InterfaceHmIPRF,
		InterfaceID: interfaceID,
	})
	// Add a climate channel so FindScheduleChannel can probe it.
	d.AddChannel(deviceAddress+":1", 1, "CLIMATECONTROL_RT_TRANSCEIVER", hmenum.ParamsetKeyValues)
	c.ModelRegistry.Put(d)

	w := client.NewValueWriter()
	w.Register(centralName, interfaceID, fakeOps)
	domain := NewSchedulesDomain(reg, w)

	err := domain.SetScheduleEnabled(context.Background(), deviceAddress, true, channelKey)
	if err != nil {
		t.Fatalf("SetScheduleEnabled: %v", err)
	}
	if len(fakeOps.setValueCalls) != 1 {
		t.Fatalf("expected 1 SetValue call, got %d", len(fakeOps.setValueCalls))
	}
	call := fakeOps.setValueCalls[0]
	if call.parameter != hmenum.ParameterCombinedParameter {
		t.Fatalf("parameter = %q; want COMBINED_PARAMETER", call.parameter)
	}
	wantValue := "WPTCLS=1,WPTCL=2" // bitmask(1_1)=1, AUTO=2
	if call.value != wantValue {
		t.Fatalf("value = %q; want %q", call.value, wantValue)
	}
}

// TestSetScheduleEnabled_WritesDisabledCombinedParameter verifies that
// disabling writes WPTCL=0 (MANU mode).
func TestSetScheduleEnabled_WritesDisabledCombinedParameter(t *testing.T) {
	t.Parallel()
	const (
		centralName   = "ccu-01"
		interfaceID   = "HmIP-RF"
		deviceAddress = "VCU0002"
		channelKey    = "1_1"
	)

	fakeOps := &scheduleEnabledFakeOps{}

	c, _ := central.New(central.Config{Name: centralName})
	reg := central.NewRegistry()
	_ = reg.Register(c)

	d := device.New(device.Config{
		Address:     deviceAddress,
		Interface:   hmenum.InterfaceHmIPRF,
		InterfaceID: interfaceID,
	})
	d.AddChannel(deviceAddress+":1", 1, "CLIMATECONTROL_RT_TRANSCEIVER", hmenum.ParamsetKeyValues)
	c.ModelRegistry.Put(d)

	w := client.NewValueWriter()
	w.Register(centralName, interfaceID, fakeOps)
	domain := NewSchedulesDomain(reg, w)

	err := domain.SetScheduleEnabled(context.Background(), deviceAddress, false, channelKey)
	if err != nil {
		t.Fatalf("SetScheduleEnabled(disabled): %v", err)
	}
	if len(fakeOps.setValueCalls) == 0 {
		t.Fatal("expected at least one SetValue call, got none")
	}
	wantValue := "WPTCLS=1,WPTCL=0" // bitmask=1, MANU=0
	if got := fakeOps.setValueCalls[0].value; got != wantValue {
		t.Fatalf("value = %q; want %q", got, wantValue)
	}
}

// TestSetScheduleEnabled_UnknownChannelKey verifies that an unknown channel key
// returns a descriptive error.
func TestSetScheduleEnabled_UnknownChannelKey(t *testing.T) {
	t.Parallel()
	const (
		centralName   = "ccu-01"
		interfaceID   = "HmIP-RF"
		deviceAddress = "VCU0003"
	)

	fakeOps := &scheduleEnabledFakeOps{}

	c, _ := central.New(central.Config{Name: centralName})
	reg := central.NewRegistry()
	_ = reg.Register(c)

	d := device.New(device.Config{
		Address:     deviceAddress,
		Interface:   hmenum.InterfaceHmIPRF,
		InterfaceID: interfaceID,
	})
	d.AddChannel(deviceAddress+":1", 1, "CLIMATECONTROL_RT_TRANSCEIVER", hmenum.ParamsetKeyValues)
	c.ModelRegistry.Put(d)

	w := client.NewValueWriter()
	w.Register(centralName, interfaceID, fakeOps)
	domain := NewSchedulesDomain(reg, w)

	err := domain.SetScheduleEnabled(context.Background(), deviceAddress, true, "99_99")
	if err == nil {
		t.Fatal("expected error for unknown channel key, got nil")
	}
	if !errors.Is(err, nil) && err.Error() == "" {
		t.Fatal("expected descriptive error message")
	}
}

// ============================================================
// DevicePipeline.applyForceSensorMarks
// ============================================================

func TestApplyForceSensorMarksNilCentral(t *testing.T) {
	t.Parallel()
	p := NewDevicePipeline(nil)
	p.applyForceSensorMarks("HmIP-RF") // must not panic
}

func TestApplyForceSensorMarksWrongInterface(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-fsm"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	dev := device.New(device.Config{Address: "FSDEV001", InterfaceID: "BidCos-RF", Model: "HM-CC-RT-DN"})
	c.ModelRegistry.Put(dev)

	p := NewDevicePipeline(c)
	// Device on BidCos-RF, calling HmIP-RF → skipped
	p.applyForceSensorMarks("HmIP-RF")
}

func TestApplyForceSensorMarksMatchingInterface(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-fsm2"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	dev := device.New(device.Config{Address: "FSDEV002", InterfaceID: "HmIP-RF", Model: "HmIP-STH"})
	dev.AddChannel("FSDEV002:1", 1, "CLIMATE", hmenum.ParamsetKeyValues)
	c.ModelRegistry.Put(dev)

	p := NewDevicePipeline(c)
	// Device on right interface → processed, must not panic
	p.applyForceSensorMarks("HmIP-RF")
}

// ============================================================
// DevicePipeline.applyChannelOperationModeGating
// ============================================================

func TestApplyChannelOperationModeGatingNilCentral(t *testing.T) {
	t.Parallel()
	p := NewDevicePipeline(nil)
	p.applyChannelOperationModeGating("HmIP-RF") // must not panic
}

func TestApplyChannelOperationModeGatingMatchingInterface(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-omg"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	dev := device.New(device.Config{Address: "OMGDEV001", InterfaceID: "HmIP-RF", Model: "HmIP-STH"})
	dev.AddChannel("OMGDEV001:1", 1, "CLIMATE", hmenum.ParamsetKeyValues)
	c.ModelRegistry.Put(dev)

	p := NewDevicePipeline(c)
	p.applyChannelOperationModeGating("HmIP-RF")
}

func TestApplyChannelOperationModeGatingWrongInterface(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-omg2"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	dev := device.New(device.Config{Address: "OMGDEV002", InterfaceID: "BidCos-RF", Model: "HM-RC-4-2"})
	c.ModelRegistry.Put(dev)

	p := NewDevicePipeline(c)
	// Different interface → skipped
	p.applyChannelOperationModeGating("HmIP-RF")
}

// ============================================================
// SetActiveProfile — invalid profile key error path
// ============================================================

func TestSetActiveProfileInvalidProfileKey(t *testing.T) {
	t.Parallel()
	s := NewSchedulesDomain(nil, nil)
	err := s.SetActiveProfile(context.Background(), "DEV001", 1, "P0") // P0 is invalid
	if err == nil {
		t.Error("invalid profile P0 must return error")
	}
}

func TestSetActiveProfileProfileP7Invalid(t *testing.T) {
	t.Parallel()
	s := NewSchedulesDomain(nil, nil)
	err := s.SetActiveProfile(context.Background(), "DEV001", 1, "P7") // P7 exceeds max
	if err == nil {
		t.Error("invalid profile P7 must return error")
	}
}

func TestSetActiveProfileDeviceNotFound(t *testing.T) {
	t.Parallel()
	reg := central.NewRegistry()
	s := NewSchedulesDomain(reg, nil)
	err := s.SetActiveProfile(context.Background(), "NOSUCHDEV", 1, "P1")
	if err == nil {
		t.Error("device not found must return error")
	}
}

// ============================================================
// isValidProfileID — additional paths
// ============================================================

func TestIsValidProfileIDValidRange(t *testing.T) {
	t.Parallel()
	valid := []string{"P1", "P2", "P3", "P4", "P5", "P6"}
	for _, id := range valid {
		if !isValidProfileID(id) {
			t.Errorf("isValidProfileID(%q) = false, want true", id)
		}
	}
}

func TestIsValidProfileIDInvalid(t *testing.T) {
	t.Parallel()
	invalid := []string{"P0", "P7", "P10", "", "p1", "1", "PROFILE1"}
	for _, id := range invalid {
		if isValidProfileID(id) {
			t.Errorf("isValidProfileID(%q) = true, want false", id)
		}
	}
}

// ============================================================
// channelKeyBitmask — all branches
// ============================================================

func TestChannelKeyBitmaskKnownKey(t *testing.T) {
	t.Parallel()
	s := NewSchedulesDomain(nil, nil)
	bitmask, err := s.channelKeyBitmask(context.Background(), "DEV001", "1_1")
	if err != nil {
		t.Fatalf("channelKeyBitmask 1_1: %v", err)
	}
	if bitmask != 1 {
		t.Errorf("bitmask 1_1 = %d, want 1", bitmask)
	}
}

func TestChannelKeyBitmaskKey2_2(t *testing.T) {
	t.Parallel()
	s := NewSchedulesDomain(nil, nil)
	bitmask, err := s.channelKeyBitmask(context.Background(), "DEV001", "2_2")
	if err != nil {
		t.Fatalf("channelKeyBitmask 2_2: %v", err)
	}
	if bitmask != 16 {
		t.Errorf("bitmask 2_2 = %d, want 16", bitmask)
	}
}

func TestChannelKeyBitmaskUnknownKey(t *testing.T) {
	t.Parallel()
	s := NewSchedulesDomain(nil, nil)
	_, err := s.channelKeyBitmask(context.Background(), "DEV001", "99_9")
	if err == nil {
		t.Error("unknown channel key must error")
	}
}

func TestChannelKeyBitmaskEmptyKeyNilRegistry(t *testing.T) {
	t.Parallel()
	// nil registry → returns fallback 1_1 = 1
	s := NewSchedulesDomain(nil, nil)
	bitmask, err := s.channelKeyBitmask(context.Background(), "DEV001", "")
	if err != nil {
		t.Fatalf("empty key nil registry: %v", err)
	}
	if bitmask != 1 {
		t.Errorf("empty key nil registry = %d, want 1", bitmask)
	}
}

func TestChannelKeyBitmaskEmptyKeyDeviceNotFound(t *testing.T) {
	t.Parallel()
	reg := central.NewRegistry()
	s := NewSchedulesDomain(reg, nil)
	// Registry is non-nil but no central → device not found → fallback 1
	bitmask, err := s.channelKeyBitmask(context.Background(), "NOSUCHDEV", "")
	if err != nil {
		t.Fatalf("empty key device not found: %v", err)
	}
	if bitmask != 1 {
		t.Errorf("empty key no device = %d, want 1", bitmask)
	}
}

func TestChannelKeyBitmaskEmptyKeyDeviceNoWeekProfile(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-bitmask"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	_ = reg.Register(c)
	// Device with channel but no week profile → falls through to default
	dev := device.New(device.Config{Address: "DEV-WP", InterfaceID: "HmIP-RF", Model: "HmIP-STH"})
	dev.AddChannel("DEV-WP:1", 1, "CLIMATE", "VALUES")
	c.ModelRegistry.Put(dev)

	s := NewSchedulesDomain(reg, nil)
	bitmask, err := s.channelKeyBitmask(context.Background(), "DEV-WP", "")
	if err != nil {
		t.Fatalf("device no week profile: %v", err)
	}
	if bitmask != 1 {
		t.Errorf("device no week profile = %d, want 1", bitmask)
	}
}

func TestChannelKeyBitmaskEmptyKeyWithWeekProfile(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-bitmask-wp"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	_ = reg.Register(c)
	dev := device.New(device.Config{Address: "DEV-WP2", InterfaceID: "HmIP-RF", Model: "HmIP-eTRV-2"})
	ch := dev.AddChannel("DEV-WP2:1", 1, "CLIMATECONTROL_VENT_DRIVE", "VALUES")
	// Attach a week profile with registered channel "1_1"
	wp := weekprofile.NewProfileDataPoint(weekprofile.ProfileDataPointConfig{
		CentralName:    "ccu-bitmask-wp",
		ChannelAddress: "DEV-WP2:1",
		ScheduleType:   weekprofile.ScheduleTypeClimate,
	})
	wp.RegisterChannel("1_1", true)
	ch.AttachWeekProfile(wp)
	c.ModelRegistry.Put(dev)

	s := NewSchedulesDomain(reg, nil)
	bitmask, err := s.channelKeyBitmask(context.Background(), "DEV-WP2", "")
	if err != nil {
		t.Fatalf("device with week profile: %v", err)
	}
	// "1_1" → bitmask = 1
	if bitmask != 1 {
		t.Errorf("device with week profile 1_1 = %d, want 1", bitmask)
	}
}

// ============================================================
// resolveOps — nil registry, device not found, nil writer
// ============================================================

func TestResolveOpsNilRegistryAndWriter(t *testing.T) {
	t.Parallel()
	s := NewSchedulesDomain(nil, nil)
	_, _, err := s.resolveOps("DEV001", 1)
	if err == nil {
		t.Error("nil registry must return error")
	}
}

func TestResolveOpsDeviceNotFound(t *testing.T) {
	t.Parallel()
	reg := central.NewRegistry()
	s := NewSchedulesDomain(reg, nil)
	// writer is nil but registry exists; device not found anyway
	// Note: nil writer triggers early return
	_, _, err := s.resolveOps("NOSUCHDEV", 1)
	if err == nil {
		t.Error("nil writer must return error")
	}
}

func TestResolveOpsDeviceFoundNilWriter(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-ops"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	_ = reg.Register(c)
	dev := device.New(device.Config{Address: "DEV-OPS", InterfaceID: "HmIP-RF", Model: "HmIP-STH"})
	c.ModelRegistry.Put(dev)

	s := NewSchedulesDomain(reg, nil) // nil writer
	_, _, err = s.resolveOps("DEV-OPS", 1)
	// writer is nil → ErrNoScheduleBackend
	if err == nil {
		t.Error("nil writer with found device must return error")
	}
}

// ============================================================
// applyScheduleEnabledToModel — device found with week profile
// ============================================================

func TestApplyScheduleEnabledToModelDeviceFound(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-enabled"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	_ = reg.Register(c)
	dev := device.New(device.Config{Address: "DEV-EN", InterfaceID: "HmIP-RF", Model: "HmIP-eTRV-2"})
	ch := dev.AddChannel("DEV-EN:1", 1, "CLIMATECONTROL_VENT_DRIVE", "VALUES")
	wp := weekprofile.NewProfileDataPoint(weekprofile.ProfileDataPointConfig{
		CentralName:    "ccu-enabled",
		ChannelAddress: "DEV-EN:1",
		ScheduleType:   weekprofile.ScheduleTypeClimate,
	})
	wp.RegisterChannel("1_1", true)
	ch.AttachWeekProfile(wp)
	c.ModelRegistry.Put(dev)

	s := NewSchedulesDomain(reg, nil)
	// Call applyScheduleEnabledToModel — must not panic.
	s.applyScheduleEnabledToModel("DEV-EN", "1_1", false)
}

func TestApplyScheduleEnabledToModelDeviceNotFound(t *testing.T) {
	t.Parallel()
	reg := central.NewRegistry()
	s := NewSchedulesDomain(reg, nil)
	// No central in registry → no device → must not panic.
	s.applyScheduleEnabledToModel("NOSUCHDEV", "1_1", true)
}

func TestApplyScheduleEnabledToModelNilRegistry(t *testing.T) {
	t.Parallel()
	s := NewSchedulesDomain(nil, nil)
	// nil registry → early return, no panic.
	s.applyScheduleEnabledToModel("DEV001", "1_1", true)
}

// ============================================================
// mapToSchedule and scheduleToMap
// ============================================================

func TestMapToScheduleAndBack(t *testing.T) {
	t.Parallel()
	schedule := &handlers.ClimateSchedule{
		Profiles: map[string]handlers.ClimateProfile{
			"P1": {},
		},
	}
	m, err := scheduleToMap(schedule)
	if err != nil {
		t.Fatalf("scheduleToMap: %v", err)
	}
	if len(m) == 0 {
		t.Error("scheduleToMap: expected non-empty map")
	}
	back, err := mapToSchedule(m)
	if err != nil {
		t.Fatalf("mapToSchedule: %v", err)
	}
	if back == nil {
		t.Error("mapToSchedule: expected non-nil schedule")
	}
}

func TestScheduleToMapNilDTO(t *testing.T) {
	t.Parallel()
	m, err := scheduleToMap(nil)
	if err != nil {
		t.Fatalf("scheduleToMap nil: %v", err)
	}
	if len(m) != 0 {
		t.Errorf("scheduleToMap nil = %v, want empty", m)
	}
}

func TestMapToScheduleEmptyMap(t *testing.T) {
	t.Parallel()
	back, err := mapToSchedule(map[string]any{})
	if err != nil {
		t.Fatalf("mapToSchedule empty: %v", err)
	}
	if back == nil {
		t.Error("mapToSchedule empty: expected non-nil")
	}
}
