// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package generic

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// --- Writer stub ---

type stubWriter struct {
	mu    sync.Mutex
	calls []stubCall
	err   error
}

type stubCall struct {
	addr     string
	param    hmenum.Parameter
	value    any
	priority hmenum.CommandPriority
}

func (w *stubWriter) SetValue(_ context.Context, addr string, p hmenum.Parameter, v any, prio hmenum.CommandPriority) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.calls = append(w.calls, stubCall{addr, p, v, prio})
	return w.err
}

func (w *stubWriter) last() (stubCall, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.calls) == 0 {
		return stubCall{}, false
	}
	return w.calls[len(w.calls)-1], true
}

func baseCfg(p hmenum.Parameter, t hmenum.ParameterType, op hmenum.Operations) Spec {
	return Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "iface",
			ChannelAddress: "A:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(p),
		},
		Descriptor: hmproto.ParameterData{Type: t, Operations: op},
	}
}

// --- Core DataPoint tests ---

func TestDataPointValueObservedFlag(t *testing.T) {
	dp := NewDataPoint[bool](baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead))
	_, observed := dp.Value()
	if observed {
		t.Fatal("fresh data point must not report observed")
	}
	dp.OnEvent(true)
	v, observed := dp.Value()
	if !observed || !v {
		t.Fatalf("after OnEvent: v=%v observed=%v", v, observed)
	}
}

func TestDataPointOnUpdateFires(t *testing.T) {
	dp := NewDataPoint[int32](baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeInteger, hmenum.OperationsRead))
	var got []int32
	unsub := dp.OnUpdate(func(_, next int32) { got = append(got, next) })
	dp.OnEvent(1)
	dp.OnEvent(2)
	unsub()
	dp.OnEvent(3)
	if len(got) != 2 || got[0] != 1 || got[1] != 2 {
		t.Fatalf("updates=%v", got)
	}
}

func TestDataPointOptimisticCleared(t *testing.T) {
	cfg := baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead|hmenum.OperationsWrite)
	w := &stubWriter{}
	cfg.Writer = w
	sw := NewSwitch(cfg)
	if err := sw.Set(context.Background(), true, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if !sw.IsOptimistic() {
		t.Fatal("after Set optimistic must be active")
	}
	sw.OnEvent(true)
	if sw.IsOptimistic() {
		t.Fatal("OnEvent must clear optimistic state")
	}
	if sw.OptimisticAge() != 0 {
		t.Fatal("OptimisticAge must return zero after confirmation")
	}
}

func TestDataPointOnRemovedFires(t *testing.T) {
	dp := NewDataPoint[bool](baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead))
	var n int
	_ = dp.OnRemoved(func() { n++ })
	dp.NotifyRemoved()
	dp.NotifyRemoved() // second notify has no callbacks left
	if n != 1 {
		t.Fatalf("NotifyRemoved called callback %d times", n)
	}
}

func TestDataPointNoWriterRejectsSend(t *testing.T) {
	cfg := baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsWrite)
	// No Writer configured.
	sw := NewSwitch(cfg)
	err := sw.Set(context.Background(), true, hmenum.CommandPriorityHigh)
	if !errors.Is(err, ErrNoWriter) {
		t.Fatalf("got %v, want ErrNoWriter", err)
	}
}

// --- Switch / Button / BinarySensor ---

func TestSwitchTurnOnOff(t *testing.T) {
	w := &stubWriter{}
	cfg := baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead|hmenum.OperationsWrite)
	cfg.Writer = w
	sw := NewSwitch(cfg)
	if err := sw.TurnOn(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	call, _ := w.last()
	if call.value != true || call.param != hmenum.ParameterState {
		t.Fatalf("last call=%+v", call)
	}
	if err := sw.TurnOff(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	call, _ = w.last()
	if call.value != false {
		t.Fatalf("TurnOff did not send false, got %v", call.value)
	}
}

func TestSwitchSetOnTime(t *testing.T) {
	w := &stubWriter{}
	cfg := baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsWrite)
	cfg.Writer = w
	sw := NewSwitch(cfg)
	if err := sw.SetOnTime(context.Background(), 5*time.Second, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	call, _ := w.last()
	if call.param != hmenum.ParameterOnTime || call.value.(float64) != 5 {
		t.Fatalf("SetOnTime call=%+v", call)
	}
}

func TestSwitchRejectsReadOnly(t *testing.T) {
	cfg := baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead)
	cfg.Writer = &stubWriter{}
	sw := NewSwitch(cfg)
	if err := sw.TurnOn(context.Background(), hmenum.CommandPriorityHigh); !errors.Is(err, ErrNotWritable) {
		t.Fatalf("got %v, want ErrNotWritable", err)
	}
}

