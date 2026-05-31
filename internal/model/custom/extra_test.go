// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package custom

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// --- AggregateStatus tests ---

// fakeSlot is a minimal AggregateSlot for testing AggregateStatus.
type fakeSlot struct {
	key         hmtypes.DataPointKey
	rawValue    any
	observed    bool
	optimistic  bool
	statusValid bool
}

func (f *fakeSlot) DataPointKey() hmtypes.DataPointKey { return f.key }
func (f *fakeSlot) RawValue() (any, bool)              { return f.rawValue, f.observed }
func (f *fakeSlot) IsOptimistic() bool                 { return f.optimistic }
func (f *fakeSlot) IsStatusValid() bool                { return f.statusValid }

// TestAggregateStatusIsRefreshed verifies IsRefreshed when at least one slot observed.
func TestAggregateStatusIsRefreshed(t *testing.T) {
	s1 := &fakeSlot{observed: false}
	s2 := &fakeSlot{observed: true, statusValid: true}

	view := AggregateStatus(s1, s2)
	if !view.IsRefreshed() {
		t.Error("IsRefreshed must be true when at least one slot is observed")
	}

	// No observed slots.
	view2 := AggregateStatus(s1)
	if view2.IsRefreshed() {
		t.Error("IsRefreshed must be false when no slot is observed")
	}

	// nil slot handled.
	view3 := AggregateStatus(nil, s2)
	if !view3.IsRefreshed() {
		t.Error("nil slot must be skipped by IsRefreshed")
	}
}

// TestAggregateStatusStateUncertain verifies StateUncertain when any slot is optimistic.
func TestAggregateStatusStateUncertain(t *testing.T) {
	s1 := &fakeSlot{optimistic: false}
	s2 := &fakeSlot{optimistic: true}

	view := AggregateStatus(s1, s2)
	if !view.StateUncertain() {
		t.Error("StateUncertain must be true when any slot is optimistic")
	}

	view2 := AggregateStatus(s1)
	if view2.StateUncertain() {
		t.Error("StateUncertain must be false when no slot is optimistic")
	}
}

// TestAggregateStatusHasDataPoints verifies HasDataPoints with nil and non-nil slots.
func TestAggregateStatusHasDataPoints(t *testing.T) {
	view := AggregateStatus(nil)
	if view.HasDataPoints() {
		t.Error("all-nil view must report no data points")
	}

	s := &fakeSlot{statusValid: true}
	view2 := AggregateStatus(nil, s)
	if !view2.HasDataPoints() {
		t.Error("view with non-nil slot must report data points")
	}
}

// TestAggregateStatusIsStatusValid verifies IsStatusValid with invalid slots.
func TestAggregateStatusIsStatusValid(t *testing.T) {
	s1 := &fakeSlot{statusValid: true}
	s2 := &fakeSlot{statusValid: false}

	view := AggregateStatus(s1, s2)
	if view.IsStatusValid() {
		t.Error("IsStatusValid must be false when any slot is invalid")
	}

	view2 := AggregateStatus(s1)
	if !view2.IsStatusValid() {
		t.Error("IsStatusValid must be true when all slots are valid")
	}
}

// TestAggregateStatusSubDataPointKeys verifies SubDataPointKeys skips nil slots.
func TestAggregateStatusSubDataPointKeys(t *testing.T) {
	key1 := hmtypes.DataPointKey{ChannelAddress: "ch:1", Parameter: "A"}
	key2 := hmtypes.DataPointKey{ChannelAddress: "ch:1", Parameter: "B"}

	s1 := &fakeSlot{key: key1}
	s2 := &fakeSlot{key: key2}

	view := AggregateStatus(nil, s1, nil, s2)
	keys := view.SubDataPointKeys()
	if len(keys) != 2 {
		t.Errorf("SubDataPointKeys() = %d, want 2", len(keys))
	}
}

// --- BaseDP tests ---

