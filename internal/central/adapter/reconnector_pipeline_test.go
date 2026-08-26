// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// registryWithRecovery registers one central whose recovery coordinator
// is wired the way the south-bound bring-up wires it.
func registryWithRecovery(t *testing.T, name string) (*central.Registry, *central.Unit) {
	t.Helper()
	reg := central.NewRegistry()
	unit, err := central.New(central.Config{Name: name})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	if err := reg.Register(unit); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	return reg, unit
}

// TestReconnectRunsTheRegisteredPipeline pins that the reconnect endpoint
// drives the interface's real recovery pipeline.
//
// It used to build a synthetic single-stage pipeline from an injected
// step, and production injects none — so the stage was a func returning
// nil. Every POST /interfaces/{id}/reconnect reported success, reset the
// circuit breaker and moved the central back to RUNNING without a single
// byte reaching the CCU, masking the very outage the operator was trying
// to clear.
func TestReconnectRunsTheRegisteredPipeline(t *testing.T) {
	t.Parallel()

	reg, unit := registryWithRecovery(t, "ccu-1")
	var ran atomic.Int32
	unit.Recovery.WithPipelineFor("HmIP-RF", []coordinators.Pipeline{{
		Stage: hmenum.RecoveryStageReconnecting,
		Run: func(context.Context) error {
			ran.Add(1)
			return nil
		},
	}})

	rc := NewRecoveryReconnector(reg, nil)
	if err := rc.Reconnect(context.Background(), "ccu-1", "HmIP-RF"); err != nil {
		t.Fatalf("Reconnect: %v", err)
	}
	if got := ran.Load(); got != 1 {
		t.Fatalf("registered pipeline ran %d times, want 1 — the endpoint reported success without reconnecting", got)
	}
}

// TestReconnectWithoutPipelineFails pins that an interface with no
// registered pipeline reports a failure instead of an empty success. An
// empty pipeline "succeeds" trivially, which is exactly how the dead
// reconnect path stayed invisible.
func TestReconnectWithoutPipelineFails(t *testing.T) {
	t.Parallel()

	reg, _ := registryWithRecovery(t, "ccu-2")
	rc := NewRecoveryReconnector(reg, nil)

	if err := rc.Reconnect(context.Background(), "ccu-2", "HmIP-RF"); err == nil {
		t.Fatal("Reconnect on an interface with no pipeline reported success")
	}
}
