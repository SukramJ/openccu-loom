// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package coordinators

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func TestRecoveryMetricsInRecoveryFalseAtRest(t *testing.T) {
	t.Parallel()

	c := NewConnectionRecoveryCoordinator("ccu-A", events.NewBus())
	if c.MetricsInRecovery() {
		t.Fatal("baseline expected false")
	}
	if got := c.MetricsRecoveryStates(); len(got) != 0 {
		t.Errorf("baseline states=%v", got)
	}
}

func TestRecoveryMetricsStatesReflectAttempts(t *testing.T) {
	t.Parallel()

	c := NewConnectionRecoveryCoordinator("ccu-A", events.NewBus())
	pipeline := []Pipeline{
		{Stage: hmenum.RecoveryStageDetecting, Run: func(_ context.Context) error {
			return errors.New("boom")
		}},
	}
	for range 3 {
		_ = c.Run(context.Background(), "hmip-rf", pipeline)
	}

	states := c.MetricsRecoveryStates()
	if len(states) != 1 {
		t.Fatalf("len=%d", len(states))
	}
	st := states["hmip-rf"]
	if st.AttemptCount() != 3 {
		t.Errorf("attempts=%d, want 3", st.AttemptCount())
	}
	if st.ConsecutiveFailures() != 3 {
		t.Errorf("consecutiveFailures=%d, want 3", st.ConsecutiveFailures())
	}
	if !st.CanRetry() {
		t.Error("canRetry=false unexpected — well below default cap (10)")
	}
}

func TestRecoveryMetricsCanRetryRespectsMaxAttempts(t *testing.T) {
	t.Parallel()

	c := NewConnectionRecoveryCoordinatorWithLimit("ccu-A", events.NewBus(), 2)
	pipeline := []Pipeline{
		{Stage: hmenum.RecoveryStageDetecting, Run: func(_ context.Context) error {
			return errors.New("nope")
		}},
	}
	for range 3 {
		_ = c.Run(context.Background(), "hmip-rf", pipeline)
	}

	states := c.MetricsRecoveryStates()
	st := states["hmip-rf"]
	if st.CanRetry() {
		t.Error("canRetry=true after exceeding limit; want false")
	}
}

func TestRecoveryMetricsMultiCCUIsolation(t *testing.T) {
	t.Parallel()

	a := NewConnectionRecoveryCoordinator("ccu-A", events.NewBus())
	b := NewConnectionRecoveryCoordinator("ccu-B", events.NewBus())

	pa := []Pipeline{{Stage: hmenum.RecoveryStageDetecting, Run: func(_ context.Context) error {
		return errors.New("a")
	}}}
	pb := []Pipeline{{Stage: hmenum.RecoveryStageDetecting, Run: func(_ context.Context) error {
		return nil
	}}}

	_ = a.Run(context.Background(), "iface", pa)
	_ = b.Run(context.Background(), "iface", pb)

	if got := a.MetricsRecoveryStates()["iface"].AttemptCount(); got != 1 {
		t.Errorf("ccu-A attempts=%d", got)
	}
	if got := b.MetricsRecoveryStates()["iface"].AttemptCount(); got != 1 {
		t.Errorf("ccu-B attempts=%d", got)
	}
	if a.MetricsRecoveryStates()["iface"].ConsecutiveFailures() != 1 {
		t.Errorf("ccu-A consecutive should be 1")
	}
	if b.MetricsRecoveryStates()["iface"].ConsecutiveFailures() != 0 {
		t.Errorf("ccu-B consecutive should be 0 (succeeded)")
	}
}

func TestRecoveryMetricsConcurrentReadsRaceFree(t *testing.T) {
	t.Parallel()

	c := NewConnectionRecoveryCoordinator("ccu-A", events.NewBus())
	pipeline := []Pipeline{{Stage: hmenum.RecoveryStageDetecting, Run: func(_ context.Context) error {
		return errors.New("e")
	}}}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := range 20 {
			_ = c.Run(context.Background(), interfaceTag(i), pipeline)
		}
	}()
	go func() {
		defer wg.Done()
		for range 100 {
			_ = c.MetricsInRecovery()
			_ = c.MetricsRecoveryStates()
		}
	}()
	wg.Wait()
}

func interfaceTag(i int) string {
	switch i % 3 {
	case 0:
		return "hmip-rf"
	case 1:
		return "bidcos-rf"
	}
	return "cuxd"
}
