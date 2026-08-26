// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package coordinators

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// TestRecoveryPipelineFailureAtEachStage migrates the
// `test_execute_recovery_stages_failure_at_*` test family.
// stages tcp_check, rpc_check, stability_check, reconnect, data_load).
//
// The Python tests are five separate methods, each setting up a full
// CCU mock and asserting the recovery fails at one specific stage.
// Go collapses them into one table-driven test because the underlying
// invariant is identical: when any stage of [DefaultRecoveryPipeline]
// returns an error, the pipeline (a) stops at that stage,
// (b) records [RecoveryResultFailed], (c) emits exactly one
// [hmevent.RecoveryFailedEvent], and (d) does NOT execute the stages
// that follow the failure.
func TestRecoveryPipelineFailureAtEachStage(t *testing.T) {
	stages := []hmenum.RecoveryStage{
		hmenum.RecoveryStageCooldown,
		hmenum.RecoveryStageTCPChecking,
		hmenum.RecoveryStageRPCChecking,
		hmenum.RecoveryStageWarmingUp,
		hmenum.RecoveryStageStabilityCheck,
		hmenum.RecoveryStageReconnecting,
		hmenum.RecoveryStageDataLoading,
	}

	for _, failAt := range stages {
		t.Run(string(failAt), func(t *testing.T) {
			bus := events.NewBus()
			c := NewConnectionRecoveryCoordinator("ccu-test", bus)

			var failedEvents atomic.Int32
			events.Subscribe(bus, func(hmevent.RecoveryFailedEvent) {
				failedEvents.Add(1)
			})

			// The failed stage is captured via the last
			// [hmevent.RecoveryStageChangedEvent] to land before the
			// failure event — openccu-loom surfaces stage progression
			// through StageChanged, not as a field on RecoveryFailed.
			var lastStage atomic.Value
			events.Subscribe(bus, func(e hmevent.RecoveryStageChangedEvent) {
				lastStage.Store(e.To)
			})

			stepErr := errors.New("simulated failure")
			var executed []hmenum.RecoveryStage
			var afterFailExecuted atomic.Int32
			seenFailed := false

			pipeline := make([]Pipeline, 0, len(stages))
			for _, s := range stages {
				stage := s
				pipeline = append(pipeline, Pipeline{
					Stage: stage,
					Run: func(_ context.Context) error {
						executed = append(executed, stage)
						if stage == failAt {
							seenFailed = true
							return stepErr
						}
						if seenFailed {
							afterFailExecuted.Add(1)
						}
						return nil
					},
				})
			}

			result := c.Run(context.Background(), "HmIP-RF", pipeline)
			if result != hmenum.RecoveryResultFailed {
				t.Fatalf("result=%s want failed (failAt=%s)", result, failAt)
			}
			if got := failedEvents.Load(); got != 1 {
				t.Errorf("failedEvents=%d want 1 (failAt=%s)", got, failAt)
			}
			if got, _ := lastStage.Load().(hmenum.RecoveryStage); got != failAt {
				t.Errorf("last stage advanced to %s want %s — pipeline stopped at wrong stage",
					got, failAt)
			}
			if afterFailExecuted.Load() != 0 {
				t.Errorf("%d stage(s) ran after failure (failAt=%s) — pipeline must short-circuit",
					afterFailExecuted.Load(), failAt)
			}
			// Last executed stage must equal the failed one.
			if len(executed) == 0 || executed[len(executed)-1] != failAt {
				t.Errorf("last executed stage=%v want %s", executed, failAt)
			}
		})
	}
}

// TestRecoveryPipelineGenericExceptionStillCountsAttempt mirrors
// `test_execute_recovery_stages_generic_exception`.
// (where Python wraps unexpected exceptions). Go does not have
// "exceptions" — every error returned by a stage is the same type,
// so the contract reduces to: a non-classified error increments the
// attempt counter and produces a `failed` result.
func TestRecoveryPipelineGenericExceptionStillCountsAttempt(t *testing.T) {
	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinator("ccu-test", bus)

	unexpected := errors.New("not a typed reliability error")
	pipeline := []Pipeline{
		{Stage: hmenum.RecoveryStageReconnecting, Run: func(context.Context) error { return unexpected }},
	}
	if r := c.Run(context.Background(), "HmIP-RF", pipeline); r != hmenum.RecoveryResultFailed {
		t.Fatalf("result=%s want failed", r)
	}
	if got := c.AttemptCount("HmIP-RF"); got != 1 {
		t.Errorf("AttemptCount=%d want 1", got)
	}
}

// TestRecoveryPipelineSuccessClearsAttempts mirrors
// `test_execute_recovery_stages_success` plus the parity invariant
// counter, so a future flap starts the backoff schedule from zero.
func TestRecoveryPipelineSuccessClearsAttempts(t *testing.T) {
	bus := events.NewBus()
	c := NewConnectionRecoveryCoordinator("ccu-test", bus)

	// Seed with a previous failure so we can observe the reset.
	failPipeline := []Pipeline{
		{Stage: hmenum.RecoveryStageReconnecting, Run: func(context.Context) error { return errors.New("boom") }},
	}
	_ = c.Run(context.Background(), "HmIP-RF", failPipeline)
	if got := c.AttemptCount("HmIP-RF"); got != 1 {
		t.Fatalf("post-failure AttemptCount=%d want 1", got)
	}

	// Now run a clean pipeline.
	pipeline := []Pipeline{
		{Stage: hmenum.RecoveryStageCooldown, Run: func(context.Context) error { return nil }},
		{Stage: hmenum.RecoveryStageReconnecting, Run: func(context.Context) error { return nil }},
		{Stage: hmenum.RecoveryStageRecovered, Run: func(context.Context) error { return nil }},
	}
	if r := c.Run(context.Background(), "HmIP-RF", pipeline); r != hmenum.RecoveryResultSuccess {
		t.Fatalf("result=%s want success", r)
	}
	if got := c.AttemptCount("HmIP-RF"); got != 0 {
		t.Errorf("post-success AttemptCount=%d want 0 (reset invariant)", got)
	}
}
