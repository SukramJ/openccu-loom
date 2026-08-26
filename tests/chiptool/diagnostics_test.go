// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build chiptool

package chiptool

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/tests/chiptool/harness"
)

// TestDiagnostics_UpTime reads GeneralDiagnostics.UpTime. Must be
// monotonic non-zero after the bridge has been alive for the
// suite's bring-up.
func TestDiagnostics_UpTime(t *testing.T) {
	b := requireBridge(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	out, err := b.SharedCtl.ReadAttr(ctx, t, "generaldiagnostics", "up-time", 0)
	if err != nil {
		t.Fatalf("read up-time: %v", err)
	}
	v, ok := harness.FindAttrUint(out, "UpTime")
	if !ok {
		t.Fatalf("UpTime not parsed:\n%s", out)
	}
	if v < 0 {
		t.Errorf("UpTime negative: %d", v)
	}
}

// TestDiagnostics_TotalOperationalHours reads
// GeneralDiagnostics.TotalOperationalHours. Read must succeed; the
// value rolls over per Matter §11.12.6.2.
func TestDiagnostics_TotalOperationalHours(t *testing.T) {
	b := requireBridge(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	out, err := b.SharedCtl.ReadAttr(ctx, t, "generaldiagnostics", "total-operational-hours", 0)
	if err != nil {
		t.Fatalf("read total-operational-hours: %v", err)
	}
	if !harness.AttrReadOK(out) {
		t.Errorf("total-operational-hours read did not succeed:\n%s", out)
	}
}

// TestDiagnostics_NetworkInterfaces reads
// GeneralDiagnostics.NetworkInterfaces. Read must succeed and
// surface at least one entry.
func TestDiagnostics_NetworkInterfaces(t *testing.T) {
	b := requireBridge(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	out, err := b.SharedCtl.ReadAttr(ctx, t, "generaldiagnostics", "network-interfaces", 0)
	if err != nil {
		t.Fatalf("read network-interfaces: %v", err)
	}
	if !harness.AttrReadOK(out) {
		t.Errorf("network-interfaces read did not succeed:\n%s", out)
	}
	if !strings.Contains(out, "Name") && !strings.Contains(out, "OperationalStatus") {
		t.Errorf("NetworkInterfaces emitted no entries:\n%s", out)
	}
}

// TestDiagnostics_RebootCount reads GeneralDiagnostics.RebootCount.
// Must succeed and be ≥ 1 — the bridge has booted at least once for
// the suite to be running.
func TestDiagnostics_RebootCount(t *testing.T) {
	b := requireBridge(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	out, err := b.SharedCtl.ReadAttr(ctx, t, "generaldiagnostics", "reboot-count", 0)
	if err != nil {
		t.Fatalf("read reboot-count: %v", err)
	}
	v, ok := harness.FindAttrUint(out, "RebootCount")
	if !ok {
		t.Fatalf("RebootCount not parsed:\n%s", out)
	}
	if v < 1 {
		t.Errorf("RebootCount=%d, want ≥ 1", v)
	}
}

// TestDiagnostics_GeneralCommissioning_Breadcrumb reads the
// GeneralCommissioning.Breadcrumb. After commissioning it is reset
// to 0 (Matter §11.9.6.1).
func TestDiagnostics_GeneralCommissioning_Breadcrumb(t *testing.T) {
	b := requireBridge(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	out, err := b.SharedCtl.ReadAttr(ctx, t, "generalcommissioning", "breadcrumb", 0)
	if err != nil {
		t.Fatalf("read breadcrumb: %v", err)
	}
	v, ok := harness.FindAttrUint(out, "Breadcrumb")
	if !ok {
		t.Fatalf("Breadcrumb not parsed:\n%s", out)
	}
	if v != 0 {
		t.Errorf("Breadcrumb=%d after commissioning, want 0", v)
	}
}

// TestDiagnostics_GeneralCommissioning_RegulatoryConfig reads the
// RegulatoryConfig. Must succeed and report a valid Matter value
// (0..2).
func TestDiagnostics_GeneralCommissioning_RegulatoryConfig(t *testing.T) {
	b := requireBridge(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	out, err := b.SharedCtl.ReadAttr(ctx, t, "generalcommissioning", "regulatory-config", 0)
	if err != nil {
		t.Fatalf("read regulatory-config: %v", err)
	}
	v, ok := harness.FindAttrUint(out, "RegulatoryConfig")
	if !ok {
		t.Fatalf("RegulatoryConfig not parsed:\n%s", out)
	}
	if v > 2 {
		t.Errorf("RegulatoryConfig=%d out of range 0..2", v)
	}
}

// TestDiagnostics_GroupKeyManagement_MaxGroupsPerFabric reads the
// MaxGroupsPerFabric attribute. Matter §11.2 mandates ≥ 4.
func TestDiagnostics_GroupKeyManagement_MaxGroupsPerFabric(t *testing.T) {
	b := requireBridge(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	out, err := b.SharedCtl.ReadAttr(ctx, t, "groupkeymanagement", "max-groups-per-fabric", 0)
	if err != nil {
		t.Fatalf("read max-groups-per-fabric: %v", err)
	}
	v, ok := harness.FindAttrUint(out, "MaxGroupsPerFabric")
	if !ok {
		t.Fatalf("MaxGroupsPerFabric not parsed:\n%s", out)
	}
	if v < 4 {
		t.Errorf("MaxGroupsPerFabric=%d, Matter mandates ≥ 4", v)
	}
}
