// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hub

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/naming"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// ErrInstallModeInvalidDuration is returned for non-positive durations.
var ErrInstallModeInvalidDuration = errors.New("install mode: duration must be positive")

// ErrLocalInstallModeUnsupported is returned by [InstallMode.EnableLocal]
// when the installed writer cannot serve the keyserver-less HmIP LOCAL
// teach-in (non-HmIP interface, or a backend without the HmIP JSON-RPC
// surface).
var ErrLocalInstallModeUnsupported = errors.New("install mode: local teach-in not supported on this interface")

// ErrInstallModeInvalidLocalInput wraps SGTIN / device-key normalisation
// failures so the REST/WS surfaces can answer 422 instead of a generic
// upstream error.
var ErrInstallModeInvalidLocalInput = errors.New("install mode: invalid local teach-in input")

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

	// hasPublished / publishedEnabled / publishedRemainingS track the
	// (enabled, remaining_s) pair last reported by
	// [InstallMode.ConsumeChangeSincePublish], so the periodic refresh
	// job can skip re-publishing an identical steady-state tuple every
	// poll. See ConsumeChangeSincePublish for why this does not affect
	// the live countdown while install mode is actually active.
	hasPublished        bool
	publishedEnabled    bool
	publishedRemainingS int
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
	remain := max(time.Until(m.expiresAt), 0)
	return true, remain, m.observed
}

// ConsumeChangeSincePublish returns the current (enabled, remaining_s) pair
// and reports whether it differs from the pair the last call returned,
// then records the current pair as "last published". The first call always
// reports changed=true so the initial state is never suppressed.
//
// remaining_s is a live countdown while install mode is active (see
// InstallState), so this only ever suppresses the steady-state case — the
// same (false, 0) tuple every poll while install mode stays off the whole
// time — never a genuine countdown tick, because a running countdown's
// remaining_s changes on almost every call by construction.
func (m *InstallMode) ConsumeChangeSincePublish() (enabled bool, remainingS int, changed bool) {
	enabled, remaining, _ := m.InstallState()
	remainingS = int(remaining.Seconds())
	m.mu.Lock()
	defer m.mu.Unlock()
	changed = !m.hasPublished || enabled != m.publishedEnabled || remainingS != m.publishedRemainingS
	m.hasPublished = true
	m.publishedEnabled = enabled
	m.publishedRemainingS = remainingS
	return enabled, remainingS, changed
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
func (m *InstallMode) MQTTTopics(base, centralName string) payload.MQTTTopicSet {
	return payload.MQTTTopicSet{
		State: naming.MQTTHubInstallMode(base, centralName),
	}
}

// Press enables install mode for [DefaultInstallModeDuration], passed to the
// writer explicitly — this daemon never omits the duration and so never
// inherits a firmware default. Returns an error if no Writer is configured.
func (m *InstallMode) Press(ctx context.Context) error {
	return m.Enable(ctx, defaultInstallModeDuration)
}

// defaultInstallModeDuration is the default pairing window duration used
// by Press().
const defaultInstallModeDuration = DefaultInstallModeDuration

// DefaultInstallModeDuration is this daemon's own pairing window for a
// request that names no duration. A north plane that defaults an omitted
// duration reads it here rather than restating the number.
//
// It is not "the CCU default": no firmware default is ever exercised from
// here, because every path puts an explicit `time` on the wire, and the
// firmware defaults are not one number anyway. rfd's XML-RPC setInstallMode
// substitutes 60 s when `seconds` is omitted
// (../OpenCCU-Base/src/rfd/XmlRpcMethods.cpp:608-609, matching the method's
// own doc comment at :594-595, which states a maximum of 600 and a default
// of 60), and the CCU's own BidCos teach-in relies on exactly that by
// passing no seconds argument
// (../OpenCCU-Base/www/config/cp_add_device.cgi:903). The
// JSON-RPC Interface.setInstallModeHMIP surface carries its own default,
// which is not in the trees read here — unverified. 60 s is a defensible
// choice of ours that happens to agree with rfd; it is not derived from it.
const DefaultInstallModeDuration = 60 * time.Second

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

// EnableLocal opens the pairing window restricted to a single device
// identified by SGTIN + device key — the keyserver-less HmIP LOCAL
// teach-in. Both inputs are normalised here (the single shared point
// for REST and WS): label formatting (dashes, spaces, case) is
// stripped and the shorter Base32 key label form is converted to its
// 32-hex form. Unlike [InstallMode.EnableForDevice] there is no
// broadcast fallback — failing loudly beats silently pairing
// everything when the operator asked for a whitelisted teach-in.
func (m *InstallMode) EnableLocal(ctx context.Context, duration time.Duration, sgtin, key string) error {
	if duration <= 0 {
		return ErrInstallModeInvalidDuration
	}
	if m.Writer == nil {
		return errors.New("install mode: no writer configured")
	}
	normalizedSGTIN, err := hmproto.NormalizeSGTIN(sgtin)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInstallModeInvalidLocalInput, err)
	}
	normalizedKey, err := hmproto.NormalizeHmIPKey(key)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInstallModeInvalidLocalInput, err)
	}
	lw, ok := m.Writer.(LocalInstallModeWriter)
	if !ok {
		return ErrLocalInstallModeUnsupported
	}
	if err := lw.SetInstallModeLocal(ctx, m.InterfaceID, duration, normalizedSGTIN, normalizedKey); err != nil {
		return err
	}
	m.OnState(true, duration)
	return nil
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