func TestBinarySensorIsOn(t *testing.T) {
	bs := NewBinarySensor(baseCfg(hmenum.ParameterMotion, hmenum.ParameterTypeBool, hmenum.OperationsRead))
	on, observed := bs.IsOn()
	if observed || on {
		t.Fatal("fresh sensor must be unobserved and off")
	}
	bs.OnEvent(true)
	on, observed = bs.IsOn()
	if !observed || !on {
		t.Fatalf("after event: on=%v observed=%v", on, observed)
	}
}

func TestButtonPress(t *testing.T) {
	w := &stubWriter{}
	cfg := baseCfg(hmenum.ParameterPressShort, hmenum.ParameterTypeAction, hmenum.OperationsEvent)
	cfg.Writer = w
	b := NewButton(cfg)
	if err := b.Press(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	call, _ := w.last()
	if call.value != true {
		t.Fatalf("value=%v", call.value)
	}
}

// --- Action family ---

func TestActionBooleanTrigger(t *testing.T) {
	w := &stubWriter{}
	cfg := baseCfg(hmenum.ParameterResetMotion, hmenum.ParameterTypeAction, hmenum.OperationsEvent)
	cfg.Writer = w
	a := NewActionBoolean(cfg)
	if err := a.Trigger(context.Background(), true, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	// ACTION never records optimistic state.
	if a.IsOptimistic() {
		t.Fatal("ACTION must not record optimistic state")
	}
}

func TestActionFloatRangeCheck(t *testing.T) {
	cfg := baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsWrite)
	cfg.Descriptor.Min = json.RawMessage("0")
	cfg.Descriptor.Max = json.RawMessage("1")
	cfg.Writer = &stubWriter{}
	a := NewActionFloat(cfg)
	if err := a.Trigger(context.Background(), 2, hmenum.CommandPriorityHigh); !errors.Is(err, ErrOutOfRange) {
		t.Fatalf("got %v, want ErrOutOfRange", err)
	}
	if err := a.Trigger(context.Background(), 0.5, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
}

func TestActionSelectByIndexAndLabel(t *testing.T) {
	cfg := baseCfg(hmenum.ParameterControlMode, hmenum.ParameterTypeEnum, hmenum.OperationsWrite)
	cfg.Descriptor.ValueList = []string{"OFF", "AUTO", "MANU"}
	w := &stubWriter{}
	cfg.Writer = w
	a := NewActionSelect(cfg)

	if err := a.TriggerIndex(context.Background(), 2, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	call, _ := w.last()
	if call.value != int32(2) {
		t.Fatalf("index call value=%v", call.value)
	}
	if err := a.TriggerLabel(context.Background(), "AUTO", hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	call, _ = w.last()
	// TriggerLabel sends the string label when a VALUE_LIST is present —
	// matching the HmIP convention where ACTION parameters carry labels, not indices.
	if call.value != "AUTO" {
		t.Fatalf("label call value=%v", call.value)
	}
	if err := a.TriggerLabel(context.Background(), "MISSING", hmenum.CommandPriorityHigh); !errors.Is(err, ErrUnknownLabel) {
		t.Fatalf("got %v, want ErrUnknownLabel", err)
	}
	if err := a.TriggerIndex(context.Background(), 5, hmenum.CommandPriorityHigh); !errors.Is(err, ErrIndexOutOfBounds) {
		t.Fatalf("got %v, want ErrIndexOutOfBounds", err)
	}
}

func TestActionStringTrigger(t *testing.T) {
	w := &stubWriter{}
	cfg := baseCfg(hmenum.ParameterDisplayDataString, hmenum.ParameterTypeString, hmenum.OperationsWrite)
	cfg.Writer = w
	a := NewActionString(cfg)
	if err := a.Trigger(context.Background(), "hello", hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	call, _ := w.last()
	if call.value != "hello" {
		t.Fatalf("value=%v", call.value)
	}
}

// --- Number / Integer / Float ---

func TestFloatSetRange(t *testing.T) {
	cfg := baseCfg(hmenum.ParameterSetTemperature, hmenum.ParameterTypeFloat, hmenum.OperationsWrite|hmenum.OperationsRead)
	cfg.Descriptor.Min = json.RawMessage("4.5")
	cfg.Descriptor.Max = json.RawMessage("30.5")
	cfg.Writer = &stubWriter{}
	f := NewFloat(cfg)
	if err := f.Set(context.Background(), 21.0, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if err := f.Set(context.Background(), 100.0, hmenum.CommandPriorityHigh); !errors.Is(err, ErrOutOfRange) {
		t.Fatalf("got %v, want ErrOutOfRange", err)
	}
}

func TestIntegerSetRange(t *testing.T) {
	cfg := baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeInteger, hmenum.OperationsWrite)
	cfg.Descriptor.Min = json.RawMessage("0")
	cfg.Descriptor.Max = json.RawMessage("100")
	cfg.Writer = &stubWriter{}
	i := NewInteger(cfg)
	if err := i.Set(context.Background(), 50, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if err := i.Set(context.Background(), 200, hmenum.CommandPriorityHigh); !errors.Is(err, ErrOutOfRange) {
		t.Fatal(err)
	}
}

// --- Select ---

func TestSelectLabelLookup(t *testing.T) {
	cfg := baseCfg(hmenum.ParameterControlMode, hmenum.ParameterTypeEnum, hmenum.OperationsRead|hmenum.OperationsWrite)
	cfg.Descriptor.ValueList = []string{"OFF", "AUTO", "MANU"}
	cfg.Writer = &stubWriter{}
	s := NewSelect(cfg)

	s.OnEvent(1)
	label, ok := s.Label()
	if !ok || label != "AUTO" {
		t.Fatalf("label=%q ok=%v", label, ok)
	}
	if err := s.SetLabel(context.Background(), "MANU", hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	v, _ := s.Value()
	if v != 2 {
		t.Fatalf("value=%d", v)
	}
}

func TestSelectEmptyValueListRejectsLabel(t *testing.T) {
	cfg := baseCfg(hmenum.ParameterControlMode, hmenum.ParameterTypeEnum, hmenum.OperationsWrite)
	cfg.Writer = &stubWriter{}
	s := NewSelect(cfg)
	if err := s.SetLabel(context.Background(), "X", hmenum.CommandPriorityHigh); !errors.Is(err, ErrEmptyValueList) {
		t.Fatalf("got %v, want ErrEmptyValueList", err)
	}
}

// --- Sensor / Text / Dummy ---

func TestSensorReadOnly(t *testing.T) {
	cfg := baseCfg(hmenum.ParameterTemperature, hmenum.ParameterTypeFloat, hmenum.OperationsRead|hmenum.OperationsEvent)
	s := NewFloatSensor(cfg)
	s.OnEvent(22.5)
	v, ok := s.Value()
	if !ok || v != 22.5 {
		t.Fatalf("v=%v ok=%v", v, ok)
	}
}

func TestTextSet(t *testing.T) {
	cfg := baseCfg(hmenum.ParameterDisplayDataString, hmenum.ParameterTypeString, hmenum.OperationsWrite|hmenum.OperationsRead)
	w := &stubWriter{}
	cfg.Writer = w
	tx := NewText(cfg)
	if err := tx.Set(context.Background(), "hi", hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	v, ok := tx.Value()
	if !ok || v != "hi" {
		t.Fatalf("v=%q ok=%v", v, ok)
	}
}

func TestDummyJustRemembersValue(t *testing.T) {
	d := NewDummy(baseCfg(hmenum.ParameterError, hmenum.ParameterTypeDummy, hmenum.OperationsRead))
	d.OnEvent("oops")
	v, ok := d.Value()
	if !ok || v.(string) != "oops" {
		t.Fatalf("v=%v ok=%v", v, ok)
	}
}

func TestDataPointUpdateDescriptor(t *testing.T) {
	cfg := baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead|hmenum.OperationsWrite)
	dp := NewDataPoint[float64](cfg)
	if got := dp.ParameterData().Type; got != hmenum.ParameterTypeFloat {
		t.Fatalf("initial type=%s", got)
	}
	dp.UpdateDescriptor(hmproto.ParameterData{
		Type:       hmenum.ParameterTypeInteger,
		Operations: hmenum.OperationsRead,
	})
	if got := dp.ParameterData().Type; got != hmenum.ParameterTypeInteger {
		t.Fatalf("after update type=%s want INTEGER", got)
	}
	if dp.IsWritable() {
		t.Fatal("after update writable must reflect new operations")
	}
}

func TestDataPointStatusTracking(t *testing.T) {
	dp := NewDataPoint[float64](baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead))
	if _, ok := dp.Status(); ok {
		t.Fatal("fresh status must not be observed")
	}
	var fired int
	var lastTransition struct {
		from, to hmenum.ParameterStatus
	}
	dp.OnStatusChange(func(from, to hmenum.ParameterStatus) {
		fired++
		lastTransition.from = from
		lastTransition.to = to
	})

	dp.UpdateStatus(hmenum.ParameterStatusNormal)
	if got, ok := dp.Status(); !ok || got != hmenum.ParameterStatusNormal {
		t.Fatalf("status=%v ok=%v want NORMAL/true", got, ok)
	}
	if fired != 1 {
		t.Fatalf("first transition fired %d times", fired)
	}

	// Same status twice → no extra fire.
	dp.UpdateStatus(hmenum.ParameterStatusNormal)
	if fired != 1 {
		t.Fatalf("idempotent UpdateStatus fired again: %d", fired)
	}

	// New status → fire with from/to.
	dp.UpdateStatus(hmenum.ParameterStatusOverflow)
	if fired != 2 {
		t.Fatalf("transition fired %d times", fired)
	}
	if lastTransition.from != hmenum.ParameterStatusNormal || lastTransition.to != hmenum.ParameterStatusOverflow {
		t.Fatalf("transition=%+v", lastTransition)
	}

	// Empty status → ignored.
	dp.UpdateStatus("")
	if fired != 2 {
		t.Fatalf("empty status fired: %d", fired)
	}
}

func TestDataPointWriteUnconfirmedValue(t *testing.T) {
	dp := NewDataPoint[float64](baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead|hmenum.OperationsWrite))
	stamp := time.Date(2026, 4, 26, 10, 0, 0, 0, time.UTC)
	dp.WriteUnconfirmedValue(0.5, stamp)
	if v, ok := dp.Value(); !ok || v != 0.5 {
		t.Fatalf("value=%v ok=%v want 0.5", v, ok)
	}
	if dp.IsOptimistic() {
		t.Fatal("WriteUnconfirmedValue must not engage optimistic tracker")
	}
	if got := dp.ModifiedAt(); !got.Equal(stamp) {
		t.Fatalf("ModifiedAt=%v want %v", got, stamp)
	}
}

func TestDataPointWriteUnconfirmedValueZeroTimeUsesNow(t *testing.T) {
	dp := NewDataPoint[float64](baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead))
	before := time.Now()
	dp.WriteUnconfirmedValue(0.7, time.Time{})
	if got := dp.ModifiedAt(); got.Before(before) {
		t.Fatalf("zero time must default to now: ModifiedAt=%v before=%v", got, before)
	}
}

func TestDataPointUsageDefaultsToDataPoint(t *testing.T) {
	dp := NewDataPoint[bool](baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead))
	if got := dp.Usage(); got != hmenum.DataPointUsageDataPoint {
		t.Fatalf("default usage=%q want data_point", got)
	}
}

func TestDataPointUsageRespectedFromConfig(t *testing.T) {
	cfg := baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead)
	cfg.Usage = hmenum.DataPointUsageNoCreate
	dp := NewDataPoint[bool](cfg)
	if dp.Usage() != hmenum.DataPointUsageNoCreate {
		t.Fatalf("explicit usage not honoured: got %q", dp.Usage())
	}
}

func TestDataPointEnabledByDefault(t *testing.T) {
	cases := []struct {
		usage hmenum.DataPointUsage
		want  bool
	}{
		{hmenum.DataPointUsageDataPoint, true},
		{hmenum.DataPointUsageCDPPrimary, true},
		{hmenum.DataPointUsageCDPVisible, true},
		{hmenum.DataPointUsageEvent, true},
		{hmenum.DataPointUsageCDPSecondary, false},
		{hmenum.DataPointUsageNoCreate, false},
		{"", true}, // empty falls back to DataPoint default
	}
	for _, c := range cases {
		t.Run(string(c.usage), func(t *testing.T) {
			cfg := baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead)
			cfg.Usage = c.usage
			dp := NewDataPoint[bool](cfg)
			if got := dp.EnabledByDefault(); got != c.want {
				t.Fatalf("usage=%q EnabledByDefault=%v want %v", c.usage, got, c.want)
			}
		})
	}
}

func TestCategoryToTypeMappingHits(t *testing.T) {
	cases := map[hmenum.DataPointCategory]hmenum.DataPointType{
		hmenum.DataPointCategorySwitch:       hmenum.DataPointTypeSwitch,
		hmenum.DataPointCategoryClimate:      hmenum.DataPointTypeClimate,
		hmenum.DataPointCategoryBinarySensor: hmenum.DataPointTypeBinarySensor,
		hmenum.DataPointCategorySensor:       hmenum.DataPointTypeSensor,
	}
	for cat, want := range cases {
		got, ok := hmenum.CategoryToType[cat]
		if !ok || got != want {
			t.Fatalf("%s → (%s, %v) want %s", cat, got, ok, want)
		}
	}
}

func TestDataPointOperationHelpers(t *testing.T) {
	rwe := hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent
	dp := NewDataPoint[bool](baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, rwe))
	if !dp.IsReadable() || !dp.IsWritable() || !dp.HasEvents() {
		t.Fatalf("rwe: read=%v write=%v event=%v", dp.IsReadable(), dp.IsWritable(), dp.HasEvents())
	}
	roDp := NewDataPoint[bool](baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead))
	if !roDp.IsReadable() || roDp.IsWritable() || roDp.HasEvents() {
		t.Fatalf("read-only: read=%v write=%v event=%v", roDp.IsReadable(), roDp.IsWritable(), roDp.HasEvents())
	}
	woDp := NewDataPoint[bool](baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsWrite))
	if woDp.IsReadable() || !woDp.IsWritable() || woDp.HasEvents() {
		t.Fatalf("write-only: read=%v write=%v event=%v", woDp.IsReadable(), woDp.IsWritable(), woDp.HasEvents())
	}
}

