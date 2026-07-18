// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package wiring_pins

import (
	"testing"

	"github.com/SukramJ/openccu-loom/tests/contract"
)

// TestPin_AlarmService_ConstructedInDaemon pins that the daemon
// composition root actually constructs the alarm service. Without
// this call the whole alarm engine — stores, state machine, output
// drivers — compiles and tests green but never runs: the archetypal
// dormant-capability failure, and for an alarm system a silent one
// (S7 exists to prevent exactly this class).
func TestPin_AlarmService_ConstructedInDaemon(t *testing.T) {
	contract.MustFindCallerInFile(t,
		"cmd/openccu-loom/daemon.go",
		"", "wireAlarmService")
}

// TestPin_AlarmCentralHook_Installed pins that runtime-adopted
// centrals are subscribed onto the alarm service — otherwise sensors
// on a live-adopted CCU silently never reach the engine.
func TestPin_AlarmCentralHook_Installed(t *testing.T) {
	contract.MustFindMethodCall(t,
		"cmd/openccu-loom",
		"centralOrch", "setAlarmCentralHook")
}
