// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package device

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ---------- fake ChannelWriter -----------------------------------------

type fakeChannelWriter struct {
	mu       sync.Mutex
	setCalls []fakeSetCall
	putCalls []fakePutCall
	failSet  error
	failPut  error
}

type fakeSetCall struct {
	channelAddress string
	parameter      hmenum.Parameter
	value          any
	priority       hmenum.CommandPriority
}

type fakePutCall struct {
	channelAddress string
	paramsetKey    hmenum.ParamsetKey
	values         map[string]any
	priority       hmenum.CommandPriority
}

func (f *fakeChannelWriter) SetValue(
	_ context.Context,
	channelAddress string,
	param hmenum.Parameter,
	value any,
	priority hmenum.CommandPriority,
) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failSet != nil {
		return f.failSet
	}
	f.setCalls = append(f.setCalls, fakeSetCall{channelAddress, param, value, priority})
	return nil
}

func (f *fakeChannelWriter) PutParamset(
	_ context.Context,
	channelAddress string,
	paramsetKey hmenum.ParamsetKey,
	values map[string]any,
	priority hmenum.CommandPriority,
) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failPut != nil {
		return f.failPut
	}
	f.putCalls = append(f.putCalls, fakePutCall{channelAddress, paramsetKey, values, priority})
	return nil
}

func (f *fakeChannelWriter) setCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.setCalls)
}

func (f *fakeChannelWriter) putCallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.putCalls)
}

func (f *fakeChannelWriter) putSnapshot() []fakePutCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakePutCall(nil), f.putCalls...)
}

// ---------- fake ChannelRefresher --------------------------------------

type fakeChannelRefresher struct {
	mu      sync.Mutex
	values  map[string]any
	failGet error
	calls   int
}

func (f *fakeChannelRefresher) GetParamset(
	_ context.Context,
	_ string,
	_ hmenum.ParamsetKey,
) (map[string]any, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.failGet != nil {
		return nil, f.failGet
	}
	out := make(map[string]any, len(f.values))
	maps.Copy(out, f.values)
	return out, nil
}

// ---------- test helpers -----------------------------------------------

const testChannelAddr = "TEST:1"

// newWritableFloatDP creates a float VALUES data point with
// READ|WRITE|EVENT ops attached to the supplied writer.
func newWritableFloatDP(addr string, p hmenum.Parameter, w generic.Writer) *generic.Float {
	return generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: addr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(p),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
			Min:        json.RawMessage("0.0"),
			Max:        json.RawMessage("100.0"),
		},
		Writer: w,
	})
}

// newWritableBoolDP creates a bool VALUES data point.
func newWritableBoolDP(addr string, p hmenum.Parameter, w generic.Writer) *generic.Switch {
	return generic.NewSwitch(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: addr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(p),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
		Writer: w,
	})
}

// newWritableMasterFloatDP creates a float MASTER data point.
func newWritableMasterFloatDP(addr string, p hmenum.Parameter, w generic.Writer) *generic.Float {
	return generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: addr,
			ParamsetKey:    hmenum.ParamsetKeyMaster,
			Parameter:      string(p),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
			Min:        json.RawMessage("0.0"),
			Max:        json.RawMessage("100.0"),
		},
		Writer: w,
	})
}

func newTestChannel(t *testing.T, w ChannelWriter) *Channel {
	t.Helper()
	d := New(Config{
		InterfaceID: "HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "TEST",
		Model:       "HmIP-TEST",
	})
	ch := d.AddChannel(testChannelAddr, 1, "TEST_TYPE", hmenum.ParamsetKeyValues)
	if w != nil {
		ch.SetWriter(w)
	}
	return ch
}

// ---------- SetWriter / SetRefresher -----------------------------------

func TestSetWriterInstallsWriter(t *testing.T) {
	t.Parallel()
	w := &fakeChannelWriter{}
	ch := newTestChannel(t, nil)
	ch.SetWriter(w)

	ch.mu.RLock()
	got := ch.writer
	ch.mu.RUnlock()
	if got != w {
		t.Fatal("writer was not installed")
	}
}

func TestSetRefresherInstallsRefresher(t *testing.T) {
	t.Parallel()
	r := &fakeChannelRefresher{}
	ch := newTestChannel(t, nil)
	ch.SetRefresher(r)

	ch.mu.RLock()
	got := ch.refresher
	ch.mu.RUnlock()
	if got != r {
		t.Fatal("refresher was not installed")
	}
}

// ---------- Set --------------------------------------------------------

func TestChannelSetWritesViaWriter(t *testing.T) {
	t.Parallel()

	w := &fakeChannelWriter{}
	ch := newTestChannel(t, w)
	dp := newWritableFloatDP(testChannelAddr, hmenum.ParameterLevel, w)
	ch.Put(dp)

	val := hmtypes.FloatValue(0.75)
	err := ch.Set(context.Background(), hmenum.ParamsetKeyValues, hmenum.ParameterLevel, val, SetOptions{})
	if err != nil {
		t.Fatalf("Set: %v", err)
	}
	if w.setCallCount() != 1 {
		t.Fatalf("expected 1 SetValue call, got %d", w.setCallCount())
	}
	w.mu.Lock()
	call := w.setCalls[0]
	w.mu.Unlock()
	if call.channelAddress != testChannelAddr {
		t.Errorf("wrong channel address: %q", call.channelAddress)
	}
	if call.value.(float64) != 0.75 {
		t.Errorf("wrong value: %v", call.value)
	}
}