// TestBaseDPMarkModifiedRefreshed exercises all three fields.
func TestBaseDPMarkModifiedRefreshed(t *testing.T) {
	var b BaseDP

	mt, ok := b.ModifiedAt()
	if ok || !mt.IsZero() {
		t.Error("ModifiedAt before any mark must be zero/false")
	}
	rt, ok := b.RefreshedAt()
	if ok || !rt.IsZero() {
		t.Error("RefreshedAt before any mark must be zero/false")
	}
	if b.UnconfirmedLastValuesSend() != 0 {
		t.Error("UnconfirmedLastValuesSend must start at 0")
	}

	b.MarkModified()
	mt, ok = b.ModifiedAt()
	if !ok || mt.IsZero() {
		t.Error("ModifiedAt after MarkModified must be non-zero/true")
	}
	if b.UnconfirmedLastValuesSend() != 1 {
		t.Errorf("UnconfirmedLastValuesSend = %d, want 1", b.UnconfirmedLastValuesSend())
	}

	b.MarkRefreshed()
	rt, ok = b.RefreshedAt()
	if !ok || rt.IsZero() {
		t.Error("RefreshedAt after MarkRefreshed must be non-zero/true")
	}
	if b.UnconfirmedLastValuesSend() != 0 {
		t.Errorf("UnconfirmedLastValuesSend after MarkRefreshed = %d, want 0", b.UnconfirmedLastValuesSend())
	}

	// MarkRefreshed when counter already 0 must not go negative.
	b.MarkRefreshed()
	if b.UnconfirmedLastValuesSend() != 0 {
		t.Errorf("counter below zero: %d", b.UnconfirmedLastValuesSend())
	}
}

// --- Position / Brightness tests ---

// TestPositionClosedOpen exercises Closed/Open.
func TestPositionClosedOpen(t *testing.T) {
	p0 := NewPosition(0)
	if !p0.Closed() {
		t.Error("NewPosition(0).Closed() must be true")
	}
	if p0.Open() {
		t.Error("NewPosition(0).Open() must be false")
	}

	p1 := NewPosition(1)
	if p1.Closed() {
		t.Error("NewPosition(1).Closed() must be false")
	}
	if !p1.Open() {
		t.Error("NewPosition(1).Open() must be true")
	}

	// Negative clamped to 0.
	pNeg := NewPosition(-0.5)
	if !pNeg.Closed() {
		t.Error("NewPosition(-0.5) must clamp to 0 and be Closed")
	}

	// Over 1.0 clamped to 1.
	pOver := NewPosition(1.5)
	if !pOver.Open() {
		t.Error("NewPosition(1.5) must clamp to 1 and be Open")
	}
}

// TestBrightnessLevel exercises Level, Byte, Pct, IsOn.
func TestBrightnessLevel(t *testing.T) {
	b := NewBrightness(0.5)
	if b.Level() != 0.5 {
		t.Errorf("Level() = %v, want 0.5", b.Level())
	}
	if b.Byte() != 127 {
		t.Errorf("Byte() = %d, want 127", b.Byte())
	}
	if b.Pct() != 50 {
		t.Errorf("Pct() = %d, want 50", b.Pct())
	}
	if !b.IsOn() {
		t.Error("IsOn() must be true for level > 0")
	}

	bOff := NewBrightness(0)
	if bOff.IsOn() {
		t.Error("IsOn() must be false for level 0")
	}

	// Clamp negative.
	bNeg := NewBrightness(-0.1)
	if bNeg.Level() != 0 {
		t.Errorf("NewBrightness(-0.1).Level() = %v, want 0", bNeg.Level())
	}
	// Clamp above 1.
	bOver := NewBrightness(1.5)
	if bOver.Level() != 1.0 {
		t.Errorf("NewBrightness(1.5).Level() = %v, want 1.0", bOver.Level())
	}
}

// --- GroupState tests ---

// TestGroupStateRemove exercises GroupState.Remove.
func TestGroupStateRemove(t *testing.T) {
	g := NewGroupState()
	g.Set("a", true)
	g.Set("b", false)
	_ = !g.AllOn() // b is off; AllOn should be false at this point
	g.Remove("b")
	// Now only "a" remains.
	if !g.AllOn() {
		t.Error("AllOn must be true after removing the off member")
	}
	g.Remove("a")
	if g.AllOn() {
		t.Error("empty GroupState must not AllOn")
	}
}

