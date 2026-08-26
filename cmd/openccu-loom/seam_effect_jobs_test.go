// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	clientpkg "github.com/SukramJ/openccu-loom/internal/client"
)

// TestSeamEffect_DevicesCreatedGate_StopsAnswering_TrueUnconditionally
// asserts what the central.devices_created_gate seam's Why claims: that
// gated hub jobs have a gate to wait on.
//
// Unit.IsDevicesCreated answers true unconditionally while no gate is
// installed — with nothing to wait on, every gated job is free to run. So
// the seam's whole observable effect is that the answer becomes false for
// a central with no devices, and the assertion has to read it in that
// direction. A test checking for true would pass in both states.
func TestSeamEffect_DevicesCreatedGate_ClosesTheGateForACentralWithNoDevices(t *testing.T) {
	t.Parallel()

	reg := central.NewRegistry()
	unit := registerSeamEffectCentral(t, reg, "gated-central")

	if !unit.IsDevicesCreated() {
		t.Fatal("a central with no gate already reports devices-not-created — the " +
			"unconditional-true baseline this test reads against is gone, so the " +
			"assertion below would not discriminate")
	}

	wireDevicesCreatedGates(reg)

	if unit.IsDevicesCreated() {
		t.Error("the gate did not close: gated hub jobs have nothing to wait on, so a job " +
			"scheduled at boot fires before any device exists")
	}
}

// TestSeamEffect_FirmwareJobs_ReachTheCentralScheduler asserts the
// jobs.firmware_per_central seam's Why: that a central polls the CCU for
// newly available device firmware.
//
// The observable is a job on the central's own scheduler, because that is
// what "ever polls" means here. A registration that failed — a nil
// scheduler, a duplicate name — logs a warning and returns, which is
// exactly the silent shape the seam exists to rule out.
func TestSeamEffect_FirmwareJobs_ReachTheCentralScheduler(t *testing.T) {
	t.Parallel()

	reg := central.NewRegistry()
	unit := registerSeamEffectCentral(t, reg, "firmware-central")
	if unit.Scheduler == nil {
		t.Fatal("the registered central has no scheduler — the seam has nowhere to put a " +
			"job, so this test would measure the fixture")
	}
	before := firmwareJobNames(unit)
	if len(before) != 0 {
		t.Fatalf("the central already carries firmware jobs %v before the seam runs — the "+
			"assertion below would not be attributable to it", before)
	}

	registerFirmwareJobs(reg, &clientpkg.ValueWriter{}, discardTestLogger())

	if got := firmwareJobNames(unit); len(got) == 0 {
		t.Error("no firmware job reached the central's scheduler: nothing polls the CCU for " +
			"versions released after boot, and the update surface stops learning about them")
	}
}

// firmwareJobNames returns the scheduled jobs whose name marks them as the
// firmware pollers.
func firmwareJobNames(u *central.Unit) []string {
	var out []string
	for _, j := range u.Scheduler.Jobs() {
		if strings.Contains(j.Name, "firmware") {
			out = append(out, j.Name)
		}
	}
	return out
}
