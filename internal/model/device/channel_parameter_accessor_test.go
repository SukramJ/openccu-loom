// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package device

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// --- fakeParameterDP for channel tests ---

type fakeParameterDP struct {
	key   hmtypes.DataPointKey
	param hmenum.Parameter
	raw   any
	data  hmproto.ParameterData
}

func (f *fakeParameterDP) DataPointKey() hmtypes.DataPointKey { return f.key }
func (f *fakeParameterDP) Parameter() hmenum.Parameter        { return f.param }
func (f *fakeParameterDP) ParameterData() hmproto.ParameterData {
	return f.data
}

func (f *fakeParameterDP) RawValue() (any, bool) {
	if f.raw == nil {
		return nil, false
	}
	return f.raw, true
}
func (f *fakeParameterDP) ModifiedAt() time.Time { return time.Time{} }
func (f *fakeParameterDP) OnAnyUpdate(func(old, next any)) func() {
	return func() {}
}

// --- Channel accessors ---

// TestChannelDeviceAndCentralName verifies Device(), ChannelName(),
// SetCentralName/CentralName round-trip.
func TestChannelDeviceAndCentralName(t *testing.T) {
	d := newAggregateDevice()
	ch := d.AddChannel("ABC0001:1", 1, "T1", hmenum.ParamsetKeyValues)

	if ch.Device() != d {
		t.Error("Device() should return parent device")
	}
	ch.SetName("MyChannel")
	if ch.ChannelName() != "MyChannel" {
		t.Errorf("ChannelName() = %q, want MyChannel", ch.ChannelName())
	}
	ch.SetCentralName("ccu1")
	if ch.CentralName() != "ccu1" {
		t.Errorf("CentralName() = %q, want ccu1", ch.CentralName())
	}
}

// TestChannelMasterLen verifies MasterLen reflects stored MASTER DPs.
func TestChannelMasterLen(t *testing.T) {
	d := newAggregateDevice()
	ch := d.AddChannel("ABC0001:1", 1, "T1", hmenum.ParamsetKeyValues)

	if ch.MasterLen() != 0 {
		t.Errorf("MasterLen() = %d before any PutMaster", ch.MasterLen())
	}
	dp := &fakeParameterDP{param: hmenum.ParameterState}
	ch.PutMaster(dp)
	ch.PutMaster(nil) // no-op
	if ch.MasterLen() != 1 {
		t.Errorf("MasterLen() = %d, want 1", ch.MasterLen())
	}
}

// TestChannelParamsetDataPoints verifies dispatch by paramset key.
func TestChannelParamsetDataPoints(t *testing.T) {
	d := newAggregateDevice()
	ch := d.AddChannel("ABC0001:1", 1, "T1", hmenum.ParamsetKeyValues)

	vdp := &fakeParameterDP{param: hmenum.ParameterState}
	ch.Put(vdp)
	mdp := &fakeParameterDP{param: hmenum.ParameterLevel}
	ch.PutMaster(mdp)

	vals := ch.ParamsetDataPoints(hmenum.ParamsetKeyValues)
	if len(vals) != 1 {
		t.Errorf("ParamsetDataPoints(VALUES) = %d, want 1", len(vals))
	}
	masters := ch.ParamsetDataPoints(hmenum.ParamsetKeyMaster)
	if len(masters) != 1 {
		t.Errorf("ParamsetDataPoints(MASTER) = %d, want 1", len(masters))
	}
	other := ch.ParamsetDataPoints(hmenum.ParamsetKeyLink)
	if other != nil {
		t.Errorf("ParamsetDataPoints(LINK) = %v, want nil", other)
	}
}

// TestChannelParameterFloatRange verifies float range extraction from ParameterData.
func TestChannelParameterFloatRange(t *testing.T) {
	d := newAggregateDevice()
	ch := d.AddChannel("ABC0001:1", 1, "T1", hmenum.ParamsetKeyValues)

	// No parameter → (0, 0, false).
	if _, _, ok := ch.ParameterFloatRange("MISSING"); ok {
		t.Error("expected false for missing parameter")
	}

	dp := &fakeParameterDP{
		param: hmenum.ParameterSetPointTemperature,
		data: hmproto.ParameterData{
			Min: json.RawMessage(`4.5`),
			Max: json.RawMessage(`30.5`),
		},
	}
	ch.Put(dp)
	lo, hi, ok := ch.ParameterFloatRange(string(hmenum.ParameterSetPointTemperature))
	if !ok {
		t.Fatal("expected ok=true for valid range")
	}
	if lo != 4.5 || hi != 30.5 {
		t.Errorf("range = (%v, %v), want (4.5, 30.5)", lo, hi)
	}

	// Nil channel → false.
	var nilCh *Channel
	if _, _, ok := nilCh.ParameterFloatRange("any"); ok {
		t.Error("nil channel must return false")
	}
}

// TestChannelParameterFloatRangeBadJSON verifies bad JSON min/max returns false.
func TestChannelParameterFloatRangeBadJSON(t *testing.T) {
	d := newAggregateDevice()
	ch := d.AddChannel("ABC0001:1", 1, "T1", hmenum.ParamsetKeyValues)
	dp := &fakeParameterDP{
		param: hmenum.ParameterSetPointTemperature,
		data:  hmproto.ParameterData{Min: json.RawMessage(`"notanumber"`)},
	}
	ch.Put(dp)
	if _, _, ok := ch.ParameterFloatRange(string(hmenum.ParameterSetPointTemperature)); ok {
		t.Error("bad JSON min should return false")
	}
}

