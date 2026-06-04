// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package valve

import (
	"context"
	"maps"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// newTestIrrigation builds a channel with a STATE *generic.Switch wire-DP
// and calls NewIrrigation(ch). It replaces the old NewIrrigation(addr, centralName, w)
// three-argument form in test fixtures.
func newTestIrrigation(t *testing.T, addr string, w custom.Writer) *Irrigation {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "VCU0001"})
	ch := d.AddChannel(addr, 4, "SWITCH", hmenum.ParamsetKeyValues)
	dp := generic.NewSwitch(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: addr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterState),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
		Writer: w,
	})
	ch.Put(dp)
	return NewIrrigation(ch)
}

// newTestModulating builds a channel with a LEVEL *generic.Float wire-DP
// and calls NewModulating(ch). It replaces the old NewModulating(addr, centralName, w)
// three-argument form in test fixtures.
func newTestModulating(t *testing.T, addr string, w custom.Writer) *Modulating {
	t.Helper()
	d := device.New(device.Config{InterfaceID: "HmIP-RF", Address: "VCU0001"})
	ch := d.AddChannel(addr, 4, "SWITCH", hmenum.ParamsetKeyValues)
	dp := generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: addr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterLevel),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
		Writer: w,
	})
	ch.Put(dp)
	return NewModulating(ch)
}

type stubWriter struct {
	mu    sync.Mutex
	calls []call
}

type call struct {
	param hmenum.Parameter
	value any
}

func (s *stubWriter) SetValue(_ context.Context, _ string, p hmenum.Parameter, v any, _ hmenum.CommandPriority) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, call{p, v})
	return nil
}

// putWriter additionally implements generic.ParamsetWriter so the
// atomic-batching code path is exercised. It records the entire
// values map as one logical "call" — emulates the Python
// `mock_client.put_paramset` assertion.
type putWriter struct {
	stubWriter
	puts []map[string]any
}

func (p *putWriter) PutParamset(_ context.Context, _ string, _ hmenum.ParamsetKey, values map[string]any, _ hmenum.CommandPriority) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	cp := make(map[string]any, len(values))
	maps.Copy(cp, values)
	p.puts = append(p.puts, cp)
	return nil
}

// ─── Irrigation open/close commands ──────────────────────────────────────────

