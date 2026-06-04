// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hub

import (
	"context"
	"errors"
	"sync"

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

// Install triggers the firmware update via the wired [FirmwareUpdater].
// Sets the in-progress flag immediately; the flag is NOT cleared
// automatically by this method — callers must call [SetInProgress](false)
// once the CCU update is done (e.g. from a progress-monitor goroutine).
func (u *Update) Install(ctx context.Context) error {
	if u.FirmwareUpdater == nil {
		return ErrNoFirmwareUpdater
	}
	if err := u.FirmwareUpdater.TriggerFirmwareUpdate(ctx); err != nil {
		return err
	}
	u.SetInProgress(true)
	return nil
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

// MonitorProgress polls the CCU for version changes after an install call and
// clears the in-progress flag once the version changes or the deadline
// passes. The caller supplies a pollFn that fetches the current firmware
// version from the CCU; polling stops when ctx is cancelled, the version
// changes, or maxPoll iterations are exhausted.
func (u *Update) MonitorProgress(ctx context.Context, pollFn func(ctx context.Context) (string, error), maxPoll int) {
	before, hasBefore := u.VersionBeforeUpdate()
	for range maxPoll {
		if ctx.Err() != nil {
			return
		}
		current, err := pollFn(ctx)
		if err != nil {
			continue
		}
		if hasBefore && current != before {
			// Version changed — update finished.
			info, _ := u.UpdateInfo()
			info.CurrentFirmware = current
			info.UpdateAvailable = false
			u.OnInfo(info)
			u.SetInProgress(false)
			return
		}
		if !hasBefore {
			// No baseline recorded — stop after first successful poll.
			u.SetInProgress(false)
			return
		}
	}
	// Max polls reached without version change — clear flag anyway.
	u.SetInProgress(false)
}

// EnabledByDefault reports that the firmware-update entity is always included
// in the default north-bound surface without requiring explicit operator opt-in.
func (*Update) EnabledByDefault() bool { return true }
