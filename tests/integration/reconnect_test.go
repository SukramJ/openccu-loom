// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestReconnectCycleAgainstMockCCU exercises the "reconnect on CCU restart"
// criterion: stop the mock CCU, start a fresh one, and verify the
// ConnectionRecoveryCoordinator transitions the attempt to success again.
func TestReconnectCycleAgainstMockCCU(t *testing.T) {
	srv := startMockCCU(t)
	c, _ := central.New(central.Config{Name: "ccu-01"})

	// Attempt one: healthy simulator → step returns nil → recovery
	// reports success.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	step := func(context.Context) error {
		client := newXMLRPCClient(t, srv.URL())
		_, err := client.Call(ctx, "system.listMethods", nil)
		return err
	}
	result := c.Recovery.Run(ctx, "HmIP-RF", []coordinators.Pipeline{{Stage: hmenum.RecoveryStage("ping"), Run: step}})
	if result != hmenum.RecoveryResultSuccess {
		t.Fatalf("first run result=%s", result)
	}

	// Attempt two: stop the simulator and run recovery → expect
	// failure. We capture the URL up front because Stop drops the
	// listener; the next step would otherwise dial a stale address.
	stoppedURL := srv.URL()
	if err := srv.Stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	stoppedStep := func(context.Context) error {
		client := newXMLRPCClient(t, stoppedURL)
		_, err := client.Call(ctx, "system.listMethods", nil)
		return err
	}
	result2 := c.Recovery.Run(ctx, "HmIP-RF", []coordinators.Pipeline{{Stage: hmenum.RecoveryStage("ping"), Run: stoppedStep}})
	if result2 == hmenum.RecoveryResultSuccess {
		t.Fatalf("expected failure after stop, got success")
	}

	// Attempt three: fresh simulator → recovery success again.
	fresh := startMockCCU(t)
	step2 := func(context.Context) error {
		client := newXMLRPCClient(t, fresh.URL())
		_, err := client.Call(ctx, "system.listMethods", nil)
		return err
	}
	result3 := c.Recovery.Run(ctx, "HmIP-RF", []coordinators.Pipeline{{Stage: hmenum.RecoveryStage("ping"), Run: step2}})
	if result3 != hmenum.RecoveryResultSuccess {
		t.Fatalf("recovery after restart result=%s", result3)
	}
}