func TestDataPointModifiedVsRefreshedTimestamps(t *testing.T) {
	dp := NewDataPoint[int32](baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeInteger, hmenum.OperationsRead|hmenum.OperationsEvent))

	if !dp.ModifiedAt().IsZero() || !dp.RefreshedAt().IsZero() {
		t.Fatal("fresh data point must have both timestamps zero")
	}

	// Use explicit, strictly-increasing timestamps via OnEventAt so the
	// modified/refreshed distinction is deterministic instead of relying on
	// the wall clock advancing between OnEvent calls.
	t0 := time.Now()
	dp.OnEventAt(5, t0)
	mod1 := dp.ModifiedAt()
	ref1 := dp.RefreshedAt()
	if mod1.IsZero() || ref1.IsZero() {
		t.Fatal("first OnEvent must bump both timestamps")
	}

	// Same value at a later timestamp: refreshedAt advances, modifiedAt does not.
	dp.OnEventAt(5, t0.Add(2*time.Millisecond))
	if !dp.ModifiedAt().Equal(mod1) {
		t.Fatalf("unchanged value must not bump modifiedAt: was=%v now=%v", mod1, dp.ModifiedAt())
	}
	if !dp.RefreshedAt().After(ref1) {
		t.Fatalf("unchanged value must still bump refreshedAt: was=%v now=%v", ref1, dp.RefreshedAt())
	}

	// Different value at a still-later timestamp: both bump.
	dp.OnEventAt(7, t0.Add(4*time.Millisecond))
	if !dp.ModifiedAt().After(mod1) {
		t.Fatal("changed value must bump modifiedAt")
	}
}