// --- PutOrSet tests ---

// stubPutWriter implements both Writer and ParamsetWriter.
type stubPutWriter struct {
	setCalls int
	putCalls int
}

func (w *stubPutWriter) SetValue(_ context.Context, _ string, _ hmenum.Parameter, _ any, _ hmenum.CommandPriority) error {
	w.setCalls++
	return nil
}

func (w *stubPutWriter) PutParamset(_ context.Context, _ string, _ hmenum.ParamsetKey, _ map[string]any, _ hmenum.CommandPriority) error {
	w.putCalls++
	return nil
}

// stubSetOnlyWriter implements only Writer (no ParamsetWriter).
type stubSetOnlyWriter struct {
	setCalls int
}

func (w *stubSetOnlyWriter) SetValue(_ context.Context, _ string, _ hmenum.Parameter, _ any, _ hmenum.CommandPriority) error {
	w.setCalls++
	return nil
}

// TestPutOrSetEmpty verifies empty values map is a no-op.
func TestPutOrSetEmpty(t *testing.T) {
	w := &stubPutWriter{}
	err := PutOrSet(context.Background(), w, "addr:1", hmenum.ParamsetKeyValues, nil, hmenum.CommandPriorityCritical)
	if err != nil {
		t.Fatalf("PutOrSet(empty): %v", err)
	}
	if w.setCalls != 0 || w.putCalls != 0 {
		t.Error("no calls should be made for empty map")
	}
}

// TestPutOrSetSingle verifies single-param maps use SetValue.
func TestPutOrSetSingle(t *testing.T) {
	w := &stubPutWriter{}
	err := PutOrSet(context.Background(), w, "addr:1", hmenum.ParamsetKeyValues,
		map[hmenum.Parameter]any{hmenum.ParameterState: true}, hmenum.CommandPriorityCritical)
	if err != nil {
		t.Fatalf("PutOrSet(single): %v", err)
	}
	if w.setCalls != 1 {
		t.Errorf("expected 1 SetValue call, got %d", w.setCalls)
	}
}

// TestPutOrSetMultiWithParamsetWriter verifies multi-param uses PutParamset.
func TestPutOrSetMultiWithParamsetWriter(t *testing.T) {
	w := &stubPutWriter{}
	err := PutOrSet(context.Background(), w, "addr:1", hmenum.ParamsetKeyMaster,
		map[hmenum.Parameter]any{
			hmenum.ParameterState: true,
			hmenum.ParameterLevel: 0.5,
		}, hmenum.CommandPriorityCritical)
	if err != nil {
		t.Fatalf("PutOrSet(multi+paramset): %v", err)
	}
	if w.putCalls != 1 {
		t.Errorf("expected 1 PutParamset call, got %d", w.putCalls)
	}
}

// TestPutOrSetMultiWithoutParamsetWriter verifies fallback to sequential SetValue.
func TestPutOrSetMultiWithoutParamsetWriter(t *testing.T) {
	w := &stubSetOnlyWriter{}
	err := PutOrSet(context.Background(), w, "addr:1", hmenum.ParamsetKeyValues,
		map[hmenum.Parameter]any{
			hmenum.ParameterState: true,
			hmenum.ParameterLevel: 0.5,
		}, hmenum.CommandPriorityCritical)
	if err != nil {
		t.Fatalf("PutOrSet(multi no paramset): %v", err)
	}
	if w.setCalls != 2 {
		t.Errorf("expected 2 SetValue calls, got %d", w.setCalls)
	}
}

// --- EnsureContext tests ---

// TestEnsureContextNil verifies EnsureContext returns Background for nil.
func TestEnsureContextNil(t *testing.T) {
	ctx := EnsureContext(nil) //nolint:staticcheck // nil is the explicit test input to verify EnsureContext handles it gracefully
	if ctx == nil {
		t.Error("EnsureContext(nil) must return non-nil context")
	}
}