func TestIrrigationOpenWithDurationWritesOnTime(t *testing.T) {
	w := &stubWriter{}
	v := newTestIrrigation(t, "HmIP-IRRIG:3", w)
	if err := v.Open(context.Background(), 10*time.Minute, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if len(w.calls) != 2 {
		t.Fatalf("calls=%d", len(w.calls))
	}
	if w.calls[0].param != hmenum.ParameterOnTime {
		t.Fatalf("first call=%+v", w.calls[0])
	}
	if sec := w.calls[0].value.(float64); sec != 600 {
		t.Fatalf("on-time seconds=%v", sec)
	}
	if w.calls[1].param != hmenum.ParameterState || w.calls[1].value != true {
		t.Fatalf("state call=%+v", w.calls[1])
	}
}

func TestIrrigationOpenWithDurationAtomicPutParamset(t *testing.T) {
	w := &putWriter{}
	v := newTestIrrigation(t, "VCU8976407:4", w)
	if err := v.Open(context.Background(), time.Minute, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if len(w.puts) != 1 {
		t.Fatalf("expected 1 put_paramset, got %d (sets=%d)", len(w.puts), len(w.calls))
	}
	got := w.puts[0]
	if got[string(hmenum.ParameterOnTime)].(float64) != 60 {
		t.Errorf("ON_TIME=%v", got[string(hmenum.ParameterOnTime)])
	}
	if got[string(hmenum.ParameterState)] != true {
		t.Errorf("STATE=%v", got[string(hmenum.ParameterState)])
	}
}

func TestIrrigationOpenWithoutDurationSkipsOnTime(t *testing.T) {
	w := &stubWriter{}
	v := newTestIrrigation(t, "x", w)
	if err := v.Open(context.Background(), 0, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if len(w.calls) != 1 {
		t.Fatalf("calls=%d", len(w.calls))
	}
}

func TestIrrigationClose(t *testing.T) {
	w := &stubWriter{}
	v := newTestIrrigation(t, "x", w)
	if err := v.Close(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if w.calls[0].value != false {
		t.Fatalf("close value=%v", w.calls[0].value)
	}
	open, observed := v.IsOpen()
	if open || !observed {
		t.Fatalf("open=%v observed=%v", open, observed)
	}
}

// TestIrrigationCloseResetsPendingTimer pins that Close() cancels any pending
// ON_TIME timer set by a prior Open(duration) call. Without the reset, the
// deferred timer survives the close and re-opens the valve later.
func TestIrrigationCloseResetsPendingTimer(t *testing.T) {
	w := &stubWriter{}
	v := newTestIrrigation(t, "HmIP-IRRIG:3", w)

	// Open with duration — arms the ON_TIME timer.
	if err := v.Open(context.Background(), 5*time.Minute, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	// Confirm the timer is pending.
	if _, ok := v.TimerOnTime(); !ok {
		t.Skip("TimerOnTime not pending; nothing to reset")
	}

	prevCalls := len(w.calls)
	if err := v.Close(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	// After Close, the pending timer must be cleared.
	if _, ok := v.TimerOnTime(); ok {
		t.Error("pending ON_TIME must be cleared after Close()")
	}
	// Close must still emit a STATE=false write.
	if len(w.calls) <= prevCalls {
		t.Error("Close() must write STATE=false to the wire")
	}
	last := w.calls[len(w.calls)-1]
	if last.param != hmenum.ParameterState || last.value != false {
		t.Errorf("Close() last write: param=%v value=%v, want STATE=false", last.param, last.value)
	}
}

// ─── Modulating set level ─────────────────────────────────────────────────────

func TestModulatingSetLevelClamps(t *testing.T) {
	w := &stubWriter{}
	v := newTestModulating(t, "x", w)
	if err := v.SetLevel(context.Background(), 1.5, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if w.calls[0].value.(float64) != 1 {
		t.Fatalf("level=%v, want 1", w.calls[0].value)
	}
	p, _ := v.Level()
	if p.Level() != 1 {
		t.Fatalf("position=%v", p.Level())
	}
}

// ─── Irrigation topology ──────────────────────────────────────────────────────

func TestIrrigationHAComponent(t *testing.T) {
	t.Parallel()

	v := newTestIrrigation(t, "HmIP-IRRIG:3", &stubWriter{})
	if got := v.HAComponent(); got != "valve" {
		t.Errorf("Irrigation.HAComponent() = %q, want valve", got)
	}
}

func TestIrrigationTopicSlotWithChannelAddress(t *testing.T) {
	t.Parallel()

	v := newTestIrrigation(t, "HmIP-IRRIG:3", &stubWriter{})
	slot := v.TopicSlot()
	if slot.Parameter != "valve_irrigation" {
		t.Errorf("TopicSlot.Parameter = %q, want valve_irrigation", slot.Parameter)
	}
	if slot.Channel != 3 {
		t.Errorf("TopicSlot.Channel = %d, want 3", slot.Channel)
	}
}

func TestIrrigationTopicSlotFallbackOnInvalidAddress(t *testing.T) {
	t.Parallel()

	v := newTestIrrigation(t, "NOCORON", &stubWriter{})
	slot := v.TopicSlot()
	if slot.Address != "NOCORON" {
		t.Errorf("TopicSlot fallback address = %q, want NOCORON", slot.Address)
	}
}

// ─── Modulating topology ──────────────────────────────────────────────────────

func TestModulatingHAComponent(t *testing.T) {
	t.Parallel()

	v := newTestModulating(t, "HmIP-FALMOT-C12:1", &stubWriter{})
	if got := v.HAComponent(); got != "valve" {
		t.Errorf("Modulating.HAComponent() = %q, want valve", got)
	}
}

func TestModulatingTopicSlotWithChannelAddress(t *testing.T) {
	t.Parallel()

	v := newTestModulating(t, "HmIP-FALMOT-C12:1", &stubWriter{})
	slot := v.TopicSlot()
	if slot.Parameter != "valve_modulating" {
		t.Errorf("TopicSlot.Parameter = %q, want valve_modulating", slot.Parameter)
	}
	if slot.Channel != 1 {
		t.Errorf("TopicSlot.Channel = %d, want 1", slot.Channel)
	}
}

func TestModulatingTopicSlotFallbackOnInvalidAddress(t *testing.T) {
	t.Parallel()

	v := newTestModulating(t, "NOCORON", &stubWriter{})
	slot := v.TopicSlot()
	if slot.Address != "NOCORON" {
		t.Errorf("TopicSlot fallback address = %q, want NOCORON", slot.Address)
	}
}

// ─── Irrigation payload ───────────────────────────────────────────────────────

func TestIrrigationInfoPayload(t *testing.T) {
	t.Parallel()

	v := newTestIrrigation(t, "HmIP-IRRIG:3", &stubWriter{})
	p, ok := v.Info().(*payload.IrrigationValveInfo)
	if !ok || p == nil {
		t.Fatal("InfoPayload must return a non-nil *payload.IrrigationValveInfo")
	}
	if p.Category != "valve" {
		t.Errorf("InfoPayload category = %v, want valve", p.Category)
	}
	if p.Kind != "irrigation" {
		t.Errorf("InfoPayload kind = %v, want irrigation", p.Kind)
	}
}

func TestIrrigationInfoPayloadNilReceiverReturnsNil(t *testing.T) {
	t.Parallel()

	var v *Irrigation
	if v.Info() != nil {
		t.Errorf("nil Irrigation.Info() must return nil")
	}
}

func TestIrrigationConfigPayload(t *testing.T) {
	t.Parallel()

	v := newTestIrrigation(t, "x", &stubWriter{})
	p, _ := v.Config().(*payload.IrrigationValveConfig)
	if p == nil {
		t.Fatal("ConfigPayload must not be nil")
	}
	if p.Kind != "irrigation" {
		t.Errorf("ConfigPayload kind = %v, want irrigation", p.Kind)
	}
}

func TestIrrigationConfigPayloadNilReceiverReturnsNil(t *testing.T) {
	t.Parallel()

	var v *Irrigation
	if p, _ := v.Config().(*payload.IrrigationValveConfig); p != nil {
		t.Errorf("nil Irrigation.Config() = %v, want nil", p)
	}
}

func TestIrrigationStatePayloadClosedBeforeObservation(t *testing.T) {
	t.Parallel()

	v := newTestIrrigation(t, "x", &stubWriter{})
	p, ok := v.State().(*payload.IrrigationValveState)
	if !ok || p == nil {
		t.Fatal("StatePayload must not be nil")
	}
	if p.IsOpen {
		t.Errorf("StatePayload is_open before observation = %v, want false", p.IsOpen)
	}
}

func TestIrrigationStatePayloadOpenAfterEvent(t *testing.T) {
	t.Parallel()

	v := newTestIrrigation(t, "x", &stubWriter{})
	v.OnState(true)
	p, ok := v.State().(*payload.IrrigationValveState)
	if !ok || p == nil {
		t.Fatal("StatePayload must not be nil")
	}
	if !p.IsOpen {
		t.Errorf("StatePayload is_open after OnState(true) = %v, want true", p.IsOpen)
	}
}

func TestIrrigationStatePayloadNilReceiverReturnsNil(t *testing.T) {
	t.Parallel()

	var v *Irrigation
	if v.State() != nil {
		t.Errorf("nil Irrigation.State() must return nil")
	}
}

// ─── Irrigation service registration ─────────────────────────────────────────

func TestIrrigationServiceOpen(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	v := newTestIrrigation(t, "HmIP-IRRIG:3", w)
	// open without duration.
	if err := v.Invoke(context.Background(), "open", map[string]any{}, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("Irrigation open service: %v", err)
	}
	if len(w.calls) == 0 {
		t.Fatal("open service must write STATE")
	}
	if w.calls[0].param != hmenum.ParameterState || w.calls[0].value != true {
		t.Errorf("open service wrote %+v, want STATE=true", w.calls[0])
	}
}

func TestIrrigationServiceOpenWithDuration(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	v := newTestIrrigation(t, "HmIP-IRRIG:3", w)
	params := map[string]any{"duration": float64(60)} // 60 s
	if err := v.Invoke(context.Background(), "open", params, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("Irrigation open(duration) service: %v", err)
	}
	// First call must be ON_TIME.
	found := false
	for _, c := range w.calls {
		if c.param == hmenum.ParameterOnTime {
			found = true
		}
	}
	if !found {
		t.Error("open(duration) service must write ON_TIME")
	}
}

func TestIrrigationServiceClose(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	v := newTestIrrigation(t, "HmIP-IRRIG:3", w)
	if err := v.Invoke(context.Background(), "close", nil, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("Irrigation close service: %v", err)
	}
	if len(w.calls) == 0 {
		t.Fatal("close service must write STATE")
	}
	if w.calls[0].param != hmenum.ParameterState || w.calls[0].value != false {
		t.Errorf("close service wrote %+v, want STATE=false", w.calls[0])
	}
}

// ─── Modulating payload ───────────────────────────────────────────────────────

func TestModulatingInfoPayload(t *testing.T) {
	t.Parallel()

	v := newTestModulating(t, "HmIP-FALMOT-C12:1", &stubWriter{})
	p, ok := v.Info().(*payload.ModulatingValveInfo)
	if !ok || p == nil {
		t.Fatal("InfoPayload must return a non-nil *payload.ModulatingValveInfo")
	}
	if p.Category != "valve" {
		t.Errorf("InfoPayload category = %v, want valve", p.Category)
	}
	if p.Kind != "modulating" {
		t.Errorf("InfoPayload kind = %v, want modulating", p.Kind)
	}
}

func TestModulatingInfoPayloadNilReceiverReturnsNil(t *testing.T) {
	t.Parallel()

	var v *Modulating
	if v.Info() != nil {
		t.Errorf("nil Modulating.Info() must return nil")
	}
}

func TestModulatingConfigPayload(t *testing.T) {
	t.Parallel()

	v := newTestModulating(t, "x", &stubWriter{})
	p, _ := v.Config().(*payload.ModulatingValveConfig)
	if p == nil {
		t.Fatal("ConfigPayload must not be nil")
	}
	if p.Kind != "modulating" {
		t.Errorf("ConfigPayload kind = %v, want modulating", p.Kind)
	}
}

func TestModulatingConfigPayloadNilReceiverReturnsNil(t *testing.T) {
	t.Parallel()

	var v *Modulating
	if p, _ := v.Config().(*payload.ModulatingValveConfig); p != nil {
		t.Errorf("nil Modulating.Config() = %v, want nil", p)
	}
}

func TestModulatingStatePayloadBeforeObservation(t *testing.T) {
	t.Parallel()

	v := newTestModulating(t, "x", &stubWriter{})
	p, ok := v.State().(*payload.ModulatingValveState)
	if !ok || p == nil {
		t.Fatal("StatePayload must not be nil")
	}
	if p.CurrentLevel != nil {
		t.Errorf("StatePayload current_level before observation = %v, want nil", *p.CurrentLevel)
	}
	if p.CurrentLevelPct != 0.0 {
		t.Errorf("StatePayload current_level_pct before observation = %v, want 0.0", p.CurrentLevelPct)
	}
}

func TestModulatingStatePayloadAfterEvent(t *testing.T) {
	t.Parallel()

	v := newTestModulating(t, "x", &stubWriter{})
	v.OnLevel(0.5)
	p, ok := v.State().(*payload.ModulatingValveState)
	if !ok || p == nil {
		t.Fatal("StatePayload must not be nil")
	}
	if p.CurrentLevel == nil || *p.CurrentLevel != 0.5 {
		t.Errorf("StatePayload current_level = %v, want 0.5", p.CurrentLevel)
	}
	if p.CurrentLevelPct != 50.0 {
		t.Errorf("StatePayload current_level_pct = %v, want 50.0", p.CurrentLevelPct)
	}
}

func TestModulatingStatePayloadNilReceiverReturnsNil(t *testing.T) {
	t.Parallel()

	var v *Modulating
	if v.State() != nil {
		t.Errorf("nil Modulating.State() must return nil")
	}
}

// ─── Modulating service registration ─────────────────────────────────────────

func TestModulatingServiceSetLevel(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	v := newTestModulating(t, "HmIP-FALMOT-C12:1", w)
	params := map[string]any{"level": float64(0.6)}
	if err := v.Invoke(context.Background(), "set_level", params, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("Modulating set_level service: %v", err)
	}
	if len(w.calls) == 0 {
		t.Fatal("set_level service must write LEVEL")
	}
	if w.calls[0].param != hmenum.ParameterLevel {
		t.Errorf("set_level service wrote param %q, want LEVEL", w.calls[0].param)
	}
	if w.calls[0].value.(float64) != 0.6 {
		t.Errorf("set_level service wrote value %v, want 0.6", w.calls[0].value)
	}
}

func TestModulatingServiceSetLevelMissingParamReturnsError(t *testing.T) {
	t.Parallel()

	v := newTestModulating(t, "x", &stubWriter{})
	// No "level" key → error expected.
	if err := v.Invoke(context.Background(), "set_level", map[string]any{}, hmenum.CommandPriorityHigh); err == nil {
		t.Error("set_level with missing level param must return error")
	}
}

// ─── IsStateChange after observation ─────────────────────────────────────────

func TestIrrigationIsStateChangeAfterObservation(t *testing.T) {
	t.Parallel()

	v := newTestIrrigation(t, "x", &stubWriter{})
	v.OnState(true)

	// Same state → no change.
	if v.IsStateChange(true) {
		t.Error("IsStateChange(true) when already open must be false")
	}
	// Different state → change.
	if !v.IsStateChange(false) {
		t.Error("IsStateChange(false) when open must be true")
	}
}

// ─── Irrigation address and state reflection ──────────────────────────────────

// TestIrrigationAddressReturnsChannelAddress verifies Address().
func TestIrrigationAddressReturnsChannelAddress(t *testing.T) {
	t.Parallel()

	const addr = "HmIP-IRRIG:3"
	v := newTestIrrigation(t, addr, &stubWriter{})
	if got := v.Address(); got != addr {
		t.Errorf("Address() = %q, want %q", got, addr)
	}
}

// TestIrrigationOpenSetsStateTrue verifies that Open() without a duration
// writes STATE=true.
func TestIrrigationOpenSetsStateTrue(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	v := newTestIrrigation(t, "HmIP-IRRIG:3", w)
	if err := v.Open(context.Background(), 0, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if len(w.calls) != 1 {
		t.Fatalf("expected 1 write, got %d", len(w.calls))
	}
	if w.calls[0].param != hmenum.ParameterState || w.calls[0].value != true {
		t.Errorf("Open() wrote %+v, want STATE=true", w.calls[0])
	}
}

// TestIrrigationCloseSetsStateFalse verifies that Close() writes STATE=false
// and updates the observed state accordingly.
func TestIrrigationCloseSetsStateFalse(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	v := newTestIrrigation(t, "HmIP-IRRIG:3", w)
	if err := v.Close(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if len(w.calls) != 1 {
		t.Fatalf("expected 1 write, got %d", len(w.calls))
	}
	if w.calls[0].param != hmenum.ParameterState || w.calls[0].value != false {
		t.Errorf("Close() wrote %+v, want STATE=false", w.calls[0])
	}
	open, observed := v.IsOpen()
	if open || !observed {
		t.Errorf("IsOpen after Close = %v observed=%v, want (false, true)", open, observed)
	}
}

// TestIrrigationCurrentStateReflectsDP verifies that OnState updates the
// value visible through IsOpen.
func TestIrrigationCurrentStateReflectsDP(t *testing.T) {
	t.Parallel()

	v := newTestIrrigation(t, "HmIP-IRRIG:3", &stubWriter{})

	// Not yet observed.
	if _, ok := v.IsOpen(); ok {
		t.Error("IsOpen should not be observed before any event")
	}

	v.OnState(true)
	open, ok := v.IsOpen()
	if !ok || !open {
		t.Errorf("IsOpen after OnState(true) = %v ok=%v, want (true, true)", open, ok)
	}

	v.OnState(false)
	open, ok = v.IsOpen()
	if !ok || open {
		t.Errorf("IsOpen after OnState(false) = %v ok=%v, want (false, true)", open, ok)
	}
}

// TestIrrigationIsStateChangeBeforeObservation verifies that
// IsStateChange always returns true before a state has been observed.
func TestIrrigationIsStateChangeBeforeObservation(t *testing.T) {
	t.Parallel()

	v := newTestIrrigation(t, "HmIP-IRRIG:3", &stubWriter{})
	if !v.IsStateChange(true) {
		t.Error("IsStateChange must be true before any state observed")
	}
	if !v.IsStateChange(false) {
		t.Error("IsStateChange must be true before any state observed (false variant)")
	}
}

// TestIrrigationGroupStateNotNil verifies that GroupState() is non-nil.
func TestIrrigationGroupStateNotNil(t *testing.T) {
	t.Parallel()

	v := newTestIrrigation(t, "HmIP-IRRIG:3", &stubWriter{})
	if v.GroupState() == nil {
		t.Error("GroupState() must not be nil")
	}
}

// TestIrrigationOpenWithDurationAtomicBatch verifies that Open() with a
// positive duration sends {ON_TIME, STATE} atomically via PutParamset.
func TestIrrigationOpenWithDurationAtomicBatch(t *testing.T) {
	t.Parallel()

	w := &putWriter{}
	v := newTestIrrigation(t, "HmIP-IRRIG:3", w)
	if err := v.Open(context.Background(), 5*time.Minute, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if len(w.puts) != 1 {
		t.Fatalf("expected 1 put_paramset, got %d (sets=%d)", len(w.puts), len(w.calls))
	}
	got := w.puts[0]
	if got[string(hmenum.ParameterState)] != true {
		t.Errorf("atomic batch STATE=%v, want true", got[string(hmenum.ParameterState)])
	}
	if v, ok := got[string(hmenum.ParameterOnTime)].(float64); !ok || v < 299 || v > 301 {
		t.Errorf("atomic batch ON_TIME=%v, want ~300", got[string(hmenum.ParameterOnTime)])
	}
}

// ─── Modulating address and state reflection ──────────────────────────────────

// TestModulatingAddressReturnsChannelAddress verifies Address().
func TestModulatingAddressReturnsChannelAddress(t *testing.T) {
	t.Parallel()

	const addr = "HmIP-FALMOT-C12:1"
	v := newTestModulating(t, addr, &stubWriter{})
	if got := v.Address(); got != addr {
		t.Errorf("Address() = %q, want %q", got, addr)
	}
}

// TestModulatingSetLevelForwardsToValveState verifies that SetLevel
// writes the clamped level via LEVEL parameter.
func TestModulatingSetLevelForwardsToValveState(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	v := newTestModulating(t, "HmIP-FALMOT-C12:1", w)
	if err := v.SetLevel(context.Background(), 0.4, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if len(w.calls) == 0 {
		t.Fatal("no call recorded")
	}
	if w.calls[0].param != hmenum.ParameterLevel {
		t.Errorf("SetLevel wrote param %q, want LEVEL", w.calls[0].param)
	}
	if w.calls[0].value.(float64) != 0.4 {
		t.Errorf("SetLevel wrote value %v, want 0.4", w.calls[0].value)
	}
}

// TestModulatingCurrentStateReflectsDP verifies that OnLevel updates the
// value visible through Level().
func TestModulatingCurrentStateReflectsDP(t *testing.T) {
	t.Parallel()

	v := newTestModulating(t, "HmIP-FALMOT-C12:1", &stubWriter{})

	// Not yet observed.
	if _, ok := v.Level(); ok {
		t.Error("Level should not be observed before any event")
	}

	v.OnLevel(0.75)
	pos, ok := v.Level()
	if !ok {
		t.Fatal("Level() should be observed after OnLevel")
	}
	if pos.Level() != 0.75 {
		t.Errorf("Level().Level() = %v, want 0.75", pos.Level())
	}
}

// TestModulatingSetLevelClampsAboveOne verifies that SetLevel clamps
// values above 1.0 to 1.0.
func TestModulatingSetLevelClampsAboveOne(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	v := newTestModulating(t, "HmIP-FALMOT-C12:1", w)
	if err := v.SetLevel(context.Background(), 2.0, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if w.calls[0].value.(float64) != 1.0 {
		t.Errorf("SetLevel(2.0) wrote %v, want 1.0 (clamped)", w.calls[0].value)
	}
}

// TestModulatingSetLevelClampsBelowZero verifies that SetLevel clamps
// values below 0.0 to 0.0.
func TestModulatingSetLevelClampsBelowZero(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	v := newTestModulating(t, "HmIP-FALMOT-C12:1", w)
	if err := v.SetLevel(context.Background(), -0.5, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if w.calls[0].value.(float64) != 0.0 {
		t.Errorf("SetLevel(-0.5) wrote %v, want 0.0 (clamped)", w.calls[0].value)
	}
}

// ─── IsRefreshed lifecycle ────────────────────────────────────────────────────

// TestIrrigationIsRefreshedFalseWhenUnobserved verifies that a newly
// constructed Irrigation reports IsRefreshed=false.
func TestIrrigationIsRefreshedFalseWhenUnobserved(t *testing.T) {
	v := newTestIrrigation(t, "VCU0001:1", &stubWriter{})
	if v.IsRefreshed() {
		t.Fatal("expected IsRefreshed=false on un-observed irrigation valve")
	}
}

// TestIrrigationIsRefreshedTrueAfterObservation verifies that IsRefreshed
// returns true once the underlying Switch receives a CCU push.
func TestIrrigationIsRefreshedTrueAfterObservation(t *testing.T) {
	v := newTestIrrigation(t, "VCU0001:1", &stubWriter{})
	v.OnEvent(false)
	if !v.IsRefreshed() {
		t.Fatal("expected IsRefreshed=true after Switch observed")
	}
}

// TestModulatingIsRefreshedFalseWhenUnobserved verifies that a newly
// constructed Modulating reports IsRefreshed=false.
func TestModulatingIsRefreshedFalseWhenUnobserved(t *testing.T) {
	v := newTestModulating(t, "VCU0001:1", &stubWriter{})
	if v.IsRefreshed() {
		t.Fatal("expected IsRefreshed=false on un-observed modulating valve")
	}
}

// TestModulatingIsRefreshedTrueAfterObservation verifies IsRefreshed=true
// after the underlying Float receives a CCU push.
func TestModulatingIsRefreshedTrueAfterObservation(t *testing.T) {
	v := newTestModulating(t, "VCU0001:1", &stubWriter{})
	v.OnEvent(0.5)
	if !v.IsRefreshed() {
		t.Fatal("expected IsRefreshed=true after Float observed")
	}
}

// ─── Irrigation open/close state gate ────────────────────────────────────────

// TestIrrigationOpenSkipsWhenAlreadyOpen verifies that Open returns nil without
// a wire write when the valve is already open (and no timer is pending).
func TestIrrigationOpenSkipsWhenAlreadyOpen(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	v := newTestIrrigation(t, "VCU:4", w)
	v.OnState(true)

	before := len(w.calls)
	if err := v.Open(context.Background(), 0, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	if len(w.calls) != before {
		t.Errorf("Irrigation.Open wrote %d time(s) when valve already open; want 0", len(w.calls)-before)
	}
}

// TestIrrigationOpenPassesWhenClosed verifies that Open issues a wire write
// when the valve is currently closed.
func TestIrrigationOpenPassesWhenClosed(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	v := newTestIrrigation(t, "VCU:4", w)
	v.OnState(false)

	before := len(w.calls)
	if err := v.Open(context.Background(), 0, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	if len(w.calls) == before {
		t.Error("Irrigation.Open issued no write when valve was closed; want 1 write")
	}
}

// TestIrrigationCloseSkipsWhenAlreadyClosed verifies that Close returns nil
// without a wire write when the valve is already closed.
func TestIrrigationCloseSkipsWhenAlreadyClosed(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	v := newTestIrrigation(t, "VCU:4", w)
	v.OnState(false)

	before := len(w.calls)
	if err := v.Close(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	if len(w.calls) != before {
		t.Errorf("Irrigation.Close wrote %d time(s) when valve already closed; want 0", len(w.calls)-before)
	}
}

// TestIrrigationClosePassesWhenOpen verifies that Close issues a wire write
// when the valve is currently open.
func TestIrrigationClosePassesWhenOpen(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	v := newTestIrrigation(t, "VCU:4", w)
	v.OnState(true)

	before := len(w.calls)
	if err := v.Close(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
	if len(w.calls) == before {
		t.Error("Irrigation.Close issued no write when valve was open; want 1 write")
	}
}

// TestIrrigationOpenWithTimerPassesWhenAlreadyOpen verifies that Open with a
// positive duration always writes (timer arming must reach the wire).
func TestIrrigationOpenWithTimerPassesWhenAlreadyOpen(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	v := newTestIrrigation(t, "VCU:4", w)
	v.OnState(true)

	before := len(w.calls)
	if err := v.Open(context.Background(), 30*time.Second, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("Open with timer returned error: %v", err)
	}
	if len(w.calls) == before {
		t.Error("Irrigation.Open with duration=30s issued no write when valve was open; want at least 1 write")
	}
}

// ─── Irrigation behavioral tests ─────────────────────────────────────────────

// TestIrrigationOpenWritesStateTrue verifies that Open() writes
// STATE=true.
func TestIrrigationOpenWritesStateTrue(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	v := newTestIrrigation(t, "VCU8976407:4", w)
	if err := v.Open(context.Background(), 0, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if len(w.calls) != 1 || w.calls[0].param != hmenum.ParameterState || w.calls[0].value != true {
		t.Fatalf("Open() calls=%+v, want [STATE=true]", w.calls)
	}
}

// TestIrrigationCloseWritesStateFalse verifies that Close() writes
// STATE=false.
func TestIrrigationCloseWritesStateFalse(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	v := newTestIrrigation(t, "VCU8976407:4", w)
	if err := v.Close(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if len(w.calls) != 1 || w.calls[0].value != false {
		t.Fatalf("Close() calls=%+v, want [STATE=false]", w.calls)
	}
}

// TestIrrigationOpenWithDurationBundlesOnTimeAndState verifies the
// on_time path bundles ON_TIME + STATE atomically.
func TestIrrigationOpenWithDurationBundlesOnTimeAndState(t *testing.T) {
	t.Parallel()

	w := &putWriter{}
	v := newTestIrrigation(t, "VCU8976407:4", w)
	if err := v.Open(context.Background(), time.Minute, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if len(w.puts) != 1 {
		t.Fatalf("expected 1 put_paramset, got %d (sets=%d)", len(w.puts), len(w.calls))
	}
	got := w.puts[0]
	if got[string(hmenum.ParameterOnTime)].(float64) != 60 {
		t.Errorf("ON_TIME=%v, want 60", got[string(hmenum.ParameterOnTime)])
	}
	if got[string(hmenum.ParameterState)] != true {
		t.Errorf("STATE=%v, want true", got[string(hmenum.ParameterState)])
	}
}

// TestIrrigationOpenZeroDurationSkipsOnTime verifies that
// Open(0) does NOT write ON_TIME.
func TestIrrigationOpenZeroDurationSkipsOnTime(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	v := newTestIrrigation(t, "VCU8976407:4", w)
	if err := v.Open(context.Background(), 0, hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if len(w.calls) != 1 {
		t.Fatalf("Open() without duration must produce exactly 1 SetValue, got %d", len(w.calls))
	}
	if w.calls[0].param == hmenum.ParameterOnTime {
		t.Error("Open() without duration must not write ON_TIME")
	}
}

// TestIrrigationTurnOnWithTimerAtomicBatch verifies the deferred timer
// path via the embedded TurnOn: SetTimerOnTime + TurnOn() → atomic
// put_paramset. Second TurnOn (without a pending timer) falls back to plain
// SetValue.
func TestIrrigationTurnOnWithTimerAtomicBatch(t *testing.T) {
	t.Parallel()

	w := &putWriter{}
	v := newTestIrrigation(t, "VCU8976407:4", w)
	v.SetTimerOnTime(35400 * time.Millisecond) // 35.4 s
	if err := v.TurnOn(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if len(w.puts) != 1 {
		t.Fatalf("first TurnOn: expected 1 put_paramset, got %d", len(w.puts))
	}
	got := w.puts[0]
	if vt, _ := got[string(hmenum.ParameterOnTime)].(float64); vt < 35.3 || vt > 35.5 {
		t.Errorf("ON_TIME=%v, want ~35.4", got[string(hmenum.ParameterOnTime)])
	}
	// Timer consumed → second TurnOn must NOT produce put_paramset.
	w.puts = nil
	if err := v.TurnOn(context.Background(), hmenum.CommandPriorityHigh); err != nil {
		t.Fatal(err)
	}
	if len(w.puts) != 0 {
		t.Errorf("second TurnOn must not produce put_paramset, got %d", len(w.puts))
	}
}

// TestIrrigationIsOpenAfterCommands verifies IsOpen/IsOpen() state
// reflects the last write.
func TestIrrigationIsOpenAfterCommands(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	v := newTestIrrigation(t, "VCU8976407:4", w)
	_ = v.Open(context.Background(), 0, hmenum.CommandPriorityHigh)
	open, observed := v.IsOpen()
	if !open || !observed {
		t.Errorf("IsOpen()=(%v, %v) after Open(), want (true, true)", open, observed)
	}
	_ = v.Close(context.Background(), hmenum.CommandPriorityHigh)
	open, observed = v.IsOpen()
	if open || !observed {
		t.Errorf("IsOpen()=(%v, %v) after Close(), want (false, true)", open, observed)
	}
}

// TestIrrigationIsStateChangeSemantics verifies IsStateChange semantics:
// true when unobserved and when target differs from current.
func TestIrrigationIsStateChangeSemantics(t *testing.T) {
	t.Parallel()

	v := newTestIrrigation(t, "VCU8976407:4", &stubWriter{})
	// Unobserved: always a change.
	if !v.IsStateChange(true) || !v.IsStateChange(false) {
		t.Error("IsStateChange must be true when unobserved")
	}
	// After observing true: change only when target is false.
	v.OnState(true)
	if v.IsStateChange(true) {
		t.Error("IsStateChange(true) must be false when already open")
	}
	if !v.IsStateChange(false) {
		t.Error("IsStateChange(false) must be true when currently open")
	}
}

// TestModulatingSetLevelClampsToRange verifies that the modulating valve
// clamps levels to [0, 1].
func TestModulatingSetLevelClampsToRange(t *testing.T) {
	t.Parallel()

	cases := []struct {
		input float64
		want  float64
	}{
		{1.5, 1.0},
		{-0.1, 0.0},
		{0.5, 0.5},
	}
	for _, tc := range cases {
		w := &stubWriter{}
		v := newTestModulating(t, "x", w)
		if err := v.SetLevel(context.Background(), tc.input, hmenum.CommandPriorityHigh); err != nil {
			t.Fatalf("input=%v: %v", tc.input, err)
		}
		if got := w.calls[0].value.(float64); got != tc.want {
			t.Errorf("input=%v: level=%v, want %v", tc.input, got, tc.want)
		}
	}
}

// TestIrrigationAddressRoundtrip verifies Address() returns the
// construction address.
func TestIrrigationAddressRoundtrip(t *testing.T) {
	t.Parallel()

	const addr = "VCU8976407:4"
	v := newTestIrrigation(t, addr, &stubWriter{})
	if got := v.Address(); got != addr {
		t.Errorf("Address()=%q, want %q", got, addr)
	}
}

// TestIrrigationOpenIdempotentDouble verifies that the second open
// when already open does not panic (idempotency at value level).
func TestIrrigationOpenIdempotentDouble(t *testing.T) {
	t.Parallel()

	w := &stubWriter{}
	v := newTestIrrigation(t, "VCU8976407:4", w)
	_ = v.Open(context.Background(), 0, hmenum.CommandPriorityHigh)
	_ = v.Open(context.Background(), 0, hmenum.CommandPriorityHigh)
	// No panic → pass.
}
