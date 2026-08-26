// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package device

import (
	"context"
	"errors"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ---------- Operator lock (G12) -----------------------------------------

func TestChannelSetRejectsWhenLocked(t *testing.T) {
	t.Parallel()

	w := &fakeChannelWriter{}
	ch := newTestChannel(t, w)
	dp := newWritableFloatDP(testChannelAddr, hmenum.ParameterLevel, w)
	ch.Put(dp)

	ch.SetOperatorFlags(false, true)

	err := ch.Set(context.Background(), hmenum.ParamsetKeyValues, hmenum.ParameterLevel,
		hmtypes.FloatValue(0.5), SetOptions{})
	if !errors.Is(err, ErrChannelOperationLocked) {
		t.Fatalf("Set on locked channel: want ErrChannelOperationLocked, got %v", err)
	}
	if w.setCallCount() != 0 {
		t.Errorf("SetValue called %d time(s); must be 0 when channel is locked", w.setCallCount())
	}
}

func TestChannelSetManyRejectsWhenLocked(t *testing.T) {
	t.Parallel()

	w := &fakeChannelWriter{}
	ch := newTestChannel(t, w)
	dpLevel := newWritableFloatDP(testChannelAddr, hmenum.ParameterLevel, w)
	dpLevel2 := newWritableFloatDP(testChannelAddr, hmenum.ParameterLevel2, w)
	ch.Put(dpLevel)
	ch.Put(dpLevel2)

	ch.SetOperatorFlags(false, true)

	values := map[hmenum.Parameter]hmtypes.ParamValue{
		hmenum.ParameterLevel:  hmtypes.FloatValue(0.5),
		hmenum.ParameterLevel2: hmtypes.FloatValue(0.2),
	}
	err := ch.SetMany(context.Background(), hmenum.ParamsetKeyValues, values, SetOptions{})
	if !errors.Is(err, ErrChannelOperationLocked) {
		t.Fatalf("SetMany on locked channel: want ErrChannelOperationLocked, got %v", err)
	}
	if w.setCallCount() != 0 || w.putCallCount() != 0 {
		t.Errorf("wire calls made (%d set, %d put) despite locked channel; must be 0",
			w.setCallCount(), w.putCallCount())
	}
}

func TestChannelSetMasterNotRejectedWhenLocked(t *testing.T) {
	t.Parallel()

	w := &fakeChannelWriter{}
	ch := newTestChannel(t, w)
	dp := newWritableMasterFloatDP(testChannelAddr, hmenum.ParameterLevel, w)
	ch.PutMaster(dp)

	ch.SetOperatorFlags(false, true)

	err := ch.Set(context.Background(), hmenum.ParamsetKeyMaster, hmenum.ParameterLevel,
		hmtypes.FloatValue(5.0), SetOptions{})
	if err != nil {
		t.Fatalf("Set MASTER on locked channel: want no error (lock is VALUES-scoped), got %v", err)
	}
	// MASTER dispatches through PutParamset — setValue is VALUES-only.
	if w.putCallCount() != 1 || w.setCallCount() != 0 {
		t.Fatalf("expected 1 PutParamset for MASTER write on locked channel, got %d PutParamset / %d SetValue",
			w.putCallCount(), w.setCallCount())
	}
}

func TestChannelSetManyMasterNotRejectedWhenLocked(t *testing.T) {
	t.Parallel()

	w := &fakeChannelWriter{}
	ch := newTestChannel(t, w)
	dp := newWritableMasterFloatDP(testChannelAddr, hmenum.ParameterLevel, w)
	ch.PutMaster(dp)

	ch.SetOperatorFlags(false, true)

	values := map[hmenum.Parameter]hmtypes.ParamValue{
		hmenum.ParameterLevel: hmtypes.FloatValue(3.0),
	}
	err := ch.SetMany(context.Background(), hmenum.ParamsetKeyMaster, values, SetOptions{})
	if err != nil {
		t.Fatalf("SetMany MASTER on locked channel: want no error, got %v", err)
	}
	// MASTER dispatches through PutParamset — setValue is VALUES-only.
	if w.putCallCount() != 1 || w.setCallCount() != 0 {
		t.Fatalf("expected 1 PutParamset for MASTER SetMany on locked channel, got %d PutParamset / %d SetValue",
			w.putCallCount(), w.setCallCount())
	}
}

func TestChannelSetProceedsAfterUnlock(t *testing.T) {
	t.Parallel()

	w := &fakeChannelWriter{}
	ch := newTestChannel(t, w)
	dp := newWritableFloatDP(testChannelAddr, hmenum.ParameterLevel, w)
	ch.Put(dp)

	ch.SetOperatorFlags(false, true)
	ch.SetOperatorFlags(false, false) // clear the lock

	err := ch.Set(context.Background(), hmenum.ParamsetKeyValues, hmenum.ParameterLevel,
		hmtypes.FloatValue(0.5), SetOptions{})
	if err != nil {
		t.Fatalf("Set after unlock: %v", err)
	}
	if w.setCallCount() != 1 {
		t.Fatalf("expected 1 SetValue after unlock, got %d", w.setCallCount())
	}
}

func TestChannelIsHiddenIsLockedReflectOperatorFlags(t *testing.T) {
	t.Parallel()

	ch := newTestChannel(t, nil)

	if ch.IsHidden() || ch.IsLocked() {
		t.Fatal("new channel must not be hidden or locked")
	}

	ch.SetOperatorFlags(true, false)
	if !ch.IsHidden() {
		t.Error("IsHidden must be true after SetOperatorFlags(true, false)")
	}
	if ch.IsLocked() {
		t.Error("IsLocked must be false after SetOperatorFlags(true, false)")
	}

	ch.SetOperatorFlags(false, true)
	if ch.IsHidden() {
		t.Error("IsHidden must be false after SetOperatorFlags(false, true)")
	}
	if !ch.IsLocked() {
		t.Error("IsLocked must be true after SetOperatorFlags(false, true)")
	}

	ch.SetOperatorFlags(true, true)
	if !ch.IsHidden() || !ch.IsLocked() {
		t.Error("both flags must be true after SetOperatorFlags(true, true)")
	}
}

// ---------- Operator lock on the captured write path --------------------

// TestChannelWriterRejectsValuesWriteWhenLocked pins the enforcement that
// matters for every custom data point: they capture Channel.Writer() once
// and afterwards write without ever touching Channel.Set.
func TestChannelWriterRejectsValuesWriteWhenLocked(t *testing.T) {
	t.Parallel()

	w := &fakeChannelWriter{}
	ch := newTestChannel(t, w)
	captured := ch.Writer()

	ch.SetOperatorFlags(false, true)

	err := captured.SetValue(context.Background(), testChannelAddr,
		hmenum.ParameterState, true, hmenum.CommandPriorityHigh)
	if !errors.Is(err, ErrChannelOperationLocked) {
		t.Fatalf("SetValue through captured writer: want ErrChannelOperationLocked, got %v", err)
	}
	if w.setCallCount() != 0 {
		t.Errorf("SetValue reached the wire %d time(s) despite the lock", w.setCallCount())
	}
}

// TestCustomDataPointWriteRejectedWhenLocked exercises the concrete shape
// the control surfaces use: a data point built on the channel's writer and
// commanded through its own model method.
func TestCustomDataPointWriteRejectedWhenLocked(t *testing.T) {
	t.Parallel()

	w := &fakeChannelWriter{}
	ch := newTestChannel(t, w)
	sw := newWritableBoolDP(testChannelAddr, hmenum.ParameterState, ch.Writer())
	ch.Put(sw)

	ch.SetOperatorFlags(false, true)

	if err := sw.Set(context.Background(), true, hmenum.CommandPriorityHigh); !errors.Is(err, ErrChannelOperationLocked) {
		t.Fatalf("data point write on locked channel: want ErrChannelOperationLocked, got %v", err)
	}
	if w.setCallCount() != 0 || w.putCallCount() != 0 {
		t.Errorf("wire calls made (%d set, %d put) despite the lock", w.setCallCount(), w.putCallCount())
	}

	ch.SetOperatorFlags(false, false)
	if err := sw.Set(context.Background(), true, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("data point write after unlock: %v", err)
	}
	if w.setCallCount() != 1 {
		t.Errorf("expected 1 SetValue after unlock, got %d", w.setCallCount())
	}
}

func TestChannelWriterRejectsValuesParamsetWhenLocked(t *testing.T) {
	t.Parallel()

	w := &fakeChannelWriter{}
	ch := newTestChannel(t, w)
	captured := ch.Writer()

	ch.SetOperatorFlags(false, true)

	err := captured.PutParamset(context.Background(), testChannelAddr, hmenum.ParamsetKeyValues,
		map[string]any{string(hmenum.ParameterState): true}, hmenum.CommandPriorityHigh)
	if !errors.Is(err, ErrChannelOperationLocked) {
		t.Fatalf("VALUES PutParamset on locked channel: want ErrChannelOperationLocked, got %v", err)
	}
	if w.putCallCount() != 0 {
		t.Errorf("PutParamset reached the wire %d time(s) despite the lock", w.putCallCount())
	}
}

func TestChannelWriterAllowsMasterParamsetWhenLocked(t *testing.T) {
	t.Parallel()

	w := &fakeChannelWriter{}
	ch := newTestChannel(t, w)
	captured := ch.Writer()

	ch.SetOperatorFlags(false, true)

	err := captured.PutParamset(context.Background(), testChannelAddr, hmenum.ParamsetKeyMaster,
		map[string]any{"SHORT_ON_TIME": 5.0}, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("MASTER PutParamset on locked channel: want no error (lock is VALUES-scoped), got %v", err)
	}
	if w.putCallCount() != 1 {
		t.Errorf("expected 1 PutParamset for the MASTER write, got %d", w.putCallCount())
	}
}

// TestChannelWriterGatesOnTheAddressedChannel covers composed data points
// that hold one channel's writer but address a sibling channel.
func TestChannelWriterGatesOnTheAddressedChannel(t *testing.T) {
	t.Parallel()

	w := &fakeChannelWriter{}
	ch := newTestChannel(t, w)
	sibling := ch.Device().AddChannel("TEST:2", 2, "TEST_TYPE", hmenum.ParamsetKeyValues)
	captured := ch.Writer()

	sibling.SetOperatorFlags(false, true)

	err := captured.SetValue(context.Background(), "TEST:2",
		hmenum.ParameterState, true, hmenum.CommandPriorityHigh)
	if !errors.Is(err, ErrChannelOperationLocked) {
		t.Fatalf("write to locked sibling channel: want ErrChannelOperationLocked, got %v", err)
	}

	// The origin channel is unlocked, so its own address stays writable.
	if err := captured.SetValue(context.Background(), testChannelAddr,
		hmenum.ParameterState, true, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("write to unlocked origin channel: %v", err)
	}
	if w.setCallCount() != 1 {
		t.Errorf("expected exactly 1 wire write, got %d", w.setCallCount())
	}
}

func TestChannelWriterIsNilWithoutInstalledWriter(t *testing.T) {
	t.Parallel()

	ch := newTestChannel(t, nil)
	if ch.Writer() != nil {
		t.Fatal("Writer() must stay nil when no writer is installed")
	}
}