// TestEnsureContextNonNil verifies EnsureContext returns the same non-nil ctx.
func TestEnsureContextNonNil(t *testing.T) {
	in := context.Background()
	if out := EnsureContext(in); out != in {
		t.Error("EnsureContext(non-nil) must return same context")
	}
}

// --- EncodeTimerDuration tests ---

// TestEncodeTimerDurationUnits exercises seconds/minutes/hours promotion.
func TestEncodeTimerDurationUnits(t *testing.T) {
	// Within seconds bucket.
	v, u := EncodeTimerDuration(60 * time.Second)
	if u != 0 {
		t.Errorf("60s: expected unit=0 (seconds), got %d", u)
	}
	if v != 60 {
		t.Errorf("60s: expected value=60, got %d", v)
	}

	// Crosses into minutes bucket.
	v, u = EncodeTimerDuration(16344 * time.Second)
	if u != 1 {
		t.Errorf("16344s: expected unit=1 (minutes), got %d", u)
	}
	_ = v

	// Zero duration.
	v, u = EncodeTimerDuration(0)
	if v != 0 || u != 0 {
		t.Errorf("0s: expected (0,0), got (%d,%d)", v, u)
	}

	// Negative duration.
	v, u = EncodeTimerDuration(-1 * time.Second)
	if v != 0 || u != 0 {
		t.Errorf("-1s: expected (0,0), got (%d,%d)", v, u)
	}
}

// --- Registry: Clear, GetAllExtendedConfigs, IsMultiChannelDevice, MustRegisterAll ---

// TestRegistryClear verifies Clear resets the registry.
func TestRegistryClear(t *testing.T) {
	r := NewRegistry()
	p := Profile{
		Name:         "MySwitch",
		DeviceType:   "HmIP-PS",
		ProductGroup: hmenum.ProductGroupHmIP,
		Category:     hmenum.DataPointCategorySwitch,
		Channels:     []ChannelRoleAssignment{{Channel: 3, Role: ChannelRolePrimary}},
	}
	if err := r.Register(p); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if r.Len() != 1 {
		t.Fatalf("Len before Clear = %d, want 1", r.Len())
	}
	r.Clear()
	if r.Len() != 0 {
		t.Errorf("Len after Clear = %d, want 0", r.Len())
	}
}

// TestRegistryGetAllExtendedConfigs verifies it returns configs with Extended.
func TestRegistryGetAllExtendedConfigs(t *testing.T) {
	r := NewRegistry()
	ext := &ExtendedDeviceConfig{}
	p := Profile{
		Name:         "ExtSwitch",
		DeviceType:   "HmIP-EXT",
		ProductGroup: hmenum.ProductGroupHmIP,
		Category:     hmenum.DataPointCategorySwitch,
		Channels:     []ChannelRoleAssignment{{Channel: 3, Role: ChannelRolePrimary}},
		Extended:     ext,
	}
	if err := r.Register(p); err != nil {
		t.Fatalf("Register: %v", err)
	}
	// Register one without Extended.
	p2 := Profile{
		Name:         "Plain",
		DeviceType:   "HmIP-PLAIN",
		ProductGroup: hmenum.ProductGroupHmIP,
		Category:     hmenum.DataPointCategorySwitch,
		Channels:     []ChannelRoleAssignment{{Channel: 3, Role: ChannelRolePrimary}},
	}
	if err := r.Register(p2); err != nil {
		t.Fatalf("Register p2: %v", err)
	}

	configs := r.GetAllExtendedConfigs()
	if len(configs) != 1 {
		t.Errorf("GetAllExtendedConfigs() = %d, want 1", len(configs))
	}
}

