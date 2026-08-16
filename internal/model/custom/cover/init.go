// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package cover

// init registers the Constructor functions for every cover DeviceProfile
// onto the global custom.DefaultRegistry. This file is the D.12 delivery
// for the cover sub-package.
//
// Profile → Constructor mapping:
//
// - "IPCover" → NewCoverOrBlindFromChannel (IP Cover / IP Blind)
// - "RfCover" → NewCoverFromChannel (RF Cover / RF Blind)
// - "IPHdm" → NewBlindFromChannel (HmIP-HDM rolling door)
// - "IPGarage" → NewGarageFromChannel (HmIP-MOD-HO / TM)
//
// The registry is the process-wide DefaultRegistry; sub-packages call
// MustRegisterConstructor in init() — a panic here means a compile-time
// invariant was violated (two constructors for the same profile).

import (
	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

func init() {
	r := custom.DefaultRegistry()

	// IP Cover / IP Blind — both use IPCover profile.
	// The IPCover ProfileConfig carries LEVEL_2 + COMBINED_PARAMETER in
	// its Fields, so the constructor promotes a plain Cover to a Blind
	// when LEVEL_2 is present on the channel.
	r.MustRegisterConstructor(hmenum.DeviceProfile("IPCover"), newIPCoverConstructor)

	// RF Cover / RF Blind — RfCover profile.
	r.MustRegisterConstructor(hmenum.DeviceProfile("RfCover"), newRfCoverConstructor)

	// IP HDM rolling door — always a Blind (IP kind).
	r.MustRegisterConstructor(hmenum.DeviceProfile("IPHdm"), newIPHdmConstructor)

	// IP Garage — HmIP-MOD-HO / HmIP-MOD-TM.
	r.MustRegisterConstructor(hmenum.DeviceProfile("IPGarage"), newIPGarageConstructor)

	// Pre-populate the package-level scalar-arg key table so north-bound
	// adapters (MQTT bridge, REST) can resolve the key without first
	// instantiating a Cover / Blind. The same mappings are also
	// re-applied by the per-Source `RegisterServiceWithArg` calls in
	// [Cover.registerCoverServices] + [Blind.registerBlindServices] —
	// the global registration just makes them visible before any device
	// has materialised.
	payload.RegisterGlobalScalarArgKey("set_position", "position")
	payload.RegisterGlobalScalarArgKey("set_tilt", "tilt")
	payload.RegisterGlobalScalarArgKey(serviceCoverCommand, argCoverCommand)
}

// Predefined capability presets mirror the reference stack's cover
// capability constants — exported so north-bound adapters and tests can
// reference them by name rather than reconstructing the struct literal
// each time.

// CoverCaps is the basic cover capability set: position + stop.
var CoverCaps = custom.CoverCapabilities{
	SupportsPosition: true,
	SupportsStop:     true,
}

// BlindCaps is the blind capability set: position + tilt + stop.
var BlindCaps = custom.CoverCapabilities{
	SupportsPosition: true,
	SupportsTilt:     true,
	SupportsStop:     true,
}

// GarageCaps is the garage door capability set: position + stop + vent.
var GarageCaps = custom.CoverCapabilities{
	SupportsPosition: true,
	SupportsStop:     true,
	SupportsVent:     true,
}

// Python-exact sentinel names — exported aliases matching
// module-level constant names for parity and north-bound adapter use.

// COVER_CAPABILITIES is the Python-parity alias for [CoverCaps].
var COVER_CAPABILITIES = CoverCaps //nolint:revive // Python-exact name required for parity

// BLIND_CAPABILITIES is the Python-parity alias for [BlindCaps].
var BLIND_CAPABILITIES = BlindCaps //nolint:revive // Python-exact name required for parity

// GARAGE_CAPABILITIES is the Python-parity alias for [GarageCaps].
var GARAGE_CAPABILITIES = GarageCaps //nolint:revive // Python-exact name required for parity

// AWNING_CAPABILITIES is the awning capability set (position + stop).
// No Python equivalent exists yet; defined here for completeness so
// north-bound adapters can reference awning devices uniformly.
var AWNING_CAPABILITIES = custom.CoverCapabilities{ //nolint:revive // Python-parity naming
	SupportsPosition: true,
	SupportsStop:     true,
}

// CURTAIN_CAPABILITIES is the curtain capability set (position + stop).
var CURTAIN_CAPABILITIES = custom.CoverCapabilities{ //nolint:revive // Python-parity naming
	SupportsPosition: true,
	SupportsStop:     true,
}

// DAMPER_CAPABILITIES is the ventilation-damper capability set
// (position only; dampers typically have no mid-travel stop command).
var DAMPER_CAPABILITIES = custom.CoverCapabilities{ //nolint:revive // Python-parity naming
	SupportsPosition: true,
}

// writerFromChannel extracts the generic.Writer that the channel's LEVEL
// data point was built with. When no LEVEL DP exists the writer is nil;
// callers fall back to a nil Writer, which causes commands to fail cleanly
// (mirrors the behaviour of unhydrated channels in tests).
func writerFromChannel(ch *device.Channel) Writer {
	if ch == nil {
		return nil
	}
	if dp := custom.FloatField(ch, hmenum.ParameterLevel); dp != nil {
		return dp.Writer
	}
	return nil
}

// newIPCoverConstructor builds a Cover or Blind (IP kind) from a channel.
// The channel carries LEVEL (and LEVEL_2 for blinds). When LEVEL_2 is
// present the constructor promotes to a Blind so tilt control is available.
func newIPCoverConstructor(ch *device.Channel, rebased custom.RebasedChannelGroupConfig) (device.AttachableDataPoint, error) {
	w := writerFromChannel(ch)
	// IP Cover profile includes LEVEL_2 for blinds. Promote to Blind when
	// the channel actually carries that parameter.
	if custom.FloatField(ch, hmenum.ParameterLevel2) != nil {
		blind := NewBlind(BlindConfig{
			Channel: ch,
			Writer:  w,
			Capabilities: custom.CoverCapabilities{
				SupportsPosition: true,
				SupportsTilt:     true,
				SupportsStop:     true,
			},
			Kind: BlindKindIP,
		})
		applyGroupLevel(blind.Cover, ch, rebased)
		return blind, nil
	}
	cov := New(Config{
		Channel: ch,
		Writer:  w,
		Capabilities: custom.CoverCapabilities{
			SupportsPosition: true,
			SupportsStop:     true,
		},
		Variant: coverVariantFromModel(ch),
	})
	applyGroupLevel(cov, ch, rebased)
	return cov, nil
}

// applyGroupLevel resolves the profile's `FieldGroupLevel` mapping (when
// present) to the absolute channel + parameter and binds the corresponding
// LEVEL DP onto the cover via [Cover.SetGroupLevel].
//
// Whether the cover follows the group channel is the per-central
// `use_group_channel_for_cover_state` toggle (read off the device via
// [useGroupChannelForState]); it defaults to true, so any cover whose
// profile declares a GROUP_LEVEL field follows the group channel
// unless the operator turns the toggle off.
func applyGroupLevel(cov *Cover, ch *device.Channel, rebased custom.RebasedChannelGroupConfig) {
	if cov == nil || ch == nil || ch.Device() == nil {
		return
	}
	for chNo, fields := range rebased.ChannelFields {
		fv, ok := fields[hmenum.FieldGroupLevel]
		if !ok {
			continue
		}
		param, _ := custom.ResolveFieldValue(fv)
		if param == "" {
			continue
		}
		var groupCh *device.Channel
		for _, sibling := range ch.Device().Channels() {
			if sibling.Number == chNo {
				groupCh = sibling
				break
			}
		}
		if groupCh == nil {
			continue
		}
		// The group channel's LEVEL is read-only on the HmIP families
		// (HmIP-BROLL reports it as OPERATIONS 5 on channel 3 while the
		// action channels are read+write), so it resolves to a sensor
		// rather than a writable float. Asking for the writable shape
		// found nothing on any device.
		if dp := custom.GroupLevelField(groupCh, param); dp != nil {
			cov.SetGroupLevel(dp, useGroupChannelForState(ch))
			return
		}
	}
}

// newRfCoverConstructor builds a Cover or Blind (HM kind) from a channel. RF
// covers use LEVEL_COMBINED for combined position+tilt commands.
//
// HM-Sec-Win is detected here by device-model match and routed into the
// WindowDrive remap path.
func newRfCoverConstructor(ch *device.Channel, rebased custom.RebasedChannelGroupConfig) (device.AttachableDataPoint, error) {
	w := writerFromChannel(ch)
	// RF Blind: promote when LEVEL_2 is present.
	if custom.FloatField(ch, hmenum.ParameterLevel2) != nil {
		blind := NewBlind(BlindConfig{
			Channel: ch,
			Writer:  w,
			Capabilities: custom.CoverCapabilities{
				SupportsPosition: true,
				SupportsTilt:     true,
				SupportsStop:     true,
			},
			Kind: BlindKindHM,
		})
		applyGroupLevel(blind.Cover, ch, rebased)
		return blind, nil
	}
	cov := New(Config{
		Channel: ch,
		Writer:  w,
		Capabilities: custom.CoverCapabilities{
			SupportsPosition: true,
			SupportsStop:     true,
		},
		WindowDrive: isHmSecWin(ch),
		Variant:     coverVariantFromModel(ch),
	})
	applyGroupLevel(cov, ch, rebased)
	return cov, nil
}

// isHmSecWin reports whether ch belongs to a HM-Sec-Win window drive.
// The match is by exact device model — every other RF cover stays on
// the standard 0.0/1.0 level mapping.
func isHmSecWin(ch *device.Channel) bool {
	if ch == nil || ch.Device() == nil {
		return false
	}
	return ch.Device().Model == "HM-Sec-Win"
}

// coverVariantFromModel derives the [CoverVariant] for a channel by
// examining the device model string. This mirrors the
// EntityDescriptionRule approach (entity_helpers/descriptions/covers.py)
// but applied at data-point construction time so the variant is baked
// into the Cover instance rather than looked up in the northbound
// adapter.
//
// Mappings (HA device_class → Homematic models):
//
//	"window" — HM-Sec-Win
//	"shutter" — HmIP-BROLL, HmIP-FROLL, HM-LC-Bl1PBU-FM (and all other
//	 RF/IP covers without a more specific class)
//
// Awning, curtain, and damper are valid HA device classes but no
// Homematic device currently maps to them. The variant can still be
// set explicitly via [Config.Variant] for custom integrations.
func coverVariantFromModel(ch *device.Channel) CoverVariant {
	if ch == nil || ch.Device() == nil {
		return VariantShutter
	}
	if ch.Device().Model == "HM-Sec-Win" {
		return VariantWindow
	}
	return VariantShutter
}

// newIPHdmConstructor builds a Blind (IP kind) for the HmIP-HDM rolling
// door driver. HDM always exposes both LEVEL and LEVEL_2. The device_class
// Is "shade" — mirrors py:29-36 which maps
// HmIP-HDM1 → CoverDeviceClass.SHADE.
func newIPHdmConstructor(ch *device.Channel, _ custom.RebasedChannelGroupConfig) (device.AttachableDataPoint, error) {
	w := writerFromChannel(ch)
	return NewBlind(BlindConfig{
		Channel: ch,
		Writer:  w,
		Capabilities: custom.CoverCapabilities{
			// LEVEL is what the HDM drives; without SupportsPosition the
			// HA payload omits position_topic / set_position_topic and the
			// shade ships with open/close/stop but no position slider,
			// even though the Blind registers a working set_position.
			SupportsPosition: true,
			SupportsTilt:     true,
			SupportsStop:     true,
		},
		Kind:    BlindKindIP,
		Variant: VariantShade,
	}), nil
}

// newIPGarageConstructor builds a Garage for the HmIP-MOD-HO / HmIP-MOD-TM
// garage door drives. The writer is derived from any available DP on the
// channel; DOOR_COMMAND is write-only so DOOR_STATE is preferred. When
// neither DP carries a writer (some firmwares + the in-process CCU simulator
// only expose write-only ENUMs that the materializer doesn't promote to
// readable DPs), the channel-level writer installed by the
// DevicePipeline is the last-resort fallback.
func newIPGarageConstructor(ch *device.Channel, _ custom.RebasedChannelGroupConfig) (device.AttachableDataPoint, error) {
	var w Writer
	if ch != nil {
		// Prefer a string-sensor-backed writer; fall back to LEVEL if present.
		if dp := custom.FloatField(ch, hmenum.ParameterLevel); dp != nil {
			w = dp.Writer
		}
		// Garage has no LEVEL — derive writer from the door-command string
		// sensor when available.
		if w == nil {
			if dp := custom.StringSensorField(ch, hmenum.ParameterDoorCommand); dp != nil {
				w = dp.Writer
			}
		}
		// Last resort: the channel itself carries the wire-level writer
		// (installed by DevicePipeline.SetWriter). Use it directly so the
		// Garage CDP can dispatch even when no readable DP is on the
		// channel.
		if w == nil {
			if cw := ch.Writer(); cw != nil {
				w = cw
			}
		}
	}
	return NewGarage(GarageConfig{
		Channel: ch,
		Writer:  w,
		Capabilities: custom.CoverCapabilities{
			SupportsStop: true,
			// SupportsVent advertises the garage drive's intermediate "vent" position.
			SupportsVent: true,
		},
	}), nil
}

// useGroupChannelForState reads the per-central
// use_group_channel_for_cover_state toggle off the channel's device,
// defaulting to true when the channel or device is absent (test
// fixtures, pre-pipeline state).
func useGroupChannelForState(ch *device.Channel) bool {
	if ch == nil || ch.Device() == nil {
		return true
	}
	return ch.Device().UseGroupChannelForCoverState()
}
