// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package generic

import (
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestOnRefreshed_FiresOnEveryObservationEvenWhenUnchanged pins the
// contract OnRefreshed exists for: it follows RefreshedAt, which moves on
// every CCU-confirmed observation, so a repeated identical reading fires it
// again while OnConfirmedUpdate stays silent.
func TestOnRefreshed_FiresOnEveryObservationEvenWhenUnchanged(t *testing.T) {
	t.Parallel()
	dp := NewDataPoint[float64](baseCfg(hmenum.ParameterLevel, hmenum.ParameterTypeFloat, hmenum.OperationsRead|hmenum.OperationsEvent))

	var refreshed []time.Time
	unsub := dp.OnRefreshed(func(at time.Time) { refreshed = append(refreshed, at) })
	confirmedFires := 0
	dp.OnConfirmedUpdate(func(_, _ float64) { confirmedFires++ })

	t1 := time.Date(2030, 1, 1, 12, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Minute)
	dp.OnEventAt(0.5, t1)
	dp.OnEventAt(0.5, t2) // same value: a refresh, not a change

	if len(refreshed) != 2 || !refreshed[0].Equal(t1) || !refreshed[1].Equal(t2) {
		t.Fatalf("OnRefreshed fired with %v, want [%v %v]", refreshed, t1, t2)
	}
	if confirmedFires != 1 {
		t.Fatalf("OnConfirmedUpdate fired %d times, want 1 (the unchanged echo is a refresh, not a change)", confirmedFires)
	}
	if got := dp.RefreshedAt(); !got.Equal(t2) {
		t.Fatalf("RefreshedAt = %v, want %v", got, t2)
	}

	unsub()
	unsub() // idempotent
	dp.OnEventAt(0.7, t2.Add(time.Minute))
	if len(refreshed) != 2 {
		t.Fatalf("OnRefreshed fired after unsubscribe: %v", refreshed)
	}
}

// TestOnRefreshed_SilentOnTriggerUpdate pins the other half of the
// contract: TriggerUpdate re-fires the update callbacks without a new wire
// value, so it must not look like a refresh.
func TestOnRefreshed_SilentOnTriggerUpdate(t *testing.T) {
	t.Parallel()
	dp := NewDataPoint[bool](baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead|hmenum.OperationsEvent))
	dp.OnEvent(true)

	fires := 0
	dp.OnRefreshed(func(time.Time) { fires++ })
	updates := 0
	dp.OnUpdate(func(_, _ bool) { updates++ })

	dp.TriggerUpdate()
	if updates != 1 {
		t.Fatalf("OnUpdate fired %d times on TriggerUpdate, want 1", updates)
	}
	if fires != 0 {
		t.Fatalf("OnRefreshed fired %d times on TriggerUpdate, want 0", fires)
	}
}

// TestOnRefreshed_NilHandlerIsNoop mirrors the other registration hooks.
func TestOnRefreshed_NilHandlerIsNoop(t *testing.T) {
	t.Parallel()
	dp := NewDataPoint[bool](baseCfg(hmenum.ParameterState, hmenum.ParameterTypeBool, hmenum.OperationsRead|hmenum.OperationsEvent))
	unsub := dp.OnRefreshed(nil)
	unsub()
	dp.OnEvent(true) // must not panic on a nil entry
}