// TestRegistryIsMultiChannelDevice verifies multi-channel detection.
func TestRegistryIsMultiChannelDevice(t *testing.T) {
	r := NewRegistry()
	// Two profiles for the same model/category but different channels.
	p1 := Profile{
		Name:         "Light1",
		DeviceType:   "HmIP-BSL",
		ProductGroup: hmenum.ProductGroupHmIP,
		Category:     hmenum.DataPointCategoryLight,
		Channels:     []ChannelRoleAssignment{{Channel: 8, Role: ChannelRolePrimary}},
	}
	p2 := Profile{
		Name:         "Light2",
		DeviceType:   "HmIP-BSL",
		ProductGroup: hmenum.ProductGroupHmIP,
		Category:     hmenum.DataPointCategoryLight,
		Channels:     []ChannelRoleAssignment{{Channel: 12, Role: ChannelRolePrimary}},
	}
	if err := r.Register(p1); err != nil {
		t.Fatalf("Register p1: %v", err)
	}
	if err := r.Register(p2); err != nil {
		t.Fatalf("Register p2: %v", err)
	}
	if !r.IsMultiChannelDevice("HmIP-BSL", hmenum.DataPointCategoryLight) {
		t.Error("HmIP-BSL with 2 light profiles must be multi-channel")
	}
	if r.IsMultiChannelDevice("HmIP-BSL", hmenum.DataPointCategoryCover) {
		t.Error("HmIP-BSL has no cover profiles, must not be multi-channel")
	}
}

// TestRegistryMustRegisterAll verifies batch registration.
func TestRegistryMustRegisterAll(t *testing.T) {
	r := NewRegistry()
	profiles := []Profile{
		{
			Name:         "S1",
			DeviceType:   "HmIP-S1",
			ProductGroup: hmenum.ProductGroupHmIP,
			Category:     hmenum.DataPointCategorySwitch,
			Channels:     []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		},
		{
			Name:         "S2",
			DeviceType:   "HmIP-S2",
			ProductGroup: hmenum.ProductGroupHmIP,
			Category:     hmenum.DataPointCategorySwitch,
			Channels:     []ChannelRoleAssignment{{Channel: 1, Role: ChannelRolePrimary}},
		},
	}
	r.MustRegisterAll(profiles)
	if r.Len() != 2 {
		t.Errorf("Len = %d, want 2", r.Len())
	}
}

// TestMustRegisterConstructorPanic verifies panic on conflict.
func TestMustRegisterConstructorPanic(t *testing.T) {
	r := NewRegistry()
	ctor := Constructor(func(_ *device.Channel, _ RebasedChannelGroupConfig) (device.AttachableDataPoint, error) {
		return nil, nil
	})
	r.MustRegisterConstructor(hmenum.DeviceProfileIPSwitch, ctor)
	defer func() {
		if r := recover(); r == nil {
			t.Error("MustRegisterConstructor on duplicate should panic")
		}
	}()
	r.MustRegisterConstructor(hmenum.DeviceProfileIPSwitch, ctor)
}

// TestConstructorNilRegistry verifies Constructor on nil Registry returns false.
func TestConstructorNilRegistry(t *testing.T) {
	var r *Registry
	_, ok := r.Constructor(hmenum.DeviceProfileIPSwitch)
	if ok {
		t.Error("nil Registry.Constructor must return false")
	}
}

// TestParamFromAnyParamset exercises VALUES and MASTER lookup.
func TestParamFromAnyParamset(t *testing.T) {
	d := device.New(device.Config{
		InterfaceID: "HmIP-RF",
		Address:     "TEST0001",
		Model:       "HmIP-X",
	})
	ch := d.AddChannel("TEST0001:1", 1, "T", hmenum.ParamsetKeyValues)

	// Nil channel returns nil.
	if ParamFromAnyParamset(nil, hmenum.ParameterState) != nil {
		t.Error("nil channel must return nil")
	}

	// VALUES parameter.
	vdp := &fakeDeviceDP{param: hmenum.ParameterState}
	ch.Put(vdp)
	got := ParamFromAnyParamset(ch, hmenum.ParameterState)
	if got == nil {
		t.Error("VALUES parameter must be found")
	}

	// MASTER-only parameter.
	mdp := &fakeDeviceDP{param: hmenum.ParameterLevel}
	ch.PutMaster(mdp)
	got = ParamFromAnyParamset(ch, hmenum.ParameterLevel)
	if got == nil {
		t.Error("MASTER parameter must be found via fallback")
	}
}

