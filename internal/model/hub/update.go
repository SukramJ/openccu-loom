// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hub

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/payload"
)

// UpdateInfo summarises the CCU's firmware-update state.
type UpdateInfo struct {
	CurrentFirmware      string
	AvailableFirmware    string
	UpdateAvailable      bool
	CheckScriptAvailable bool
}

// Update tracks the firmware-update state of the central.
type Update struct {
	// ServiceRegistry implements the write-half of [payload.Source].
	// The zero value gives correct no-service behaviour; the install
	// service is intentionally not registered because no InstallWriter
	// is wired on the Update struct (install is triggered via
	// Hub.TriggerFirmwareUpdateRemote).
	payload.ServiceRegistry

	// FirmwareUpdater is the optional CCU-side install trigger wired
	// after construction. When nil, Install returns ErrNoFirmwareUpdater.
	FirmwareUpdater FirmwareUpdater

	mu                  sync.RWMutex
	info                UpdateInfo
	inProgress          bool
	observed            bool
	versionBeforeUpdate string
	hasVersionBefore    bool
	callbacks           []func(UpdateInfo)
	// installMonitor, when wired via [SetInstallMonitor], is launched by
	// [Install] after a successful trigger to watch the firmware version and
	// clear inProgress once the update completes. Mirrors install()
	// spawning _monitor_update_progress (model/hub/update.py:127,175).
	installMonitor func()
}

// NewUpdate returns an empty tracker.
func NewUpdate() *Update { return &Update{} }

// UpdateInfo returns the last observed snapshot.
func (u *Update) UpdateInfo() (UpdateInfo, bool) {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.info, u.observed
}

// InProgress reports whether a firmware install is in flight.
func (u *Update) InProgress() bool {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.inProgress
}

// Install triggers the firmware update via the wired [FirmwareUpdater], then
// launches the wired install monitor (see [SetInstallMonitor]) to clear the
// in-progress flag once the CCU finishes installing and reboots. Mirrors
// HmUpdate.install (model/hub/update.py:127): snapshot the
// current version, trigger, flag in-progress, spawn the progress monitor.
// With no monitor wired the flag must be cleared by the caller.
func (u *Update) Install(ctx context.Context) error {
	fw := u.firmwareUpdater()
	if fw == nil {
		return ErrNoFirmwareUpdater
	}
	// Snapshot the current version so the monitor can detect the post-reboot
	// version change.
	if info, ok := u.UpdateInfo(); ok && info.CurrentFirmware != "" {
		u.SetVersionBeforeUpdate(info.CurrentFirmware)
	}
	if err := fw.TriggerFirmwareUpdate(ctx); err != nil {
		return err
	}
	u.SetInProgress(true)
	if m := u.installMonitorFn(); m != nil {
		m()
	}
	return nil
}

// SetFirmwareUpdater wires (or re-wires) the CCU-side install trigger under
// the update mutex. Use this instead of assigning the exported field directly
// when the wiring can run concurrently with [Install] — specifically the
// background WireHub recovery.
func (u *Update) SetFirmwareUpdater(fw FirmwareUpdater) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.FirmwareUpdater = fw
}

func (u *Update) firmwareUpdater() FirmwareUpdater {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.FirmwareUpdater
}

// SetInstallMonitor wires the progress monitor that [Install] launches after
// a successful trigger. The closure should start a bounded, detached
// goroutine (typically wrapping [Update.MonitorProgress]) that clears the
// in-progress flag once the CCU update completes. Nil disables monitoring.
func (u *Update) SetInstallMonitor(fn func()) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.installMonitor = fn
}

func (u *Update) installMonitorFn() func() {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.installMonitor
}

// SetInProgress sets the in-progress flag and fires update callbacks.
// Used by progress-monitoring code outside the Update struct to
// signal that an install has completed. Mirrors the in-progress flag
// Management in _monitor_update_progress
// (hub/update.py:175-225).
func (u *Update) SetInProgress(v bool) {
	u.mu.Lock()
	u.inProgress = v
	info := u.info
	cbs := make([]func(UpdateInfo), len(u.callbacks))
	copy(cbs, u.callbacks)
	u.mu.Unlock()
	for _, cb := range cbs {
		if cb != nil {
			cb(info)
		}
	}
}

