// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// recordingLoader records every GetValue call's (address, parameter)
// tuple so tests can assert which DPs were swept and which were
// skipped because they were already observed.
type recordingLoader struct {
	calls atomic.Int32
	value any
	// param is the VALUES parameter the bulk GetParamset response is keyed
	// under, so the requested data point is actually filled. Empty means the
	// response carries no parameter (the load still counts as a call).
	param hmenum.Parameter
	err   error
}

func (l *recordingLoader) GetValue(_ context.Context, _ string, _ hmenum.Parameter) (any, error) {
	l.calls.Add(1)
	if l.err != nil {
		return nil, l.err
	}
	return l.value, nil
}

// GetParamset is the VALUES load path (per-channel bulk fetch). It records the
// call and returns the configured value keyed under l.param.
func (l *recordingLoader) GetParamset(_ context.Context, _ string, _ hmenum.ParamsetKey) (map[string]any, error) {
	l.calls.Add(1)
	if l.err != nil {
		return nil, l.err
	}
	if l.param == "" {
		return nil, nil
	}
	return map[string]any{string(l.param): l.value}, nil
}

// makeUnreachDP builds a Channel-0 UNREACH DP fixture (the canonical
// member of relevantInitParameters).
func makeUnreachDP(channelAddr, wireID string, observed bool) *generic.DataPoint[bool] {
	dp := generic.NewDataPoint[bool](generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    wireID,
			ChannelAddress: channelAddr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterUnreach),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
	if observed {
		dp.OnWireValue(false)
	}
	return dp
}

func TestUnobservedSweepLoadsRelevantInitParameter(t *testing.T) {
	t.Parallel()
	reg := central.NewRegistry()
	unit, _ := central.New(central.Config{Name: "TestCentral"})
	if err := reg.Register(unit); err != nil {
		t.Fatalf("register: %v", err)
	}
	wireID := WireInterfaceID(unit.Name(), hmenum.InterfaceHmIPRF)

	d := device.New(device.Config{
		InterfaceID: wireID,
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "0001ABCD",
	})
	ch0 := d.AddChannel("0001ABCD:0", 0, "MAINTENANCE", hmenum.ParamsetKeyValues)
	// Unobserved UNREACH — sweep MUST load.
	ch0.Put(makeUnreachDP("0001ABCD:0", wireID, false))
	loader := &recordingLoader{value: false, param: hmenum.ParameterUnreach}
	d.SetValueLoader(loader)
	unit.ModelRegistry.Put(d)

	sweep := NewUnobservedSweep(reg, nil)
	loaded, errored := sweep.SweepUnobserved(context.Background())
	if loaded != 1 {
		t.Errorf("loaded = %d, want 1", loaded)
	}
	if errored != 0 {
		t.Errorf("errored = %d, want 0", errored)
	}
	if got := loader.calls.Load(); got != 1 {
		t.Errorf("loader calls = %d, want 1", got)
	}
}

func TestUnobservedSweepSkipsAlreadyObservedDPs(t *testing.T) {
	t.Parallel()
	reg := central.NewRegistry()
	unit, _ := central.New(central.Config{Name: "TestCentral"})
	if err := reg.Register(unit); err != nil {
		t.Fatalf("register: %v", err)
	}
	wireID := WireInterfaceID(unit.Name(), hmenum.InterfaceHmIPRF)

	d := device.New(device.Config{
		InterfaceID: wireID,
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "0001ABCD",
	})
	ch0 := d.AddChannel("0001ABCD:0", 0, "MAINTENANCE", hmenum.ParamsetKeyValues)
	// Observed — sweep must skip.
	ch0.Put(makeUnreachDP("0001ABCD:0", wireID, true))
	loader := &recordingLoader{value: false}
	d.SetValueLoader(loader)
	unit.ModelRegistry.Put(d)

	sweep := NewUnobservedSweep(reg, nil)
	loaded, errored := sweep.SweepUnobserved(context.Background())
	if loaded != 0 || errored != 0 {
		t.Errorf("(loaded, errored) = (%d, %d), want (0, 0)", loaded, errored)
	}
	if got := loader.calls.Load(); got != 0 {
		t.Errorf("loader calls = %d, want 0 (already observed)", got)
	}
}

