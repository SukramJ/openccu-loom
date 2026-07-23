// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

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
	if w.setCallCount() != 1 {
		t.Fatalf("expected 1 SetValue for MASTER write on locked channel, got %d", w.setCallCount())
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
	if w.setCallCount() != 1 {
		t.Fatalf("expected 1 SetValue for MASTER SetMany on locked channel, got %d", w.setCallCount())
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