// OnInfo records a fresh snapshot. Callbacks fire only when any
// field changed.
func (u *Update) OnInfo(info UpdateInfo) {
	u.mu.Lock()
	prev := u.info
	was := u.observed
	u.info = info
	u.observed = true
	cbs := make([]func(UpdateInfo), len(u.callbacks))
	copy(cbs, u.callbacks)
	u.mu.Unlock()
	if was && prev == info {
		return
	}
	for _, cb := range cbs {
		if cb != nil {
			cb(info)
		}
	}
}

// OnUpdate registers a subscription.
func (u *Update) OnUpdate(fn func(UpdateInfo)) func() {
	u.mu.Lock()
	u.callbacks = append(u.callbacks, fn)
	idx := len(u.callbacks) - 1
	u.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			u.mu.Lock()
			defer u.mu.Unlock()
			if idx < len(u.callbacks) {
				u.callbacks[idx] = nil
			}
		})
	}
}

// ErrUpdateAlreadyInProgress is returned when Install is called while
// a previous install is already in flight.
var ErrUpdateAlreadyInProgress = errors.New("update: install already in progress")

// VersionBeforeUpdate returns the firmware version that was current when
// Install was last called, together with a boolean that is false when no
// version snapshot was taken yet. Callers should call SetVersionBeforeUpdate
// before triggering Install so the change- detection logic can diff current
// vs. post-update versions.
func (u *Update) VersionBeforeUpdate() (string, bool) {
	u.mu.RLock()
	defer u.mu.RUnlock()
	return u.versionBeforeUpdate, u.hasVersionBefore
}

// SetVersionBeforeUpdate stores the current firmware version as the "before"
// snapshot. Should be called by the coordinator immediately before triggering
// the update via Install so the post-update check can detect the version
// change.
func (u *Update) SetVersionBeforeUpdate(version string) {
	u.mu.Lock()
	u.versionBeforeUpdate = version
	u.hasVersionBefore = true
	u.mu.Unlock()
}

// clearVersionBeforeUpdate resets the "before" snapshot once monitoring is
// done, mirroring `_version_before_update = None` (model/hub/update.py).
func (u *Update) clearVersionBeforeUpdate() {
	u.mu.Lock()
	u.versionBeforeUpdate = ""
	u.hasVersionBefore = false
	u.mu.Unlock()
}

// MonitorProgress watches the CCU firmware version after an install and
// clears the in-progress flag once the version changes, the deadline
// (interval × maxPoll) passes, or ctx is cancelled. It waits `interval`
// before each poll and tolerates poll errors — the CCU is unreachable while
// it reboots, so polling continues until the deadline. The caller supplies a
// pollFn that fetches the current firmware version from the CCU. Mirrors
// _monitor_update_progress (model/hub/update.py:175-222):
// sleep-first, break on version change, always clear in-progress on exit.
func (u *Update) MonitorProgress(ctx context.Context, pollFn func(ctx context.Context) (string, error), interval time.Duration, maxPoll int) {
	// Always clear the flag + baseline on exit (mirrors the Python `finally`).
	defer func() {
		u.SetInProgress(false)
		u.clearVersionBeforeUpdate()
	}()
	before, hasBefore := u.VersionBeforeUpdate()
	for range maxPoll {
		// Explicit pre-check so an already-cancelled ctx returns before the
		// (interruptible) inter-poll wait, deterministically skipping the poll.
		if ctx.Err() != nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
		current, err := pollFn(ctx)
		if err != nil {
			continue // CCU may be mid-reboot; keep polling until the deadline
		}
		if !hasBefore {
			return // no baseline → stop after the first successful poll
		}
		if current != before {
			// Version changed — update finished; publish the new version.
			info, _ := u.UpdateInfo()
			info.CurrentFirmware = current
			info.UpdateAvailable = false
			u.OnInfo(info)
			return
		}
	}
}

// EnabledByDefault reports that the firmware-update entity is always included
// in the default north-bound surface without requiring explicit operator opt-in.
func (*Update) EnabledByDefault() bool { return true }
