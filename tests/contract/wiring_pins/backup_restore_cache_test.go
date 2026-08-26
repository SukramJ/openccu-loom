// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package wiring_pins

import (
	"testing"

	"github.com/SukramJ/openccu-loom/tests/contract"
)

// TestPin_BackupRestoreCacheInvalidator_WiredInDaemon pins that the
// daemon hands the cache-reset service to the backup adapter.
//
// The seam is optional by design — a daemon whose south-bound never came
// up has no re-init manager and therefore no cache-reset service — which
// is exactly why the line that fills it in a working daemon needs
// pinning. Without it a restore still succeeds and still reports
// success; what changes is that the persisted MASTER values, read
// cache-first with an unconditional early return on a hit, survive the
// CCU's post-restore reboot and every subsequent daemon start. An
// operator who restored an older backup to roll a device configuration
// back is then shown the values they rolled away for the lifetime of the
// installation, until they happen to run POST /admin/cache/clear by
// hand.
//
// [adapter.BackupAdapter.Restore] carries the call itself; its own unit
// tests pin that. This pins the other half — that a real daemon gives it
// something to call.
func TestPin_BackupRestoreCacheInvalidator_WiredInDaemon(t *testing.T) {
	contract.MustFindMethodCall(
		t,
		"cmd/openccu-loom/daemon.go",
		"backupAdapter", "SetCacheInvalidator",
	)
}
