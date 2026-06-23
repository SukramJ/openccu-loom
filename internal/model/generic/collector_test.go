// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package generic

import (
	"context"
	"errors"
	"maps"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// --- helpers ---------------------------------------------------------

type recordingBackend struct {
	mu          sync.Mutex
	setCalls    []setCall
	putCalls    []putCall
	failOnSet   error
	failOnPut   error
	delaySingle time.Duration
}

type setCall struct {
	channel  string
	param    string
	value    any
	priority hmenum.CommandPriority
}

type putCall struct {
	channel  string
	key      hmenum.ParamsetKey
	values   map[string]any
	priority hmenum.CommandPriority
}

func (b *recordingBackend) SetValue(_ context.Context, ch string, p hmenum.Parameter, v any, prio hmenum.CommandPriority) error {
	if b.delaySingle > 0 {
		time.Sleep(b.delaySingle)
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.setCalls = append(b.setCalls, setCall{ch, string(p), v, prio})
	return b.failOnSet
}

func (b *recordingBackend) PutParamset(_ context.Context, ch string, key hmenum.ParamsetKey, vals map[string]any, prio hmenum.CommandPriority) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	clone := make(map[string]any, len(vals))
	maps.Copy(clone, vals)
	b.putCalls = append(b.putCalls, putCall{ch, key, clone, prio})
	return b.failOnPut
}

func (b *recordingBackend) snapshot() ([]setCall, []putCall) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]setCall(nil), b.setCalls...), append([]putCall(nil), b.putCalls...)
}

func dpForCollector(t *testing.T, channel, param string) *DataPoint[float64] {
	t.Helper()
	return NewDataPoint[float64](Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "iface",
			ChannelAddress: channel,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      param,
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	})
}

// --- Add / shape tests ----------------------------------------------

func TestCollectorSingleParameterDispatchesSetValue(t *testing.T) {
	b := &recordingBackend{}
	c := NewCollector(b)
	dp := dpForCollector(t, "0001:1", "LEVEL")

	if err := c.Add(dp, 0.5, 0); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := c.Send(context.Background()); err != nil {
		t.Fatalf("Send: %v", err)
	}

	sets, puts := b.snapshot()
	if len(sets) != 1 || len(puts) != 0 {
		t.Fatalf("expected 1 SetValue + 0 PutParamset, got %d set / %d put", len(sets), len(puts))
	}
	if sets[0].channel != "0001:1" || sets[0].param != "LEVEL" || sets[0].value != 0.5 {
		t.Fatalf("call mismatch: %+v", sets[0])
	}
}

func TestCollectorMultiParameterDispatchesPutParamset(t *testing.T) {
	b := &recordingBackend{}
	c := NewCollector(b)
	level := dpForCollector(t, "0001:1", "LEVEL")
	level2 := dpForCollector(t, "0001:1", "LEVEL_2")

	_ = c.Add(level, 0.8, 0)
	_ = c.Add(level2, 0.2, 0)

	if err := c.Send(context.Background()); err != nil {
		t.Fatalf("Send: %v", err)
	}

	sets, puts := b.snapshot()
	if len(sets) != 0 || len(puts) != 1 {
		t.Fatalf("expected 0 SetValue + 1 PutParamset, got %d / %d", len(sets), len(puts))
	}
	if puts[0].channel != "0001:1" {
		t.Fatalf("channel mismatch: %s", puts[0].channel)
	}
	if puts[0].values["LEVEL"] != 0.8 || puts[0].values["LEVEL_2"] != 0.2 {
		t.Fatalf("values mismatch: %+v", puts[0].values)
	}
}

func TestCollectorOrderingControlsDispatchSequence(t *testing.T) {
	b := &recordingBackend{}
	c := NewCollector(b)
	a := dpForCollector(t, "0001:1", "A")
	bDP := dpForCollector(t, "0001:1", "B")
	cDP := dpForCollector(t, "0001:1", "C")

	// Add in reverse to verify the collector sorts by `order`.
	_ = c.Add(cDP, 3.0, 30)
	_ = c.Add(a, 1.0, 10)
	_ = c.Add(bDP, 2.0, 20)

	if err := c.Send(context.Background()); err != nil {
		t.Fatalf("Send: %v", err)
	}

	sets, _ := b.snapshot()
	if len(sets) != 3 {
		t.Fatalf("expected 3 SetValue calls (each in its own order group), got %d", len(sets))
	}
	if sets[0].param != "A" || sets[1].param != "B" || sets[2].param != "C" {
		t.Fatalf("dispatch order: %+v", sets)
	}
}

