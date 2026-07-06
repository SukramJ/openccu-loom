// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// init.go registers Constructor functions for every siren
// DeviceProfile in the process-wide custom registry ( D.12).
//
// Three profiles are covered here:
//
// - IPSiren → [Siren] (HmIP-ASIR; acoustic + optical + duration)
// - IPSirenSmoke → [SmokeSiren] (HmIP-SWSD; smoke-command only, no config)
// - IPSoundPlayer → [SoundPlayer] (HmIP-MP3P channel 2; soundfile + volume)
//
// IPSoundPlayerLed (HmIP-MP3P channel 6) is registered in the light package
// because it is categorised as DataPointCategoryLight — it controls the device's
// RGB LED strip, not the acoustic siren.
// CustomDpSoundPlayerLed in model/custom/light.py
//
// Capabilities mirror
// - IP siren: acoustic + optical + duration (BASIC_SIREN_CAPABILITIES).
// - Smoke siren: no configurable options (SMOKE_SENSOR_SIREN_CAPABILITIES).
// - Sound player: duration + tones (SOUND_PLAYER_CAPABILITIES).

package siren

import (
	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func init() {
	reg := custom.DefaultRegistry()

	reg.MustRegisterConstructor(hmenum.DeviceProfileIPSiren, ipSirenConstructor)
	reg.MustRegisterConstructor(hmenum.DeviceProfileIPSirenSmoke, ipSirenSmokeConstructor)
	reg.MustRegisterConstructor(hmenum.DeviceProfileIPSoundPlayer, ipSoundPlayerConstructor)
	// IPSoundPlayerLed is registered in the light package (category=light).
}

// Predefined capability presets mirror
// capabilities/siren.py — exported so north-bound adapters and tests can
// reference them by name rather than reconstructing the struct literal.

// BasicSirenCaps is the standard IP siren capability set: acoustic alarm,
// optical alarm, and configurable duration.
var BasicSirenCaps = custom.SirenCapabilities{
	SupportsAcoustic: true,
	SupportsOptical:  true,
	SupportsDuration: true,
}

// SmokeSensorSirenCaps is the smoke-sensor siren capability set: acoustic
// alarm only (no optical, no duration).
var SmokeSensorSirenCaps = custom.SirenCapabilities{
	SupportsAcoustic: true,
}

// SoundPlayerCaps is the sound-player capability set: duration + soundfiles
// (no acoustic tone selector, no optical).
var SoundPlayerCaps = custom.SirenCapabilities{
	SupportsDuration:   true,
	SupportsSoundfiles: true,
}

// Python-exact sentinel names — exported aliases matching
// module-level constant names for parity and north-bound adapter use.

// BASIC_SIREN_CAPABILITIES is the Python-parity alias for [BasicSirenCaps].
var BASIC_SIREN_CAPABILITIES = BasicSirenCaps //nolint:revive // Python-exact name required for parity

// SMOKE_SENSOR_SIREN_CAPABILITIES is the Python-parity alias for [SmokeSensorSirenCaps].
var SMOKE_SENSOR_SIREN_CAPABILITIES = SmokeSensorSirenCaps //nolint:revive // Python-exact name required for parity

// SOUND_PLAYER_CAPABILITIES is the Python-parity alias for [SoundPlayerCaps].
var SOUND_PLAYER_CAPABILITIES = SoundPlayerCaps //nolint:revive // Python-exact name required for parity

// ipSirenCapabilities describes acoustic alarm, optical alarm, and
// configurable duration support.
var ipSirenCapabilities = custom.SirenCapabilities{
	SupportsAcoustic: true,
	SupportsOptical:  true,
	SupportsDuration: true,
}

func ipSirenConstructor(channel *device.Channel, _ custom.RebasedChannelGroupConfig) (device.AttachableDataPoint, error) {
	return New(Config{
		Channel:      channel,
		Writer:       channel.Writer(),
		Capabilities: ipSirenCapabilities,
	}), nil
}

func ipSirenSmokeConstructor(channel *device.Channel, _ custom.RebasedChannelGroupConfig) (device.AttachableDataPoint, error) {
	return NewSmokeSiren(SmokeSirenConfig{
		Channel: channel,
		Writer:  channel.Writer(),
	}), nil
}

func ipSoundPlayerConstructor(channel *device.Channel, _ custom.RebasedChannelGroupConfig) (device.AttachableDataPoint, error) {
	return NewSoundPlayer(SoundPlayerConfig{
		Channel: channel,
		Writer:  channel.Writer(),
	}), nil
}

// Compile-time assertions: concrete siren types satisfy [device.AttachableDataPoint].
var (
	_ device.AttachableDataPoint = (*Siren)(nil)
	_ device.AttachableDataPoint = (*SmokeSiren)(nil)
	_ device.AttachableDataPoint = (*SoundPlayer)(nil)
)