// TestChannelParameterFloatValue verifies ParameterFloatValue returns the observed float.
func TestChannelParameterFloatValue(t *testing.T) {
	d := newAggregateDevice()
	ch := d.AddChannel("ABC0001:1", 1, "T1", hmenum.ParamsetKeyValues)

	// Not present → false.
	if _, ok := ch.ParameterFloatValue("MISSING"); ok {
		t.Error("missing param must return false")
	}

	dp := &fakeParameterDP{param: hmenum.ParameterLevel, raw: float64(0.75)}
	ch.Put(dp)
	v, ok := ch.ParameterFloatValue(string(hmenum.ParameterLevel))
	if !ok {
		t.Error("expected ok=true")
	}
	if v != 0.75 {
		t.Errorf("value = %v, want 0.75", v)
	}

	// int raw value.
	dpInt := &fakeParameterDP{param: hmenum.ParameterState, raw: int(42)}
	ch.Put(dpInt)
	vi, ok := ch.ParameterFloatValue(string(hmenum.ParameterState))
	if !ok || vi != 42.0 {
		t.Errorf("int raw: value=%v ok=%v, want 42.0/true", vi, ok)
	}

	// nil channel.
	var nilCh *Channel
	if _, ok := nilCh.ParameterFloatValue("any"); ok {
		t.Error("nil channel must return false")
	}
}

// TestChannelParameterFloatValueInt32 verifies int32 raw value conversion.
func TestChannelParameterFloatValueInt32(t *testing.T) {
	d := newAggregateDevice()
	ch := d.AddChannel("ABC0001:1", 1, "T1", hmenum.ParamsetKeyValues)
	dp := &fakeParameterDP{param: hmenum.ParameterLevel, raw: int32(10)}
	ch.Put(dp)
	v, ok := ch.ParameterFloatValue(string(hmenum.ParameterLevel))
	if !ok || v != 10.0 {
		t.Errorf("int32: value=%v ok=%v, want 10.0/true", v, ok)
	}
}

// TestChannelParameterFloatValueInt64 verifies int64 raw value conversion.
func TestChannelParameterFloatValueInt64(t *testing.T) {
	d := newAggregateDevice()
	ch := d.AddChannel("ABC0001:1", 1, "T1", hmenum.ParamsetKeyValues)
	dp := &fakeParameterDP{param: hmenum.ParameterLevel, raw: int64(7)}
	ch.Put(dp)
	v, ok := ch.ParameterFloatValue(string(hmenum.ParameterLevel))
	if !ok || v != 7.0 {
		t.Errorf("int64: value=%v ok=%v, want 7.0/true", v, ok)
	}
}

// TestChannelParameterFloatValueFloat32 verifies float32 raw value conversion.
func TestChannelParameterFloatValueFloat32(t *testing.T) {
	d := newAggregateDevice()
	ch := d.AddChannel("ABC0001:1", 1, "T1", hmenum.ParamsetKeyValues)
	dp := &fakeParameterDP{param: hmenum.ParameterLevel, raw: float32(0.5)}
	ch.Put(dp)
	v, ok := ch.ParameterFloatValue(string(hmenum.ParameterLevel))
	if !ok || v < 0.499 || v > 0.501 {
		t.Errorf("float32: value=%v ok=%v, want ~0.5/true", v, ok)
	}
}

// TestChannelParameterFloatValueNotObserved verifies unobserved returns false.
func TestChannelParameterFloatValueNotObserved(t *testing.T) {
	d := newAggregateDevice()
	ch := d.AddChannel("ABC0001:1", 1, "T1", hmenum.ParamsetKeyValues)
	dp := &fakeParameterDP{param: hmenum.ParameterLevel, raw: nil}
	ch.Put(dp)
	if _, ok := ch.ParameterFloatValue(string(hmenum.ParameterLevel)); ok {
		t.Error("unobserved should return false")
	}
}

// TestChannelIsCustomDPPrimarySecondary verifies primary/secondary channel classification.
func TestChannelIsCustomDPPrimarySecondary(t *testing.T) {
	d := newAggregateDevice()
	ch1 := d.AddChannel("ABC0001:3", 3, "T", hmenum.ParamsetKeyValues)
	ch1.AssignGroupNumber(3) // group master (GroupNo == Number)

	ch2 := d.AddChannel("ABC0001:4", 4, "T", hmenum.ParamsetKeyValues)
	ch2.AssignGroupNumber(3) // group secondary

	custom := &fakeAttachable{key: hmtypes.DataPointKey{ChannelAddress: ch1.Address}}
	ch1.SetCustomDataPoint(custom)

	custom2 := &fakeAttachable{key: hmtypes.DataPointKey{ChannelAddress: ch2.Address}}
	ch2.SetCustomDataPoint(custom2)

	if !ch1.IsCustomDPPrimaryChannel() {
		t.Error("ch1 (GroupNo==Number) must be primary")
	}
	if ch1.IsCustomDPSecondaryChannel() {
		t.Error("ch1 must not be secondary")
	}
	if ch2.IsCustomDPPrimaryChannel() {
		t.Error("ch2 must not be primary")
	}
	if !ch2.IsCustomDPSecondaryChannel() {
		t.Error("ch2 (GroupNo!=Number) must be secondary")
	}

	// No custom DP.
	ch3 := d.AddChannel("ABC0001:5", 5, "T", hmenum.ParamsetKeyValues)
	if ch3.IsCustomDPPrimaryChannel() || ch3.IsCustomDPSecondaryChannel() {
		t.Error("channel with no custom DP must be neither primary nor secondary")
	}
	// nil channel.
	var nilCh *Channel
	if nilCh.IsCustomDPPrimaryChannel() || nilCh.IsCustomDPSecondaryChannel() {
		t.Error("nil channel must be neither primary nor secondary")
	}
}

