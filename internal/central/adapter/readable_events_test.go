// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// stubLoader is a minimal device.ValueLoader that records call counts
// per parameter and returns canned values. Reused across the readable
// event tests.
type stubLoader struct {
	calls atomic.Int32
	value any
	// param keys the bulk VALUES response so the requested DP is seeded.
	param hmenum.Parameter
}

func (s *stubLoader) GetValue(_ context.Context, _ string, _ hmenum.Parameter) (any, error) {
	s.calls.Add(1)
	return s.value, nil
}

// GetParamset is the VALUES load path (per-channel bulk fetch); it records the
// call and returns the configured value keyed under s.param.
func (s *stubLoader) GetParamset(_ context.Context, _ string, _ hmenum.ParamsetKey) (map[string]any, error) {
	s.calls.Add(1)
	if s.param == "" {
		return nil, nil
	}
	return map[string]any{string(s.param): s.value}, nil
}

// TestSeedReadableEventsLoadsButtonsWithoutObservedValue pins the core
// behaviour: fresh button DPs (Operations: Read+Event) on a channel
// must be loaded once during bootstrap. fetch_all_device_data does not
// always ship event values, so the explicit pass is the only way to
// guarantee a starting state.
func TestSeedReadableEventsLoadsButtonsWithoutObservedValue(t *testing.T) {
	t.Parallel()

	unit, _ := central.New(central.Config{Name: "TestCentral"})
	wireID := WireInterfaceID(unit.Name(), hmenum.InterfaceHmIPRF)

	d := device.New(device.Config{
		InterfaceID: wireID,
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "0001ABCD",
	})
	ch := d.AddChannel("0001ABCD:1", 1, "REMOTECONTROL_RECEIVER", hmenum.ParamsetKeyValues)
	// Build a button DP — Resolver maps Operations=Read+Event with
	// Type=ACTION/BOOL to the Button category.
	btn := generic.NewDataPoint[bool](generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    wireID,
			ChannelAddress: "0001ABCD:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterPressShort),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeAction,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
		Kind: generic.KindButton,
	})
	ch.Put(btn)

	// Wire a stub loader so LoadValue actually runs.
	loader := &stubLoader{value: false, param: hmenum.ParameterPressShort}
	d.SetValueLoader(loader)
	unit.ModelRegistry.Put(d)

	seedReadableEvents(context.Background(), unit, hmenum.InterfaceHmIPRF, nil)

	if got := loader.calls.Load(); got == 0 {
		t.Fatalf("loader was never called — readable event DP not seeded")
	}
}

// TestSeedReadableEventsSkipsObservedDPs verifies the helper is a
// no-op for DPs that already have an observed value (fetch_all_device_data
// shipped one). Bootstrap latency matters; redundant CCU calls hurt.
func TestSeedReadableEventsSkipsObservedDPs(t *testing.T) {
	t.Parallel()

	unit, _ := central.New(central.Config{Name: "TestCentral"})
	wireID := WireInterfaceID(unit.Name(), hmenum.InterfaceHmIPRF)
	d := device.New(device.Config{
		InterfaceID: wireID,
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "0001ABCD",
	})
	ch := d.AddChannel("0001ABCD:1", 1, "REMOTECONTROL_RECEIVER", hmenum.ParamsetKeyValues)
	btn := generic.NewDataPoint[bool](generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    wireID,
			ChannelAddress: "0001ABCD:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterPressShort),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeAction,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
	// Mark observed by feeding a wire value through OnWireValue.
	if !btn.OnWireValue(true) {
		t.Fatalf("OnWireValue(true) failed — fixture cannot mark DP as observed")
	}
	ch.Put(btn)

	loader := &stubLoader{value: true}
	d.SetValueLoader(loader)
	unit.ModelRegistry.Put(d)

	seedReadableEvents(context.Background(), unit, hmenum.InterfaceHmIPRF, nil)

	if got := loader.calls.Load(); got != 0 {
		t.Errorf("loader called %d times for already-observed DP, want 0", got)
	}
}

// TestSeedReadableEventsSkipsNonReadableDPs verifies that write-only
// or pure-event-without-read DPs do not trigger a load.
func TestSeedReadableEventsSkipsNonReadableDPs(t *testing.T) {
	t.Parallel()

	unit, _ := central.New(central.Config{Name: "TestCentral"})
	wireID := WireInterfaceID(unit.Name(), hmenum.InterfaceHmIPRF)
	d := device.New(device.Config{
		InterfaceID: wireID,
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "0001ABCD",
	})
	ch := d.AddChannel("0001ABCD:1", 1, "REMOTECONTROL_RECEIVER", hmenum.ParamsetKeyValues)
	// Write-only / event-only — IsReadable() returns false.
	dp := generic.NewDataPoint[bool](generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    wireID,
			ChannelAddress: "0001ABCD:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterPressShort),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeAction,
			Operations: hmenum.OperationsEvent, // no Read flag
		},
	})
	ch.Put(dp)

	loader := &stubLoader{value: true}
	d.SetValueLoader(loader)
	unit.ModelRegistry.Put(d)

	seedReadableEvents(context.Background(), unit, hmenum.InterfaceHmIPRF, nil)

	if got := loader.calls.Load(); got != 0 {
		t.Errorf("loader called %d times for non-readable DP, want 0", got)
	}
}

// TestSeedReadableEventsRespectsWireInterfaceID pins the
// multi-instance contract: devices stamped with `<central>-<iface>`
// only get loaded when the wireID matches; bare-iface devices from
// another central are skipped.
func TestSeedReadableEventsRespectsWireInterfaceID(t *testing.T) {
	t.Parallel()

	unit, _ := central.New(central.Config{Name: "PrimaryDaemon"})
	otherCentralWireID := WireInterfaceID("BackupDaemon", hmenum.InterfaceHmIPRF)
	d := device.New(device.Config{
		InterfaceID: otherCentralWireID, // does NOT match unit's wireID
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "0001ABCD",
	})
	ch := d.AddChannel("0001ABCD:1", 1, "REMOTECONTROL_RECEIVER", hmenum.ParamsetKeyValues)
	ch.Put(generic.NewDataPoint[bool](generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    otherCentralWireID,
			ChannelAddress: "0001ABCD:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterPressShort),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeAction,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	}))

	loader := &stubLoader{value: true}
	d.SetValueLoader(loader)
	unit.ModelRegistry.Put(d)

	seedReadableEvents(context.Background(), unit, hmenum.InterfaceHmIPRF, nil)

	if got := loader.calls.Load(); got != 0 {
		t.Errorf("loader called %d times for foreign-central device, want 0", got)
	}
}
