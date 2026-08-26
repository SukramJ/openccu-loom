// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package generic

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestMarkStaleSkipsUnsubscribedConfirmedCallback guards against a nil
// function call in the source-transition fan-out. OnConfirmedUpdate's
// unsubscribe nils out its slot in confirmedUpdateCallbacks; MarkStale must
// skip those holes rather than invoking a nil func value. The Matter bridge
// registers confirmed-update notifiers and tears them down + re-wires them on
// every recovery, so a connection-lost MarkStale that fires after an unsub
// would otherwise panic with "invalid memory address or nil pointer
// dereference" — the field crash observed on reconnect.
func TestMarkStaleSkipsUnsubscribedConfirmedCallback(t *testing.T) {
	t.Parallel()
	dp := NewDataPoint[bool](baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead|hmenum.OperationsEvent))
	dp.OnEvent(true) // observed → source live

	// Register then immediately unsubscribe — leaves a nil slot in the slice.
	unsub := dp.OnConfirmedUpdate(func(_, _ bool) {})
	unsub()

	// Must transition live→stale without invoking the nil callback.
	old, changed := dp.MarkStale()
	if !changed {
		t.Fatalf("MarkStale: changed = false, want true (was %v)", old)
	}
	if dp.Source() != hmenum.ValueSourceStale {
		t.Fatalf("Source = %v, want stale", dp.Source())
	}
}

// TestMarkLiveSkipsUnsubscribedConfirmedCallback is the recovery.completed
// counterpart of [TestMarkStaleSkipsUnsubscribedConfirmedCallback]: MarkLive
// fans out over the same confirmedUpdateCallbacks slice and must likewise skip
// nilled-out slots left by a prior OnConfirmedUpdate unsubscribe.
func TestMarkLiveSkipsUnsubscribedConfirmedCallback(t *testing.T) {
	t.Parallel()
	dp := NewDataPoint[bool](baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead|hmenum.OperationsEvent))
	dp.OnEvent(true) // observed → source live

	unsub := dp.OnConfirmedUpdate(func(_, _ bool) {})
	unsub()

	// Drive the DP to stale first so MarkLive performs a real transition.
	if _, changed := dp.MarkStale(); !changed {
		t.Fatal("precondition: MarkStale must transition")
	}

	old, changed := dp.MarkLive()
	if !changed {
		t.Fatalf("MarkLive: changed = false, want true (was %v)", old)
	}
	if dp.Source() != hmenum.ValueSourceLive {
		t.Fatalf("Source = %v, want live", dp.Source())
	}
}

// TestMarkStaleStillFiresLiveConfirmedCallbacks ensures the nil-skip does not
// silence a still-registered confirmed-update callback when another was
// unsubscribed alongside it.
func TestMarkStaleStillFiresLiveConfirmedCallbacks(t *testing.T) {
	t.Parallel()
	dp := NewDataPoint[bool](baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead|hmenum.OperationsEvent))
	dp.OnEvent(true)

	fired := 0
	unsub := dp.OnConfirmedUpdate(func(_, _ bool) {})
	dp.OnConfirmedUpdate(func(_, _ bool) { fired++ })
	unsub() // nil out the first slot only

	if _, changed := dp.MarkStale(); !changed {
		t.Fatal("MarkStale must transition")
	}
	if fired != 1 {
		t.Fatalf("live confirmed callback fired %d times, want 1", fired)
	}
}