func TestCollectorSameOrderCollapsesToPutParamset(t *testing.T) {
	b := &recordingBackend{}
	c := NewCollector(b)
	a := dpForCollector(t, "0001:1", "A")
	bDP := dpForCollector(t, "0001:1", "B")

	_ = c.Add(a, 1.0, 0)
	_ = c.Add(bDP, 2.0, 0)

	_ = c.Send(context.Background())
	sets, puts := b.snapshot()
	if len(sets) != 0 || len(puts) != 1 {
		t.Fatalf("expected one PutParamset, got sets=%d puts=%d", len(sets), len(puts))
	}
}

func TestCollectorReplaceSameTargetWithLastValue(t *testing.T) {
	b := &recordingBackend{}
	c := NewCollector(b)
	dp := dpForCollector(t, "0001:1", "LEVEL")

	_ = c.Add(dp, 0.1, 0)
	_ = c.Add(dp, 0.5, 0)
	_ = c.Add(dp, 0.9, 0)

	if got := c.Len(); got != 1 {
		t.Fatalf("collector should hold one entry per (channel,paramset,param,order); got %d", got)
	}
	_ = c.Send(context.Background())

	sets, _ := b.snapshot()
	if len(sets) != 1 || sets[0].value != 0.9 {
		t.Fatalf("last value wins: got %+v", sets)
	}
}

