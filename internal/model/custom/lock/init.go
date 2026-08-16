// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// init.go registers Constructor functions for every lock
// DeviceProfile in the process-wide custom registry ( D.12).
//
// Four profiles are covered:
//
// - IPLock → KindIP (LOCK_TARGET_LEVEL write path)
// - RfLock → KindRF (STATE/OPEN write path)
// - IPButtonLock → KindButton (GLOBAL_BUTTON_LOCK visibility only)
// - RFButtonLock → KindButton (GLOBAL_BUTTON_LOCK visibility only)
//
// Button-lock profiles only control visibility of GLOBAL_BUTTON_LOCK;
// their custom DP uses KindButton and has no open/relock capabilities.
//
// Capabilities mirror
// - IP locks support open (LOCK_TARGET_LEVEL=2).
// - RF locks support open (OPEN parameter).
// - Button locks have no open capability.

package lock

import (
	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func init() {
	reg := custom.DefaultRegistry()

	reg.MustRegisterConstructor(hmenum.DeviceProfileIPLock, ipLockConstructor)
	reg.MustRegisterConstructor(hmenum.DeviceProfileRfLock, rfLockConstructor)
	reg.MustRegisterConstructor(hmenum.DeviceProfileIPButtonLock, ipButtonLockConstructor)
	reg.MustRegisterConstructor(hmenum.DeviceProfileRFButtonLock, rfButtonLockConstructor)

	// Pre-populate the package-level scalar-arg key table so north-bound
	// adapters can resolve the key before any lock has materialised. The
	// same mapping is re-applied per Source by [Lock.registerServices].
	payload.RegisterGlobalScalarArgKey(serviceLockCommand, argLockCommand)
}

// Predefined capability presets mirror
// capabilities/lock.py — exported so north-bound adapters and tests can
// reference them by name rather than reconstructing the struct literal.

// IPLockCaps mirrors capabilities/lock.py:40.
var IPLockCaps = custom.LockCapabilities{SupportsOpen: true}

// ButtonLockCaps mirrors capabilities/lock.py:41.
var ButtonLockCaps = custom.LockCapabilities{SupportsOpen: false}

// SmartDoorLockCaps describes a smart door lock that supports OPEN
// (open=true). Defined here for parity completeness; no DeviceProfile
// constructor currently registers this variant — add one when a smart-door-
// lock profile is generated. Mirrors capabilities/lock.py:42.
var SmartDoorLockCaps = custom.LockCapabilities{SupportsOpen: true}

// Python-exact sentinel names — exported aliases matching
// module-level constant names for parity and north-bound adapter use.

// IP_LOCK_CAPABILITIES is the Python-parity alias for [IPLockCaps].
var IP_LOCK_CAPABILITIES = IPLockCaps //nolint:revive // Python-exact name required for parity

// BUTTON_LOCK_CAPABILITIES is the Python-parity alias for [ButtonLockCaps].
var BUTTON_LOCK_CAPABILITIES = ButtonLockCaps //nolint:revive // Python-exact name required for parity

// SMART_DOOR_LOCK_CAPABILITIES is the Python-parity alias for [SmartDoorLockCaps].
var SMART_DOOR_LOCK_CAPABILITIES = SmartDoorLockCaps //nolint:revive // Python-exact name required for parity

// ipLockCapabilities describes an IP lock whose OPEN action is
// supported via LOCK_TARGET_LEVEL=2.
var ipLockCapabilities = custom.LockCapabilities{
	SupportsOpen: true,
}

// rfLockCapabilities describes an RF lock whose OPEN action is
// supported via the OPEN parameter.
var rfLockCapabilities = custom.LockCapabilities{
	SupportsOpen: true,
}

// buttonLockCapabilities: button locks have no open capability.
var buttonLockCapabilities = custom.LockCapabilities{
	SupportsOpen: false,
}

func ipLockConstructor(channel *device.Channel, _ custom.RebasedChannelGroupConfig) (device.AttachableDataPoint, error) {
	return New(Config{
		Channel:      channel,
		Writer:       channel.Writer(),
		Capabilities: ipLockCapabilities,
		Kind:         KindIP,
	}), nil
}

func rfLockConstructor(channel *device.Channel, _ custom.RebasedChannelGroupConfig) (device.AttachableDataPoint, error) {
	return New(Config{
		Channel:      channel,
		Writer:       channel.Writer(),
		Capabilities: rfLockCapabilities,
		Kind:         KindRF,
	}), nil
}

// hasButtonLockField reports whether the channel carries the button-lock wire
// parameter (GLOBAL_BUTTON_LOCK, BUTTON_LOCK fallback). The reference
// CustomDpButtonLock declares BUTTON_LOCK as a required field, so a device whose
// model matches the button-lock profile but whose channel lacks the parameter
// (e.g. HmIP-eTRV-C-2) materialises no lock entity.
func hasButtonLockField(ch *device.Channel) bool {
	return custom.SwitchField(ch, hmenum.ParameterGlobalButtonLock) != nil ||
		custom.SwitchField(ch, hmenum.ParameterButtonLock) != nil
}

func ipButtonLockConstructor(channel *device.Channel, _ custom.RebasedChannelGroupConfig) (device.AttachableDataPoint, error) {
	if !hasButtonLockField(channel) {
		return nil, nil //nolint:nilnil // required field absent — no custom DP, reference parity
	}
	return New(Config{
		Channel:      channel,
		Writer:       channel.Writer(),
		Capabilities: buttonLockCapabilities,
		Kind:         KindButton,
	}), nil
}

func rfButtonLockConstructor(channel *device.Channel, _ custom.RebasedChannelGroupConfig) (device.AttachableDataPoint, error) {
	if !hasButtonLockField(channel) {
		return nil, nil //nolint:nilnil // required field absent — no custom DP, reference parity
	}
	return New(Config{
		Channel:      channel,
		Writer:       channel.Writer(),
		Capabilities: buttonLockCapabilities,
		Kind:         KindButton,
	}), nil
}

// Compile-time assertion: *Lock satisfies [device.AttachableDataPoint].
var _ device.AttachableDataPoint = (*Lock)(nil)