func TestDataPointIsValueInRangeFloat(t *testing.T) {
	cfg := baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead|hmenum.OperationsWrite)
	cfg.Descriptor.Min = []byte(`0`)
	cfg.Descriptor.Max = []byte(`1`)
	dp := NewDataPoint[float64](cfg)
	if !dp.IsValueInRange(0.5) {
		t.Fatal("0.5 must be in [0, 1]")
	}
	if dp.IsValueInRange(-0.1) {
		t.Fatal("-0.1 must be out of [0, 1]")
	}
	if dp.IsValueInRange(1.5) {
		t.Fatal("1.5 must be out of [0, 1]")
	}
	// Boundary edges are inclusive (matches CCU semantics).
	if !dp.IsValueInRange(0) || !dp.IsValueInRange(1) {
		t.Fatal("boundaries must be inclusive")
	}
}

func TestDataPointIsValueInRangeInt(t *testing.T) {
	cfg := baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeInteger, hmenum.OperationsWrite)
	cfg.Descriptor.Min = []byte(`0`)
	cfg.Descriptor.Max = []byte(`100`)
	dp := NewDataPoint[int32](cfg)
	if !dp.IsValueInRange(50) {
		t.Fatal("50 must be in [0, 100]")
	}
	if dp.IsValueInRange(101) {
		t.Fatal("101 must be out")
	}
	if dp.IsValueInRange(-1) {
		t.Fatal("-1 must be out")
	}
}

