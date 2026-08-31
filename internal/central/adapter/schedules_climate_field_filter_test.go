// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/registry"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// TestFilterClimateScheduleByDescKeysDropsUndeclaredSlots is the direct
// regression guard for the climate write path's unsupported-field filter:
// ExtractSupportedScheduleFields/FilterRawScheduleByFields only recognise
// the `_WP_` group-numbered shape and are a permanent no-op against
// ENDTIME_/TEMPERATURE_ climate keys, so the filter must be an exact
// membership check against the device's own MASTER description instead.
func TestFilterClimateScheduleByDescKeysDropsUndeclaredSlots(t *testing.T) {
	t.Parallel()
	raw := map[string]any{
		"P1_ENDTIME_MONDAY_1":     480,
		"P1_TEMPERATURE_MONDAY_1": 21.0,
		"P1_ENDTIME_MONDAY_2":     1440,
		"P1_TEMPERATURE_MONDAY_2": 17.0,
	}
	// The device declares only the first slot.
	descKeys := map[string]struct{}{
		"P1_ENDTIME_MONDAY_1":     {},
		"P1_TEMPERATURE_MONDAY_1": {},
	}
	got := filterClimateScheduleByDescKeys(raw, descKeys)
	if len(got) != 2 {
		t.Fatalf("filtered raw = %v, want exactly the 2 declared keys", got)
	}
	if _, ok := got["P1_ENDTIME_MONDAY_1"]; !ok {
		t.Error("declared key P1_ENDTIME_MONDAY_1 was dropped")
	}
	if _, ok := got["P1_ENDTIME_MONDAY_2"]; ok {
		t.Error("undeclared key P1_ENDTIME_MONDAY_2 was kept")
	}
}

// TestFilterClimateScheduleByDescKeysPassesThroughWhenDescIsEmpty verifies
// the "no description available" degrade path: an empty/unreadable
// descKeys must not strip a write down to nothing.
func TestFilterClimateScheduleByDescKeysPassesThroughWhenDescIsEmpty(t *testing.T) {
	t.Parallel()
	raw := map[string]any{"P1_ENDTIME_MONDAY_1": 480}
	got := filterClimateScheduleByDescKeys(raw, nil)
	if len(got) != 1 {
		t.Fatalf("filtered raw = %v, want the input unchanged", got)
	}
}

// climateFieldFilterFakeOperations wraps [fakeOperations] (already a full
// backends.Operations implementation, defined in
// device_admin_unpair_test.go) and overrides only GetParamsetDescription /
// PutParamset, so a test can serve a fixed MASTER description and capture
// exactly which keys reached the wire without re-implementing the rest of
// the interface.
type climateFieldFilterFakeOperations struct {
	*fakeOperations
	descData map[string]hmproto.ParameterData
	putCalls []map[string]any
}

func (f *climateFieldFilterFakeOperations) GetParamsetDescription(_ context.Context, _ string, _ hmenum.ParamsetKey) (map[string]hmproto.ParameterData, error) {
	return f.descData, nil
}

func (f *climateFieldFilterFakeOperations) PutParamset(_ context.Context, _ string, _ hmenum.ParamsetKey, values map[string]any, _ hmenum.CommandPriority, _ hmenum.CommandRxMode) error {
	f.putCalls = append(f.putCalls, values)
	return nil
}

// TestPutClimateScheduleDropsSlotsTheDeviceDoesNotDeclare is the end-to-end
// regression guard through the real write path: a device whose MASTER
// description advertises only 2 of the 3 slots the serializer emits for
// MONDAY must have the undeclared slot's keys stripped from the paramset
// that actually reaches PutParamset.
func TestPutClimateScheduleDropsSlotsTheDeviceDoesNotDeclare(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-climate-filter9"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	dev := device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "DEVCF1",
		Model:       "HmIP-eTRV-3",
	})
	dev.AddChannel("DEVCF1:1", 1, "CLIMATECONTROL_RECEIVER", hmenum.ParamsetKeyValues)
	c.ModelRegistry.Put(dev)
	c.DeviceRegistry.Put(registry.DeviceEntry{
		Interface: wireHmIPRF,
		Address:   "DEVCF1",
		Model:     "HmIP-eTRV-3",
	})
	c.DescRegistry.Put(wireHmIPRF, hmproto.DeviceDescription{Address: "DEVCF1", Type: "HmIP-eTRV-3"})

	// The device's own MASTER description only declares the first of the
	// two slots the schedule below will produce for MONDAY.
	fake := &climateFieldFilterFakeOperations{
		fakeOperations: &fakeOperations{kind: backends.KindCCU},
		descData: map[string]hmproto.ParameterData{
			"P1_ENDTIME_MONDAY_1":     {},
			"P1_TEMPERATURE_MONDAY_1": {},
		},
	}
	w := client.NewValueWriter()
	w.Register("ccu-climate-filter9", "HmIP-RF", fake)

	sd := NewSchedulesDomain(reg, w)
	sched := &hmapi.ClimateSchedule{
		Kind: "climate",
		Profiles: map[string]hmapi.ClimateProfile{
			"P1": {
				Weekdays: map[string]hmapi.ClimateWeekday{
					"MONDAY": {
						BaseTemperature: 17.0,
						Periods: []hmapi.ClimatePeriod{
							{StartTime: "06:00", EndTime: "22:00", Temperature: 21.0},
						},
					},
				},
			},
		},
	}
	if _, err := sd.PutClimateSchedule(context.Background(), "DEVCF1", 1, sched); err != nil {
		t.Fatalf("PutClimateSchedule: %v", err)
	}

	if len(fake.putCalls) != 1 {
		t.Fatalf("PutParamset called %d times, want 1", len(fake.putCalls))
	}
	sent := fake.putCalls[0]
	if _, ok := sent["P1_ENDTIME_MONDAY_1"]; !ok {
		t.Errorf("declared slot P1_ENDTIME_MONDAY_1 missing from the write, got %v", sent)
	}
	if _, ok := sent["P1_ENDTIME_MONDAY_2"]; ok {
		t.Errorf("undeclared slot P1_ENDTIME_MONDAY_2 reached PutParamset, got %v", sent)
	}
	if _, ok := sent["P1_ENDTIME_MONDAY_3"]; ok {
		t.Errorf("undeclared slot P1_ENDTIME_MONDAY_3 reached PutParamset, got %v", sent)
	}
}