func TestUnobservedSweepCountsLoadFailureAsErrored(t *testing.T) {
	t.Parallel()
	reg := central.NewRegistry()
	unit, _ := central.New(central.Config{Name: "TestCentral"})
	if err := reg.Register(unit); err != nil {
		t.Fatalf("register: %v", err)
	}
	wireID := WireInterfaceID(unit.Name(), hmenum.InterfaceHmIPRF)

	d := device.New(device.Config{
		InterfaceID: wireID,
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "0001ABCD",
	})
	ch0 := d.AddChannel("0001ABCD:0", 0, "MAINTENANCE", hmenum.ParamsetKeyValues)
	ch0.Put(makeUnreachDP("0001ABCD:0", wireID, false))

	// Loader returns an error — sweep counts it as errored, not loaded.
	loader := &recordingLoader{err: errors.New("ccu down")}
	d.SetValueLoader(loader)
	unit.ModelRegistry.Put(d)

	sweep := NewUnobservedSweep(reg, nil)
	loaded, errored := sweep.SweepUnobserved(context.Background())
	if loaded != 0 {
		t.Errorf("loaded = %d, want 0", loaded)
	}
	if errored != 1 {
		t.Errorf("errored = %d, want 1", errored)
	}
}

func TestUnobservedSweepLoadsReadableEvents(t *testing.T) {
	t.Parallel()
	reg := central.NewRegistry()
	unit, _ := central.New(central.Config{Name: "TestCentral"})
	if err := reg.Register(unit); err != nil {
		t.Fatalf("register: %v", err)
	}
	wireID := WireInterfaceID(unit.Name(), hmenum.InterfaceHmIPRF)

	d := device.New(device.Config{
		InterfaceID: wireID,
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "0001ABCD",
	})
	// Add a button on Channel 1 (not Channel 0) to verify the helper
	// walks every channel for events, not just Channel 0.
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
		Kind: generic.KindButton,
	})
	ch.Put(btn)

	loader := &recordingLoader{value: false, param: hmenum.ParameterPressShort}
	d.SetValueLoader(loader)
	unit.ModelRegistry.Put(d)

	sweep := NewUnobservedSweep(reg, nil)
	loaded, _ := sweep.SweepUnobserved(context.Background())
	if loaded != 1 {
		t.Errorf("loaded = %d, want 1 (button on Channel 1)", loaded)
	}
}

func TestUnobservedSweepNilRegistryNoop(t *testing.T) {
	t.Parallel()
	sweep := NewUnobservedSweep(nil, nil)
	loaded, errored := sweep.SweepUnobserved(context.Background())
	if loaded != 0 || errored != 0 {
		t.Errorf("(loaded, errored) = (%d, %d), want (0, 0)", loaded, errored)
	}
}

// ============================================================
// isLoadableValueDP — all branches
// ============================================================

// visibleDP wraps parameter + visibility for isLoadableValueDP tests.
type visibleDP struct {
	visible bool
	ops     hmenum.Operations
}

func (d *visibleDP) RawValue() (any, bool)       { return nil, false }
func (d *visibleDP) Parameter() hmenum.Parameter { return "LEVEL" }
func (d *visibleDP) Visible() bool               { return d.visible }
func (d *visibleDP) ParameterData() hmproto.ParameterData {
	return hmproto.ParameterData{Operations: d.ops}
}

// nonVisibleDP does NOT implement Visible() — falls through visibility check.
type nonVisibleDP struct {
	ops hmenum.Operations
}

func (d *nonVisibleDP) RawValue() (any, bool)       { return nil, false }
func (d *nonVisibleDP) Parameter() hmenum.Parameter { return "LEVEL" }
func (d *nonVisibleDP) ParameterData() hmproto.ParameterData {
	return hmproto.ParameterData{Operations: d.ops}
}

// bareDP does NOT implement Visible() or ParameterData() — minimal interface.
type bareDP struct{}

func (d *bareDP) RawValue() (any, bool)       { return nil, false }
func (d *bareDP) Parameter() hmenum.Parameter { return "LEVEL" }

func TestIsLoadableValueDPVisibleFalse(t *testing.T) {
	t.Parallel()
	dp := &visibleDP{visible: false, ops: hmenum.OperationsRead}
	if isLoadableValueDP(dp) {
		t.Error("invisible DP must return false")
	}
}