// TestChannelIsCustomDPPrimaryNoGroup verifies that a channel with no group
// but a custom DP is treated as primary.
func TestChannelIsCustomDPPrimaryNoGroup(t *testing.T) {
	d := newAggregateDevice()
	ch := d.AddChannel("ABC0001:1", 1, "T", hmenum.ParamsetKeyValues)
	// GroupNo == 0 (not in any group)
	ch.SetCustomDataPoint(&fakeAttachable{key: hmtypes.DataPointKey{ChannelAddress: ch.Address}})
	if !ch.IsCustomDPPrimaryChannel() {
		t.Error("channel with no group and custom DP must be primary")
	}
}

// TestDeviceIsInMultiChannelGroup verifies IsInMultiChannelGroup.
func TestDeviceIsInMultiChannelGroup(t *testing.T) {
	d := newAggregateDevice()
	ch1 := d.AddChannel("ABC0001:3", 3, "T", hmenum.ParamsetKeyValues)
	ch2 := d.AddChannel("ABC0001:4", 4, "T", hmenum.ParamsetKeyValues)
	d.AddChannelToGroup(3, ch1.Number)
	d.AddChannelToGroup(3, ch2.Number)

	if !d.IsInMultiChannelGroup(3) {
		t.Error("channel 3 should be in a group")
	}
	if !d.IsInMultiChannelGroup(4) {
		t.Error("channel 4 should be in a group")
	}
	if d.IsInMultiChannelGroup(1) {
		t.Error("channel 1 not in any group")
	}
}

// TestDeviceGetDataPoints verifies GetDataPoints returns custom+calculated DPs.
func TestDeviceGetDataPoints(t *testing.T) {
	d := newAggregateDevice()
	ch := d.AddChannel("ABC0001:1", 1, "T", hmenum.ParamsetKeyValues)

	cust := &fakeCategorisedDP{
		key:      hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: "SWITCH"},
		category: hmenum.DataPointCategorySwitch,
	}
	ch.SetCustomDataPoint(cust)

	calc := &fakeCategorisedDP{
		key:      hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: "CALC_SWITCH"},
		category: hmenum.DataPointCategorySwitch,
	}
	ch.AttachCalculatedDataPoint(calc)

	// No category filter → returns both.
	all := d.GetDataPoints("", false, nil)
	if len(all) != 2 {
		t.Errorf("GetDataPoints(\"\") = %d, want 2", len(all))
	}

	// Filtered by Switch → returns both.
	sw := d.GetDataPoints(hmenum.DataPointCategorySwitch, false, nil)
	if len(sw) != 2 {
		t.Errorf("GetDataPoints(Switch) = %d, want 2", len(sw))
	}

	// Filtered by Cover → returns none.
	cov := d.GetDataPoints(hmenum.DataPointCategoryCover, false, nil)
	if len(cov) != 0 {
		t.Errorf("GetDataPoints(Cover) = %d, want 0", len(cov))
	}
}

// fakeCategorisedDP implements AttachableDataPoint + CategorisedDataPoint.
type fakeCategorisedDP struct {
	key      hmtypes.DataPointKey
	category hmenum.DataPointCategory
}

func (f *fakeCategorisedDP) DataPointKey() hmtypes.DataPointKey { return f.key }
func (f *fakeCategorisedDP) Category() hmenum.DataPointCategory { return f.category }

// TestDeviceGetEvents verifies GetEvents delegates to GenericEvents.
func TestDeviceGetEvents(t *testing.T) {
	d := newAggregateDevice()
	ch := d.AddChannel("ABC0001:1", 1, "T", hmenum.ParamsetKeyValues)

	ev := &fakeEvent{key: hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: "PRESS_SHORT"}, kind: "keypress"}
	ch.AttachGenericEvent(ev)

	events := d.GetEvents()
	if len(events) != 1 {
		t.Errorf("GetEvents() = %d, want 1", len(events))
	}
}

// TestDeviceGetGenericDataPoint verifies lookup across channels.
func TestDeviceGetGenericDataPoint(t *testing.T) {
	d := newAggregateDevice()
	ch := d.AddChannel("ABC0001:1", 1, "T", hmenum.ParamsetKeyValues)
	dp := &fakeParameterDP{param: hmenum.ParameterState, raw: true}
	ch.Put(dp)

	got := d.GetGenericDataPoint("ABC0001:1", hmenum.ParameterState, "")
	if got == nil {
		t.Error("expected DP, got nil")
	}
	// Missing channel.
	if got := d.GetGenericDataPoint("NOEXIST:1", hmenum.ParameterState, ""); got != nil {
		t.Error("expected nil for missing channel")
	}
}

