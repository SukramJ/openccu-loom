// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

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

// TestPin_AlarmMotionReset_Wired pins that the alarm service passes a
// motion-reset port into the engine.
//
// The engine treats a nil port as "feature off" — every reset becomes
// a no-op and TriggeredMotionSensors reports nothing — and the engine's
// own tests inject their own fake, so dropping the production line
// leaves the whole package green while the button in the UI silently
// stops clearing anything.
//
// This is a source-level pin, so it proves the constructor is called,
// not that a real detector gets written to. The behavioural half lives
// in the engine tests (the reset set and the reported count derive
// from one predicate) and in the REST handler tests.
func TestPin_AlarmMotionReset_Wired(t *testing.T) {
	contract.MustFindCallerInFile(t,
		"internal/alarm/service.go",
		"", "newMotionResetter")
}

// TestPin_AlarmCentralHook_Installed pins that runtime-adopted
// centrals are subscribed onto the alarm service — otherwise sensors
// on a live-adopted CCU silently never reach the engine.
func TestPin_AlarmCentralHook_Installed(t *testing.T) {
	contract.MustFindMethodCall(t,
		"cmd/openccu-loom",
		"centralOrch", "setAlarmCentralHook")
}