func TestDataPointIsValueInRangeEnum(t *testing.T) {
	cfg := baseCfg(hmenum.ParameterControlMode, hmenum.ParameterTypeEnum, hmenum.OperationsWrite)
	cfg.Descriptor.ValueList = []string{"AUTO", "MANUAL", "BOOST", "AWAY"}
	dp := NewDataPoint[int32](cfg)
	for _, idx := range []int32{0, 1, 2, 3} {
		if !dp.IsValueInRange(idx) {
			t.Fatalf("idx=%d must be in enum range", idx)
		}
	}
	if dp.IsValueInRange(4) {
		t.Fatal("idx=4 must be out (len=4)")
	}
	if dp.IsValueInRange(-1) {
		t.Fatal("idx=-1 must be out")
	}
}

func TestDataPointIsValueInRangeBoolStringAlwaysOK(t *testing.T) {
	bdp := NewDataPoint[bool](baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsWrite))
	if !bdp.IsValueInRange(true) || !bdp.IsValueInRange(false) {
		t.Fatal("bool has no range constraint")
	}
	sdp := NewDataPoint[string](baseCfg(hmenum.ParameterDisplayDataString, hmenum.ParameterTypeString, hmenum.OperationsWrite))
	if !sdp.IsValueInRange("anything goes") {
		t.Fatal("string has no range constraint")
	}
}

