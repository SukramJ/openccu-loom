// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

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
	contract.MustFindCallerInFile(
		t,
		"cmd/openccu-loom/daemon.go",
		"internal/north/matter", "StartReaper",
	)
}
