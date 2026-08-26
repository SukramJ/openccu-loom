// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package wiring_pins

import (
	"testing"

	"github.com/SukramJ/openccu-loom/tests/contract"
)

// TestPin_StartReaper_CalledInDaemon pins that daemon.go starts the session
// reaper via Manager.StartReaper.  Without this call, stale CASE sessions
// accumulate unboundedly; the CCU bridge would eventually exhaust its session
// table and reject new commissioning attempts.
func TestPin_StartReaper_CalledInDaemon(t *testing.T) {
	// StartReaper is a method on the operational session Manager (the
	// local `opMgr` value), not a package-level function — pin the
	// method call directly.
	contract.MustFindMethodCall(
		t,
		"cmd/openccu-loom",
		"opMgr", "StartReaper",
	)
}