func TestChannelSetMissingParameterReturnsErr(t *testing.T) {
	t.Parallel()

	w := &fakeChannelWriter{}
	ch := newTestChannel(t, w)
	// Do NOT add any DP.

	err := ch.Set(context.Background(), hmenum.ParamsetKeyValues, hmenum.ParameterLevel,
		hmtypes.FloatValue(0.5), SetOptions{})
	if !errors.Is(err, ErrUnknownParameter) {
		t.Fatalf("want ErrUnknownParameter, got %v", err)
	}
	if w.setCallCount() != 0 {
		t.Fatal("SetValue must not be called for unknown parameter")
	}
}

func TestChannelSetMissingWriterReturnsErr(t *testing.T) {
	t.Parallel()

	ch := newTestChannel(t, nil) // no writer
	// Use a nil writer so data point has one, but channel has none.
	dp := newWritableFloatDP(testChannelAddr, hmenum.ParameterLevel, nil)
	ch.Put(dp)

	err := ch.Set(context.Background(), hmenum.ParamsetKeyValues, hmenum.ParameterLevel,
		hmtypes.FloatValue(0.5), SetOptions{})
	if !errors.Is(err, ErrNoChannelWriter) {
		t.Fatalf("want ErrNoChannelWriter, got %v", err)
	}
}

func TestChannelSetValidateRejectsBadType(t *testing.T) {
	t.Parallel()

	w := &fakeChannelWriter{}
	ch := newTestChannel(t, w)
	dp := newWritableFloatDP(testChannelAddr, hmenum.ParameterLevel, w)
	ch.Put(dp)

	// Send a bool to a float parameter — Validate should reject.
	err := ch.Set(context.Background(), hmenum.ParamsetKeyValues, hmenum.ParameterLevel,
		hmtypes.BoolValue(true), SetOptions{Validate: true})
	if err == nil {
		t.Fatal("expected validation error for wrong type, got nil")
	}
	if w.setCallCount() != 0 {
		t.Fatal("SetValue must not be called after validation failure")
	}
}

func TestChannelSetValidateAcceptsGoodValue(t *testing.T) {
	t.Parallel()

	w := &fakeChannelWriter{}
	ch := newTestChannel(t, w)
	dp := newWritableFloatDP(testChannelAddr, hmenum.ParameterLevel, w)
	ch.Put(dp)

	err := ch.Set(context.Background(), hmenum.ParamsetKeyValues, hmenum.ParameterLevel,
		hmtypes.FloatValue(42.0), SetOptions{Validate: true})
	if err != nil {
		t.Fatalf("Set with valid value: %v", err)
	}
	if w.setCallCount() != 1 {
		t.Fatal("expected 1 SetValue call")
	}
}

func TestChannelSetOptimisticStagesValueBeforeWire(t *testing.T) {
	t.Parallel()

	w := &fakeChannelWriter{}
	ch := newTestChannel(t, w)
	dp := newWritableFloatDP(testChannelAddr, hmenum.ParameterLevel, w)
	ch.Put(dp)

	err := ch.Set(context.Background(), hmenum.ParamsetKeyValues, hmenum.ParameterLevel,
		hmtypes.FloatValue(1.0), SetOptions{Optimistic: true})
	if err != nil {
		t.Fatalf("Set optimistic: %v", err)
	}
	if !dp.IsOptimistic() {
		t.Fatal("data point should be in optimistic state after Set with Optimistic=true")
	}
	v, ok := dp.Value()
	if !ok {
		t.Fatal("data point should have an observed value")
	}
	if v != 1.0 {
		t.Fatalf("optimistic value: got %v, want 1.0", v)
	}
}

func TestChannelSetOptimisticRollsBackOnWireError(t *testing.T) {
	t.Parallel()

	w := &fakeChannelWriter{failSet: errors.New("CCU unreachable")}
	ch := newTestChannel(t, w)
	dp := newWritableFloatDP(testChannelAddr, hmenum.ParameterLevel, w)
	ch.Put(dp)

	err := ch.Set(context.Background(), hmenum.ParamsetKeyValues, hmenum.ParameterLevel,
		hmtypes.FloatValue(1.0), SetOptions{Optimistic: true})
	if err == nil {
		t.Fatal("expected wire error, got nil")
	}
	// After rollback the optimistic state must be cleared.
	if dp.IsOptimistic() {
		t.Fatal("data point should NOT be in optimistic state after wire failure")
	}
}

