// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package coordinators

import (
	"context"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// RecoveryStageDeps holds the injectable function-pointers that back each
// stage of [DefaultRecoveryPipeline]. Every field has a stub default (no-op,
// nil error) so callers only need to fill the slots that matter for their
// use-case, and tests can construct a pipeline without full CCU wiring.
type RecoveryStageDeps struct {
	// CooldownDuration is the initial wait before any probing starts.
	// Zero → no cooldown stage is run (stage still appears but returns immediately).
	CooldownDuration time.Duration

	// WarmupDuration is the delay inserted between RPC_CHECKING and
	// STABILITY_CHECK. Zero → stage still appears but returns immediately.
	WarmupDuration time.Duration

	// TCPProbe is called during TCP_CHECKING. Should attempt a TCP
	// connection to the CCU host:port and return an error if unreachable.
	// Nil → stage is a no-op success.
	TCPProbe func(ctx context.Context) error

	// RPCProbe is called during RPC_CHECKING. Should issue a lightweight
	// RPC (e.g. system.listMethods) and return an error on failure.
	// Nil → stage is a no-op success.
	RPCProbe func(ctx context.Context) error

	// StabilityProbe is called during STABILITY_CHECK (post-warmup
	// re-validation). Nil → stage is a no-op success.
	StabilityProbe func(ctx context.Context) error

	// Reconnect is called during RECONNECTING. Should re-initialise the
	// client / backend (e.g. call backend.Init). Nil → no-op success.
	Reconnect func(ctx context.Context) error

	// LoadData is called during DATA_LOADING. Should refresh data-point
	// values and hub entities after reconnect. Nil → no-op success.
	LoadData func(ctx context.Context) error
}

// noopStage returns a RecoveryStep that is always a no-op success.
func noopStage() RecoveryStep {
	return func(_ context.Context) error { return nil }
}

// probeStage wraps an optional probe func into a RecoveryStep. When
// probe is nil the step is a no-op.
func probeStage(probe func(context.Context) error) RecoveryStep {
	if probe == nil {
		return noopStage()
	}
	return func(ctx context.Context) error {
		return probe(ctx)
	}
}

// cooldownStage returns a RecoveryStep that sleeps for d, honouring
// context cancellation.
func cooldownStage(d time.Duration) RecoveryStep {
	if d <= 0 {
		return noopStage()
	}
	return func(ctx context.Context) error {
		select {
		case <-time.After(d):
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// DefaultRecoveryPipeline returns the eight-stage recovery pipeline that
// follows the eight-stage CCU reconnect sequence:
//
//	COOLDOWN → TCP_CHECKING → RPC_CHECKING → WARMING_UP →
//	STABILITY_CHECK → RECONNECTING → DATA_LOADING → RECOVERED
//
// Each stage delegates to the corresponding field in deps. Fields that are
// nil become no-op stages so callers only need to fill relevant slots.
//
// loom:reachable:reason="called by NewConnectionRecoveryCoordinator to assemble the production pipeline"
func DefaultRecoveryPipeline(deps RecoveryStageDeps) []Pipeline {
	return []Pipeline{
		{
			Stage: hmenum.RecoveryStageCooldown,
			Run:   cooldownStage(deps.CooldownDuration),
		},
		{
			Stage: hmenum.RecoveryStageTCPChecking,
			Run:   probeStage(deps.TCPProbe),
		},
		{
			Stage: hmenum.RecoveryStageRPCChecking,
			Run:   probeStage(deps.RPCProbe),
		},
		{
			Stage: hmenum.RecoveryStageWarmingUp,
			Run:   cooldownStage(deps.WarmupDuration),
		},
		{
			Stage: hmenum.RecoveryStageStabilityCheck,
			Run:   probeStage(deps.StabilityProbe),
		},
		{
			Stage: hmenum.RecoveryStageReconnecting,
			Run:   probeStage(deps.Reconnect),
		},
		{
			Stage: hmenum.RecoveryStageDataLoading,
			Run:   probeStage(deps.LoadData),
		},
		{
			Stage: hmenum.RecoveryStageRecovered,
			Run:   noopStage(),
		},
	}
}
