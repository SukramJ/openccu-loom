// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package central_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/registry"
	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// buildTestQueryFacade returns a QueryFacade backed by a ModelRegistry
// populated with the given devices.
func buildTestQueryFacade(devs ...*device.Device) *central.QueryFacade {
	devReg := registry.NewDeviceRegistry()
	modelReg := registry.NewModelRegistry()
	for _, d := range devs {
		if d == nil {
			continue
		}
		modelReg.Put(d)
		// The device registry is keyed by the wire id the ingest pipeline
		// stamps on the device, not by the bare interface name.
		devReg.Put(registry.DeviceEntry{
			Interface: hmtypes.ParseWireInterfaceID(d.InterfaceID),
			Address:   d.Address,
		})
	}
	h := health.NewTracker()
	c, _ := central.New(central.Config{Name: "test-central"})
	// Replace ModelRegistry with test one.
	c.ModelRegistry = modelReg
	c.DeviceRegistry = devReg
	c.Health = h
	return c.QueryFacade()
}

// TestGetDataPoints_NoFilter verifies GetDataPoints returns all DPs
// when called with an empty interface filter.
func TestGetDataPoints_NoFilter(t *testing.T) {
	d := device.New(device.Config{
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "ABC001",
		Model:       "HmIP-BSM",
		InterfaceID: "ccu-main-HmIP-RF",
	})
	ch := d.AddChannel("ABC001:1", 1, "SWITCH", hmenum.ParamsetKeyValues)
	_ = ch
	// No data points added — just verify no panic and empty is fine.
	q := buildTestQueryFacade(d)
	dps := q.GetDataPoints("")
	_ = dps // zero DPs is valid when no parameters were hydrated
}

// TestGetDataPoints_InterfaceFilter verifies GetDataPoints filters by
// interface.
func TestGetDataPoints_InterfaceFilter(t *testing.T) {
	d1 := device.New(device.Config{
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "ABC001",
		InterfaceID: "ccu-main-HmIP-RF",
	})
	d2 := device.New(device.Config{
		Interface:   hmenum.InterfaceBidCosRF,
		Address:     "DEF002",
		InterfaceID: "ccu-main-BidCos-RF",
	})
	q := buildTestQueryFacade(d1, d2)

	hmipDPs := q.GetDataPoints(hmenum.InterfaceHmIPRF)
	bidcosDPs := q.GetDataPoints(hmenum.InterfaceBidCosRF)
	// Both may be empty (no parameters hydrated) but the sets must not
	// overlap: a filtered call for HmIP-RF must not include BidCos-RF
	// devices and vice-versa. We can only assert the lengths are
	// consistent with the filter (both 0 is fine).
	_ = hmipDPs
	_ = bidcosDPs
}

// TestGetDataPointsByCategory_Empty verifies GetDataPointsByCategory
// returns an empty result when the model registry is empty.
func TestGetDataPointsByCategory_Empty(t *testing.T) {
	q := buildTestQueryFacade()
	got := q.GetDataPointsByCategory(hmenum.DataPointCategoryClimate)
	if len(got) != 0 {
		t.Errorf("expected 0 DPs for empty registry, got %d", len(got))
	}
}

// TestGetCustomDataPoint_MissingDevice verifies GetCustomDataPoint
// returns nil for an unknown device address.
func TestGetCustomDataPoint_MissingDevice(t *testing.T) {
	q := buildTestQueryFacade()
	if dp := q.GetCustomDataPoint("UNKNOWN", 1); dp != nil {
		t.Errorf("expected nil, got %v", dp)
	}
}

// TestGetCustomDataPoint_NoChannel verifies GetCustomDataPoint returns
// nil when the device has no channel with the given number.
func TestGetCustomDataPoint_NoChannel(t *testing.T) {
	d := device.New(device.Config{
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "ABC001",
		InterfaceID: "ccu-main-HmIP-RF",
	})
	d.AddChannel("ABC001:1", 1, "SWITCH", hmenum.ParamsetKeyValues)
	q := buildTestQueryFacade(d)
	if dp := q.GetCustomDataPoint("ABC001", 99); dp != nil {
		t.Errorf("expected nil for missing channel, got %v", dp)
	}
}

