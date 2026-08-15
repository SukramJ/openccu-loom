// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package generic

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/wire"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// Compile-time assertions: Generic Switch is the OnOff-aktor surface
// for HM channels that have no [model/custom/switch.Switch] wrapper —
// e.g. a no-frills HmIP-PSM with just a STATE writable. The endpoint
// assembler materialises one OnOffPlugInUnit endpoint per such DP.
var (
	_ interfaces.MatterEndpointSource = (*Switch)(nil)
	_ interfaces.MatterClusterServer  = (*Switch)(nil)
	_ interfaces.MatterChangeNotifier = (*Switch)(nil)
)

// Matter Device Type IDs and OnOff cluster IDs follow the Matter 1.5.1
// Application Cluster Specification. The constants mirror those in
// `model/custom/switch/matter.go` (the rich Switch projection); the
// generic projection here is intentionally a subset (no
// power/energy hosting).
const (
	matterGenericSwitchDeviceType uint16 = 0x010A // OnOffPlugInUnit

	matterGenericSwitchClusterOnOff uint32 = 0x0006

	matterGenericSwitchAttrOnOff           uint32 = 0x0000
	matterGenericSwitchAttrFeatureMap      uint32 = 0xFFFC
	matterGenericSwitchAttrClusterRevision uint32 = 0xFFFD

	// LT (Lighting) feature-gated OnOff attributes. OnOffPlugInUnit
	// (0x010A) mandates the LT feature on the OnOff cluster, which in
	// turn makes these four attributes mandatory.
	// matter.js packages/model/src/standard/elements/on-off.element.ts:30-36:
	//   GlobalSceneControl 0x4000 bool   conformance "LT" access "R V"
	//   OnTime             0x4001 uint16 conformance "LT" access "RW VO"
	//   OffWaitTime        0x4002 uint16 conformance "LT" access "RW VO"
	//   StartUpOnOff       0x4003 enum8  conformance "LT" access "RW VM" quality "X N"
	matterGenericSwitchAttrGlobalSceneControl uint32 = 0x4000
	matterGenericSwitchAttrOnTime             uint32 = 0x4001
	matterGenericSwitchAttrOffWaitTime        uint32 = 0x4002
	matterGenericSwitchAttrStartUpOnOff       uint32 = 0x4003

	// matterGenericFeatureOnOffLT is the LT (Lighting) FeatureMap bit on
	// the OnOff cluster: constraint "0" → bit 0 (0x01).
	// matter.js on-off.element.ts:24 (Field LT).
	matterGenericFeatureOnOffLT uint32 = 0x01

	matterGenericSwitchCmdOff    uint32 = 0x00
	matterGenericSwitchCmdOn     uint32 = 0x01
	matterGenericSwitchCmdToggle uint32 = 0x02
	// LT (Lighting) feature-gated OnOff commands — mandatory once LT is
	// advertised. matter.js on-off.element.ts:41,46,51 mark all three
	// conformance "LT".
	matterGenericSwitchCmdOffWithEffect           uint32 = 0x40
	matterGenericSwitchCmdOnWithRecallGlobalScene uint32 = 0x41
	matterGenericSwitchCmdOnWithTimedOff          uint32 = 0x42

	matterGenericOnOffClusterRevision uint16 = 6
)

// matterGenericStartUpOnOffNull is the sentinel stored in
// [matterOnOffLTState.startUpOnOff] when the attribute holds TLV null
// ("keep the last state on startup"), which is also its default.
// matter.js packages/node/src/behaviors/on-off/OnOffServer.ts:39.
const matterGenericStartUpOnOffNull uint32 = 0xFFFFFFFF

// matterOnOffLTState carries the four LT-gated OnOff attributes. The
// bridge has no scene, on-timer or dimming-effect engine, so the values
// are stored in process and read back verbatim — the device-type
// conformance requires that the attributes exist and answer, not that a
// timer runs behind them.
//
// Kept in its own struct so the Matter projection owns its state instead
// of spreading atomics through the wire-level [Switch].
type matterOnOffLTState struct {
	onTime       atomic.Uint32 // uint16 tenths of a second; 0 = none
	offWaitTime  atomic.Uint32 // uint16 tenths of a second; 0 = none
	startUpOnOff atomic.Uint32 // matterGenericStartUpOnOffNull = null (default)

	// globalSceneControl mirrors the LT-gated GlobalSceneControl
	// attribute: set by On / OnWithRecallGlobalScene / OnWithTimedOff,
	// cleared by OffWithEffect, untouched by a plain Off. Defaults to
	// true. matter.js OnOffServer.ts:97-104, :158-169.
	globalSceneControl atomic.Bool
}

// initMatterOnOffLT installs the LT attribute defaults. Called from
// [NewSwitch] so a Switch answers the mandatory attributes from the very
// first read, before any command has been invoked.
func (s *matterOnOffLTState) initMatterOnOffLT() {
	s.startUpOnOff.Store(matterGenericStartUpOnOffNull)
	s.globalSceneControl.Store(true)
}