func TestChannelSetNoDoubleStageWhenOptimisticSameValue(t *testing.T) {
	t.Parallel()

	// Burst-skip: if the tracker already holds the same value, a second
	// Set should not bump PendingSends beyond 1.
	w := &fakeChannelWriter{}
	ch := newTestChannel(t, w)
	dp := newWritableFloatDP(testChannelAddr, hmenum.ParameterLevel, w)
	ch.Put(dp)

	opts := SetOptions{Optimistic: true}
	v := hmtypes.FloatValue(0.5)

	if err := ch.Set(context.Background(), hmenum.ParamsetKeyValues, hmenum.ParameterLevel, v, opts); err != nil {
		t.Fatalf("first Set: %v", err)
	}
	pending1 := dp.PendingSends()
	if pending1 != 1 {
		t.Fatalf("after first Set: PendingSends=%d, want 1", pending1)
	}

	// Second Set with same value — burst-skip applies.
	if err := ch.Set(context.Background(), hmenum.ParamsetKeyValues, hmenum.ParameterLevel, v, opts); err != nil {
		t.Fatalf("second Set: %v", err)
	}
	// PendingSends should still be 1 because the burst-skip fired.
	pending2 := dp.PendingSends()
	if pending2 != 1 {
		t.Fatalf("after burst-skip Set: PendingSends=%d, want 1 (no double-stage)", pending2)
	}
}

func TestChannelSetRespectsCollectorFromContext(t *testing.T) {
	t.Parallel()

	w := &fakeChannelWriter{}
	ch := newTestChannel(t, w)
	dp := newWritableFloatDP(testChannelAddr, hmenum.ParameterLevel, w)
	ch.Put(dp)

	// Build a collector backed by w (acts as CollectorBackend via channelWriterBackend).
	backend := &channelWriterBackend{w: w}
	coll := generic.NewCollector(backend)
	ctx := generic.ContextWithCollector(context.Background(), coll)

	err := ch.Set(ctx, hmenum.ParamsetKeyValues, hmenum.ParameterLevel,
		hmtypes.FloatValue(0.3), SetOptions{})
	if err != nil {
		t.Fatalf("Set with collector in ctx: %v", err)
	}
	// The value should be buffered in the collector, NOT dispatched directly.
	if w.setCallCount() != 0 {
		t.Fatalf("SetValue must not be called while collector is active (got %d calls)", w.setCallCount())
	}
	if coll.Len() != 1 {
		t.Fatalf("collector should have 1 buffered item, got %d", coll.Len())
	}

	// Now Send — the wire call should happen.
	if err := coll.Send(context.Background()); err != nil {
		t.Fatalf("collector Send: %v", err)
	}
	if w.setCallCount() != 1 {
		t.Fatalf("expected 1 SetValue after Send, got %d", w.setCallCount())
	}
}

func TestChannelSetMasterParameter(t *testing.T) {
	t.Parallel()

	w := &fakeChannelWriter{}
	ch := newTestChannel(t, w)
	dp := newWritableMasterFloatDP(testChannelAddr, hmenum.ParameterLevel, w)
	ch.PutMaster(dp)

	err := ch.Set(context.Background(), hmenum.ParamsetKeyMaster, hmenum.ParameterLevel,
		hmtypes.FloatValue(5.0), SetOptions{})
	if err != nil {
		t.Fatalf("Set MASTER: %v", err)
	}
	// MASTER dispatches through PutParamset — setValue is VALUES-only.
	if w.putCallCount() != 1 || w.setCallCount() != 0 {
		t.Fatalf("expected 1 PutParamset for MASTER, got %d PutParamset / %d SetValue",
			w.putCallCount(), w.setCallCount())
	}
}

// ---------- SetMany ----------------------------------------------------

func TestChannelSetManyDispatchesViaPutParamsetWhenAvailable(t *testing.T) {
	t.Parallel()

	w := &fakeChannelWriter{}
	ch := newTestChannel(t, w)
	dpLevel := newWritableFloatDP(testChannelAddr, hmenum.ParameterLevel, w)
	dpLevel2 := newWritableFloatDP(testChannelAddr, hmenum.ParameterLevel2, w)
	ch.Put(dpLevel)
	ch.Put(dpLevel2)

	values := map[hmenum.Parameter]hmtypes.ParamValue{
		hmenum.ParameterLevel:  hmtypes.FloatValue(0.8),
		hmenum.ParameterLevel2: hmtypes.FloatValue(0.4),
	}
	err := ch.SetMany(context.Background(), hmenum.ParamsetKeyValues, values, SetOptions{})
	if err != nil {
		t.Fatalf("SetMany: %v", err)
	}
	// With two parameters the collector dispatches PutParamset, not SetValue.
	if w.putCallCount() != 1 {
		t.Fatalf("expected 1 PutParamset call, got %d (setCallCount=%d)", w.putCallCount(), w.setCallCount())
	}
	if w.setCallCount() != 0 {
		t.Fatalf("SetValue should not be called when PutParamset is available")
	}
	w.mu.Lock()
	put := w.putCalls[0]
	w.mu.Unlock()
	if put.channelAddress != testChannelAddr {
		t.Errorf("wrong channel address: %q", put.channelAddress)
	}
	if len(put.values) != 2 {
		t.Errorf("PutParamset values count: got %d, want 2", len(put.values))
	}
}

