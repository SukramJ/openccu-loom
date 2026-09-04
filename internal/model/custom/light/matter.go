// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package light

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/onoff"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/wire"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// matterDispatchPriority is the southbound urgency every Matter-driven
// write and invoke carries. The bridge is a controller-facing
// foreground path — a tap in a Matter app must not queue behind a
// background refresh — so it dispatches at High, and the cluster
// contract no longer negotiates it per call.
//
// Spelled out as a constant rather than left to a variable: the zero
// value of [hmenum.CommandPriority] is Critical, so anything that
// reached these calls defaulted would silently escalate every bridged
// command.
const matterDispatchPriority = hmenum.CommandPriorityHigh

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
	// Light inherits OnMatterValueChanged from the embedded *generic.Float
	// (LEVEL / brightness), so an external CCU-confirmed level change
	// dirty-marks the OnOff / LevelControl attributes for Apple's Subscribe.
	_ interfaces.MatterChangeNotifier = (*Light)(nil)
)

// Matter Device Type IDs and cluster IDs follow the Matter 1.5.1
// Application Cluster Specification. The OnOff command ids come from
// internal/north/matter/cluster/wire, which owns the wire contract; the
// device types, cluster ids, LT attribute ids, the LT feature bit and the
// cluster revisions are declared here next to the projection that
// advertises them. The OnOff half is pinned against the matter.js
// snapshot by TestHmLgtLightOnOffMatchesMatterJS, so the ids are not
// reviewed by eye against a second hand-written list.
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

	// The six OnOff command IDs are the wire contract, so they are read
	// from the package that owns it —
	// internal/north/matter/cluster/wire/onoff.go — instead of being
	// transcribed a second time here. matter.js
	// packages/model/src/standard/elements/on-off.element.ts marks Off "M",
	// On and Toggle "!OFFONLY", and the three 0x4x commands "LT": all six
	// are mandatory for the FeatureMap this projection advertises.
	matterCmdOff                     = wire.OnOffCmdOff
	matterCmdOn                      = wire.OnOffCmdOn
	matterCmdToggle                  = wire.OnOffCmdToggle
	matterCmdOffWithEffect           = wire.OnOffCmdOffWithEffect
	matterCmdOnWithRecallGlobalScene = wire.OnOffCmdOnWithRecallGlobalScene
	matterCmdOnWithTimedOff          = wire.OnOffCmdOnWithTimedOff

	matterCmdMoveToLevel          uint32 = 0x00
	matterCmdMove                 uint32 = 0x01
	matterCmdStep                 uint32 = 0x02
	matterCmdStop                 uint32 = 0x03
	matterCmdMoveToLevelWithOnOff uint32 = 0x04
	matterCmdMoveWithOnOff        uint32 = 0x05
	matterCmdStepWithOnOff        uint32 = 0x06
	matterCmdStopWithOnOff        uint32 = 0x07

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
		// GlobalSceneControl (bool, conformance LT): true after On /
		// OnWithTimedOff / OnWithRecallGlobalScene, false after
		// OffWithEffect; a plain Off leaves it unchanged. Defaults to
		// true. matter.js packages/node/src/behaviors/on-off/OnOffServer.ts:
		// 97-104 (on), :158-169 (offWithEffect).
		return s.l.matterGlobalSceneControl(), true
	case matterAttrOnOffOnTime:
		// OnTime (uint16, conformance LT): remaining timed-on countdown
		// in tenths of a second, driven by the OnWithTimedOff engine.
		// matter.js OnOffServer.ts:239 #timedOnTick.
		return s.l.matterOnTime(), true
	case matterAttrOnOffOffWaitTime:
		// OffWaitTime (uint16, conformance LT): remaining delayed-off
		// guard in tenths of a second. matter.js OnOffServer.ts:312
		// #delayedOffTick.
		return s.l.matterOffWaitTime(), true
	case matterAttrOnOffStartUpOnOff:
		// StartUpOnOff (StartUpOnOffEnum, conformance LT, quality "X N"):
		// nullable; null = "keep last state on startup". matter.js
		// OnOffServer.ts:39 reads `this.state.startUpOnOff ?? null`.
		// (nil, true) encodes the TLV null.
		if v := s.l.matterStartUpOnOff(); v != nil {
			return *v, true
		}
		return nil, true
	case matterAttrFeatureMap:
		// LT (Lighting) feature, bit 0 (0x01). OnOffLight / DimmableLight
		// device types mandate LT on the OnOff cluster.
		// matter.js packages/model/src/standard/elements/on-off.element.ts:24
		// (Field LT, constraint "0").
		return matterFeatureOnOffLT, true
	case matterAttrClusterRevision:
		return onoff.Revision(), true
	default:
		return nil, false
	}
}