func TestCollectorAcrossChannelsSplits(t *testing.T) {
	b := &recordingBackend{}
	c := NewCollector(b)
	a := dpForCollector(t, "0001:1", "STATE")
	bDP := dpForCollector(t, "0002:1", "STATE")

	_ = c.Add(a, 1.0, 0)
	_ = c.Add(bDP, 2.0, 0)

	if err := c.Send(context.Background()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	sets, puts := b.snapshot()
	if len(sets) != 2 || len(puts) != 0 {
		t.Fatalf("two channels → two single-param SetValues; got %d / %d", len(sets), len(puts))
	}
	// Sorted by channel address.
	if sets[0].channel != "0001:1" || sets[1].channel != "0002:1" {
		t.Fatalf("channel ordering: %+v", sets)
	}
}

// --- options --------------------------------------------------------

func TestCollectorWithPriority(t *testing.T) {
	b := &recordingBackend{}
	c := NewCollector(b, WithPriority(hmenum.CommandPriorityLow))
	dp := dpForCollector(t, "0001:1", "LEVEL")
	_ = c.Add(dp, 0.5, 0)
	_ = c.Send(context.Background())

	sets, _ := b.snapshot()
	if len(sets) != 1 || sets[0].priority != hmenum.CommandPriorityLow {
		t.Fatalf("priority not propagated: %+v", sets)
	}
}

func TestCollectorDefaultPriorityIsHigh(t *testing.T) {
	b := &recordingBackend{}
	c := NewCollector(b)
	dp := dpForCollector(t, "0001:1", "LEVEL")
	_ = c.Add(dp, 0.5, 0)
	_ = c.Send(context.Background())

	sets, _ := b.snapshot()
	if sets[0].priority != hmenum.CommandPriorityHigh {
		t.Fatalf("default priority = %v, want High", sets[0].priority)
	}
}

// --- consumed / cancel ---------------------------------------------

func TestCollectorSendIsIdempotentSingleShot(t *testing.T) {
	b := &recordingBackend{}
	c := NewCollector(b)
	dp := dpForCollector(t, "0001:1", "LEVEL")
	_ = c.Add(dp, 0.5, 0)
	if err := c.Send(context.Background()); err != nil {
		t.Fatalf("first Send: %v", err)
	}
	if err := c.Send(context.Background()); !errors.Is(err, ErrCollectorConsumed) {
		t.Fatalf("second Send must return ErrCollectorConsumed, got %v", err)
	}
}

func TestCollectorAddAfterSendFails(t *testing.T) {
	b := &recordingBackend{}
	c := NewCollector(b)
	dp := dpForCollector(t, "0001:1", "LEVEL")
	_ = c.Add(dp, 0.5, 0)
	_ = c.Send(context.Background())

	if err := c.Add(dp, 0.7, 0); !errors.Is(err, ErrCollectorConsumed) {
		t.Fatalf("Add after Send must error, got %v", err)
	}
}

func TestCollectorCancelDiscardsPending(t *testing.T) {
	b := &recordingBackend{}
	c := NewCollector(b)
	dp := dpForCollector(t, "0001:1", "LEVEL")
	_ = c.Add(dp, 0.5, 0)
	c.Cancel()

	if err := c.Send(context.Background()); !errors.Is(err, ErrCollectorConsumed) {
		t.Fatalf("Send after Cancel: %v", err)
	}
	sets, _ := b.snapshot()
	if len(sets) != 0 {
		t.Fatal("Cancel must drop pending items")
	}
}

func TestCollectorEmptySendIsNoOp(t *testing.T) {
	b := &recordingBackend{}
	c := NewCollector(b)
	if err := c.Send(context.Background()); err != nil {
		t.Fatalf("empty Send: %v", err)
	}
	sets, puts := b.snapshot()
	if len(sets) != 0 || len(puts) != 0 {
		t.Fatal("empty Send must not dispatch anything")
	}
}

func TestCollectorNoBackendErrors(t *testing.T) {
	c := NewCollector(nil)
	dp := dpForCollector(t, "0001:1", "LEVEL")
	_ = c.Add(dp, 0.5, 0)
	if err := c.Send(context.Background()); !errors.Is(err, ErrNoBackend) {
		t.Fatalf("expected ErrNoBackend, got %v", err)
	}
}

// --- optimistic integration ----------------------------------------

func TestCollectorAppliesOptimisticBeforeSend(t *testing.T) {
	b := &recordingBackend{delaySingle: 30 * time.Millisecond}
	c := NewCollector(b)
	dp := dpForCollector(t, "0001:1", "LEVEL")
	dp.OnEvent(0.0)

	// Run Send in a goroutine so we can sample the optimistic value
	// while the wire call is in flight.
	done := make(chan struct{})
	_ = c.Add(dp, 0.7, 0)
	go func() {
		_ = c.Send(context.Background())
		close(done)
	}()

	deadline := time.After(100 * time.Millisecond)
	for !dp.IsOptimistic() {
		select {
		case <-deadline:
			t.Fatal("optimistic state never set during Send")
		default:
			time.Sleep(2 * time.Millisecond)
		}
	}

	v, _ := dp.Value()
	if v != 0.7 {
		t.Fatalf("Value during Send = %v, want 0.7 (optimistic)", v)
	}
	<-done
}

func TestCollectorWireErrorRollsBackAllStagedOptimistic(t *testing.T) {
	b := &recordingBackend{failOnPut: errors.New("wire down")}
	c := NewCollector(b)
	a := dpForCollector(t, "0001:1", "A")
	bDP := dpForCollector(t, "0001:1", "B")
	a.OnEvent(0.0)
	bDP.OnEvent(0.0)

	_ = c.Add(a, 0.4, 0)
	_ = c.Add(bDP, 0.6, 0)

	if err := c.Send(context.Background()); err == nil {
		t.Fatal("expected wire error")
	}
	if a.IsOptimistic() || bDP.IsOptimistic() {
		t.Fatal("rollback must clear optimistic state on every staged DP")
	}
	if v, _ := a.Value(); v != 0.0 {
		t.Fatalf("A rolled back to %v, want 0.0", v)
	}
	if v, _ := bDP.Value(); v != 0.0 {
		t.Fatalf("B rolled back to %v, want 0.0", v)
	}
}

func TestCollectorWireErrorOnSetValueRollsBack(t *testing.T) {
	b := &recordingBackend{failOnSet: errors.New("ccu busy")}
	c := NewCollector(b)
	dp := dpForCollector(t, "0001:1", "LEVEL")
	dp.OnEvent(0.0)

	_ = c.Add(dp, 0.5, 0)

	if err := c.Send(context.Background()); err == nil {
		t.Fatal("expected wire error")
	}
	if dp.IsOptimistic() {
		t.Fatal("rollback must clear optimistic state")
	}
}

func TestCollectorBurstSkipDoesNotInflateRollback(t *testing.T) {
	// Two staging calls with the same (dp, value) — burst-skip
	// returns a no-op rollback. We verify no spurious rollback fires
	// when the wire fails for an unrelated DP.
	b := &recordingBackend{failOnPut: errors.New("wire fail")}
	a := dpForCollector(t, "0001:1", "A")
	bDP := dpForCollector(t, "0001:1", "B")
	a.OnEvent(0.5)
	bDP.OnEvent(0.0)

	// Pre-stage A optimistically with the same value via the direct
	// send path. This isn't strictly needed for burst-skip — the
	// collector applies its own optimistic state — but it sets up
	// the situation where ApplyOptimistic on the same value returns
	// a no-op rollback. The wire failure must still roll back B.
	_ = a.sendAndObserve(context.Background(), 0.5, 0.5, hmenum.CommandPriorityHigh)

	c := NewCollector(b)
	_ = c.Add(a, 0.5, 0)
	_ = c.Add(bDP, 0.6, 0)
	if err := c.Send(context.Background()); err == nil {
		t.Fatal("expected wire error")
	}
	if a.IsOptimistic() || bDP.IsOptimistic() {
		t.Fatal("rollback must clear both DPs even when burst-skip was involved")
	}
}

// TestCollectorRollsBackPreStagedOptimisticOnSendError pins the #3238 fix for
// the collector "Path A": a generic setter routes through sendAndObserve WHILE
// a CallParameterCollector is present in ctx. sendAndObserve stages the
// optimistic value (and arms the 30s timeout) and hands the dispatch to the
// collector. When the collector's batched Send fails, the pre-staged optimistic
// value must roll back immediately — not linger until the timeout. Before the
// fix the collector's re-entrant ApplyOptimistic burst-skipped and returned a
// no-op rollback, so rollbackAll() did nothing for this DP.
func TestCollectorRollsBackPreStagedOptimisticOnSendError(t *testing.T) {
	b := &recordingBackend{failOnSet: errors.New("wire down")}
	c := NewCollector(b)
	dp := dpForCollector(t, "0001:1", "LEVEL")
	// A non-nil writer so sendAndObserve proceeds; the actual dispatch is
	// routed through the collector backend, not this writer.
	dp.Writer = &stubWriter{}
	dp.OnEvent(0.0) // last CCU-confirmed value

	ctx := ContextWithCollector(context.Background(), c)
	// Mirrors what a typed setter (Float.Set etc.) does internally.
	if err := dp.sendAndObserve(ctx, 0.7, 0.7, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("sendAndObserve (collector add) unexpected err: %v", err)
	}

	if err := c.Send(ctx); err == nil {
		t.Fatal("expected wire error from collector Send")
	}
	if dp.IsOptimistic() {
		t.Fatal("collector send error must roll back the pre-staged optimistic value immediately (#3238)")
	}
	if v, _ := dp.Value(); v != 0.0 {
		t.Fatalf("value must revert to last confirmed 0.0, got %v", v)
	}
}

// --- concurrency ---------------------------------------------------

func TestCollectorConcurrentAddAndSend(t *testing.T) {
	b := &recordingBackend{}
	c := NewCollector(b)
	dps := make([]*DataPoint[float64], 8)
	for i := range dps {
		dps[i] = dpForCollector(t, "0001:1", string(rune('A'+i)))
	}

	var wg sync.WaitGroup
	for i, dp := range dps {
		wg.Add(1)
		go func(i int, dp *DataPoint[float64]) {
			defer wg.Done()
			_ = c.Add(dp, float64(i)/10, 0)
		}(i, dp)
	}
	wg.Wait()

	if err := c.Send(context.Background()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	_, puts := b.snapshot()
	if len(puts) != 1 {
		t.Fatalf("8 same-channel adds → 1 PutParamset; got %d", len(puts))
	}
	if len(puts[0].values) != 8 {
		t.Fatalf("values count = %d, want 8", len(puts[0].values))
	}
}

func TestCollectorConcurrentSendsAreSerialised(t *testing.T) {
	b := &recordingBackend{}
	c1 := NewCollector(b)
	c2 := NewCollector(b)
	dp := dpForCollector(t, "0001:1", "LEVEL")
	_ = c1.Add(dp, 0.3, 0)
	_ = c2.Add(dp, 0.7, 0)

	var firstSends, secondSends atomic.Int32
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_ = c1.Send(context.Background())
		firstSends.Add(1)
	}()
	go func() {
		defer wg.Done()
		_ = c2.Send(context.Background())
		secondSends.Add(1)
	}()
	wg.Wait()

	sets, _ := b.snapshot()
	if len(sets) != 2 {
		t.Fatalf("expected 2 SetValue calls, got %d", len(sets))
	}
	if firstSends.Load() != 1 || secondSends.Load() != 1 {
		t.Fatal("both sends must complete")
	}
}

// --- stable sort across paramsetKeys ------------------------------

func TestCollectorSortsAcrossParamsetKeys(t *testing.T) {
	b := &recordingBackend{}
	c := NewCollector(b)

	master := NewDataPoint[float64](Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "iface",
			ChannelAddress: "0001:1",
			ParamsetKey:    hmenum.ParamsetKeyMaster,
			Parameter:      "MASTER_PARAM",
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	})
	values := dpForCollector(t, "0001:1", "VAL_PARAM")

	_ = c.Add(values, 1.0, 0)
	_ = c.Add(master, 2.0, 0)

	if err := c.Send(context.Background()); err != nil {
		t.Fatalf("Send: %v", err)
	}
	sets, _ := b.snapshot()
	if len(sets) != 2 {
		t.Fatalf("two paramset keys → two SetValues; got %d", len(sets))
	}
	// MASTER < VALUES alphabetically → MASTER first.
	if sets[0].param != "MASTER_PARAM" {
		t.Fatalf("expected MASTER_PARAM first, got %s", sets[0].param)
	}
}