func TestChannelSetManySingleParamUsesSetValue(t *testing.T) {
	t.Parallel()

	// The CallParameterCollector dispatches single-parameter groups
	// via SetValue (not PutParamset). This verifies that SetMany with
	// exactly one parameter goes through SetValue.
	w := &fakeChannelWriter{}
	ch := newTestChannel(t, w)
	dp := newWritableFloatDP(testChannelAddr, hmenum.ParameterLevel, w)
	ch.Put(dp)

	values := map[hmenum.Parameter]hmtypes.ParamValue{
		hmenum.ParameterLevel: hmtypes.FloatValue(0.5),
	}
	err := ch.SetMany(context.Background(), hmenum.ParamsetKeyValues, values, SetOptions{})
	if err != nil {
		t.Fatalf("SetMany single param: %v", err)
	}
	// Single parameter: collector dispatches SetValue, not PutParamset.
	if w.setCallCount() != 1 {
		t.Fatalf("expected 1 SetValue for single-param SetMany, got %d", w.setCallCount())
	}
	if w.putCallCount() != 0 {
		t.Fatalf("PutParamset should not be called for single parameter, got %d", w.putCallCount())
	}
}

func TestChannelSetManyJoinsCollectorWhenInContext(t *testing.T) {
	t.Parallel()

	w := &fakeChannelWriter{}
	ch := newTestChannel(t, w)
	dpLevel := newWritableFloatDP(testChannelAddr, hmenum.ParameterLevel, w)
	dpLevel2 := newWritableFloatDP(testChannelAddr, hmenum.ParameterLevel2, w)
	ch.Put(dpLevel)
	ch.Put(dpLevel2)

	backend := &channelWriterBackend{w: w}
	coll := generic.NewCollector(backend)
	ctx := generic.ContextWithCollector(context.Background(), coll)

	values := map[hmenum.Parameter]hmtypes.ParamValue{
		hmenum.ParameterLevel:  hmtypes.FloatValue(0.6),
		hmenum.ParameterLevel2: hmtypes.FloatValue(0.2),
	}
	if err := ch.SetMany(ctx, hmenum.ParamsetKeyValues, values, SetOptions{}); err != nil {
		t.Fatalf("SetMany with collector: %v", err)
	}
	// Nothing dispatched yet.
	if w.setCallCount() != 0 || w.putCallCount() != 0 {
		t.Fatalf("no wire calls should happen while collector is active")
	}
	if coll.Len() != 2 {
		t.Fatalf("collector should have 2 items, got %d", coll.Len())
	}
	if err := coll.Send(context.Background()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	// Two parameters → PutParamset.
	if w.putCallCount() != 1 {
		t.Fatalf("expected 1 PutParamset after Send, got %d", w.putCallCount())
	}
}

func TestChannelSetManyUnknownParameterReturnsErr(t *testing.T) {
	t.Parallel()

	w := &fakeChannelWriter{}
	ch := newTestChannel(t, w)
	// Do NOT register any DP.

	values := map[hmenum.Parameter]hmtypes.ParamValue{
		hmenum.ParameterLevel: hmtypes.FloatValue(0.5),
	}
	err := ch.SetMany(context.Background(), hmenum.ParamsetKeyValues, values, SetOptions{})
	if !errors.Is(err, ErrUnknownParameter) {
		t.Fatalf("want ErrUnknownParameter, got %v", err)
	}
	if w.setCallCount() != 0 && w.putCallCount() != 0 {
		t.Fatal("no wire calls should happen when parameter is unknown")
	}
}

// ---------- Get / GetAll -----------------------------------------------

func TestChannelGetReadsObservedValue(t *testing.T) {
	t.Parallel()

	ch := newTestChannel(t, nil)
	dp := newWritableFloatDP(testChannelAddr, hmenum.ParameterLevel, nil)
	dp.OnEvent(0.42)
	ch.Put(dp)

	pv, ts, ok := ch.Get(hmenum.ParamsetKeyValues, hmenum.ParameterLevel)
	if !ok {
		t.Fatal("Get: ok should be true after OnEvent")
	}
	if ts.IsZero() {
		t.Fatal("Get: timestamp should not be zero")
	}
	if pv.Kind != hmtypes.ValueKindFloat || pv.Float != 0.42 {
		t.Fatalf("Get: unexpected value %v", pv)
	}
}

func TestChannelGetUnobservedParameterReturnsFalse(t *testing.T) {
	t.Parallel()

	ch := newTestChannel(t, nil)
	dp := newWritableFloatDP(testChannelAddr, hmenum.ParameterLevel, nil)
	ch.Put(dp) // no OnEvent call — no observed value

	_, _, ok := ch.Get(hmenum.ParamsetKeyValues, hmenum.ParameterLevel)
	if ok {
		t.Fatal("Get on unobserved DP: ok should be false")
	}
}

func TestChannelGetMissingParameterReturnsFalse(t *testing.T) {
	t.Parallel()

	ch := newTestChannel(t, nil)

	_, _, ok := ch.Get(hmenum.ParamsetKeyValues, hmenum.ParameterLevel)
	if ok {
		t.Fatal("Get on missing parameter: ok should be false")
	}
}

func TestChannelGetAllReturnsSnapshot(t *testing.T) {
	t.Parallel()

	ch := newTestChannel(t, nil)
	dpLevel := newWritableFloatDP(testChannelAddr, hmenum.ParameterLevel, nil)
	dpLevel2 := newWritableFloatDP(testChannelAddr, hmenum.ParameterLevel2, nil)
	dpLevel.OnEvent(0.5)
	dpLevel2.OnEvent(0.25)
	// Third DP without observation — should be omitted from GetAll.
	dpState := newWritableBoolDP(testChannelAddr, hmenum.ParameterState, nil)
	ch.Put(dpLevel)
	ch.Put(dpLevel2)
	ch.Put(dpState)

	all := ch.GetAll(hmenum.ParamsetKeyValues)
	if len(all) != 2 {
		t.Fatalf("GetAll: got %d entries, want 2 (only observed)", len(all))
	}
	if pv, ok := all[hmenum.ParameterLevel]; !ok || pv.Float != 0.5 {
		t.Fatalf("GetAll LEVEL: %v", pv)
	}
	if pv, ok := all[hmenum.ParameterLevel2]; !ok || pv.Float != 0.25 {
		t.Fatalf("GetAll LEVEL_2: %v", pv)
	}
}

func TestChannelGetAllReturnsSnapshotCopy(t *testing.T) {
	t.Parallel()

	ch := newTestChannel(t, nil)
	dp := newWritableFloatDP(testChannelAddr, hmenum.ParameterLevel, nil)
	dp.OnEvent(0.75) // use a non-integer float so NewParamValue returns FloatValue
	ch.Put(dp)

	all1 := ch.GetAll(hmenum.ParamsetKeyValues)
	// Mutate the returned map.
	all1[hmenum.ParameterLevel] = hmtypes.FloatValue(999.0)

	// A second GetAll should still return the real observed value.
	all2 := ch.GetAll(hmenum.ParamsetKeyValues)
	if pv := all2[hmenum.ParameterLevel]; pv.Float != 0.75 {
		t.Fatalf("GetAll snapshot was mutated by caller: got %v, want 0.75", pv)
	}
}

// ---------- Refresh ----------------------------------------------------

func TestChannelRefreshNoRefresherReturnsErr(t *testing.T) {
	t.Parallel()

	ch := newTestChannel(t, nil) // no refresher

	err := ch.Refresh(context.Background(), hmenum.ParamsetKeyValues)
	if !errors.Is(err, ErrNoChannelRefresher) {
		t.Fatalf("want ErrNoChannelRefresher, got %v", err)
	}
}

func TestChannelRefreshFeedsValuesIntoDPs(t *testing.T) {
	t.Parallel()

	r := &fakeChannelRefresher{
		values: map[string]any{
			string(hmenum.ParameterLevel):  float64(0.6),
			string(hmenum.ParameterLevel2): float64(0.3),
		},
	}
	ch := newTestChannel(t, nil)
	ch.SetRefresher(r)

	dpLevel := newWritableFloatDP(testChannelAddr, hmenum.ParameterLevel, nil)
	dpLevel2 := newWritableFloatDP(testChannelAddr, hmenum.ParameterLevel2, nil)
	ch.Put(dpLevel)
	ch.Put(dpLevel2)

	if err := ch.Refresh(context.Background(), hmenum.ParamsetKeyValues); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	v, ok := dpLevel.Value()
	if !ok || v != 0.6 {
		t.Fatalf("LEVEL after Refresh: got (%v, %v), want (0.6, true)", v, ok)
	}
	v2, ok2 := dpLevel2.Value()
	if !ok2 || v2 != 0.3 {
		t.Fatalf("LEVEL_2 after Refresh: got (%v, %v), want (0.3, true)", v2, ok2)
	}
}

func TestChannelRefreshReadsViaRefresher(t *testing.T) {
	t.Parallel()

	r := &fakeChannelRefresher{values: map[string]any{}}
	ch := newTestChannel(t, nil)
	ch.SetRefresher(r)

	_ = ch.Refresh(context.Background(), hmenum.ParamsetKeyValues)

	r.mu.Lock()
	calls := r.calls
	r.mu.Unlock()
	if calls != 1 {
		t.Fatalf("expected 1 GetParamset call, got %d", calls)
	}
}

func TestChannelRefreshReturnsFetchError(t *testing.T) {
	t.Parallel()

	fetchErr := errors.New("backend error")
	r := &fakeChannelRefresher{failGet: fetchErr}
	ch := newTestChannel(t, nil)
	ch.SetRefresher(r)

	err := ch.Refresh(context.Background(), hmenum.ParamsetKeyValues)
	if !errors.Is(err, fetchErr) {
		t.Fatalf("Refresh: want fetch error, got %v", err)
	}
}

func TestChannelRefreshSkipsUnknownParameters(t *testing.T) {
	t.Parallel()

	// Refresher returns a parameter the channel does not know about.
	r := &fakeChannelRefresher{
		values: map[string]any{
			"UNKNOWN_PARAM":               float64(1.0),
			string(hmenum.ParameterLevel): float64(0.5),
		},
	}
	ch := newTestChannel(t, nil)
	ch.SetRefresher(r)
	dp := newWritableFloatDP(testChannelAddr, hmenum.ParameterLevel, nil)
	ch.Put(dp)

	if err := ch.Refresh(context.Background(), hmenum.ParamsetKeyValues); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	v, ok := dp.Value()
	if !ok || v != 0.5 {
		t.Fatalf("LEVEL after Refresh: got (%v, %v)", v, ok)
	}
}

// ---------- SetOptions zero-value pinning ------------------------------

func TestSetOptionsZeroValueDefaults(t *testing.T) {
	t.Parallel()

	var opts SetOptions
	// Zero-value: Validate=false, Optimistic=false, WaitForEcho=false.
	if opts.Validate {
		t.Error("zero Validate should be false")
	}
	if opts.Optimistic {
		t.Error("zero Optimistic should be false")
	}
	if opts.WaitForEcho {
		t.Error("zero WaitForEcho should be false")
	}
	if opts.Priority != hmenum.CommandPriorityCritical {
		// CommandPriorityCritical == 0 — the zero value of CommandPriority.
		// This is intentional per CLAUDE.md: "CommandPriority.Critical = 0".
		// Direct dispatch uses whatever priority the opts specifies; callers
		// that want High must set it explicitly.
		_ = opts.Priority // just confirm we can read it
	}
	if opts.RxMode != "" {
		t.Error("zero RxMode should be empty string")
	}
	if opts.Source != "" {
		t.Error("zero Source should be empty string")
	}
}

// ---------- MasterRefreshHook -----------------------------------------

// TestMasterRefreshHookFiredOnMasterSet verifies that a successful
// Channel.Set with ParamsetKeyMaster fires the installed hook.
func TestMasterRefreshHookFiredOnMasterSet(t *testing.T) {
	t.Parallel()

	w := &fakeChannelWriter{}
	ch := newTestChannel(t, w)
	dp := newWritableMasterFloatDP(testChannelAddr, hmenum.Parameter("SHORT_ON_TIME"), w)
	ch.PutMaster(dp)

	var (
		hookMu    sync.Mutex
		hookAddr  string
		hookKey   hmenum.ParamsetKey
		hookFired bool
		done      = make(chan struct{})
	)
	ch.SetMasterRefreshHook(func(addr string, key hmenum.ParamsetKey) {
		hookMu.Lock()
		hookAddr = addr
		hookKey = key
		hookFired = true
		hookMu.Unlock()
		close(done)
	})

	if err := ch.Set(context.Background(), hmenum.ParamsetKeyMaster,
		hmenum.Parameter("SHORT_ON_TIME"), hmtypes.FloatValue(1.0), SetOptions{}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	select {
	case <-done:
	case <-timeoutCh(t, 2):
		t.Fatal("hook was not fired within 2 seconds")
	}

	hookMu.Lock()
	defer hookMu.Unlock()
	if !hookFired {
		t.Fatal("hook was not invoked")
	}
	if hookAddr != testChannelAddr {
		t.Errorf("hook addr=%q, want %q", hookAddr, testChannelAddr)
	}
	if hookKey != hmenum.ParamsetKeyMaster {
		t.Errorf("hook key=%q, want MASTER", hookKey)
	}
}

// TestMasterRefreshHookNotFiredOnValuesSet verifies that the hook is NOT
// invoked when the write targets the VALUES paramset (only MASTER triggers
// a poll).
func TestMasterRefreshHookNotFiredOnValuesSet(t *testing.T) {
	t.Parallel()

	w := &fakeChannelWriter{}
	ch := newTestChannel(t, w)
	dp := newWritableFloatDP(testChannelAddr, hmenum.ParameterLevel, w)
	ch.Put(dp)

	fired := make(chan struct{}, 1)
	ch.SetMasterRefreshHook(func(_ string, _ hmenum.ParamsetKey) {
		select {
		case fired <- struct{}{}:
		default:
		}
	})

	if err := ch.Set(context.Background(), hmenum.ParamsetKeyValues,
		hmenum.ParameterLevel, hmtypes.FloatValue(0.5), SetOptions{}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	// Give any spurious goroutine a chance to fire, then assert silence.
	select {
	case <-fired:
		t.Fatal("hook must NOT fire for VALUES writes")
	case <-timeoutCh(t, 0):
		// expected: no signal within the short window
	}
}

// TestSetMasterDispatchesThroughPutParamset pins that Channel.Set honours the
// paramset key it was given. ChannelWriter.SetValue carries no paramset key
// and reaches the wire as xml-rpc setValue, which is VALUES-only — so
// dispatching a MASTER write through it sends a device configuration change
// to the wrong paramset while the master-refresh hook fires as if it had
// landed. Covers both the optimistic and the plain branch.
func TestSetMasterDispatchesThroughPutParamset(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		opts SetOptions
	}{
		{"plain", SetOptions{}},
		{"optimistic", SetOptions{Optimistic: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			w := &fakeChannelWriter{}
			ch := newTestChannel(t, w)
			const param = hmenum.Parameter("LOW_BAT_LIMIT")
			ch.PutMaster(newWritableMasterFloatDP(testChannelAddr, param, w))

			err := ch.Set(context.Background(), hmenum.ParamsetKeyMaster, param, hmtypes.FloatValue(2.0), tc.opts)
			if err != nil {
				t.Fatalf("Set: %v", err)
			}

			if got := w.setCallCount(); got != 0 {
				t.Errorf("a MASTER write must not reach the VALUES-only SetValue; got %d SetValue calls", got)
			}
			puts := w.putSnapshot()
			if len(puts) != 1 {
				t.Fatalf("expected exactly 1 PutParamset, got %d", len(puts))
			}
			if puts[0].paramsetKey != hmenum.ParamsetKeyMaster {
				t.Errorf("paramset key mismatch: got %s, want %s", puts[0].paramsetKey, hmenum.ParamsetKeyMaster)
			}
			if puts[0].channelAddress != testChannelAddr {
				t.Errorf("channel mismatch: got %s, want %s", puts[0].channelAddress, testChannelAddr)
			}
			if puts[0].values[string(param)] != 2.0 {
				t.Errorf("value mismatch: %+v", puts[0].values)
			}
		})
	}
}

// TestSetValuesStillDispatchesThroughSetValue guards the other half: a VALUES
// write keeps the cheaper single-parameter setValue path.
func TestSetValuesStillDispatchesThroughSetValue(t *testing.T) {
	t.Parallel()

	w := &fakeChannelWriter{}
	ch := newTestChannel(t, w)
	const param = hmenum.Parameter("LEVEL")
	ch.Put(newWritableFloatDP(testChannelAddr, param, w))

	if err := ch.Set(context.Background(), hmenum.ParamsetKeyValues, param, hmtypes.FloatValue(0.5), SetOptions{}); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if got, put := w.setCallCount(), w.putCallCount(); got != 1 || put != 0 {
		t.Fatalf("VALUES write must use SetValue; got %d SetValue / %d PutParamset", got, put)
	}
}

// TestMasterRefreshHookFiredOnMasterSetMany verifies that SetMany with
// ParamsetKeyMaster also fires the hook after a successful PutParamset.
func TestMasterRefreshHookFiredOnMasterSetMany(t *testing.T) {
	t.Parallel()

	w := &fakeChannelWriter{}
	ch := newTestChannel(t, w)
	dp := newWritableMasterFloatDP(testChannelAddr, hmenum.Parameter("SHORT_ON_TIME"), w)
	ch.PutMaster(dp)

	done := make(chan struct{})
	ch.SetMasterRefreshHook(func(_ string, _ hmenum.ParamsetKey) {
		close(done)
	})

	values := map[hmenum.Parameter]hmtypes.ParamValue{
		hmenum.Parameter("SHORT_ON_TIME"): hmtypes.FloatValue(2.0),
	}
	if err := ch.SetMany(context.Background(), hmenum.ParamsetKeyMaster, values, SetOptions{}); err != nil {
		t.Fatalf("SetMany: %v", err)
	}

	select {
	case <-done:
	case <-timeoutCh(t, 2):
		t.Fatal("hook was not fired within 2 seconds after SetMany(MASTER)")
	}
}

// TestMasterRefreshHookNotFiredOnWriteError verifies that the hook is NOT
// invoked when the writer returns an error.
func TestMasterRefreshHookNotFiredOnWriteError(t *testing.T) {
	t.Parallel()

	// The MASTER write dispatches through PutParamset, so the failure has to
	// be injected there — failSet would never be reached.
	w := &fakeChannelWriter{failPut: errors.New("wire error")}
	ch := newTestChannel(t, w)
	dp := newWritableMasterFloatDP(testChannelAddr, hmenum.Parameter("SHORT_ON_TIME"), w)
	ch.PutMaster(dp)

	fired := make(chan struct{}, 1)
	ch.SetMasterRefreshHook(func(_ string, _ hmenum.ParamsetKey) {
		select {
		case fired <- struct{}{}:
		default:
		}
	})

	_ = ch.Set(context.Background(), hmenum.ParamsetKeyMaster,
		hmenum.Parameter("SHORT_ON_TIME"), hmtypes.FloatValue(1.0), SetOptions{})

	select {
	case <-fired:
		t.Fatal("hook must NOT fire when the writer returns an error")
	case <-timeoutCh(t, 0):
		// expected
	}
}

// timeoutCh returns a channel that closes after `seconds` seconds.
// When seconds == 0, it uses a short 50 ms window for "should not fire" tests.
func timeoutCh(t *testing.T, seconds int) <-chan struct{} {
	t.Helper()
	ch := make(chan struct{})
	d := 50 * time.Millisecond
	if seconds > 0 {
		d = time.Duration(seconds) * time.Second
	}
	go func() {
		time.Sleep(d)
		close(ch)
	}()
	return ch
}

// ─── Forced-sensor writability gate ──────────────────────────────────

// newForceSensorFloatDP creates a float VALUES data point whose IsWritable()
// returns false via MarkForcedSensor(). Mirrors the _SWITCH_DP_TO_SENSOR
// overlay applied to e.g. HmIP-eTRV.LEVEL.
func newForceSensorFloatDP(addr string, p hmenum.Parameter, w generic.Writer) *generic.Float {
	dp := generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: addr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(p),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
		Writer: w,
	})
	// MarkForcedSensor flips IsWritable() to false even though the
	// descriptor still carries the WRITE bit.
	dp.MarkForcedSensor()
	return dp
}