var (
	errMatterGenericSwitchAttribute = errors.New("matter: unknown attribute")
	errMatterGenericSwitchCommand   = errors.New("matter: unknown command")
	errMatterGenericSwitchValueType = errors.New("matter: unexpected value type")
)

// matterGenericSwitchEligible reports whether the underlying parameter
// makes the Generic.Switch a credible OnOff source. Only STATE
// (the canonical HM switch parameter) qualifies; other booleans
// (e.g. ENABLE_TIMER, RESET) opt out by returning false so they
// don't pollute the topology with phantom OnOff endpoints.
func matterGenericSwitchEligible(p hmenum.Parameter) bool {
	return p == hmenum.ParameterState
}

// MatterDeviceType implements [interfaces.MatterEndpointSource].
// Generic OnOff actors map to OnOffPlugInUnit (0x010A); a richer
// classification (Light vs. Plug) requires the Custom-DP layer.
func (s *Switch) MatterDeviceType() uint16 { return matterGenericSwitchDeviceType }

// MatterClusterServers implements [interfaces.MatterEndpointSource].
// Returns nil when the underlying parameter is not the canonical
// STATE so the assembler skips the projection — see
// [matterGenericSwitchEligible].
//
// Besides OnOff the endpoint mounts the Groups (0x0004) and
// ScenesManagement (0x0062) stubs, both `conformance: "M"` on the
// OnOffPlugInUnit device type this source advertises — matter.js
// packages/model/src/standard/elements/on-off-plug-in-unit.element.ts:22,38.
// Leaving them off produced a ServerList that contradicts the
// DeviceTypeList the endpoint publishes.
func (s *Switch) MatterClusterServers() []interfaces.MatterClusterServer {
	if s == nil || !matterGenericSwitchEligible(s.Parameter()) {
		return nil
	}
	return []interfaces.MatterClusterServer{
		s,
		wire.Groups{},
		wire.ScenesManagement{},
	}
}

// MatterClusterID implements [interfaces.MatterClusterServer].
func (s *Switch) MatterClusterID() uint32 { return matterGenericSwitchClusterOnOff }

// MatterRead implements [interfaces.MatterClusterServer]. Returns
// (nil, false) when the underlying STATE wire DP has not been
// observed yet — the bridge maps that to a stale-data status.
func (s *Switch) MatterRead(attrID uint32) (any, bool) {
	switch attrID {
	case matterGenericSwitchAttrOnOff:
		on, observed := s.Value()
		if !observed {
			return nil, false
		}
		return on, true
	case matterGenericSwitchAttrGlobalSceneControl:
		return s.matterLT.globalSceneControl.Load(), true
	case matterGenericSwitchAttrOnTime:
		return uint16(s.matterLT.onTime.Load() & 0xFFFF), true
	case matterGenericSwitchAttrOffWaitTime:
		return uint16(s.matterLT.offWaitTime.Load() & 0xFFFF), true
	case matterGenericSwitchAttrStartUpOnOff:
		// Nullable enum8, quality "X N": null = "keep last state on
		// startup". (nil, true) encodes TLV null.
		v := s.matterLT.startUpOnOff.Load()
		if v == matterGenericStartUpOnOffNull {
			return nil, true
		}
		return uint8(v & 0xFF), true
	case matterGenericSwitchAttrFeatureMap:
		// LT (Lighting) feature, bit 0. OnOffPlugInUnit (0x010A) mandates
		// it on the OnOff cluster — matter.js on-off-plug-in-unit.element.ts:26
		// requires the feature with conformance "M", so a controller that
		// trusts the device type expects the four LT attributes and the
		// three LT commands to answer.
		return matterGenericFeatureOnOffLT, true
	case matterGenericSwitchAttrClusterRevision:
		return matterGenericOnOffClusterRevision, true
	default:
		return nil, false
	}
}