func TestDataPointIsCurrentValueInRange(t *testing.T) {
	cfg := baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead|hmenum.OperationsEvent)
	cfg.Descriptor.Min = []byte(`0`)
	cfg.Descriptor.Max = []byte(`1`)
	dp := NewDataPoint[float64](cfg)
	// Unobserved is vacuously valid.
	if !dp.IsCurrentValueInRange() {
		t.Fatal("fresh DP must report current-value-in-range as true")
	}
	dp.OnEvent(0.5)
	if !dp.IsCurrentValueInRange() {
		t.Fatal("0.5 must be in range")
	}
	// We cannot simulate "out-of-range CCU value" via OnEvent (the DP
	// stores whatever the wire reports), but the helper still returns
	// the bound result correctly: shrink the implicit bound to 0.4
	// would require descriptor mutation, which we deliberately avoid.
}

func TestButtonAndActionsForceOptimisticDisabled(t *testing.T) {
	cfg := baseCfg(hmenum.ParameterPressShort, hmenum.ParameterTypeAction, hmenum.OperationsWrite|hmenum.OperationsEvent)
	cfg.OptimisticDisabled = false // caller may try to enable — constructor must override
	cfg.Writer = &stubWriter{}
	if !NewButton(cfg).OptimisticDisabled {
		t.Fatal("Button must force OptimisticDisabled")
	}
	if !NewActionBoolean(cfg).OptimisticDisabled {
		t.Fatal("ActionBoolean must force OptimisticDisabled")
	}
	if !NewActionFloat(cfg).OptimisticDisabled {
		t.Fatal("ActionFloat must force OptimisticDisabled")
	}
	if !NewActionInteger(cfg).OptimisticDisabled {
		t.Fatal("ActionInteger must force OptimisticDisabled")
	}
	if !NewActionString(cfg).OptimisticDisabled {
		t.Fatal("ActionString must force OptimisticDisabled")
	}
	if !NewActionSelect(cfg).OptimisticDisabled {
		t.Fatal("ActionSelect must force OptimisticDisabled")
	}
}

