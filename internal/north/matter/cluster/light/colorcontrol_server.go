// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package light provides standalone Matter cluster-server implementations
// for light device types (OnOffLight, DimmableLight, ColorTemperatureLight,
// ExtendedColorLight). The servers in this package are thin, stateless
// wrappers whose state is owned by the caller; they implement
// [mattercontract.ClusterServer] and the optional lister interfaces so the
// endpoint assembler in internal/north/matter/endpoint can attach them without
// knowing the device-specific model types.
//
// ColorTemperatureLight (0x010C) requires ColorControl (0x0300) in CT-only
// mode. The [ColorControlServer] here covers the minimum mandatory surface
// for chip-tool conformance and Apple Home pairing.
package light

import (
	"context"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster"
	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/wire"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// ColorControlServerConfig holds the static configuration injected at
// construction time. All fields are in mired units.
type ColorControlServerConfig struct {
	// MinMireds is the physical lower bound of the CT range expressed in
	// mireds. Corresponds to ColorTempPhysicalMinMireds (0x400B). Higher
	// Kelvin → lower mired value, so MinMireds < MaxMireds.
	MinMireds uint16
	// MaxMireds is the physical upper bound of the CT range in mireds.
	// Corresponds to ColorTempPhysicalMaxMireds (0x400C).
	MaxMireds uint16
	// InitialMireds is the starting value for CurrentColorTemperature (0x0007).
	InitialMireds uint16
}

// DefaultColorControlServerConfig returns a sensible default: warm-cool
// LED range (153–500 mireds, corresponding to ~6535 K–2000 K). Device
// profiles that expose narrower Kelvin ranges should compute their own
// config from their Min/MaxKelvin fields before calling
// [NewColorControlServer].
func DefaultColorControlServerConfig() ColorControlServerConfig {
	return ColorControlServerConfig{
		MinMireds:     153, // ≈ 6535 K
		MaxMireds:     500, // ≈ 2000 K
		InitialMireds: 370, // ≈ 2700 K, warm white default
	}
}

// ColorTemperatureWriter is the optional sink a [ColorControlServer]
// drives on a successful MoveToColorTemperature. Implementations translate
// the cropped mired value into the device's native unit — HM exposes
// COLOR_TEMPERATURE in Kelvin (mireds = 1_000_000 / Kelvin) — and push it
// to the CCU. A write error aborts the command and leaves the in-process
// CurrentColorTemperatureMireds attribute unchanged, so the reported state
// never claims a value the device did not accept.
type ColorTemperatureWriter interface {
	SetColorTemperatureMireds(ctx context.Context, mireds uint16, priority hmenum.CommandPriority) error
}

// ColorControlServer is a minimal CT-only ColorControl cluster server
// (cluster 0x0300) for the ColorTemperatureLight (0x010C) device type.
// It holds the current CT value in-process and, when a
// [ColorTemperatureWriter] is wired via [ColorControlServer.SetWriter],
// pushes every MoveToColorTemperature down to the device. Without a writer
// the server updates in-process state only and answers Success — the
// chip-tool conformance / test path that needs no live CCU target.
type ColorControlServer struct {
	cfg     ColorControlServerConfig
	current uint16
	writer  ColorTemperatureWriter
}

// NewColorControlServer constructs a ColorControlServer with the given
// configuration. cfg.InitialMireds is clamped to [cfg.MinMireds,
// cfg.MaxMireds].
func NewColorControlServer(cfg ColorControlServerConfig) *ColorControlServer {
	init := min(max(cfg.InitialMireds, cfg.MinMireds), cfg.MaxMireds)
	return &ColorControlServer{cfg: cfg, current: init}
}

// SetWriter wires the optional write-through sink. Pass nil to detach.
// Intended to be called once at endpoint-assembly time, before the server
// handles any command; the IM dispatcher serializes command delivery, so
// no further synchronisation is needed.
func (s *ColorControlServer) SetWriter(w ColorTemperatureWriter) { s.writer = w }

// MatterClusterID returns the ColorControl cluster ID (0x0300).
func (s *ColorControlServer) MatterClusterID() uint32 { return wire.ColorControlClusterID }

// MatterRead returns attribute values for the CT-only subset. Returns
// (nil, false) for unrecognised attribute IDs so the IM dispatcher can
// fall back to the global attribute path.
//
// CurrentHue / CurrentSaturation (HS conformance) and CurrentX /
// CurrentY (XY conformance) are NOT served — CT-only mode sets neither
// the HS nor the XY feature bit, so advertising those attributes would
// violate the conformance rules in Matter §3.2.6.
func (s *ColorControlServer) MatterRead(attrID uint32) (any, bool) {
	switch attrID {
	case wire.ColorCtrlAttrColorTemperatureMireds:
		return s.current, true
	case wire.ColorCtrlAttrColorMode, wire.ColorCtrlAttrEnhancedColorMode:
		// ColorMode 2 = ColorTemperatureMireds is the active mode.
		return colorModeCT, true
	case wire.ColorCtrlAttrOptions:
		// Options bitmap8: 0 = execute command unconditionally.
		return uint8(0), true
	case wire.ColorCtrlAttrColorCapabilities:
		// CT feature bit only (bit 4).
		return colorCapCT, true
	case wire.ColorCtrlAttrColorTempPhysicalMin:
		return s.cfg.MinMireds, true
	case wire.ColorCtrlAttrColorTempPhysicalMax:
		return s.cfg.MaxMireds, true
	case wire.ColorCtrlAttrNumberOfPrimaries:
		// NumberOfPrimaries is mandatory (spec §3.2.6.6) with Quality X
		// (nullable). CT-only mode has no primary colours; return null
		// per the Quality X sentinel contract (nil value, present=true).
		return nil, true
	case cluster.AttrGlobalFeatureMap:
		// CT feature = bit 4 (constraint "4" in matter.js color-control.element.ts).
		return colorFeatureCT, true
	case cluster.AttrGlobalClusterRevision:
		return ColorControlClusterRevision, true
	default:
		return nil, false
	}
}

// MatterWrite rejects all attribute writes; ColorControl attributes are
// read-only — commands are the intended mutation path.
func (s *ColorControlServer) MatterWrite(_ context.Context, attrID uint32, _ any) error {
	return fmt.Errorf("colorcontrol: attribute 0x%04X is not writable", attrID)
}

// matterDispatchPriority is the southbound urgency every Matter-driven
// write and invoke carries. The bridge is a controller-facing
// foreground path — a tap in a Matter app must not queue behind a
// background refresh — so it dispatches at High, and the cluster
// contract no longer negotiates it per call.
//
// Spelled out as a constant rather than left to a variable: the zero
// value of [hmenum.CommandPriority] is Critical, so anything that
// reached this call defaulted would silently escalate every bridged
// command.
const matterDispatchPriority = hmenum.CommandPriorityHigh

// MatterInvoke handles ColorControl commands. MoveToColorTemperature
// (0x0A) crops the target to [MinMireds, MaxMireds] and updates the
// in-process CT state per matter.js ColorControlServer.ts:moveToColorTemperatureLogic
// (lines 973-980) which passes the value through #cropColorTemperature (line 221).
// When a [ColorTemperatureWriter] is wired the cropped value is pushed to
// the device first; a write error aborts the command and leaves the
// reported state unchanged. The CT-move and CT-step commands (0x4B, 0x4C)
// and StopMoveStep (0x47) are accepted as no-ops — HM devices have no
// continuous-rate CT sweep.
func (s *ColorControlServer) MatterInvoke(ctx context.Context, cmdID uint32, fields any) (any, error) {
	switch cmdID {
	case wire.ColorCtrlCmdMoveToColorTemperature:
		// The bridge's command-fields reader decodes the payload into the
		// typed wire request (tag 0 = ColorTemperatureMireds); the generic
		// tag-keyed map (uint64 values) is accepted as a fallback.
		var target uint16
		switch v := fields.(type) {
		case wire.MoveToColorTemperatureRequest:
			target = v.ColorTemperatureMireds
		case map[uint8]any:
			if raw, ok := v[0].(uint64); ok {
				target = uint16(raw & 0xFFFF)
			}
		}
		// Crop to [MinMireds, MaxMireds]. Mirrors matter.js
		// ColorControlServer.ts:#cropColorTemperature (line 221):
		//   set colorTemperatureMireds = #cropColorTemperature(value).
		if target < s.cfg.MinMireds {
			target = s.cfg.MinMireds
		}
		if target > s.cfg.MaxMireds {
			target = s.cfg.MaxMireds
		}
		// Push to the device before committing the reported state, so a
		// rejected write never leaves CurrentColorTemperatureMireds claiming
		// a value the CCU did not accept.
		if s.writer != nil {
			if err := s.writer.SetColorTemperatureMireds(ctx, target, matterDispatchPriority); err != nil {
				return nil, fmt.Errorf("colorcontrol: MoveToColorTemperature write-through: %w", err)
			}
		}
		s.current = target
		return nil, nil
	case wire.ColorCtrlCmdMoveColorTemperature, wire.ColorCtrlCmdStepColorTemperature, wire.ColorCtrlCmdStopMoveStep:
		// No continuous-rate CT sweep on HM; accept and return Success.
		return nil, nil
	default:
		return nil, fmt.Errorf("colorcontrol: unknown command 0x%02X", cmdID)
	}
}

// MatterReportable returns the CT attribute that triggers reports on
// value change.
func (s *ColorControlServer) MatterReportable() []uint32 {
	return []uint32{wire.ColorCtrlAttrColorTemperatureMireds}
}

// MatterAttributes lists the CT-only attribute set served by MatterRead.
// CurrentHue / CurrentSaturation (HS conformance) and CurrentX / CurrentY
// (XY conformance) are excluded — they are only legal when the HS or XY
// feature bit is set in FeatureMap. CT-only FeatureMap has neither.
func (s *ColorControlServer) MatterAttributes() []uint32 {
	return []uint32{
		wire.ColorCtrlAttrColorTemperatureMireds,
		wire.ColorCtrlAttrColorMode,
		wire.ColorCtrlAttrOptions,
		wire.ColorCtrlAttrNumberOfPrimaries,
		wire.ColorCtrlAttrEnhancedColorMode,
		wire.ColorCtrlAttrColorCapabilities,
		wire.ColorCtrlAttrColorTempPhysicalMin,
		wire.ColorCtrlAttrColorTempPhysicalMax,
	}
}

// MatterAcceptedCommands lists the command IDs the server handles via
// MatterInvoke. Required by MatterClusterCommandLister so
// AcceptedCommandList (0xFFF9) is populated correctly during
// commissioning.
func (s *ColorControlServer) MatterAcceptedCommands() []uint32 {
	return []uint32{
		wire.ColorCtrlCmdMoveToColorTemperature,
		wire.ColorCtrlCmdStopMoveStep,
		wire.ColorCtrlCmdMoveColorTemperature,
		wire.ColorCtrlCmdStepColorTemperature,
	}
}

// MatterGeneratedCommands returns nil; ColorControl commands carry no
// response payload.
func (s *ColorControlServer) MatterGeneratedCommands() []uint32 { return nil }

// colorModeCT is the ColorMode / EnhancedColorMode enum value for
// ColorTemperatureMireds mode (Matter §3.2.7.7 / §3.2.7.18).
const colorModeCT uint8 = 2

// colorCapCT is the ColorCapabilities bitmap value advertising CT-only
// capability (bit 4 = CT feature, Matter §3.2.7.19).
const colorCapCT uint16 = 1 << 4

// colorFeatureCT is the FeatureMap bitmap for CT-only mode.
// Matter §3.2.4 Feature bit CT has constraint "4"
// (packages/model/src/standard/elements/color-control.element.ts).
const colorFeatureCT uint32 = 1 << 4

// ColorControlClusterRevision is the cluster revision for ColorControl
// pinned to matter.js HEAD (@matter/model 0.16.11).
const ColorControlClusterRevision uint16 = 9