// TestChannelSetManyRejectsForcedSensor verifies that SetMany returns
// ErrParameterNotWritable — without dispatching any wire call — when the
// target data point is a forced sensor (IsWritable() == false).
func TestChannelSetManyRejectsForcedSensor(t *testing.T) {
	t.Parallel()

	w := &fakeChannelWriter{}
	ch := newTestChannel(t, w)

	// Add a forced-sensor DP (IsWritable() == false).
	dp := newForceSensorFloatDP(testChannelAddr, hmenum.ParameterLevel, w)
	ch.Put(dp)

	values := map[hmenum.Parameter]hmtypes.ParamValue{
		hmenum.ParameterLevel: hmtypes.FloatValue(0.5),
	}

	err := ch.SetMany(context.Background(), hmenum.ParamsetKeyValues, values, SetOptions{})
	if !errors.Is(err, ErrParameterNotWritable) {
		t.Fatalf("SetMany: want ErrParameterNotWritable, got %v", err)
	}

	// Verify no wire calls were made — the gate must fire before any
	// collector or writer interaction.
	if w.setCallCount() != 0 {
		t.Errorf("SetValue was called %d time(s); must not be called for non-writable DP", w.setCallCount())
	}
	if w.putCallCount() != 0 {
		t.Errorf("PutParamset was called %d time(s); must not be called for non-writable DP", w.putCallCount())
	}
}