// --- DataPoint.ApplyOptimistic surface tests -----------------------

func TestApplyOptimisticReturnsNopOnDisabled(t *testing.T) {
	dp := NewDataPoint[bool](Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "iface",
			ChannelAddress: "A:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterState),
		},
		Descriptor:         hmproto.ParameterData{Type: hmenum.ParameterTypeBool, Operations: hmenum.OperationsRead | hmenum.OperationsEvent},
		OptimisticDisabled: true,
	})
	if rb := dp.ApplyOptimistic(true); rb != nil {
		t.Fatal("disabled tracker must return nil rollback")
	}
}

func TestApplyOptimisticHandlesTypeCoercion(t *testing.T) {
	dp := NewDataPoint[float64](Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "iface",
			ChannelAddress: "A:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterLevel),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	})
	rb := dp.ApplyOptimistic(int(50)) // int → float64 coerce
	if rb == nil {
		t.Fatal("coercible value must succeed")
	}
	if !dp.IsOptimistic() {
		t.Fatal("tracker should be active")
	}
	rb()
}

func TestApplyOptimisticReturnsNopOnUnsupportedType(t *testing.T) {
	dp := NewDataPoint[bool](Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "iface",
			ChannelAddress: "A:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterState),
		},
		Descriptor: hmproto.ParameterData{Type: hmenum.ParameterTypeBool, Operations: hmenum.OperationsRead | hmenum.OperationsEvent},
	})
	if rb := dp.ApplyOptimistic(struct{ X int }{42}); rb != nil {
		t.Fatal("uncoercible value must return nil")
	}
}