// TestDeviceGetGenericEvent verifies event lookup.
func TestDeviceGetGenericEvent(t *testing.T) {
	d := newAggregateDevice()
	ch := d.AddChannel("ABC0001:1", 1, "T", hmenum.ParamsetKeyValues)

	ev := &fakeParameterEvent{
		key:   hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: "PRESS_SHORT"},
		kind:  "keypress",
		param: hmenum.ParameterPressShort,
	}
	ch.AttachGenericEvent(ev)

	found := d.GetGenericEvent("ABC0001:1", hmenum.ParameterPressShort)
	if found == nil {
		t.Error("GetGenericEvent: expected result, got nil")
	}
	// Missing channel.
	if got := d.GetGenericEvent("NOEXIST:1", hmenum.ParameterPressShort); got != nil {
		t.Error("expected nil for missing channel")
	}
	// Wrong parameter.
	if got := d.GetGenericEvent("ABC0001:1", hmenum.ParameterState); got != nil {
		t.Error("expected nil for wrong parameter")
	}
}

// fakeParameterEvent implements GenericEvent (AttachableEvent + EventParameter).
type fakeParameterEvent struct {
	key   hmtypes.DataPointKey
	kind  string
	param hmenum.Parameter
}

func (f *fakeParameterEvent) DataPointKey() hmtypes.DataPointKey { return f.key }
func (f *fakeParameterEvent) EventKind() string                  { return f.kind }
func (f *fakeParameterEvent) EventParameter() hmenum.Parameter   { return f.param }

// TestAvailabilityForced verifies forced availability override.
func TestAvailabilityForced(t *testing.T) {
	d := newAggregateDevice()
	avail := d.Availability()

	if avail.Forced() != hmenum.ForcedDeviceAvailabilityNotSet {
		t.Error("initial forced should be NotSet")
	}

	// Force true.
	changed := avail.SetForced(hmenum.ForcedDeviceAvailabilityForceTrue)
	_ = changed // we just verify the API works
	if !avail.IsReachable() {
		t.Error("after ForceTrue, IsReachable must be true")
	}

	// Force false.
	avail.SetForced(hmenum.ForcedDeviceAvailabilityForceFalse)
	if avail.IsReachable() {
		t.Error("after ForceFalse, IsReachable must be false")
	}

	// Reset to NotSet → derives from channel0.
	avail.SetForced(hmenum.ForcedDeviceAvailabilityNotSet)
	// No channel 0 → defaults to reachable.
	if !avail.IsReachable() {
		t.Error("with no channel0, IsReachable should default to true")
	}
}

// TestAvailabilityIsConfigPending verifies IsConfigPending reads from channel 0.
func TestAvailabilityIsConfigPending(t *testing.T) {
	d := newAggregateDevice()
	avail := d.Availability()

	// No channel 0 → false.
	if avail.IsConfigPending() {
		t.Error("no channel 0 → IsConfigPending must be false")
	}
}

// TestChannelHasParameterNilChannel verifies HasParameter on nil channel.
func TestChannelHasParameterNilChannel(t *testing.T) {
	var ch *Channel
	if ch.HasParameter("STATE") {
		t.Error("nil channel must return false for HasParameter")
	}
}

// TestChannelNameNilChannel verifies ChannelName on nil channel.
func TestChannelNameNilChannel(t *testing.T) {
	var ch *Channel
	if ch.ChannelName() != "" {
		t.Error("nil channel ChannelName must return empty string")
	}
}

// TestChannelGetEventsAndGetGenericDataPoint exercises channel-level
// GetEvents and GetGenericDataPoint.
func TestChannelGetEventsAndGetGenericDataPoint(t *testing.T) {
	d := newAggregateDevice()
	ch := d.AddChannel("ABC0001:1", 1, "T", hmenum.ParamsetKeyValues)

	ev := &fakeParameterEvent{
		key:   hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: "PRESS_SHORT"},
		kind:  "keypress",
		param: hmenum.ParameterPressShort,
	}
	ch.AttachGenericEvent(ev)

	events := ch.GetEvents()
	if len(events) != 1 {
		t.Errorf("GetEvents() = %d, want 1", len(events))
	}

	dp := &fakeParameterDP{param: hmenum.ParameterState, raw: true}
	ch.Put(dp)
	got := ch.GetGenericDataPoint(hmenum.ParameterState)
	if got == nil {
		t.Error("GetGenericDataPoint: expected non-nil")
	}
}

// TestChannelParamsetParameter exercises ParamsetParameter dispatch.
func TestChannelParamsetParameter(t *testing.T) {
	d := newAggregateDevice()
	ch := d.AddChannel("ABC0001:1", 1, "T", hmenum.ParamsetKeyValues)

	vdp := &fakeParameterDP{param: hmenum.ParameterState}
	ch.Put(vdp)
	mdp := &fakeParameterDP{param: hmenum.ParameterLevel}
	ch.PutMaster(mdp)

	if ch.ParamsetParameter(hmenum.ParamsetKeyValues, hmenum.ParameterState) == nil {
		t.Error("VALUES/STATE should return DP")
	}
	if ch.ParamsetParameter(hmenum.ParamsetKeyMaster, hmenum.ParameterLevel) == nil {
		t.Error("MASTER/LEVEL should return DP")
	}
	if ch.ParamsetParameter(hmenum.ParamsetKeyLink, hmenum.ParameterState) != nil {
		t.Error("LINK paramset should return nil")
	}
}

