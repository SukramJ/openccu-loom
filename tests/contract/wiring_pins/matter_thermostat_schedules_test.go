// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package wiring_pins

import (
	"strings"
	"testing"
)

// TestThermostatScheduleWriterIsTheSchedulesDomain pins the composition
// root's half of a Matter week-program commit: the writer the bridge hands
// every thermostat is the schedules domain the REST schedule editor writes
// through, and the operator's flag reaches the build options.
//
// The bridge itself is pinned by its effect in cmd/openccu-loom
// (TestMatterThermostatScheduleCommitReachesTheWriter), with a writer the
// test passes in. What that cannot see is the daemon passing the real one:
// pass nil and every commit a controller makes is refused with
// UNSUPPORTED_WRITE while reads, the hold and the program switch all keep
// working — a defect that reads as "the controller does not support
// editing".
func TestThermostatScheduleWriterIsTheSchedulesDomain(t *testing.T) {
	t.Parallel()

	root := collapseSpaces(readDaemonRoot(t))
	if !strings.Contains(root, "schedulesDomain := adapter.NewSchedulesDomain(") {
		t.Fatal("daemon.go no longer builds the schedules domain under the name this pin follows")
	}
	if !strings.Contains(root, "wireMatterRuntime(ctx, cfg, reg, auditDB, healthTracker, parameterLabels, catalogs, auditRec, logger, wsHub, schedulesDomain)") {
		t.Error("daemon.go does not hand the schedules domain to the Matter runtime — a controller's week-program commit has no writer")
	}

	matter := collapseSpaces(readMatterComposition(t))
	for _, want := range []struct{ what, expr string }{
		{"the runtime's writer to the bridge", "bundle := startMatterBridge(ctx, cfg, reg, db, healthTracker, labels, logger, schedules)"},
		{"the hold flag to the build options", "ThermostatHoldAndRunningState: mc.ThermostatHoldAndRunningState,"},
		{"the schedules flag to the build options", "ThermostatSchedules: mc.ThermostatSchedules,"},
		{"the writer to the build options", "ClimateSchedules: schedules,"},
		{"the build options to the assembler", "Build: matterBuild,"},
	} {
		if !strings.Contains(matter, want.expr) {
			t.Errorf("daemon_matter.go does not hand %s (%q)", want.what, want.expr)
		}
	}
}
