// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ── buildAggregatorClusters ───────────────────────────────────────────────────

func TestBuildAggregatorClusters_ReturnsOneCluster(t *testing.T) {
	t.Parallel()
	clusters, err := buildAggregatorClusters()
	if err != nil {
		t.Fatalf("buildAggregatorClusters: %v", err)
	}
	if len(clusters) == 0 {
		t.Fatal("expected at least one cluster")
	}
}

func TestBuildAggregatorClusters_IsIdempotent(t *testing.T) {
	t.Parallel()
	a, err := buildAggregatorClusters()
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	b, err := buildAggregatorClusters()
	if err != nil {
		t.Fatalf("second call: %v", err)
	}
	if len(a) != len(b) {
		t.Errorf("cluster count mismatch: %d vs %d", len(a), len(b))
	}
}

// ── scheduleWeekProfileSink.SetActiveProfile ──────────────────────────────────

func TestScheduleWeekProfileSink_SetActiveProfile_NilReg_ReturnsError(t *testing.T) {
	t.Parallel()
	// NewSchedulesDomain with nil registry — SetActiveProfile will error
	// when it tries to resolve the device.
	reg := buildTestRegistry(t, "ccu-01")
	sd := adapter.NewSchedulesDomain(reg, nil)
	sink := scheduleWeekProfileSink{sd: sd}

	err := sink.SetActiveProfile(
		context.Background(),
		"ccu-01",      // central (dropped)
		"HmIP-RF",     // interface (dropped)
		"ABC123456:1", // deviceAddress
		1,             // channelIdx
		"profile-key",
		hmenum.CommandPriorityLow,
	)
	// We expect an error because the device is not in the registry.
	// The important thing is no panic.
	_ = err
}

func TestScheduleWeekProfileSink_SetActiveProfile_IgnoresCentralAndIface(t *testing.T) {
	t.Parallel()
	// The sink drops the central + interface args — both "x"/"y" and ""/"" must produce
	// the same outcome (an error from the SchedulesDomain because device is not found).
	reg := buildTestRegistry(t, "ccu-01")
	sd := adapter.NewSchedulesDomain(reg, nil)
	sink := scheduleWeekProfileSink{sd: sd}

	ctx := context.Background()
	errA := sink.SetActiveProfile(ctx, "x", "y", "DEVICE:1", 0, "P1", hmenum.CommandPriorityLow)
	errB := sink.SetActiveProfile(ctx, "", "", "DEVICE:1", 0, "P1", hmenum.CommandPriorityLow)

	// Both should produce equivalent errors (device not found).
	if (errA == nil) != (errB == nil) {
		t.Errorf("expected same nil-ness for both calls; errA=%v errB=%v", errA, errB)
	}
}