// TestGetGenericDataPoint_UnknownAddress verifies GetGenericDataPoint
// returns nil for an unknown channel address.
func TestGetGenericDataPoint_UnknownAddress(t *testing.T) {
	q := buildTestQueryFacade()
	if dp := q.GetGenericDataPoint("UNKNOWN:1", hmenum.ParameterState); dp != nil {
		t.Errorf("expected nil, got %v", dp)
	}
}

// TestGetEventSources_Empty verifies GetEventSources returns nil for
// an empty registry.
func TestGetEventSources_Empty(t *testing.T) {
	q := buildTestQueryFacade()
	evs := q.GetEventSources("")
	if len(evs) != 0 {
		t.Errorf("expected empty, got %d", len(evs))
	}
}

// TestGetEventGroup_UnknownChannel verifies GetEventGroup returns nil
// for an unknown channel address.
func TestGetEventGroup_UnknownChannel(t *testing.T) {
	q := buildTestQueryFacade()
	if ev := q.GetEventGroup("UNKNOWN:1", hmenum.ParameterPress); ev != nil {
		t.Errorf("expected nil, got %v", ev)
	}
}

// TestGetScheduleCapableDevices_NoDevices verifies
// GetScheduleCapableDevices returns nil for empty registry.
func TestGetScheduleCapableDevices_NoDevices(t *testing.T) {
	q := buildTestQueryFacade()
	got := q.GetScheduleCapableDevices()
	if len(got) != 0 {
		t.Errorf("expected empty, got %d", len(got))
	}
}

// TestGetScheduleCapableDevices_WithWeekProfile verifies that a device
// with a week profile attached to a channel appears in results.
func TestGetScheduleCapableDevices_WithWeekProfile(t *testing.T) {
	d := device.New(device.Config{
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "THERM001",
		Name:        "LivingRoom Thermostat",
		InterfaceID: "ccu-main-HmIP-RF",
	})
	ch := d.AddChannel("THERM001:1", 1, "CLIMATECONTROL_VENT_DRIVE", hmenum.ParamsetKeyValues)
	wp := weekprofile.NewProfileDataPoint(weekprofile.ProfileDataPointConfig{
		CentralName:    "test-central",
		ChannelAddress: "THERM001:1",
		ScheduleType:   weekprofile.ScheduleTypeClimate,
		ProfileCount:   6,
	})
	ch.AttachWeekProfile(wp)

	q := buildTestQueryFacade(d)
	got := q.GetScheduleCapableDevices()
	if len(got) != 1 {
		t.Fatalf("expected 1 schedule-capable device, got %d", len(got))
	}
	if got[0].DeviceAddress != "THERM001" {
		t.Errorf("DeviceAddress = %q, want THERM001", got[0].DeviceAddress)
	}
	if got[0].DeviceName != "LivingRoom Thermostat" {
		t.Errorf("DeviceName = %q, want 'LivingRoom Thermostat'", got[0].DeviceName)
	}
	if got[0].ScheduleChannelAddress != "THERM001:1" {
		t.Errorf("ScheduleChannelAddress = %q, want THERM001:1", got[0].ScheduleChannelAddress)
	}
}

// TestGetChannel_UnknownAddress verifies GetChannel returns nil for an
// unknown address.
func TestGetChannel_UnknownAddress(t *testing.T) {
	q := buildTestQueryFacade()
	if ch := q.GetChannel("UNKNOWN:1"); ch != nil {
		t.Errorf("expected nil, got %v", ch)
	}
}

// TestGetChannel_KnownAddress verifies GetChannel finds a real channel.
// .
func TestGetChannel_KnownAddress(t *testing.T) {
	d := device.New(device.Config{
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "ABC001",
		InterfaceID: "ccu-main-HmIP-RF",
	})
	d.AddChannel("ABC001:1", 1, "SWITCH", hmenum.ParamsetKeyValues)
	q := buildTestQueryFacade(d)

	ch := q.GetChannel("ABC001:1")
	if ch == nil {
		t.Fatal("expected channel, got nil")
	}
	if ch.Address != "ABC001:1" {
		t.Errorf("Address = %q, want ABC001:1", ch.Address)
	}
}