func (s lightOnOffServer) MatterWrite(ctx context.Context, attrID uint32, value any) error {
	switch attrID {
	case matterAttrOnOffOnOff:
		on, ok := value.(bool)
		if !ok {
			return fmt.Errorf("%w: OnOff write expected bool, got %T", errMatterValueType, value)
		}
		var err error
		if on {
			err = s.l.TurnOn(ctx, matterDispatchPriority)
		} else {
			err = s.l.TurnOff(ctx, matterDispatchPriority)
		}
		if err != nil {
			return err
		}
		s.l.dataVersion.Bump()
		return nil
	case matterAttrOnOffOnTime, matterAttrOnOffOffWaitTime:
		// OnTime / OffWaitTime are RW (on-off.element.ts:31-32). A write
		// updates the countdown attribute; parking it at 0 or the 0xFFFF
		// hold ends a running countdown, and writes never start one —
		// matter.js OnOffServer.ts:66-84 #stopHeldTimer.
		v, ok := matterWriteUint16(value)
		if !ok {
			return fmt.Errorf("%w: OnTime/OffWaitTime write expected uint16, got %T", errMatterValueType, value)
		}
		if attrID == matterAttrOnOffOnTime {
			s.l.matterSetOnTime(v)
		} else {
			s.l.matterSetOffWaitTime(v)
		}
		s.l.dataVersion.Bump()
		return nil
	case matterAttrOnOffStartUpOnOff:
		// StartUpOnOff is RW VM, nullable, enum 0..2 (on-off.element.ts:33-36).
		// The value is stored on the projection; the physical power-on
		// behaviour of an HM device stays governed by its own device
		// configuration.
		if value == nil {
			s.l.matterSetStartUpOnOff(nil)
			s.l.dataVersion.Bump()
			return nil
		}
		v, ok := matterWriteUint8(value)
		if !ok {
			return fmt.Errorf("%w: StartUpOnOff write expected enum8, got %T", errMatterValueType, value)
		}
		if v > 2 {
			return fmt.Errorf("%w: StartUpOnOff constraint 0..2, got %d", errMatterValueType, v)
		}
		s.l.matterSetStartUpOnOff(&v)
		s.l.dataVersion.Bump()
		return nil
	default:
		return fmt.Errorf("%w: 0x%04X", errMatterUnknownAttribute, attrID)
	}
}

// MinWritePrivilege implements
// [interfaces.MatterClusterAttributeWritePrivilege]: StartUpOnOff is
// RW VM per on-off.element.ts:34 (write access Manage); the countdown
// attributes stay at the RW VO default.
func (s lightOnOffServer) MinWritePrivilege(attrID uint32) uint8 {
	if attrID == matterAttrOnOffStartUpOnOff {
		return 4 // Manage
	}
	return 3 // Operate
}

