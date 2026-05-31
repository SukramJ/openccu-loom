// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Tests for Update firmware-tracking: MonitorProgress tolerates transient
// poll errors and recovers on version change; Install sets VersionBeforeUpdate
// and triggers InProgress.

package hub

import (
	"context"
	"errors"
	"testing"
)

// TestUpdateMonitorProgressToleratesPollErrors verifies that MonitorProgress
// continues polling after transient errors from the poll function and
// completes successfully once the version changes.
// The scenario: the CCU goes offline during reboot (poll returns error on
// first call), then returns a new version on the second call.
func TestUpdateMonitorProgressToleratesPollErrors(t *testing.T) {
	t.Parallel()
	u := NewUpdate()
	u.SetVersionBeforeUpdate("3.75.6")
	u.SetInProgress(true)

	callCount := 0
	pollFn := func(_ context.Context) (string, error) {
		callCount++
		if callCount == 1 {
			return "", errors.New("CCU offline during reboot")
		}
		return "3.77.0", nil // version changed on second call
	}

	u.MonitorProgress(context.Background(), pollFn, 10)

	if u.InProgress() {
		t.Fatal("InProgress must be false after version change")
	}
	if callCount < 2 {
		t.Fatalf("pollFn called %d times, want at least 2 (error + success)", callCount)
	}
	info, ok := u.UpdateInfo()
	if !ok {
		t.Fatal("Info must be observed after version change")
	}
	if info.CurrentFirmware != "3.77.0" {
		t.Errorf("CurrentFirmware=%q, want 3.77.0", info.CurrentFirmware)
	}
}

// TestUpdateInstallSetsVersionBeforeUpdate verifies that Install does not
// automatically snapshot the version — the caller must call
// SetVersionBeforeUpdate before Install, and InProgress is set afterwards.
func TestUpdateInstallSetsVersionBeforeUpdate(t *testing.T) {
	t.Parallel()
	u := NewUpdate()
	u.SetVersionBeforeUpdate("3.55.0")

	var triggered bool
	u.FirmwareUpdater = &stubFirmwareUpdaterWithFn{fn: func() { triggered = true }}

	if err := u.Install(context.Background()); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !triggered {
		t.Fatal("FirmwareUpdater.TriggerFirmwareUpdate not called")
	}
	if !u.InProgress() {
		t.Fatal("InProgress must be true after Install")
	}
	v, ok := u.VersionBeforeUpdate()
	if !ok || v != "3.55.0" {
		t.Errorf("VersionBeforeUpdate()=(%q,%v), want (3.55.0,true)", v, ok)
	}
}

// stubFirmwareUpdaterWithFn is a test double for FirmwareUpdater that
// supports an optional callback to verify the trigger was called.
type stubFirmwareUpdaterWithFn struct {
	fn  func()
	err error
}

func (s *stubFirmwareUpdaterWithFn) TriggerFirmwareUpdate(_ context.Context) error {
	if s.fn != nil {
		s.fn()
	}
	return s.err
}