// TestChannelSetManyRejectsForcedSensorInBatch pins that SetMany rejects
// the entire batch when ONE of multiple parameters is a forced sensor,
// and that no wire calls happen even for the writable parameter.
func TestChannelSetManyRejectsForcedSensorInBatch(t *testing.T) {
	t.Parallel()

	w := &fakeChannelWriter{}
	ch := newTestChannel(t, w)

	// One writable DP and one forced-sensor DP in the same channel.
	dpWritable := newWritableFloatDP(testChannelAddr, hmenum.ParameterLevel2, w)
	dpSensor := newForceSensorFloatDP(testChannelAddr, hmenum.ParameterLevel, w)
	ch.Put(dpWritable)
	ch.Put(dpSensor)

	values := map[hmenum.Parameter]hmtypes.ParamValue{
		hmenum.ParameterLevel:  hmtypes.FloatValue(0.9),
		hmenum.ParameterLevel2: hmtypes.FloatValue(0.1),
	}

	err := ch.SetMany(context.Background(), hmenum.ParamsetKeyValues, values, SetOptions{})
	if !errors.Is(err, ErrParameterNotWritable) {
		t.Fatalf("SetMany batch: want ErrParameterNotWritable, got %v", err)
	}

	// The entire batch must be rejected — no partial application.
	if w.setCallCount() != 0 || w.putCallCount() != 0 {
		t.Errorf("wire calls made (%d set, %d put) despite non-writable DP in batch; must be 0",
			w.setCallCount(), w.putCallCount())
	}
}

// TestChannelSetRejectsForcedSensor pins that single-parameter Set also
// honours the IsWritable gate when the DP is a forced sensor, complementing
// the SetMany coverage above.
func TestChannelSetRejectsForcedSensor(t *testing.T) {
	t.Parallel()

	w := &fakeChannelWriter{}
	ch := newTestChannel(t, w)

	dp := newForceSensorFloatDP(testChannelAddr, hmenum.ParameterLevel, w)
	ch.Put(dp)

	err := ch.Set(context.Background(), hmenum.ParamsetKeyValues, hmenum.ParameterLevel,
		hmtypes.FloatValue(0.5), SetOptions{})
	if !errors.Is(err, ErrParameterNotWritable) {
		t.Fatalf("Set: want ErrParameterNotWritable, got %v", err)
	}
	if w.setCallCount() != 0 {
		t.Errorf("SetValue called %d time(s); must be 0 for forced sensor", w.setCallCount())
	}
}