func TestApplyOptimisticBurstSkipKeepsWorkingRollback(t *testing.T) {
	dp := dpForCollector(t, "A:1", "LEVEL")
	dp.OnEvent(0.0)

	rb1 := dp.ApplyOptimistic(0.5)
	rb2 := dp.ApplyOptimistic(0.5) // burst-skip: same value already staged
	if rb1 == nil || rb2 == nil {
		t.Fatal("both calls must return non-nil rollbacks")
	}
	// Burst-skip must NOT re-Apply: PendingSends stays at 1 so a single CCU
	// echo settles the tracker (no spurious timeout rollback — issue #3049).
	if dp.PendingSends() != 1 {
		t.Fatalf("burst-skip must keep counter at 1, got %d", dp.PendingSends())
	}
	if !dp.IsOptimistic() {
		t.Fatal("tracker must be active before rollback")
	}
	// ...but the burst-skip rollback must still revert the active optimistic
	// state — a failed batched send through a collector relies on this to undo
	// the value (#3238). A no-op here would leave the value lingering until the
	// 30s timeout.
	rb2()
	if dp.IsOptimistic() {
		t.Fatal("burst-skip rollback must revert the active optimistic state (#3238)")
	}
	if v, _ := dp.Value(); v != 0.0 {
		t.Fatalf("value must roll back to previous confirmed 0.0, got %v", v)
	}
	// rollback is idempotent — firing the first closure after a completed
	// rollback is a safe no-op.
	rb1()
}