// TestChannelWeekProfile exercises AttachWeekProfile/WeekProfile/HasWeekProfile.
func TestChannelWeekProfile(t *testing.T) {
	d := newAggregateDevice()
	ch := d.AddChannel("ABC0001:1", 1, "T", hmenum.ParamsetKeyValues)

	if ch.HasWeekProfile() {
		t.Error("HasWeekProfile must be false before attach")
	}
	if ch.WeekProfile() != nil {
		t.Error("WeekProfile must be nil before attach")
	}
	// Attaching nil is a no-op (detach).
	ch.AttachWeekProfile(nil)
	if ch.HasWeekProfile() {
		t.Error("HasWeekProfile must still be false after nil attach")
	}
}

// TestChannelHasSinglePrimaryCustomDP exercises HasSinglePrimaryCustomDP.
func TestChannelHasSinglePrimaryCustomDP(t *testing.T) {
	// nil channel.
	var nilCh *Channel
	if nilCh.HasSinglePrimaryCustomDP() {
		t.Error("nil channel must return false")
	}

	d := newAggregateDevice()
	// Channel with no custom DP → false.
	ch := d.AddChannel("ABC0001:1", 1, "T", hmenum.ParamsetKeyValues)
	if ch.HasSinglePrimaryCustomDP() {
		t.Error("channel with no custom DP must return false")
	}

	// Custom DP that doesn't implement haComponentProvider → false.
	ch.SetCustomDataPoint(&fakeAttachable{
		key: hmtypes.DataPointKey{ChannelAddress: ch.Address},
	})
	if ch.HasSinglePrimaryCustomDP() {
		t.Error("custom DP without HAComponent must return false")
	}
}

// TestChannelHasSinglePrimaryCustomDPWithComponent exercises the single-primary path
// via a HA-component-aware custom DP.
func TestChannelHasSinglePrimaryCustomDPWithComponent(t *testing.T) {
	d := newAggregateDevice()
	ch := d.AddChannel("ABC0001:3", 3, "T", hmenum.ParamsetKeyValues)
	// The channel stays ungrouped, which makes it a primary by default.

	cdp := &fakeHAComponentDP{
		key:         hmtypes.DataPointKey{ChannelAddress: ch.Address},
		haComponent: "switch",
	}
	ch.SetCustomDataPoint(cdp)

	// Only one primary with component "switch" → true.
	if !ch.HasSinglePrimaryCustomDP() {
		t.Error("single primary switch should return true")
	}

	// Add a second channel with same component → both should return false.
	ch2 := d.AddChannel("ABC0001:4", 4, "T2", hmenum.ParamsetKeyValues)
	// ch2 stays ungrouped too.
	cdp2 := &fakeHAComponentDP{
		key:         hmtypes.DataPointKey{ChannelAddress: ch2.Address},
		haComponent: "switch",
	}
	ch2.SetCustomDataPoint(cdp2)

	if ch.HasSinglePrimaryCustomDP() {
		t.Error("two primaries with same component must return false for first")
	}
	if ch2.HasSinglePrimaryCustomDP() {
		t.Error("two primaries with same component must return false for second")
	}
}

// fakeHAComponentDP implements AttachableDataPoint + haComponentProvider.
type fakeHAComponentDP struct {
	key         hmtypes.DataPointKey
	haComponent string
}

func (f *fakeHAComponentDP) DataPointKey() hmtypes.DataPointKey { return f.key }
func (f *fakeHAComponentDP) HAComponent() string                { return f.haComponent }

// TestChannelGroupMasterNilDevice verifies GroupMaster returns nil when device is nil.
func TestChannelGroupMasterNilDevice(t *testing.T) {
	ch := &Channel{
		Address: "ABC:3",
		Number:  3,
		device:  nil,
	}
	ch.AssignGroupNumber(3)
	// GroupMaster on self (group number == Number, but device nil).
	master := ch.GroupMaster()
	if master != ch {
		t.Error("GroupMaster should return self when it is the group master")
	}
}

// TestChannelGroupMasterNoGroup verifies GroupMaster returns nil when GroupNo is 0.
func TestChannelGroupMasterNoGroup(t *testing.T) {
	d := newAggregateDevice()
	ch := d.AddChannel("ABC0001:1", 1, "T", hmenum.ParamsetKeyValues)
	if master := ch.GroupMaster(); master != nil {
		t.Errorf("GroupMaster for GroupNo=0 must be nil, got %v", master)
	}
}

// TestChannelAvailabilityInfoBatteryLevel exercises batteryLevel with float.
func TestChannelAvailabilityInfoBatteryLevel(t *testing.T) {
	d := New(Config{
		InterfaceID: "HmIP-RF",
		Address:     "ABC0001",
		Model:       "HmIP-X",
	})
	ch0 := d.AddChannel("ABC0001:0", 0, "MAINTENANCE", hmenum.ParamsetKeyValues)

	// batteryLevel from ParameterBatteryState > 10.
	dp := &fakeParameterDP{
		param: hmenum.ParameterBatteryState,
		raw:   float64(85),
	}
	ch0.Put(dp)

	avail := d.Availability()
	info := avail.Info()
	if info.BatteryLevel == nil {
		t.Error("expected non-nil BatteryLevel from BATTERY_STATE")
	} else if *info.BatteryLevel != 85 {
		t.Errorf("BatteryLevel = %d, want 85", *info.BatteryLevel)
	}
}

