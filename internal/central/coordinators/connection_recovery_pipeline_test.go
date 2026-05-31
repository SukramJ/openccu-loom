// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package coordinators

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

func successfulPipeline(t *testing.T, stages ...hmenum.RecoveryStage) []Pipeline {
	t.Helper()
	out := make([]Pipeline, len(stages))
	for i, s := range stages {
		out[i] = Pipeline{Stage: s, Run: func(context.Context) error { return nil }}
	}
	return out
}

func TestRecoveryPipelineEmitsExpectedEvents(t *testing.T) {
	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinator("test", bus)

	var (
		mu         sync.Mutex
		started    int
		stages     []hmenum.RecoveryStage
		completed  int
		failed     int
		stageOrder []hmenum.RecoveryStage
	)
	events.Subscribe(bus, func(hmevent.RecoveryStartedEvent) {
		mu.Lock()
		started++
		mu.Unlock()
	})
	events.Subscribe(bus, func(e hmevent.RecoveryStageChangedEvent) {
		mu.Lock()
		stages = append(stages, e.To)
		stageOrder = append(stageOrder, e.From)
		mu.Unlock()
	})
	events.Subscribe(bus, func(hmevent.RecoveryCompletedEvent) {
		mu.Lock()
		completed++
		mu.Unlock()
	})
	events.Subscribe(bus, func(hmevent.RecoveryFailedEvent) {
		mu.Lock()
		failed++
		mu.Unlock()
	})

	pipeline := successfulPipeline(
		t,
		hmenum.RecoveryStageDetecting,
		hmenum.RecoveryStageReconnecting,
		hmenum.RecoveryStageRecovered,
	)
	if got := c.Run(context.Background(), "HmIP-RF", pipeline); got != hmenum.RecoveryResultSuccess {
		t.Fatalf("Run = %s want success", got)
	}

	mu.Lock()
	defer mu.Unlock()
	if started != 1 {
		t.Fatalf("started=%d want 1", started)
	}
	if completed != 1 {
		t.Fatalf("completed=%d want 1", completed)
	}
	if failed != 0 {
		t.Fatalf("failed=%d want 0", failed)
	}
	if len(stages) != 3 {
		t.Fatalf("stages=%d want 3", len(stages))
	}
	if stageOrder[0] != hmenum.RecoveryStageIdle {
		t.Fatalf("first transition From=%s want idle", stageOrder[0])
	}
}

func TestRecoveryPipelineFailedStageEmitsRecoveryFailed(t *testing.T) {
	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinator("test", bus)

	var failed atomic.Int32
	events.Subscribe(bus, func(hmevent.RecoveryFailedEvent) { failed.Add(1) })

	stepErr := errors.New("stage 2 boom")
	pipeline := []Pipeline{
		{Stage: hmenum.RecoveryStageDetecting, Run: func(context.Context) error { return nil }},
		{Stage: hmenum.RecoveryStageReconnecting, Run: func(context.Context) error { return stepErr }},
		// Third stage must NOT execute after a failed stage 2.
		{Stage: hmenum.RecoveryStageRecovered, Run: func(context.Context) error {
			t.Fatal("third stage must not run after failure")
			return nil
		}},
	}
	if got := c.Run(context.Background(), "HmIP-RF", pipeline); got != hmenum.RecoveryResultFailed {
		t.Fatalf("Run = %s want failed", got)
	}
	if failed.Load() != 1 {
		t.Fatalf("failed=%d want 1", failed.Load())
	}
	// Attempt counter must have advanced.
	if got := c.AttemptCount("HmIP-RF"); got != 1 {
		t.Fatalf("AttemptCount=%d want 1", got)
	}
}

func TestRecoveryPipelineCancelledByContext(t *testing.T) {
	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinator("test", bus)

	ctx, cancel := context.WithCancel(context.Background())
	pipeline := []Pipeline{
		{Stage: hmenum.RecoveryStageDetecting, Run: func(context.Context) error {
			cancel() // cancel mid-pipeline
			return nil
		}},
		{Stage: hmenum.RecoveryStageReconnecting, Run: func(ctx context.Context) error {
			return ctx.Err()
		}},
	}
	got := c.Run(ctx, "HmIP-RF", pipeline)
	if got != hmenum.RecoveryResultFailed && got != hmenum.RecoveryResultCancelled {
		t.Fatalf("Run = %s want failed or cancelled", got)
	}
}

func TestRecoveryConcurrentRunsSerialiseSameInterface(t *testing.T) {
	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinator("test", bus)

	gate := make(chan struct{})
	releaseFirst := make(chan struct{})
	var inFlight atomic.Int32
	var maxConcurrent atomic.Int32

	step := func(_ context.Context) error {
		now := inFlight.Add(1)
		if now > maxConcurrent.Load() {
			maxConcurrent.Store(now)
		}
		// Hold the first run open until the second one has been
		// allowed to attempt entry.
		select {
		case <-gate:
		default:
			close(gate)
			<-releaseFirst
		}
		inFlight.Add(-1)
		return nil
	}

	pipeline := []Pipeline{{Stage: hmenum.RecoveryStageDetecting, Run: step}}

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_ = c.Run(context.Background(), "HmIP-RF", pipeline)
	}()
	// Wait until the first run is inside the step.
	<-gate
	go func() {
		defer wg.Done()
		_ = c.Run(context.Background(), "HmIP-RF", pipeline)
	}()
	// Allow some time for the second goroutine to start; it must
	// still be blocked because the first holds the per-interface lock.
	time.Sleep(20 * time.Millisecond)
	if inFlight.Load() != 1 {
		t.Fatalf("inFlight=%d want 1 (second run must wait)", inFlight.Load())
	}
	close(releaseFirst)
	wg.Wait()
	if maxConcurrent.Load() != 1 {
		t.Fatalf("maxConcurrent=%d want 1 (per-interface serialisation)", maxConcurrent.Load())
	}
}

func TestRecoveryConcurrentRunsParallelDifferentInterfaces(t *testing.T) {
	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinator("test", bus)

	var inFlight atomic.Int32
	var maxConcurrent atomic.Int32
	hold := make(chan struct{})

	step := func(_ context.Context) error {
		now := inFlight.Add(1)
		if now > maxConcurrent.Load() {
			maxConcurrent.Store(now)
		}
		<-hold
		inFlight.Add(-1)
		return nil
	}
	pipeline := []Pipeline{{Stage: hmenum.RecoveryStageDetecting, Run: step}}

	var wg sync.WaitGroup
	for _, iface := range []string{"HmIP-RF", "BidCos-RF", "CUxD"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			_ = c.Run(context.Background(), id, pipeline)
		}(iface)
	}
	// Wait until all three have entered the step concurrently.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && inFlight.Load() != 3 {
		time.Sleep(5 * time.Millisecond)
	}
	if inFlight.Load() != 3 {
		t.Fatalf("inFlight=%d want 3 (different interfaces must run concurrently)", inFlight.Load())
	}
	close(hold)
	wg.Wait()
	if maxConcurrent.Load() != 3 {
		t.Fatalf("maxConcurrent=%d want 3", maxConcurrent.Load())
	}
}
