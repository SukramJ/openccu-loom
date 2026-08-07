// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
)

// RecoveryReconnector implements [Reconnector] by delegating to a
// central's [coordinators.ConnectionRecoveryCoordinator]. The MVP
// pipeline is a single "re-init the client" step — extensions (cache
// invalidation, re-subscribe) attach via [ConnectionRecoveryCoordinator].
type RecoveryReconnector struct {
	registry *central.Registry
	step     coordinators.RecoveryStep
}

// NewRecoveryReconnector constructs a reconnector.
//
// `step` is the work the coordinator runs per-interface; typically
// the caller wraps `backend.Init(ctx, interfaceID, callbackURL)` so
// the CCU re-advertises the callback channel.
func NewRecoveryReconnector(r *central.Registry, step coordinators.RecoveryStep) *RecoveryReconnector {
	return &RecoveryReconnector{registry: r, step: step}
}

// Reconnect implements [Reconnector].
func (rc *RecoveryReconnector) Reconnect(ctx context.Context, centralName, interfaceID string) error {
	c, ok := rc.registry.Get(centralName)
	if !ok {
		return fmt.Errorf("reconnector: unknown central %q", centralName)
	}
	if c.Recovery == nil {
		return fmt.Errorf("reconnector: central %q has no recovery coordinator", centralName)
	}
	// Run the pipeline the south-bound wiring registered for this
	// interface — the one that actually reconnects and re-announces the
	// callback to the CCU. The endpoint used to build a synthetic
	// single-stage pipeline instead, and production wires no step, so the
	// stage was a func that returned nil: every reconnect request reported
	// success, force-closed the circuit breaker and moved the central back
	// to RUNNING without a single byte reaching the CCU. That is worse
	// than doing nothing, because it masks the outage the operator was
	// trying to clear.
	pipeline, ok := c.Recovery.PipelineFor(interfaceID)
	if rc.step != nil {
		// An explicitly injected step wins — the seam exists so a caller
		// can drive a specific stage.
		pipeline, ok = []coordinators.Pipeline{{Stage: "reinit", Run: rc.step}}, true
	}
	if !ok {
		return fmt.Errorf("reconnector: no recovery pipeline registered for %q on central %q", interfaceID, centralName)
	}
	result := c.Recovery.Run(ctx, interfaceID, pipeline)
	if result != "success" {
		return fmt.Errorf("reconnector: recovery result %s", result)
	}
	return nil
}