// TestChannelAvailabilityInfoSignalStrength exercises signalStrength.
func TestChannelAvailabilityInfoSignalStrength(t *testing.T) {
	d := New(Config{
		InterfaceID: "HmIP-RF",
		Address:     "RSSI0001",
		Model:       "HmIP-X",
	})
	ch0 := d.AddChannel("RSSI0001:0", 0, "MAINTENANCE", hmenum.ParamsetKeyValues)

	dp := &fakeParameterDP{
		param: hmenum.ParameterRSSIDevice,
		raw:   float64(-65),
	}
	ch0.Put(dp)

	avail := d.Availability()
	info := avail.Info()
	if info.SignalStrength == nil {
		t.Error("expected non-nil SignalStrength")
	} else if *info.SignalStrength != -65 {
		t.Errorf("SignalStrength = %d, want -65", *info.SignalStrength)
	}
}

// TestChannelAvailabilityInfoLowBat exercises lowBattery via channel :0.
func TestChannelAvailabilityInfoLowBat(t *testing.T) {
	d := New(Config{
		InterfaceID: "BidCos-RF",
		Address:     "BAT0001",
		Model:       "HM-X",
	})
	ch0 := d.AddChannel("BAT0001:0", 0, "MAINTENANCE", hmenum.ParamsetKeyValues)

	dp := &fakeParameterDP{
		param: hmenum.ParameterLowBat,
		raw:   true,
	}
	ch0.Put(dp)

	avail := d.Availability()
	info := avail.Info()
	if info.LowBattery == nil {
		t.Error("expected non-nil LowBattery")
	} else if !*info.LowBattery {
		t.Error("expected LowBattery=true")
	}
}

// TestAvailabilityIsReachableFromUnreach exercises IsReachable via UN_REACH.
func TestAvailabilityIsReachableFromUnreach(t *testing.T) {
	d := New(Config{InterfaceID: "HmIP-RF", Address: "UNREACH0001", Model: "HmIP-X"})
	ch0 := d.AddChannel("UNREACH0001:0", 0, "MAINTENANCE", hmenum.ParamsetKeyValues)

	dp := &fakeParameterDP{param: hmenum.ParameterUnreach, raw: true}
	ch0.Put(dp)

	avail := d.Availability()
	if avail.IsReachable() {
		t.Error("IsReachable must be false when UN_REACH=true")
	}
}

// TestAvailabilityIsConfigPendingWithChannel0 exercises IsConfigPending.
func TestAvailabilityIsConfigPendingWithChannel0(t *testing.T) {
	d := New(Config{InterfaceID: "HmIP-RF", Address: "CFG0001", Model: "HmIP-X"})
	ch0 := d.AddChannel("CFG0001:0", 0, "MAINTENANCE", hmenum.ParamsetKeyValues)

	dp := &fakeParameterDP{param: hmenum.ParameterConfigPending, raw: true}
	ch0.Put(dp)

	avail := d.Availability()
	if !avail.IsConfigPending() {
		t.Error("IsConfigPending must be true when CONFIG_PENDING=true")
	}
}

// TestDataPointPathsCustomAndCalc exercises DataPointPaths with a path-aware custom DP.
func TestDataPointPathsCustomAndCalc(t *testing.T) {
	d := newAggregateDevice()
	ch := d.AddChannel("ABC0001:1", 1, "T", hmenum.ParamsetKeyValues)

	pathDP := &fakePathDP{
		key:       hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: "CALC_DP"},
		statePath: "/state/calc",
	}
	ch.AttachCalculatedDataPoint(pathDP)

	// Custom DP that also provides a path.
	customPath := &fakePathDP{
		key:       hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: "CUSTOM"},
		statePath: "/state/custom",
	}
	ch.SetCustomDataPoint(customPath)

	paths := ch.DataPointPaths()
	if len(paths) != 2 {
		t.Errorf("DataPointPaths() = %d, want 2", len(paths))
	}
}

// TestDeviceAllowUndefinedGenericDataPoints exercises the allowUndefined check.
func TestDeviceAllowUndefinedGenericDataPoints(t *testing.T) {
	d := newAggregateDevice()

	// No custom DPs → false.
	if d.AllowUndefinedGenericDataPoints() {
		t.Error("no custom DPs must return false")
	}

	// Custom DP that does NOT implement UndefinedGenericDataPointAllower.
	ch := d.AddChannel("ABC0001:1", 1, "T", hmenum.ParamsetKeyValues)
	ch.SetCustomDataPoint(&fakeAttachable{key: hmtypes.DataPointKey{ChannelAddress: ch.Address}})
	if d.AllowUndefinedGenericDataPoints() {
		t.Error("custom DP without allower interface must return false")
	}
}

// TestChannelNotifyLinkPeerChangedNilSafe verifies NotifyLinkPeerChanged is
// safe on a nil channel.
func TestChannelNotifyLinkPeerChangedNilSafe(t *testing.T) {
	var ch *Channel
	ch.NotifyLinkPeerChanged() // must not panic
}

// fakeSubscribingDP is an AttachableDataPoint that also implements
// SubscribingDataPoint so we can test the Subscribe wiring path.
type fakeSubscribingDP struct {
	key        hmtypes.DataPointKey
	subscribed bool
}

func (f *fakeSubscribingDP) DataPointKey() hmtypes.DataPointKey { return f.key }
func (f *fakeSubscribingDP) Subscribe(_ *Channel) func() {
	f.subscribed = true
	return func() { f.subscribed = false }
}