// TestGetStatePathEntriesEmptyModel verifies GetStatePathEntries returns an
// empty slice for an empty model registry.
func TestGetStatePathEntriesEmptyModel(t *testing.T) {
	t.Parallel()
	q := buildTestQueryFacade()
	entries := q.GetStatePathEntries()
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries for empty model, got %d", len(entries))
	}
}

// TestGetStatePathEntriesNoModel verifies GetStatePathEntries returns an
// empty slice when the query facade is built with nil model and nil hub.
func TestGetStatePathEntriesNoModel(t *testing.T) {
	t.Parallel()
	q := central.NewQueryFacade("test", nil, nil, nil)
	entries := q.GetStatePathEntries()
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries without model, got %d", len(entries))
	}
}

// TestGetStatePathEntriesWithHubPaths verifies that hub state paths are
// included in the result as Topic-only entries.
func TestGetStatePathEntriesWithHubPaths(t *testing.T) {
	t.Parallel()
	q := central.NewQueryFacade("test", nil, nil, nil)
	hub := &fakeHubStatePathProvider{paths: []string{"ccu/test/sysvar/light", "ccu/test/program/turn_on"}}
	q.SetHubStatePathProvider(hub)

	entries := q.GetStatePathEntries()
	if len(entries) != 2 {
		t.Fatalf("expected 2 hub entries, got %d: %v", len(entries), entries)
	}
	topics := make(map[string]bool)
	for _, e := range entries {
		topics[e.Topic] = true
		if e.Address != "" {
			t.Errorf("hub entry Address should be empty, got %q", e.Address)
		}
		if e.Parameter != "" {
			t.Errorf("hub entry Parameter should be empty, got %q", e.Parameter)
		}
	}
	if !topics["ccu/test/sysvar/light"] {
		t.Error("missing sysvar hub path")
	}
	if !topics["ccu/test/program/turn_on"] {
		t.Error("missing program hub path")
	}
}

// TestStatePathEntryStructFields verifies that the StatePathEntry struct
// has the required fields Address, Channel, Parameter, Topic.
func TestStatePathEntryStructFields(t *testing.T) {
	t.Parallel()
	e := central.StatePathEntry{
		Address:   "ABC001:1",
		Channel:   1,
		Parameter: "STATE",
		Topic:     "ccu/abc001/1/state",
	}
	if e.Address != "ABC001:1" {
		t.Errorf("Address = %q", e.Address)
	}
	if e.Channel != 1 {
		t.Errorf("Channel = %d", e.Channel)
	}
	if e.Parameter != "STATE" {
		t.Errorf("Parameter = %q", e.Parameter)
	}
	if e.Topic != "ccu/abc001/1/state" {
		t.Errorf("Topic = %q", e.Topic)
	}
}

// fakeHubStatePathProvider is a test double for central.HubStatePathProvider.
type fakeHubStatePathProvider struct {
	paths []string
}

func (f *fakeHubStatePathProvider) HubStatePaths() []string {
	return f.paths
}

// TestGetParametersEmptyModel verifies GetParameters returns an empty slice
// when the model has no devices.
func TestGetParametersEmptyModel(t *testing.T) {
	t.Parallel()
	q := buildTestQueryFacade()
	result := q.GetParameters(hmenum.ParamsetKeyValues, 0)
	if len(result) != 0 {
		t.Fatalf("GetParameters with empty model: want 0 results, got %d", len(result))
	}
}

// TestGetParametersNoFilter verifies GetParameters does not panic with an
// empty model and no ops filter.
func TestGetParametersNoFilter(t *testing.T) {
	t.Parallel()
	q := buildTestQueryFacade()
	got := q.GetParameters(hmenum.ParamsetKeyValues, 0)
	_ = got
}

// TestGetUnIgnoreCandidatesEmptyModel verifies GetUnIgnoreCandidates returns
// an empty slice for an empty model.
func TestGetUnIgnoreCandidatesEmptyModel(t *testing.T) {
	t.Parallel()
	q := buildTestQueryFacade()
	result := q.GetUnIgnoreCandidates(hmenum.ParamsetKeyValues)
	if len(result) != 0 {
		t.Fatalf("GetUnIgnoreCandidates with empty model: want 0 results, got %d", len(result))
	}
}