func (s lightOnOffServer) MatterInvoke(ctx context.Context, cmdID uint32, fields any) (any, error) {
	var err error
	switch cmdID {
	case matterCmdOff:
		err = s.l.TurnOff(ctx, matterDispatchPriority)
		if err == nil {
			s.l.matterTimedHandleOff()
		}
	case matterCmdOn:
		err = s.l.TurnOn(ctx, matterDispatchPriority)
		if err == nil {
			s.l.matterTimedHandleOn()
		}
	case matterCmdToggle:
		on, observed := s.l.IsOn()
		if !observed || !on {
			err = s.l.TurnOn(ctx, matterDispatchPriority)
			if err == nil {
				s.l.matterTimedHandleOn()
			}
		} else {
			err = s.l.TurnOff(ctx, matterDispatchPriority)
			if err == nil {
				s.l.matterTimedHandleOff()
			}
		}
	case matterCmdOffWithEffect:
		// OffWithEffect (LT, mandatory): the bridge has no dimming-effect
		// engine, so the effect identifier/variant are ignored and the
		// device is turned off plainly. matter.js OnOffServer.ts treats
		// the effect as best-effort. on-off.element.ts:41. matter.js
		// OnOffServer.ts:158-169 also clears GlobalSceneControl here —
		// a plain Off never touches it.
		err = s.l.TurnOff(ctx, matterDispatchPriority)
		if err == nil {
			s.l.matterTimedHandleOff()
			s.l.matterClearGlobalSceneControl()
		}
	case matterCmdOnWithRecallGlobalScene:
		// OnWithRecallGlobalScene (LT, mandatory): no scene engine, so
		// recall collapses to a plain On. on-off.element.ts:46.
		err = s.l.TurnOn(ctx, matterDispatchPriority)
		if err == nil {
			s.l.matterTimedHandleOn()
		}
	case matterCmdOnWithTimedOff:
		// OnWithTimedOff (LT, mandatory): full timed-off engine —
		// AcceptOnlyWhenOn gate, delayed-off guard, OnTime/OffWaitTime
		// countdowns. matter.js OnOffServer.ts:198-225.
		control, onTime, offWaitTime, ferr := extractOnWithTimedOff(fields)
		if ferr != nil {
			return nil, ferr
		}
		err = s.l.matterOnWithTimedOff(ctx, control, onTime, offWaitTime)
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
// A positive MoveToLevel / MoveToLevelWithOnOff TransitionTime maps to
// HM's RAMP_TIME parameter via [Light.TurnOnWith] /
// [Light.TurnOffWithRamp] on devices that accept RAMP_TIME
// ([custom.LightCapabilities.Transition]); a null or zero transition
// time keeps the instant SetLevel path — matter.js
// LevelControlServer.ts:297-303 (moveToLevelLogic) only derives a rate
// from a truthy transition time, and transition()'s changePerS contract
// (LevelControlServer.ts:459) reads "0 or nullish means transition
// instantly".
// The non-WithOnOff command variants are gated on the effective
// ExecuteIfOff option while the device is off (matter.js
// LevelControlServer.ts:596 #optionsAllowExecution); the WithOnOff
// variants couple a MinLevel target to Off (LevelControlServer.ts:500).
// HM dimmers carry a single LEVEL knob (LEVEL=0 implicitly off,
// LEVEL>0 on), so both couplings collapse onto SetLevel — an
// ExecuteIfOff level change while off still powers the device on,
// which is the closest single-knob projection of the Matter state pair.
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
		// transition, in 1/10 s. Transitions are delegated to the device's
		// native RAMP_TIME handling and the CCU does not report ramp
		// progress, so the bridge never tracks an in-flight transition → 0.
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

func (s lightLevelServer) MatterWrite(ctx context.Context, attrID uint32, value any) error {
	if attrID != matterAttrLevelCurrent {
		return fmt.Errorf("%w: 0x%04X", errMatterUnknownAttribute, attrID)
	}
	b, ok := value.(uint8)
	if !ok {
		return fmt.Errorf("%w: CurrentLevel write expected uint8, got %T", errMatterValueType, value)
	}
	if err := s.l.SetLevel(ctx, matterLevelToHM(b), matterDispatchPriority); err != nil {
		return err
	}
	s.l.dataVersion.Bump()
	return nil
}

func (s lightLevelServer) MatterInvoke(ctx context.Context, cmdID uint32, fields any) (any, error) {
	switch cmdID {
	case matterCmdMoveToLevel, matterCmdMoveToLevelWithOnOff:
		req, err := extractMoveToLevel(fields)
		if err != nil {
			return nil, err
		}
		withOnOff := cmdID == matterCmdMoveToLevelWithOnOff
		if !withOnOff && !s.levelOptionsAllowExecution(req.OptionsMask, req.OptionsOverride) {
			// matter.js LevelControlServer.ts:245 returns without acting
			// when the effective options forbid execution while the device
			// is off — a silent Success, not an error status.
			return nil, nil
		}
		level := cropMatterLevel(req.Level)
		target := matterLevelToHM(level)
		if withOnOff && level == matterLevelMinDefault {
			// The WithOnOff variant couples MinLevel to Off: matter.js
			// LevelControlServer.ts:500 (couple) and chip
			// LevelControlCluster.cpp:344 flip the OnOff state off when
			// the target level equals minLevel (Matter §1.6.4.1.2).
			// HM dimmers carry a single LEVEL knob, so "CurrentLevel=min +
			// OnOff=false" projects to LEVEL=0.
			target = 0
		}
		if ramp, ramped := transitionRampDuration(req.TransitionTime); ramped && s.l.Capabilities.Transition {
			// A positive transition time becomes the device-side ramp:
			// matter.js LevelControlServer.ts:297-303 (moveToLevelLogic)
			// turns it into a rate towards the target level; HM dimmers
			// ramp natively when RAMP_TIME accompanies LEVEL in one
			// atomic put_paramset, so the bridge delegates the whole
			// transition to the device. Devices without RAMP_TIME
			// (Capabilities.Transition unset) keep the instant path.
			if err := s.setLevelRamped(ctx, target, ramp, matterDispatchPriority); err != nil {
				return nil, err
			}
			s.l.dataVersion.Bump()
			return nil, nil
		}
		if err := s.l.SetLevel(ctx, target, matterDispatchPriority); err != nil {
			return nil, err
		}
		s.l.dataVersion.Bump()
		return nil, nil

	case matterCmdMove, matterCmdMoveWithOnOff:
		// A zero rate is rejected before anything else, mirroring
		// matter.js LevelControlServer.ts:271 (#assertRateValue →
		// StatusResponseError InvalidCommand); a null/absent rate would
		// fall back to DefaultMoveRate there and is accepted here.
		// HM has no continuous-rate dimming; a valid Move is a no-op
		// that returns Success so conformance checkers accept the
		// command. A future implementation could map Rate to RAMP_TIME
		// on devices that expose it.
		if rate, ok := extractMoveRate(fields); ok && rate == 0 {
			return nil, errors.New("matter: Move: invalid command argument: rate must not be 0")
		}
		return nil, nil

	case matterCmdStep, matterCmdStepWithOnOff:
		// Apply a discrete brightness step. StepSize is in the same
		// 1–254 range as CurrentLevel; the target is clamped to
		// [MinLevel, MaxLevel] = [1, 254], mirroring matter.js
		// Transitions.ts:139 (min/max property clamp) — a plain Step
		// can therefore never turn the device off.
		stepSize, err := extractStepSize(fields)
		if err != nil {
			return nil, err
		}
		stepMode, err := extractStepMode(fields)
		if err != nil {
			return nil, err
		}
		if cmdID == matterCmdStep {
			// Fields decode first, gate second — matter.js validates the
			// payload before the options check reaches the handler.
			mask, override := extractLevelOptions(fields)
			if !s.levelOptionsAllowExecution(mask, override) {
				// Silent Success while off; LevelControlServer.ts:387.
				return nil, nil
			}
		}
		b, _ := s.l.Brightness()
		current := brightnessToMatter(b)
		var next uint8
		if stepMode == wire.LevelStepModeDown {
			diff := int(current) - int(stepSize)
			if diff < int(matterLevelMinDefault) {
				next = matterLevelMinDefault
			} else {
				next = uint8(diff) //nolint:gosec // clamped above; see #20
			}
		} else {
			sum := int(current) + int(stepSize)
			if sum > int(matterLevelMax) {
				next = matterLevelMax
			} else {
				next = uint8(sum) //nolint:gosec // clamped above; see #20
			}
		}
		target := matterLevelToHM(next)
		if cmdID == matterCmdStepWithOnOff && next == matterLevelMinDefault {
			// StepWithOnOff clamped to MinLevel turns the device off
			// (Matter §1.6.7.6: new CurrentLevel == minimum → OnOff
			// FALSE). Mirrors chip LevelControlCluster.cpp:508, which
			// compares the post-clamp target; matter.js couple()
			// compares the pre-clamp target and would stay on — chip +
			// spec win here. Single-LEVEL-knob projection: LEVEL=0.
			target = 0
		}
		if err := s.l.SetLevel(ctx, target, matterDispatchPriority); err != nil {
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

// levelOptionsAllowExecution mirrors matter.js LevelControlServer.ts:581
// (#calculateEffectiveOptions) + :596 (#optionsAllowExecution) for the
// non-WithOnOff command variants: while the device is off, the command
// only executes when the effective ExecuteIfOff option (bit 0) is set.
// The Options attribute is a constant 0 on this projection, so the
// effective option reduces to "mask bit set AND override bit set". An
// unobserved OnOff data point counts as off, consistent with
// lightOnOffServer.MatterRead's FALSE default.
func (s lightLevelServer) levelOptionsAllowExecution(optionsMask, optionsOverride uint8) bool {
	const executeIfOffBit = 0x01
	if optionsMask&executeIfOffBit != 0 && optionsOverride&executeIfOffBit != 0 {
		return true
	}
	on, _ := s.l.IsOn()
	return on
}

// cropMatterLevel crops a requested level into [MinLevel, MaxLevel] =
// [1, 254]. Mirrors matter.js LevelControlServer.ts:249
// cropValueRange(level, this.minLevel, this.maxLevel) with the LT
// feature's minLevel=1 / maxLevel=254 defaults (LevelControlServer.ts:64-70).
func cropMatterLevel(level uint8) uint8 {
	if level < matterLevelMinDefault {
		return matterLevelMinDefault
	}
	if level > matterLevelMax {
		return matterLevelMax
	}
	return level
}

// transitionRampDuration converts a decoded MoveToLevel TransitionTime
// (nullable uint16, tenths of a second per Matter §1.6.7.1) into the
// RAMP_TIME duration handed to the CCU. Null/absent and 0 both report
// ramped=false so the caller keeps the instant SetLevel path: matter.js
// LevelControlServer.ts:297-303 (moveToLevelLogic) only computes an
// effectiveRate for a truthy transition time, and transition()'s
// changePerS contract (LevelControlServer.ts:459) documents "0 or
// nullish means transition instantly".
func transitionRampDuration(tenths *uint16) (ramp time.Duration, ramped bool) {
	if tenths == nil || *tenths == 0 {
		return 0, false
	}
	return time.Duration(*tenths) * 100 * time.Millisecond, true
}

// setLevelRamped drives LEVEL to target over the given ramp duration.
// The off direction (target 0, only reachable via the WithOnOff
// MinLevel coupling) goes through [Light.TurnOffWithRamp]; every other
// target goes through [Light.TurnOnWith] — both bundle
// {LEVEL, RAMP_TIME, ON_TIME=TimerNotUsed} into one atomic put_paramset so
// the device performs the transition natively.
func (s lightLevelServer) setLevelRamped(ctx context.Context, target float64, ramp time.Duration, priority hmenum.CommandPriority) error {
	if target == 0 {
		return s.l.TurnOffWithRamp(ctx, ramp, priority)
	}
	return s.l.TurnOnWith(ctx, OnConfig{Brightness: &target, RampTime: &ramp}, priority)
}

// extractMoveToLevel pulls the MoveToLevel / MoveToLevelWithOnOff
// request out of the bridge-decoded fields. The wire path delivers the
// typed [wire.MoveToLevelRequest]; a bare uint8 and the map carrying a
// "level" key are legacy shapes kept for in-package callers (both
// carry no Options, so the ExecuteIfOff gate sees an all-zero bitmap).
func extractMoveToLevel(fields any) (wire.MoveToLevelRequest, error) {
	switch v := fields.(type) {
	case wire.MoveToLevelRequest:
		return v, nil
	case uint8:
		return wire.MoveToLevelRequest{Level: v}, nil
	case map[string]any:
		raw, ok := v["level"]
		if !ok {
			return wire.MoveToLevelRequest{}, fmt.Errorf("%w: MoveToLevel missing level field", errMatterValueType)
		}
		level, ok := raw.(uint8)
		if !ok {
			return wire.MoveToLevelRequest{}, fmt.Errorf("%w: MoveToLevel level expected uint8, got %T", errMatterValueType, raw)
		}
		return wire.MoveToLevelRequest{Level: level}, nil
	default:
		return wire.MoveToLevelRequest{}, fmt.Errorf("%w: MoveToLevel expected wire.MoveToLevelRequest, uint8 or map[string]any, got %T", errMatterValueType, fields)
	}
}

// extractMoveRate pulls the nullable Rate field (context tag 1) out of
// a Move / MoveWithOnOff payload. Returns ok=false when the field is
// absent or null — matter.js substitutes DefaultMoveRate then
// (LevelControlServer.ts:274). The wire path lands here as
// decodeGenericTagMap's map[uint8]any; the string-keyed shape serves
// in-package tests.
func extractMoveRate(fields any) (uint8, bool) {
	switch v := fields.(type) {
	case map[uint8]any:
		raw, ok := v[1]
		if !ok || raw == nil {
			return 0, false
		}
		return wireUint8(raw)
	case map[string]any:
		raw, ok := v["rate"]
		if !ok || raw == nil {
			return 0, false
		}
		r, ok := raw.(uint8)
		return r, ok
	default:
		return 0, false
	}
}

// extractLevelOptions pulls the OptionsMask / OptionsOverride bitmaps
// (context tags 3 / 4 per Matter §1.6.7.3) out of a Step payload.
// Absent fields default to 0, matching the spec default. The wire path
// lands here as decodeGenericTagMap's map[uint8]any; the string-keyed
// shape serves in-package tests.
func extractLevelOptions(fields any) (mask, override uint8) {
	switch v := fields.(type) {
	case map[uint8]any:
		if raw, ok := v[3]; ok {
			if m, mok := wireUint8(raw); mok {
				mask = m
			}
		}
		if raw, ok := v[4]; ok {
			if o, ook := wireUint8(raw); ook {
				override = o
			}
		}
	case map[string]any:
		if raw, ok := v["options_mask"].(uint8); ok {
			mask = raw
		}
		if raw, ok := v["options_override"].(uint8); ok {
			override = raw
		}
	}
	return mask, override
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

// wireUint16 is the 16-bit sibling of [wireUint8].
func wireUint16(raw any) (uint16, bool) {
	switch n := raw.(type) {
	case uint64:
		return uint16(n & 0xFFFF), true
	case uint16:
		return n, true
	default:
		return 0, false
	}
}

// matterWriteUint16 coerces an attribute-write value into uint16. The
// IM write layer delivers decoded TLV unsigned ints as uint64; the
// narrower types keep in-package callers working.
func matterWriteUint16(value any) (uint16, bool) {
	return wireUint16(value)
}

// matterWriteUint8 coerces an attribute-write value into uint8.
func matterWriteUint8(value any) (uint8, bool) {
	return wireUint8(value)
}

// extractOnWithTimedOff pulls the OnWithTimedOff (0x42) fields out of
// the bridge-decoded payload. Tags per on-off.element.ts:50-55:
// [0] OnOffControl (bitmap8, constraint "0 to 1"), [1] OnTime
// (uint16, max 65534), [2] OffWaitTime (uint16, max 65534). All three
// are conformance M; absent fields default to 0 for robustness. The
// constraints reject the reserved 0xFFFF hold value on the command
// path — it is reachable only via an attribute write.
func extractOnWithTimedOff(fields any) (control uint8, onTime, offWaitTime uint16, err error) {
	read := func(rawControl, rawOnTime, rawOffWait any) error {
		if rawControl != nil {
			v, ok := wireUint8(rawControl)
			if !ok {
				return fmt.Errorf("%w: OnWithTimedOff OnOffControl expected integer, got %T", errMatterValueType, rawControl)
			}
			control = v
		}
		if rawOnTime != nil {
			v, ok := wireUint16(rawOnTime)
			if !ok {
				return fmt.Errorf("%w: OnWithTimedOff OnTime expected integer, got %T", errMatterValueType, rawOnTime)
			}
			onTime = v
		}
		if rawOffWait != nil {
			v, ok := wireUint16(rawOffWait)
			if !ok {
				return fmt.Errorf("%w: OnWithTimedOff OffWaitTime expected integer, got %T", errMatterValueType, rawOffWait)
			}
			offWaitTime = v
		}
		return nil
	}
	switch v := fields.(type) {
	case map[uint8]any:
		err = read(v[0], v[1], v[2])
	case map[string]any:
		err = read(v["on_off_control"], v["on_time"], v["off_wait_time"])
	case nil:
		// No fields decoded — all defaults.
	default:
		return 0, 0, 0, fmt.Errorf("%w: OnWithTimedOff expected map[uint8]any, got %T", errMatterValueType, fields)
	}
	if err != nil {
		return 0, 0, 0, err
	}
	if control > 1 {
		return 0, 0, 0, fmt.Errorf("%w: OnOffControl constraint 0..1, got %d", errMatterValueType, control)
	}
	if onTime > 0xFFFE {
		return 0, 0, 0, fmt.Errorf("%w: OnTime constraint max 65534, got %d", errMatterValueType, onTime)
	}
	if offWaitTime > 0xFFFE {
		return 0, 0, 0, fmt.Errorf("%w: OffWaitTime constraint max 65534, got %d", errMatterValueType, offWaitTime)
	}
	return control, onTime, offWaitTime, nil
}
