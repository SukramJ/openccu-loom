// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hub

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/naming"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ErrInstallModeInvalidDuration is returned for non-positive durations.
var ErrInstallModeInvalidDuration = errors.New("install mode: duration must be positive")

// InstallMode captures the CCU's pairing/install-mode state for a
// single interface. The remaining duration is exposed as a
// [time.Duration]; zero means "not active".
type InstallMode struct {
	InterfaceID string
	Writer      InstallModeWriter

	// ServiceRegistry implements the write-half of [payload.Source].
	// Each InstallMode instance gets its own registry so enable/disable
	// services are registered per-instance.
	payload.ServiceRegistry

	mu        sync.RWMutex
	enabled   bool
	remaining time.Duration
	expiresAt time.Time
	observed  bool
	callbacks []func(enabled bool, remaining time.Duration)
}

// NewInstallMode constructs an InstallMode for interfaceID.
func NewInstallMode(interfaceID string, w InstallModeWriter) *InstallMode {
	m := &InstallMode{InterfaceID: interfaceID, Writer: w}
	m.RegisterService("enable", func(ctx context.Context, params map[string]any, _ hmenum.CommandPriority) error {
		secs, err := payload.ParamInt32(params, "seconds")
		if err != nil {
			return err
		}
		return m.Enable(ctx, time.Duration(secs)*time.Second)
	})
	m.RegisterService("disable", func(ctx context.Context, _ map[string]any, _ hmenum.CommandPriority) error {
		return m.Disable(ctx)
	})
	return m
}

// InstallState returns the last observed enabled flag and remaining duration.
// When enabled is true, Remaining reflects the time-to-expiry computed
// from the last OnState timestamp — it can return zero if the window
// has elapsed without another update.
func (m *InstallMode) InstallState() (enabled bool, remaining time.Duration, observed bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if !m.enabled {
		return false, 0, m.observed
	}
	remain := time.Until(m.expiresAt)
	if remain < 0 {
		remain = 0
	}
	return true, remain, m.observed
}

// OnState records a CCU-driven install-mode update.
func (m *InstallMode) OnState(enabled bool, remaining time.Duration) {
	now := time.Now()
	m.mu.Lock()
	m.enabled = enabled
	m.remaining = remaining
	if enabled {
		m.expiresAt = now.Add(remaining)
	} else {
		m.expiresAt = time.Time{}
	}
	m.observed = true
	cbs := make([]func(enabled bool, remaining time.Duration), len(m.callbacks))
	copy(cbs, m.callbacks)
	m.mu.Unlock()
	for _, cb := range cbs {
		if cb != nil {
			cb(enabled, remaining)
		}
	}
}

// Enable switches install mode on for duration. Returns
// [ErrInstallModeInvalidDuration] on non-positive input.
func (m *InstallMode) Enable(ctx context.Context, duration time.Duration) error {
	if duration <= 0 {
		return ErrInstallModeInvalidDuration
	}
	if m.Writer == nil {
		return errors.New("install mode: no writer configured")
	}
	if err := m.Writer.SetInstallMode(ctx, m.InterfaceID, true, duration); err != nil {
		return err
	}
	m.OnState(true, duration)
	return nil
}

// Disable switches install mode off.
func (m *InstallMode) Disable(ctx context.Context) error {
	if m.Writer == nil {
		return errors.New("install mode: no writer configured")
	}
	if err := m.Writer.SetInstallMode(ctx, m.InterfaceID, false, 0); err != nil {
		return err
	}
	m.OnState(false, 0)
	return nil
}

// OnUpdate registers a subscription.
func (m *InstallMode) OnUpdate(fn func(enabled bool, remaining time.Duration)) func() {
	m.mu.Lock()
	m.callbacks = append(m.callbacks, fn)
	idx := len(m.callbacks) - 1
	m.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			m.mu.Lock()
			defer m.mu.Unlock()
			if idx < len(m.callbacks) {
				m.callbacks[idx] = nil
			}
		})
	}
}

// TranslationKey returns the HA translation key used to localise the
// install-mode sensor entity.
func (m *InstallMode) TranslationKey() string { return "install_mode" }

// MQTTTopics implements [payload.MQTTAddressable] — the canonical
// ADR-0011 install-mode topic `<base>/<central>/hub/install_mode`.
// InstallMode lives per-interface in the model but its broker topic
// is central-weite; the adapter aggregates remaining seconds across
// interfaces before publishing.
func (m *InstallMode) MQTTTopics(base, central string) payload.MQTTTopicSet {
	return payload.MQTTTopicSet{
		State: naming.MQTTHubInstallMode(base, central),
	}
}

// Press enables install mode for the default duration (60 seconds).
// which calls activate with the default time. Returns an error if no
// Writer is configured.
func (m *InstallMode) Press(ctx context.Context) error {
	return m.Enable(ctx, defaultInstallModeDuration)
}

// defaultInstallModeDuration is the default pairing window duration used
// by Press().
const defaultInstallModeDuration = 60 * time.Second

// EnableForDevice enables install mode and restricts pairing to the given
// device address. When deviceAddress is empty this is equivalent to [Enable]
// (all devices may pair). When the installed writer also implements
// [DeviceInstallModeWriter], the address is forwarded to the CCU so only the
// named device enters pairing mode; otherwise the call degrades to a
// broadcast [Enable].
func (m *InstallMode) EnableForDevice(ctx context.Context, duration time.Duration, deviceAddress string) error {
	if duration <= 0 {
		return ErrInstallModeInvalidDuration
	}
	if m.Writer == nil {
		return errors.New("install mode: no writer configured")
	}
	if deviceAddress == "" {
		return m.Enable(ctx, duration)
	}
	if dw, ok := m.Writer.(DeviceInstallModeWriter); ok {
		if err := dw.SetInstallModeForDevice(ctx, m.InterfaceID, duration, deviceAddress); err != nil {
			return err
		}
		m.OnState(true, duration)
		return nil
	}
	// Writer does not support targeted pairing — fall back to broadcast.
	return m.Enable(ctx, duration)
}

// IsActive reports whether install mode is currently enabled and the
// countdown has not yet elapsed. Convenience wrapper around [State]
// That mirrors.
func (m *InstallMode) IsActive() bool {
	enabled, remaining, _ := m.InstallState()
	return enabled && remaining > 0
}

// Remaining returns the time-to-expiry of the current install-mode
// window, or zero when none is active. Mirrors the countdown
// Behaviour
// the value is computed from `expiresAt` so consumers always see the
// live remaining duration without a background timer goroutine.
func (m *InstallMode) Remaining() time.Duration {
	_, remaining, _ := m.InstallState()
	return remaining
}

// EnabledByDefault reports that the install-mode entity is always included in
// the default north-bound surface. InstallMode is a structural hub entity that
// operators expect to be visible without explicit opt-in.
func (*InstallMode) EnabledByDefault() bool { return true }

// LegacyName returns the original pre-slug name stored on the CCU.
// InstallMode is a structural aggregate without a CCU-side variable
// name, so this always returns "".
func (*InstallMode) LegacyName() string { return "" }

// Description returns the optional human-readable description. InstallMode
// has no CCU-side description field, so this always returns "".
func (*InstallMode) Description() string { return "" }