func TestIsLoadableValueDPNonReadable(t *testing.T) {
	t.Parallel()
	// Visible but write-only
	dp := &visibleDP{visible: true, ops: hmenum.OperationsWrite}
	if isLoadableValueDP(dp) {
		t.Error("write-only DP must return false")
	}
}

func TestIsLoadableValueDPReadable(t *testing.T) {
	t.Parallel()
	dp := &visibleDP{visible: true, ops: hmenum.OperationsRead}
	if !isLoadableValueDP(dp) {
		t.Error("readable visible DP must return true")
	}
}

func TestIsLoadableValueDPNoVisibleInterface(t *testing.T) {
	t.Parallel()
	// No Visible() method → skip visibility check; has ParameterData
	dp := &nonVisibleDP{ops: hmenum.OperationsRead}
	if !isLoadableValueDP(dp) {
		t.Error("DP without Visible() but readable must return true")
	}
}

func TestIsLoadableValueDPNoBothInterfaces(t *testing.T) {
	t.Parallel()
	// No Visible(), no ParameterData() → returns true (no reasons to skip)
	dp := &bareDP{}
	if !isLoadableValueDP(dp) {
		t.Error("bare DP with no checks must return true")
	}
}

// ============================================================
// InterfacesAdapter.Reconnect — unknown interface (past nil-reconnector check)
// ============================================================

func TestInterfacesAdapterReconnectUnknownInterface(t *testing.T) {
	t.Parallel()
	reg := central.NewRegistry()
	// Provide a reconnector so we get past the nil check
	rec := &fakeReconnector{}
	a := NewInterfacesAdapter(reg, rec)
	err := a.Reconnect(context.Background(), "NOSUCHINTERFACE")
	if err == nil {
		t.Error("unknown interface must error")
	}
}

// fakeReconnector satisfies the Reconnector interface.
type fakeReconnector struct{}

func (f *fakeReconnector) Reconnect(_ context.Context, _, _ string) error { return nil }

// ============================================================
// DevicePipeline.refineAttachedWeekProfiles
// ============================================================

func TestRefineAttachedWeekProfilesNilCentral(t *testing.T) {
	t.Parallel()
	p := NewDevicePipeline(nil)
	// nil central → early return, no panic
	p.refineAttachedWeekProfiles("HmIP-RF", nil)
}

func TestRefineAttachedWeekProfilesWrongInterface(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-refine"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	dev := device.New(device.Config{Address: "RDEV001", InterfaceID: "BidCos-RF", Model: "HM-CC-RT-DN"})
	c.ModelRegistry.Put(dev)

	p := NewDevicePipeline(c)
	// Calling with HmIP-RF → device skipped (wrong interface)
	p.refineAttachedWeekProfiles("HmIP-RF", nil)
}

func TestRefineAttachedWeekProfilesNoWeekProfile(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-refine2"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	dev := device.New(device.Config{Address: "RDEV002", InterfaceID: "HmIP-RF", Model: "HmIP-STH"})
	dev.AddChannel("RDEV002:1", 1, "CLIMATE", hmenum.ParamsetKeyValues)
	c.ModelRegistry.Put(dev)

	p := NewDevicePipeline(c)
	// Device on right interface but no week profile → skips inner loop, no panic
	p.refineAttachedWeekProfiles("HmIP-RF", nil)
}

func TestRefineAttachedWeekProfilesWithWeekProfile(t *testing.T) {
	t.Parallel()
	c, err := central.New(central.Config{Name: "ccu-refine3"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	dev := device.New(device.Config{Address: "RDEV003", InterfaceID: "HmIP-RF", Model: "HmIP-eTRV-2"})
	ch := dev.AddChannel("RDEV003:1", 1, "CLIMATECONTROL_VENT_DRIVE", hmenum.ParamsetKeyValues)
	wp := weekprofile.NewProfileDataPoint(weekprofile.ProfileDataPointConfig{
		CentralName:    "ccu-refine3",
		ChannelAddress: "RDEV003:1",
		ScheduleType:   weekprofile.ScheduleTypeClimate,
	})
	ch.AttachWeekProfile(wp)
	c.ModelRegistry.Put(dev)

	p := NewDevicePipeline(c)
	// Device on right interface with week profile → processes, must not panic
	p.refineAttachedWeekProfiles("HmIP-RF", nil)
}