// TestAttachCalculatedDataPointSubscribing verifies Subscribe is called on attach
// and the unsubscribe runs on re-attach.
func TestAttachCalculatedDataPointSubscribing(t *testing.T) {
	d := newAggregateDevice()
	ch := d.AddChannel("ABC0001:1", 1, "T", hmenum.ParamsetKeyValues)

	key := hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: "DP1"}
	dp1 := &fakeSubscribingDP{key: key}
	ch.AttachCalculatedDataPoint(dp1)
	if !dp1.subscribed {
		t.Error("Subscribe should have been called on first attach")
	}

	// Re-attach a different DP at the same key: old unsub should fire.
	dp2 := &fakeSubscribingDP{key: key}
	ch.AttachCalculatedDataPoint(dp2)
	if dp1.subscribed {
		t.Error("old DP's unsubscribe should have fired on re-attach")
	}
	if !dp2.subscribed {
		t.Error("new DP's Subscribe should have been called")
	}
}

// TestSetCustomDataPointSubscribing verifies Subscribe/unsubscribe wiring for custom DPs.
func TestSetCustomDataPointSubscribing(t *testing.T) {
	d := newAggregateDevice()
	ch := d.AddChannel("ABC0001:1", 1, "T", hmenum.ParamsetKeyValues)

	key := hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: "CUSTOM"}
	dp := &fakeSubscribingDP{key: key}
	ch.SetCustomDataPoint(dp)
	if !dp.subscribed {
		t.Error("Subscribe should be called on SetCustomDataPoint")
	}

	// Setting nil should call unsubscribe.
	ch.SetCustomDataPoint(nil)
	if dp.subscribed {
		t.Error("unsubscribe should fire when SetCustomDataPoint(nil) is called")
	}
}

// TestSetCustomDataPointReplaceSubscribing verifies replace fires old unsubscribe
// and then subscribes new DP.
func TestSetCustomDataPointReplaceSubscribing(t *testing.T) {
	d := newAggregateDevice()
	ch := d.AddChannel("ABC0001:1", 1, "T", hmenum.ParamsetKeyValues)

	key := hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: "CUSTOM"}
	dp1 := &fakeSubscribingDP{key: key}
	ch.SetCustomDataPoint(dp1)

	dp2 := &fakeSubscribingDP{key: hmtypes.DataPointKey{ChannelAddress: ch.Address, Parameter: "CUSTOM2"}}
	ch.SetCustomDataPoint(dp2)

	if dp1.subscribed {
		t.Error("old custom DP's unsubscribe should fire on replacement")
	}
	if !dp2.subscribed {
		t.Error("new custom DP's Subscribe should be called")
	}
}

// TestChannelParameterMultiplier exercises ParameterMultiplier paths.
func TestChannelParameterMultiplier(t *testing.T) {
	d := newAggregateDevice()
	ch := d.AddChannel("ABC0001:1", 1, "T", hmenum.ParamsetKeyValues)

	// nil channel.
	var nilCh *Channel
	if _, ok := nilCh.ParameterMultiplier("STATE"); ok {
		t.Error("nil channel must return false")
	}

	// Missing parameter.
	if _, ok := ch.ParameterMultiplier("MISSING"); ok {
		t.Error("missing param must return false")
	}

	// DP that doesn't implement multiplierReader.
	dp := &fakeParameterDP{param: hmenum.ParameterState}
	ch.Put(dp)
	if _, ok := ch.ParameterMultiplier(string(hmenum.ParameterState)); ok {
		t.Error("DP without multiplier must return false")
	}

	// DP with multiplier = 1.0 (returns false).
	dpM := &fakeMultiplierDP{param: hmenum.ParameterLevel, multiplier: 1.0}
	ch.Put(dpM)
	if _, ok := ch.ParameterMultiplier(string(hmenum.ParameterLevel)); ok {
		t.Error("multiplier=1.0 must return false")
	}

	// DP with multiplier != 1.0 (returns true).
	dpM2 := &fakeMultiplierDP{param: hmenum.ParameterTemperature, multiplier: 0.1}
	ch.Put(dpM2)
	m, ok := ch.ParameterMultiplier(string(hmenum.ParameterTemperature))
	if !ok {
		t.Error("multiplier=0.1 must return true")
	}
	if m != 0.1 {
		t.Errorf("multiplier = %v, want 0.1", m)
	}

	// DP with multiplier=0 returns false.
	dpM3 := &fakeMultiplierDP{param: hmenum.ParameterHumidity, multiplier: 0}
	ch.Put(dpM3)
	if _, ok := ch.ParameterMultiplier(string(hmenum.ParameterHumidity)); ok {
		t.Error("multiplier=0 must return false")
	}
}

// fakeMultiplierDP implements ParameterDataPoint + dataPointMultiplierReader.
type fakeMultiplierDP struct {
	key        hmtypes.DataPointKey
	param      hmenum.Parameter
	raw        any
	multiplier float64
}

func (f *fakeMultiplierDP) DataPointKey() hmtypes.DataPointKey { return f.key }
func (f *fakeMultiplierDP) Parameter() hmenum.Parameter        { return f.param }
func (f *fakeMultiplierDP) ParameterData() hmproto.ParameterData {
	return hmproto.ParameterData{}
}

