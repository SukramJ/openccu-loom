// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package coordinators

// recovery_stages_test.go — tests for DefaultRecoveryPipeline and stage helpers.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestDefaultRecoveryPipelineHas8Stages verifies the pipeline has exactly
// The 8 stages defined in py:474-531.
func TestDefaultRecoveryPipelineHas8Stages(t *testing.T) {
	t.Parallel()

	pipeline := DefaultRecoveryPipeline(RecoveryStageDeps{})
	if got := len(pipeline); got != 8 {
		t.Fatalf("pipeline stages = %d, want 8", got)
	}

	want := []hmenum.RecoveryStage{
		hmenum.RecoveryStageCooldown,
		hmenum.RecoveryStageTCPChecking,
		hmenum.RecoveryStageRPCChecking,
		hmenum.RecoveryStageWarmingUp,
		hmenum.RecoveryStageStabilityCheck,
		hmenum.RecoveryStageReconnecting,
		hmenum.RecoveryStageDataLoading,
		hmenum.RecoveryStageRecovered,
	}
	for i, s := range want {
		if pipeline[i].Stage != s {
			t.Errorf("stage[%d] = %q, want %q", i, pipeline[i].Stage, s)
		}
	}
}

// TestDefaultRecoveryPipelineNilDepsAllSucceed verifies that a pipeline
// constructed with zero-value deps (all nil probes) runs to completion
// without error.
func TestDefaultRecoveryPipelineNilDepsAllSucceed(t *testing.T) {
	t.Parallel()

	pipeline := DefaultRecoveryPipeline(RecoveryStageDeps{})
	ctx := context.Background()
	for _, step := range pipeline {
		if err := step.Run(ctx); err != nil {
			t.Errorf("stage %q returned unexpected error: %v", step.Stage, err)
		}
	}
}

// TestCooldownStageRespectsContext verifies that a cooldown is cut short
// when the context is cancelled.
func TestCooldownStageRespectsContext(t *testing.T) {
	t.Parallel()

	stage := cooldownStage(10 * time.Second) // very long
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- stage(ctx)
	}()

	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cooldown returned %v, want context.Canceled", err)
		}
	case <-time.After(eventWaitTimeout):
		t.Fatal("cooldown did not respect context cancellation")
	}
}

// TestCooldownStageZeroDurationSucceeds verifies that a zero-duration
// cooldown is a no-op that returns nil immediately.
func TestCooldownStageZeroDurationSucceeds(t *testing.T) {
	t.Parallel()

	stage := cooldownStage(0)
	if err := stage(context.Background()); err != nil {
		t.Fatalf("zero cooldown returned %v, want nil", err)
	}
}

// TestTCPCheckStageDelegatesToProbe verifies that when the probe returns an
// error, the stage propagates it.
func TestTCPCheckStageDelegatesToProbe(t *testing.T) {
	t.Parallel()

	probeErr := errors.New("tcp: connection refused")
	called := false
	probe := func(_ context.Context) error {
		called = true
		return probeErr
	}

	stage := probeStage(probe)
	err := stage(context.Background())
	if !called {
		t.Fatal("probe was not called")
	}
	if !errors.Is(err, probeErr) {
		t.Fatalf("stage returned %v, want %v", err, probeErr)
	}
}

// TestTCPCheckStageNilProbeSucceeds verifies that a nil probe is a no-op.
func TestTCPCheckStageNilProbeSucceeds(t *testing.T) {
	t.Parallel()

	stage := probeStage(nil)
	if err := stage(context.Background()); err != nil {
		t.Fatalf("nil probe stage returned %v, want nil", err)
	}
}

// TestDefaultRecoveryPipelineProbesAreCalled verifies that all injected
// probes are actually invoked when the pipeline runs.
func TestDefaultRecoveryPipelineProbesAreCalled(t *testing.T) {
	t.Parallel()

	called := make(map[string]bool)
	mark := func(name string) func(context.Context) error {
		return func(_ context.Context) error {
			called[name] = true
			return nil
		}
	}

	pipeline := DefaultRecoveryPipeline(RecoveryStageDeps{
		TCPProbe:       mark("tcp"),
		RPCProbe:       mark("rpc"),
		StabilityProbe: mark("stability"),
		Reconnect:      mark("reconnect"),
		LoadData:       mark("loaddata"),
	})

	ctx := context.Background()
	for _, step := range pipeline {
		if err := step.Run(ctx); err != nil {
			t.Fatalf("stage %q error: %v", step.Stage, err)
		}
	}

	for _, name := range []string{"tcp", "rpc", "stability", "reconnect", "loaddata"} {
		if !called[name] {
			t.Errorf("probe %q was not called", name)
		}
	}
}
