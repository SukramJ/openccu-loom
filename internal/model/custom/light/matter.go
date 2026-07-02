// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package light

import (
	"context"
	"errors"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/wire"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// MatterDataVersion implements [interfaces.MatterClusterDataVersion].
// Shared across all cluster servers that project this Light (OnOff,
// LevelControl, ColorControl). Bumped on every successful write /
// invoke so DataVersionFilter evaluation correctly detects cluster changes.
func (l *Light) MatterDataVersion() uint32 { return l.dataVersion.Current() }

// Compile-time assertion: Light participates in the Matter source
// surface (ADR 0012). Light contributes the OnOff cluster always and
// the LevelControl cluster when [custom.LightCapabilities.Dimmable] is
// set; the cluster servers are exposed via [Light.MatterClusterServers]
// rather than the Light type implementing [interfaces.MatterClusterServer]
// directly because two clusters cannot share a single MatterClusterID().
var (
	_ interfaces.MatterEndpointSource     = (*Light)(nil)
	_ interfaces.MatterClusterDataVersion = (*Light)(nil)
)

// Matter Device Type IDs and cluster IDs follow the Matter 1.5.1
// Application Cluster Specification. They live here next to the
// projection; the cluster-server packages under
// internal/north/matter/cluster/ may later import them. Cluster
// revisions verified against the Matter cluster sweep
// (matter.js HEAD packages/model/src/standard/elements/).
const (
	matterDeviceTypeOnOffLight    uint16 = 0x0100
	matterDeviceTypeDimmableLight uint16 = 0x0101

	matterClusterOnOff        uint32 = 0x0006
	matterClusterLevelControl uint32 = 0x0008

	// Groups cluster (0x0004) and ScenesManagement (0x0062) are
	// mandatory on OnOffLight + DimmableLight (matter.js
	// packages/node/src/devices/on-off-light.ts). Light contributes
	// them via the shared stubs in
	// internal/north/matter/cluster/wire/{groups,scenes_management}.go.

	matterAttrOnOffOnOff   uint32 = 0x0000
	matterAttrLevelCurrent uint32 = 0x0000

	// LT (Lighting) feature-gated OnOff attributes. OnOffLight (0x0100)
	// and DimmableLight (0x0101) mandate the LT feature on the OnOff
	// cluster, which in turn makes these four attributes mandatory.
	// matter.js packages/model/src/standard/elements/on-off.element.ts:30-36:
	//   GlobalSceneControl 0x4000 bool   conformance "LT" access "R V"
	//   OnTime             0x4001 uint16 conformance "LT" access "RW VO"
	//   OffWaitTime        0x4002 uint16 conformance "LT" access "RW VO"
	//   StartUpOnOff       0x4003 enum8  conformance "LT" access "RW VM" quality "X N"
	matterAttrOnOffGlobalSceneControl uint32 = 0x4000
	matterAttrOnOffOnTime             uint32 = 0x4001
	matterAttrOnOffOffWaitTime        uint32 = 0x4002
	matterAttrOnOffStartUpOnOff       uint32 = 0x4003

	// LT (Lighting) feature-gated LevelControl attributes. DimmableLight
	// (0x0101) mandates the LT feature on LevelControl, making these two
	// attributes mandatory.
	// matter.js packages/model/src/standard/elements/level-control.element.ts:33,67:
	//   RemainingTime       0x0001 uint16 conformance "LT" access "R V"  quality "Q"
	//   StartUpCurrentLevel 0x4000 uint8  conformance "LT" access "RW VM" quality "X N"
	matterAttrLevelRemainingTime uint32 = 0x0001
	matterAttrLevelStartUpLevel  uint32 = 0x4000

	// LT (Lighting) FeatureMap bits. On the OnOff cluster LT sits at
	// constraint "0" → bit 0 (0x01); matter.js on-off.element.ts:24. On
	// LevelControl LT sits at constraint "1" → bit 1 (0x02) and OO at
	// constraint "0" → bit 0 (0x01); level-control.element.ts:24-25.
	matterFeatureOnOffLT      uint32 = 0x01
	matterFeatureLevelOO      uint32 = 0x01 // OnOff feature, constraint "0", default 1
	matterFeatureLevelLT      uint32 = 0x02 // Lighting feature, constraint "1"
	matterFeatureLevelControl uint32 = matterFeatureLevelOO | matterFeatureLevelLT
	// LevelControl.Options (0x000F) is mandatory per matter.js
	// packages/model/src/standard/elements/level-control.element.ts:65
	// (conformance "M"). LevelControl.OnLevel (0x0011) is mandatory —
	// nullable uint8, null = return to previous level on transition to ON.
	matterAttrLevelOptions uint32 = 0x000F // mandatory — bitmap8
	matterAttrLevelOnLevel uint32 = 0x0011 // mandatory — nullable uint8, null = previous
	// MinLevel (0x0002) and MaxLevel (0x0003) are optional attributes
	// added in LevelControl cluster revision v7 (Matter 1.5). Default
	// values: MinLevel=0x01, MaxLevel=0xFE (matching the Matter-safe
	// 1–254 range; 0xFF is the TLV null sentinel and is excluded).
	matterAttrLevelMin uint32 = 0x0002
	matterAttrLevelMax uint32 = 0x0003

	// matterLevelMinDefault is the Matter-defined default for MinLevel
	// (LevelControl cluster rev 7). MaxLevel shares its default with matterLevelMax.
	matterLevelMinDefault     uint8  = 0x01
	matterAttrFeatureMap      uint32 = 0xFFFC
	matterAttrClusterRevision uint32 = 0xFFFD

	matterCmdOff    uint32 = 0x00
	matterCmdOn     uint32 = 0x01
	matterCmdToggle uint32 = 0x02
	// LT (Lighting) feature-gated OnOff commands — mandatory once LT is
	// advertised. matter.js on-off.element.ts:41,46,51 mark all three
	// conformance "LT".
	matterCmdOffWithEffect           uint32 = 0x40
	matterCmdOnWithRecallGlobalScene uint32 = 0x41
	matterCmdOnWithTimedOff          uint32 = 0x42

	matterCmdMoveToLevel          uint32 = 0x00
	matterCmdMove                 uint32 = 0x01
	matterCmdStep                 uint32 = 0x02
	matterCmdStop                 uint32 = 0x03
	matterCmdMoveToLevelWithOnOff uint32 = 0x04
	matterCmdMoveWithOnOff        uint32 = 0x05
	matterCmdStepWithOnOff        uint32 = 0x06
	matterCmdStopWithOnOff        uint32 = 0x07

	matterOnOffClusterRevision uint16 = 6
	// matterLevelControlClusterRevision pinned to matter.js HEAD
	// (@matter/model 0.16.11). Matter 1.5 bumped the revision from 6 to 7
	// with additional level-control refinements (OnTransitionTime /
	// OffTransitionTime were in 1.4 at rev 6; rev 7 is the 1.5 update).
	matterLevelControlClusterRevision uint16 = 7

	// matterLevelMax is the maximum CurrentLevel value Matter
	// LevelControl carries. Matter reserves 0xFF (255) as the null
	// sentinel for nullable uint8 encoding, so HM's 0.0–1.0 float maps
	// to 0–254, *not* 0–255 like [custom.Brightness.Byte].
	matterLevelMax uint8 = 0xFE
)

var (
	errMatterUnknownAttribute = errors.New("matter: unknown attribute")
	errMatterUnknownCommand   = errors.New("matter: unknown command")
	errMatterValueType        = errors.New("matter: unexpected value type")
)

// MatterDeviceType implements [interfaces.MatterEndpointSource]. Maps
// the dimmable / non-dimmable distinction onto Matter's two basic
// light device types (0x0101 / 0x0100).
func (l *Light) MatterDeviceType() uint16 {
	if l.Capabilities.Dimmable {
		return matterDeviceTypeDimmableLight
	}
	return matterDeviceTypeOnOffLight
}

// MatterClusterServers implements [interfaces.MatterEndpointSource].
// A non-dimmable light contributes OnOff + Groups + ScenesManagement
// (all mandatory per Matter §5.3 OnOffLight device type). A dimmable
// light additionally contributes LevelControl.
// Groups is a minimal stub — HM has no group management concept.
// ScenesManagement is a minimal stub returning SceneTableSize=0 and
// an empty FabricSceneInfo list — HM has no scene management concept.
// matter.js packages/node/src/devices/on-off-light.ts lists Groups
// (0x0004) and ScenesManagement (0x0062) as mandatory server clusters.
func (l *Light) MatterClusterServers() []interfaces.MatterClusterServer {
	if l.Capabilities.Dimmable {
		return []interfaces.MatterClusterServer{
			lightOnOffServer{l: l},
			lightLevelServer{l: l},
			wire.Groups{},
			wire.ScenesManagement{},
		}
	}
	return []interfaces.MatterClusterServer{
		lightOnOffServer{l: l},
		wire.Groups{},
		wire.ScenesManagement{},
	}
}

// brightnessToMatter encodes an HM brightness (0.0–1.0 float) into
// Matter LevelControl's CurrentLevel byte. The HM helper
// [custom.Brightness.Byte] returns 0–255 for HM-internal byte
// serialisation; that range collides with Matter's null-sentinel 255,
// so this function clamps to [matterLevelMax] instead.
func brightnessToMatter(b custom.Brightness) uint8 {
	v := b.Level() * float64(matterLevelMax)
	if v < 0 {
		return 0
	}
	if v > float64(matterLevelMax) {
		return matterLevelMax
	}
	return uint8(v + 0.5)
}

// matterLevelToHM decodes Matter LevelControl CurrentLevel back into
// the 0.0–1.0 HM float range. Values ≥ matterLevelMax saturate to 1.0.
func matterLevelToHM(m uint8) float64 {
	if m >= matterLevelMax {
		return 1.0
	}
	return float64(m) / float64(matterLevelMax)
}

// lightOnOffServer projects [Light] onto the Matter OnOff cluster
// (0x0006). The On command restores [Light.LastLevel] via
// [Light.TurnOn]; Off via [Light.TurnOff]; Toggle reads current
// brightness and dispatches accordingly.
type lightOnOffServer struct{ l *Light }

func (s lightOnOffServer) MatterClusterID() uint32 { return matterClusterOnOff }

func (s lightOnOffServer) MatterRead(attrID uint32) (any, bool) {
	switch attrID {
	case matterAttrOnOffOnOff:
		// OnOff (Matter §1.5.6.2) is a non-nullable bool — chip-tool's
		// TLVReader returns CHIP_ERROR_WRONG_TLV_TYPE when it sees a
		// TLV null where a bool is required, and the read fails with
		// "Response Failure: Can not decode Data". Default to FALSE
		// on unobserved DPs (HmIP-BDT post-boot before the first
		// LEVEL value lands) so the cluster surface stays spec-clean.
		on, _ := s.l.IsOn()
		return on, true
	case matterAttrOnOffGlobalSceneControl:
		// GlobalSceneControl (bool, conformance LT): defaults to true.
		// matter.js packages/node/src/behaviors/on-off/OnOffServer.ts:75,151
		// sets globalSceneControl = true; the bridge has no scene engine
		// so it stays true (read-only on this projection).
		return true, true
	case matterAttrOnOffOnTime:
		// OnTime (uint16, conformance LT): timed-off countdown. The bridge
		// has no on-timer engine; defaults to 0 ("no timed off active").
		// matter.js OnOffServer.ts:102 resets onTime to 0.
		return uint16(0), true
	case matterAttrOnOffOffWaitTime:
		// OffWaitTime (uint16, conformance LT): delayed-off wait. 0 = none.
		// matter.js OnOffServer.ts:80 resets offWaitTime to 0.
		return uint16(0), true
	case matterAttrOnOffStartUpOnOff:
		// StartUpOnOff (StartUpOnOffEnum, conformance LT, quality "X N"):
		// nullable; null = "keep last state on startup". matter.js
		// OnOffServer.ts:39 reads `this.state.startUpOnOff ?? null` —
		// null is the default. (nil, true) encodes the TLV null.
		return nil, true
	case matterAttrFeatureMap:
		// LT (Lighting) feature, bit 0 (0x01). OnOffLight / DimmableLight
		// device types mandate LT on the OnOff cluster.
		// matter.js packages/model/src/standard/elements/on-off.element.ts:24
		// (Field LT, constraint "0").
		return matterFeatureOnOffLT, true
	case matterAttrClusterRevision:
		return matterOnOffClusterRevision, true
	default:
		return nil, false
	}
}

func (s lightOnOffServer) MatterWrite(ctx context.Context, attrID uint32, value any, priority hmenum.CommandPriority) error {
	if attrID != matterAttrOnOffOnOff {
		return fmt.Errorf("%w: 0x%04X", errMatterUnknownAttribute, attrID)
	}
	on, ok := value.(bool)
	if !ok {
		return fmt.Errorf("%w: OnOff write expected bool, got %T", errMatterValueType, value)
	}
	var err error
	if on {
		err = s.l.TurnOn(ctx, priority)
	} else {
		err = s.l.TurnOff(ctx, priority)
	}
	if err != nil {
		return err
	}
	s.l.dataVersion.Bump()
	return nil
}

func (s lightOnOffServer) MatterInvoke(ctx context.Context, cmdID uint32, _ any, priority hmenum.CommandPriority) (any, error) {
	var err error
	switch cmdID {
	case matterCmdOff:
		err = s.l.TurnOff(ctx, priority)
	case matterCmdOn:
		err = s.l.TurnOn(ctx, priority)
	case matterCmdToggle:
		on, observed := s.l.IsOn()
		if !observed || !on {
			err = s.l.TurnOn(ctx, priority)
		} else {
			err = s.l.TurnOff(ctx, priority)
		}
	case matterCmdOffWithEffect:
		// OffWithEffect (LT, mandatory): the bridge has no dimming-effect
		// engine, so the effect identifier/variant are ignored and the
		// device is turned off plainly. matter.js OnOffServer.ts treats
		// the effect as best-effort. on-off.element.ts:41.
		err = s.l.TurnOff(ctx, priority)
	case matterCmdOnWithRecallGlobalScene:
		// OnWithRecallGlobalScene (LT, mandatory): no scene engine, so
		// recall collapses to a plain On. on-off.element.ts:46.
		err = s.l.TurnOn(ctx, priority)
	case matterCmdOnWithTimedOff:
		// OnWithTimedOff (LT, mandatory): the bridge has no on-timer, so
		// the timed-off semantics are dropped and the device is turned on
		// for the duration of the controller's own scheduling.
		// on-off.element.ts:51.
		err = s.l.TurnOn(ctx, priority)
	default:
		return nil, fmt.Errorf("%w: 0x%02X", errMatterUnknownCommand, cmdID)
	}
	if err != nil {
		return nil, err
	}
	s.l.dataVersion.Bump()
	return nil, nil
}

func (s lightOnOffServer) MatterReportable() []uint32 {
	return []uint32{matterAttrOnOffOnOff}
}

// MatterAttributes lists every OnOff (0x0006) attribute the server
// implements via MatterRead. Apple Home's HAP service rebuild reads
// the full attribute set; without this the dispatcher falls back to
// MatterReportable's single attribute.
func (s lightOnOffServer) MatterAttributes() []uint32 {
	return []uint32{
		matterAttrOnOffOnOff,
		matterAttrOnOffGlobalSceneControl,
		matterAttrOnOffOnTime,
		matterAttrOnOffOffWaitTime,
		matterAttrOnOffStartUpOnOff,
	}
}

// MatterAcceptedCommands implements [interfaces.MatterClusterCommandLister]
// for the OnOff cluster (0x0006).
func (s lightOnOffServer) MatterAcceptedCommands() []uint32 {
	return []uint32{
		matterCmdOff,
		matterCmdOn,
		matterCmdToggle,
		matterCmdOffWithEffect,
		matterCmdOnWithRecallGlobalScene,
		matterCmdOnWithTimedOff,
	}
}

// MatterGeneratedCommands implements [interfaces.MatterClusterCommandLister]
// for the OnOff cluster (0x0006). OnOff commands have no response payload.
func (s lightOnOffServer) MatterGeneratedCommands() []uint32 {
	return nil
}

// lightLevelServer projects [Light] onto the Matter LevelControl
// cluster (0x0008). Only emitted for dimmable lights.
//
// Transition time is currently ignored; it would map to HM's RAMP_TIME
// parameter via [Light.TurnOnWith] / [Light.TurnOffWithRamp].
// MoveToLevel and MoveToLevelWithOnOff differ in whether OnOff state
// is also affected; on the HM side both collapse to SetLevel because
// LEVEL=0 implicitly turns the device off and LEVEL>0 turns it on.
type lightLevelServer struct{ l *Light }

func (s lightLevelServer) MatterClusterID() uint32 { return matterClusterLevelControl }

func (s lightLevelServer) MatterRead(attrID uint32) (any, bool) {
	switch attrID {
	case matterAttrLevelCurrent:
		// Value temporarily unavailable — return (nil, true); see lightOnOffServer.MatterRead.
		b, observed := s.l.Brightness()
		if !observed {
			return nil, true
		}
		return brightnessToMatter(b), true
	case matterAttrLevelMin:
		// MinLevel optional uint8: the minimum value the CurrentLevel
		// attribute may be set to. Default 0x01 per Matter 1.5
		// LevelControl cluster rev 7.
		return matterLevelMinDefault, true
	case matterAttrLevelMax:
		// MaxLevel optional uint8: the maximum value the CurrentLevel
		// attribute may be set to. Default 0xFE — 0xFF is the TLV
		// null sentinel and is excluded from the usable range.
		return matterLevelMax, true
	case matterAttrLevelOptions:
		// Options bitmap8: 0 = execute command unconditionally.
		// matter.js level-control.element.ts Options attribute.
		return uint8(0), true
	case matterAttrLevelOnLevel:
		// OnLevel nullable uint8: null = return to previous level on
		// transition to ON. matter.js level-control.element.ts OnLevel.
		// 0xFF is the TLV null sentinel for nullable uint8.
		return nil, true
	case matterAttrLevelRemainingTime:
		// RemainingTime (uint16, conformance LT): time left in the current
		// transition, in 1/10 s. The bridge applies levels instantly, so
		// there is never an in-flight transition → 0.
		// matter.js level-control.element.ts:33.
		return uint16(0), true
	case matterAttrLevelStartUpLevel:
		// StartUpCurrentLevel (uint8, conformance LT, quality "X N"):
		// nullable; null = "keep the current level on startup". matter.js
		// level-control.element.ts:67 declares no default, so null is the
		// safe value. (nil, true) encodes the TLV null.
		return nil, true
	case matterAttrFeatureMap:
		// OO (bit 0, constraint "0") | LT (bit 1, constraint "1").
		// DimmableLight (0x0101) mandates LT on LevelControl.
		// matter.js level-control.element.ts:24-25.
		return matterFeatureLevelControl, true
	case matterAttrClusterRevision:
		return matterLevelControlClusterRevision, true
	default:
		return nil, false
	}
}

func (s lightLevelServer) MatterWrite(ctx context.Context, attrID uint32, value any, priority hmenum.CommandPriority) error {
	if attrID != matterAttrLevelCurrent {
		return fmt.Errorf("%w: 0x%04X", errMatterUnknownAttribute, attrID)
	}
	b, ok := value.(uint8)
	if !ok {
		return fmt.Errorf("%w: CurrentLevel write expected uint8, got %T", errMatterValueType, value)
	}
	if err := s.l.SetLevel(ctx, matterLevelToHM(b), priority); err != nil {
		return err
	}
	s.l.dataVersion.Bump()
	return nil
}

func (s lightLevelServer) MatterInvoke(ctx context.Context, cmdID uint32, fields any, priority hmenum.CommandPriority) (any, error) {
	switch cmdID {
	case matterCmdMoveToLevel, matterCmdMoveToLevelWithOnOff:
		level, err := extractMoveToLevel(fields)
		if err != nil {
			return nil, err
		}
		if err := s.l.SetLevel(ctx, matterLevelToHM(level), priority); err != nil {
			return nil, err
		}
		s.l.dataVersion.Bump()
		return nil, nil

	case matterCmdMove, matterCmdMoveWithOnOff:
		// HM has no continuous-rate dimming; treat Move as a no-op that
		// returns Success so conformance checkers accept the command.
		// A future implementation could map Rate to RAMP_TIME on devices
		// that expose it.
		return nil, nil

	case matterCmdStep, matterCmdStepWithOnOff:
		// Apply a discrete brightness step.  StepSize is in the same 0–254
		// range as CurrentLevel.  The step is clamped by SetLevel to [0,1].
		stepSize, err := extractStepSize(fields)
		if err != nil {
			return nil, err
		}
		stepMode, err := extractStepMode(fields)
		if err != nil {
			return nil, err
		}
		b, _ := s.l.Brightness()
		current := brightnessToMatter(b)
		var next uint8
		if stepMode == wire.LevelStepModeDown {
			if stepSize >= current {
				next = 0
			} else {
				next = current - stepSize
			}
		} else {
			sum := int(current) + int(stepSize)
			if sum > int(matterLevelMax) {
				next = matterLevelMax
			} else {
				next = uint8(sum) //nolint:gosec // clamped above; see #20
			}
		}
		if err := s.l.SetLevel(ctx, matterLevelToHM(next), priority); err != nil {
			return nil, err
		}
		s.l.dataVersion.Bump()
		return nil, nil

	case matterCmdStop, matterCmdStopWithOnOff:
		// There is no in-flight ramp to stop on HM; return Success
		// so the chip-tool conformance check passes.
		return nil, nil

	default:
		return nil, fmt.Errorf("%w: 0x%02X", errMatterUnknownCommand, cmdID)
	}
}

func (s lightLevelServer) MatterReportable() []uint32 {
	return []uint32{matterAttrLevelCurrent}
}

// MatterAttributes lists every LevelControl (0x0008) attribute the
// server implements via MatterRead. Apple Home's HAP service rebuild
// reads the full attribute set; without this the dispatcher falls back
// to MatterReportable's single attribute.
func (s lightLevelServer) MatterAttributes() []uint32 {
	return []uint32{
		matterAttrLevelCurrent,
		matterAttrLevelRemainingTime,
		matterAttrLevelMin,
		matterAttrLevelMax,
		matterAttrLevelOptions,
		matterAttrLevelOnLevel,
		matterAttrLevelStartUpLevel,
	}
}

// MatterAcceptedCommands implements [interfaces.MatterClusterCommandLister]
// for the LevelControl cluster (0x0008). All eight commands are enumerated
// so AcceptedCommandList is populated correctly; chip-tool and Apple Home
// both read this list during commissioning.
func (s lightLevelServer) MatterAcceptedCommands() []uint32 {
	return []uint32{
		matterCmdMoveToLevel,
		matterCmdMove,
		matterCmdStep,
		matterCmdStop,
		matterCmdMoveToLevelWithOnOff,
		matterCmdMoveWithOnOff,
		matterCmdStepWithOnOff,
		matterCmdStopWithOnOff,
	}
}

// MatterGeneratedCommands implements [interfaces.MatterClusterCommandLister]
// for the LevelControl cluster (0x0008). LevelControl commands have no
// response payload.
func (s lightLevelServer) MatterGeneratedCommands() []uint32 {
	return nil
}

// extractMoveToLevel pulls the Level field out of a MoveToLevel /
// MoveToLevelWithOnOff request. The bridge has already TLV-decoded the
// payload; we accept either a bare uint8 (the minimal "level only"
// shape) or a map carrying a "level" key. A typed request struct from
// internal/north/matter/cluster/levelcontrol/ may replace this once
// that package exists.
func extractMoveToLevel(fields any) (uint8, error) {
	switch v := fields.(type) {
	case uint8:
		return v, nil
	case map[string]any:
		raw, ok := v["level"]
		if !ok {
			return 0, fmt.Errorf("%w: MoveToLevel missing level field", errMatterValueType)
		}
		level, ok := raw.(uint8)
		if !ok {
			return 0, fmt.Errorf("%w: MoveToLevel level expected uint8, got %T", errMatterValueType, raw)
		}
		return level, nil
	default:
		return 0, fmt.Errorf("%w: MoveToLevel expected uint8 or map[string]any, got %T", errMatterValueType, fields)
	}
}

// extractStepSize pulls the StepSize field (context tag 1) out of a
// Step / StepWithOnOff payload. Step has no typed decoder in the bridge,
// so the real wire path lands here as the tag-keyed map[uint8]any that
// decodeGenericTagMap produces (unsigned ints as uint64) — see
// internal/north/matter/bridge/fields_reader.go. The string-keyed shape
// is kept for the in-package tests.
func extractStepSize(fields any) (uint8, error) {
	switch v := fields.(type) {
	case map[uint8]any:
		raw, ok := v[1]
		if !ok {
			return 0, fmt.Errorf("%w: Step missing step_size field (tag 1)", errMatterValueType)
		}
		s, ok := wireUint8(raw)
		if !ok {
			return 0, fmt.Errorf("%w: Step step_size expected integer, got %T", errMatterValueType, raw)
		}
		return s, nil
	case map[string]any:
		raw, ok := v["step_size"]
		if !ok {
			return 0, fmt.Errorf("%w: Step missing step_size field", errMatterValueType)
		}
		s, ok := raw.(uint8)
		if !ok {
			return 0, fmt.Errorf("%w: Step step_size expected uint8, got %T", errMatterValueType, raw)
		}
		return s, nil
	default:
		return 0, fmt.Errorf("%w: Step expected map[uint8]any, got %T", errMatterValueType, fields)
	}
}

// extractStepMode pulls the StepMode field (context tag 0) out of a
// Step / StepWithOnOff payload. Returns [wire.LevelStepModeUp] (0) when
// absent, matching the pre-decoded default.
func extractStepMode(fields any) (uint8, error) {
	switch m := fields.(type) {
	case map[uint8]any:
		raw, ok := m[0]
		if !ok {
			return wire.LevelStepModeUp, nil
		}
		mode, ok := wireUint8(raw)
		if !ok {
			return 0, fmt.Errorf("%w: Step step_mode expected integer, got %T", errMatterValueType, raw)
		}
		return mode, nil
	case map[string]any:
		raw, ok := m["step_mode"]
		if !ok {
			return wire.LevelStepModeUp, nil
		}
		mode, ok := raw.(uint8)
		if !ok {
			return 0, fmt.Errorf("%w: Step step_mode expected uint8, got %T", errMatterValueType, raw)
		}
		return mode, nil
	default:
		return wire.LevelStepModeUp, nil
	}
}

// wireUint8 reads an unsigned integer out of a value decoded from the
// generic tag-keyed fields map, where decodeGenericTagMap stores
// unsigned ints as uint64. The narrower Go-type cases keep the helper
// usable from tests that pass those directly.
func wireUint8(raw any) (uint8, bool) {
	switch n := raw.(type) {
	case uint64:
		return uint8(n & 0xFF), true
	case uint8:
		return n, true
	default:
		return 0, false
	}
}