func (f *fakeMultiplierDP) RawValue() (any, bool) {
	if f.raw == nil {
		return nil, false
	}
	return f.raw, true
}
func (f *fakeMultiplierDP) ModifiedAt() time.Time                  { return time.Time{} }
func (f *fakeMultiplierDP) OnAnyUpdate(func(old, next any)) func() { return func() {} }
func (f *fakeMultiplierDP) Multiplier() float64                    { return f.multiplier }

// TestSortKeysNilSlice verifies sortKeys is a no-op on a nil slice.
func TestSortKeysNilSlice(t *testing.T) {
	// sortKeys sorts in-place and returns nothing; calling it with nil must not panic.
	sortKeys(nil)
	// Also verify a populated slice is sorted correctly.
	keys := []hmtypes.DataPointKey{
		{ChannelAddress: "ABC:2", Parameter: "B"},
		{ChannelAddress: "ABC:1", Parameter: "A"},
	}
	sortKeys(keys)
	if keys[0].ChannelAddress != "ABC:1" {
		t.Errorf("sortKeys did not sort by address; got %v first", keys[0].ChannelAddress)
	}
}

// TestChannelUniqueIDNoColon verifies UniqueID for a device address without colon.
func TestChannelUniqueIDNoColon(t *testing.T) {
	// Channel whose address has no colon (device root channel).
	ch := &Channel{
		Address: "ABC0001",
		Number:  ChannelNumberDevice,
	}
	uid := ch.UniqueID()
	// Should use the full address as device prefix.
	if uid == "" {
		t.Error("UniqueID must be non-empty")
	}
}

// TestChannel0FloatInt32 exercises channel0Float with int32 raw value.
func TestChannel0FloatInt32(t *testing.T) {
	d := New(Config{InterfaceID: "HmIP-RF", Address: "INT320001", Model: "HmIP-X"})
	ch0 := d.AddChannel("INT320001:0", 0, "MAINTENANCE", hmenum.ParamsetKeyValues)

	dp := &fakeParameterDP{param: hmenum.ParameterRSSIDevice, raw: int32(-70)}
	ch0.Put(dp)

	avail := d.Availability()
	info := avail.Info()
	if info.SignalStrength == nil {
		t.Error("expected non-nil SignalStrength from int32 raw value")
	}
}

// TestChannel0FloatInt64 exercises channel0Float with int64 raw value.
func TestChannel0FloatInt64(t *testing.T) {
	d := New(Config{InterfaceID: "HmIP-RF", Address: "INT640001", Model: "HmIP-X"})
	ch0 := d.AddChannel("INT640001:0", 0, "MAINTENANCE", hmenum.ParamsetKeyValues)

	dp := &fakeParameterDP{param: hmenum.ParameterRSSIDevice, raw: int64(-55)}
	ch0.Put(dp)

	avail := d.Availability()
	info := avail.Info()
	if info.SignalStrength == nil {
		t.Error("expected non-nil SignalStrength from int64 raw value")
	}
}

// TestBatteryLevelFromOperatingVoltageLevel exercises batteryLevel from
// OperatingVoltageLevel calculated parameter.
func TestBatteryLevelFromOperatingVoltageLevel(t *testing.T) {
	d := New(Config{InterfaceID: "HmIP-RF", Address: "OVL0001", Model: "HmIP-X"})
	ch0 := d.AddChannel("OVL0001:0", 0, "MAINTENANCE", hmenum.ParamsetKeyValues)

	dp := &fakeParameterDP{
		param: hmenum.Parameter(hmenum.CalculatedParameterOperatingVoltageLevel),
		raw:   float64(75),
	}
	ch0.Put(dp)

	avail := d.Availability()
	info := avail.Info()
	if info.BatteryLevel == nil {
		t.Errorf("expected non-nil BatteryLevel from OperatingVoltageLevel")
	} else if *info.BatteryLevel != 75 {
		t.Errorf("BatteryLevel = %d, want 75", *info.BatteryLevel)
	}
}

// TestBatteryStateLowValue verifies BATTERY_STATE <= 10 is not treated as a percentage.
func TestBatteryStateLowValue(t *testing.T) {
	d := New(Config{InterfaceID: "HmIP-RF", Address: "BATLOW0001", Model: "HmIP-X"})
	ch0 := d.AddChannel("BATLOW0001:0", 0, "MAINTENANCE", hmenum.ParamsetKeyValues)

	// BATTERY_STATE = 2.4 (voltage, not percentage) → should NOT produce a BatteryLevel.
	dp := &fakeParameterDP{param: hmenum.ParameterBatteryState, raw: float64(2.4)}
	ch0.Put(dp)

	avail := d.Availability()
	info := avail.Info()
	if info.BatteryLevel != nil {
		t.Errorf("BATTERY_STATE <= 10 must not produce BatteryLevel, got %v", *info.BatteryLevel)
	}
}

// TestDeviceDefaultScheduleChannelWithWeekProfile exercises DefaultScheduleChannel.
func TestDeviceDefaultScheduleChannelWithWeekProfile(t *testing.T) {
	d := newAggregateDevice()

	// No channels → nil.
	if got := d.DefaultScheduleChannel(); got != nil {
		t.Error("expected nil when no channels have week profiles")
	}

	// Add a channel without week profile.
	d.AddChannel("ABC0001:1", 1, "T", hmenum.ParamsetKeyValues)
	if got := d.DefaultScheduleChannel(); got != nil {
		t.Error("expected nil: channel has no week profile")
	}
}