// MatterWrite implements [interfaces.MatterClusterServer]. Writable
// attributes on the OnOff cluster with the LT feature:
//   - OnOff (0x0000): dispatches to the CCU.
//   - OnTime (0x4001), OffWaitTime (0x4002): uint16 counters accepted and
//     stored in-process; no CCU write-through (no on-timer engine).
//   - StartUpOnOff (0x4003): nullable enum8 stored in-process.
//
// matter.js on-off.element.ts:31-36 marks all three access "RW VO"/"RW VM".
func (s *Switch) MatterWrite(ctx context.Context, attrID uint32, value any, priority hmenum.CommandPriority) error {
	switch attrID {
	case matterGenericSwitchAttrOnOff:
		// OnOff carries quality "N S"; a scene controller may write nil to
		// reset a scene-tagged attribute. The attribute is not nullable, so
		// nil has no spec-defined meaning here — no-op instead of failing
		// the type assertion. matter.js on-off.element.ts:29.
		if value == nil {
			return nil
		}
		on, ok := value.(bool)
		if !ok {
			return fmt.Errorf("%w: OnOff write expected bool, got %T", errMatterGenericSwitchValueType, value)
		}
		return s.Set(ctx, on, priority)
	case matterGenericSwitchAttrOnTime:
		v, ok := value.(uint16)
		if !ok {
			return fmt.Errorf("%w: OnTime write expected uint16, got %T", errMatterGenericSwitchValueType, value)
		}
		s.matterLT.onTime.Store(uint32(v))
		return nil
	case matterGenericSwitchAttrOffWaitTime:
		v, ok := value.(uint16)
		if !ok {
			return fmt.Errorf("%w: OffWaitTime write expected uint16, got %T", errMatterGenericSwitchValueType, value)
		}
		s.matterLT.offWaitTime.Store(uint32(v))
		return nil
	case matterGenericSwitchAttrStartUpOnOff:
		if value == nil {
			s.matterLT.startUpOnOff.Store(matterGenericStartUpOnOffNull)
			return nil
		}
		v, ok := value.(uint8)
		if !ok {
			return fmt.Errorf("%w: StartUpOnOff write expected uint8 or nil, got %T", errMatterGenericSwitchValueType, value)
		}
		s.matterLT.startUpOnOff.Store(uint32(v))
		return nil
	default:
		return fmt.Errorf("%w: 0x%04X", errMatterGenericSwitchAttribute, attrID)
	}
}

// MatterInvoke implements [interfaces.MatterClusterServer]. Implements
// the OnOff baseline (Off/On/Toggle) plus the three LT-mandatory
// commands (OffWithEffect 0x40, OnWithRecallGlobalScene 0x41,
// OnWithTimedOff 0x42). With no dimming-effect, scene or on-timer engine
// behind the bridge the LT commands collapse to plain On/Off — the
// device-type conformance requires only that they be accepted.
func (s *Switch) MatterInvoke(ctx context.Context, cmdID uint32, _ any, priority hmenum.CommandPriority) (any, error) {
	var err error
	switch cmdID {
	case matterGenericSwitchCmdOff:
		err = s.Set(ctx, false, priority)
	case matterGenericSwitchCmdOn:
		err = s.turnOnAndRecallGlobalScene(ctx, priority)
	case matterGenericSwitchCmdToggle:
		cur, observed := s.Value()
		if !observed || !cur {
			err = s.turnOnAndRecallGlobalScene(ctx, priority)
		} else {
			err = s.Set(ctx, false, priority)
		}
	case matterGenericSwitchCmdOffWithEffect:
		// No dimming-effect engine, so the effect identifier/variant are
		// ignored. matter.js OnOffServer.ts:158-169 also clears
		// GlobalSceneControl here — a plain Off never touches it.
		if err = s.Set(ctx, false, priority); err == nil {
			s.matterLT.globalSceneControl.Store(false)
		}
	case matterGenericSwitchCmdOnWithRecallGlobalScene:
		// No scene engine, so the recall collapses to a plain On.
		err = s.turnOnAndRecallGlobalScene(ctx, priority)
	case matterGenericSwitchCmdOnWithTimedOff:
		// No on-timer, so the timed-off semantics are dropped.
		// matter.js OnOffServer.ts:224 ends onWithTimedOff() with on(),
		// which also sets GlobalSceneControl.
		err = s.turnOnAndRecallGlobalScene(ctx, priority)
	default:
		return nil, fmt.Errorf("%w: 0x%02X", errMatterGenericSwitchCommand, cmdID)
	}
	return nil, err
}

// turnOnAndRecallGlobalScene switches on and sets GlobalSceneControl, the
// side effect matter.js applies to every On-flavoured command
// (OnOffServer.ts:97-104).
func (s *Switch) turnOnAndRecallGlobalScene(ctx context.Context, priority hmenum.CommandPriority) error {
	if err := s.TurnOn(ctx, priority); err != nil {
		return err
	}
	s.matterLT.globalSceneControl.Store(true)
	return nil
}

// MatterReportable implements [interfaces.MatterClusterServer]. Only
// the OnOff attribute fires Matter reports; FeatureMap / Revision
// are static.
func (s *Switch) MatterReportable() []uint32 {
	return []uint32{matterGenericSwitchAttrOnOff}
}

// MatterAttributes implements
// [interfaces.MatterClusterAttributeLister] — wildcard reads expand
// to OnOff plus the four LT-gated attributes the advertised
// OnOffPlugInUnit device type mandates; the dispatcher merges in the
// universal globals (FeatureMap + ClusterRevision) on top.
func (s *Switch) MatterAttributes() []uint32 {
	return []uint32{
		matterGenericSwitchAttrOnOff,
		matterGenericSwitchAttrGlobalSceneControl,
		matterGenericSwitchAttrOnTime,
		matterGenericSwitchAttrOffWaitTime,
		matterGenericSwitchAttrStartUpOnOff,
	}
}