// --- ReplayCurrentValue ---

// TestReplayCurrentValue verifies apply is called when value is observed.
func TestReplayCurrentValue(t *testing.T) {
	var called bool
	dp := &fakeRawValuer{raw: "hello", observed: true}
	ReplayCurrentValue(dp, func(v any) { called = true })
	if !called {
		t.Error("apply should be called when value is observed")
	}

	called = false
	dpUnobs := &fakeRawValuer{observed: false}
	ReplayCurrentValue(dpUnobs, func(v any) { called = true })
	if called {
		t.Error("apply must not be called when value is not observed")
	}

	// nil DP is a no-op.
	ReplayCurrentValue(nil, func(v any) { t.Error("called for nil DP") })
}

type fakeRawValuer struct {
	raw      any
	observed bool
}

func (f *fakeRawValuer) RawValue() (any, bool) { return f.raw, f.observed }

// fakeDeviceDP is a device.ParameterDataPoint for ParamFromAnyParamset tests.
type fakeDeviceDP struct {
	param hmenum.Parameter
}

func (f *fakeDeviceDP) DataPointKey() hmtypes.DataPointKey {
	return hmtypes.DataPointKey{Parameter: string(f.param)}
}
func (f *fakeDeviceDP) Parameter() hmenum.Parameter          { return f.param }
func (f *fakeDeviceDP) ParameterData() hmproto.ParameterData { return hmproto.ParameterData{} }
func (f *fakeDeviceDP) RawValue() (any, bool)                { return nil, false }
func (f *fakeDeviceDP) ModifiedAt() time.Time                { return time.Time{} }
func (f *fakeDeviceDP) OnAnyUpdate(func(any, any)) func()    { return func() {} }

// ─── AggregateView.HasAnyKey ─────────────────────────────────────────────────

// TestAggregateViewHasAnyKeyFound verifies that HasAnyKey returns true when at
// least one of the given keys matches a slot's DataPointKey.
func TestAggregateViewHasAnyKeyFound(t *testing.T) {
	t.Parallel()

	k1 := hmtypes.DataPointKey{InterfaceID: "HmIP-RF", ChannelAddress: "A:1", Parameter: "STATE"}
	k2 := hmtypes.DataPointKey{InterfaceID: "HmIP-RF", ChannelAddress: "A:1", Parameter: "LEVEL"}
	s1 := &fakeSlot{key: k1, statusValid: true}
	s2 := &fakeSlot{key: k2, statusValid: true}
	view := AggregateStatus(s1, s2)

	if !view.HasAnyKey([]hmtypes.DataPointKey{k2}) {
		t.Error("HasAnyKey must return true when k2 is present")
	}
}

// TestAggregateViewHasAnyKeyNotFound verifies that HasAnyKey returns false
// when none of the given keys match.
func TestAggregateViewHasAnyKeyNotFound(t *testing.T) {
	t.Parallel()

	k1 := hmtypes.DataPointKey{InterfaceID: "HmIP-RF", ChannelAddress: "A:1", Parameter: "STATE"}
	other := hmtypes.DataPointKey{InterfaceID: "HmIP-RF", ChannelAddress: "A:2", Parameter: "STATE"}
	s1 := &fakeSlot{key: k1, statusValid: true}
	view := AggregateStatus(s1)

	if view.HasAnyKey([]hmtypes.DataPointKey{other}) {
		t.Error("HasAnyKey must return false when no key matches")
	}
}

// TestAggregateViewHasAnyKeyEmpty verifies that HasAnyKey with an empty keys
// slice always returns false.
func TestAggregateViewHasAnyKeyEmpty(t *testing.T) {
	t.Parallel()

	k1 := hmtypes.DataPointKey{InterfaceID: "HmIP-RF", ChannelAddress: "A:1", Parameter: "STATE"}
	s1 := &fakeSlot{key: k1, statusValid: true}
	view := AggregateStatus(s1)

	if view.HasAnyKey(nil) {
		t.Error("HasAnyKey(nil) must return false")
	}
}