// --- ForcedUsage (Wave-X D.11) ------------------------------------

func TestSetForcedUsageVisible(t *testing.T) {
	dp := NewDataPoint[bool](baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead))
	if got := dp.Usage(); got != hmenum.DataPointUsageDataPoint {
		t.Fatalf("default Usage = %q, want DataPoint", got)
	}
	if !dp.Visible() {
		t.Fatal("DataPoint usage must be visible by default")
	}
	dp.SetForcedUsage(hmenum.DataPointUsageCDPVisible)
	if got := dp.Usage(); got != hmenum.DataPointUsageCDPVisible {
		t.Fatalf("after SetForcedUsage(CDPVisible) Usage = %q", got)
	}
	if !dp.Visible() {
		t.Fatal("forced CDPVisible must be Visible()")
	}
	if !dp.EnabledByDefault() {
		t.Fatal("forced CDPVisible must be EnabledByDefault()")
	}
}

func TestSetForcedUsageNoCreate(t *testing.T) {
	cfg := baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead)
	cfg.Usage = hmenum.DataPointUsageCDPPrimary // would otherwise be visible
	dp := NewDataPoint[bool](cfg)
	if !dp.Visible() {
		t.Fatal("CDPPrimary must be visible before forcing")
	}
	dp.SetForcedUsage(hmenum.DataPointUsageNoCreate)
	if got := dp.Usage(); got != hmenum.DataPointUsageNoCreate {
		t.Fatalf("Usage = %q, want NoCreate", got)
	}
	if dp.Visible() {
		t.Fatal("forced NoCreate must NOT be Visible()")
	}
	if dp.EnabledByDefault() {
		t.Fatal("forced NoCreate must NOT be EnabledByDefault()")
	}
}

func TestSetForcedUsageRoundtrip(t *testing.T) {
	dp := NewDataPoint[int32](baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeInteger, hmenum.OperationsRead))
	if _, ok := dp.ForcedUsage(); ok {
		t.Fatal("ForcedUsage must report false on a fresh DP")
	}
	dp.SetForcedUsage(hmenum.DataPointUsageDataPoint)
	got, ok := dp.ForcedUsage()
	if !ok {
		t.Fatal("ForcedUsage must report true after SetForcedUsage")
	}
	if got != hmenum.DataPointUsageDataPoint {
		t.Fatalf("ForcedUsage = %q, want DataPoint", got)
	}
	// Override
	dp.SetForcedUsage(hmenum.DataPointUsageCDPSecondary)
	if u := dp.Usage(); u != hmenum.DataPointUsageCDPSecondary {
		t.Fatalf("after override Usage = %q", u)
	}
	if dp.Visible() {
		t.Fatal("CDPSecondary must NOT be Visible()")
	}
}

func TestSetForcedUsageOverridesConfigUsage(t *testing.T) {
	// A DP whose Spec.Usage says NoCreate becomes visible once the
	// materializer forces CDPVisible.
	cfg := baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead)
	cfg.Usage = hmenum.DataPointUsageNoCreate
	dp := NewDataPoint[bool](cfg)
	if dp.Visible() {
		t.Fatal("Spec.Usage=NoCreate must yield Visible()=false")
	}
	dp.SetForcedUsage(hmenum.DataPointUsageCDPVisible)
	if !dp.Visible() {
		t.Fatal("forced CDPVisible must override Spec.Usage=NoCreate")
	}
}
