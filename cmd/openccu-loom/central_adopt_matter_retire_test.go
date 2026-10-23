// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/config"
)

// TestRemoveCentralRetiresTheMatterPartition pins that a live removal
// retires the central's partition of the node's Aggregator — through the
// orchestrator's own removeCentral, after the central's rows are purged —
// and that only the removed central is named: a daemon shutdown stops
// every central too, and their endpoint numbers must survive it, so the
// retirement rides on removeCentral alone.
func TestRemoveCentralRetiresTheMatterPartition(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	const removed = "matter-retire-removed"
	cfg := &config.Config{Centrals: []config.CentralConfig{{
		Name:       removed,
		Interfaces: []config.InterfaceSpec{{Name: "HmIP-RF"}},
	}}}

	reg := central.NewRegistry()
	unit := startRegisteredTestCentral(ctx, t, reg, removed)
	orch := buildLiveTestOrchestrator(ctx, t, reg, cfg)
	wireCentralNorthbound(orch.sbDeps, unit)

	var retired []string
	orch.setMatterPartitionRetirer(func(_ context.Context, name string) error {
		retired = append(retired, name)
		return nil
	})

	if err := orch.removeCentral(ctx, removed); err != nil {
		t.Fatalf("removeCentral: %v", err)
	}
	if len(retired) != 1 || retired[0] != removed {
		t.Fatalf("removeCentral retired %v, want exactly [%s]", retired, removed)
	}
}
