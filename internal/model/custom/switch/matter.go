// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package switchdev

import (
	"context"
	"errors"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/measurement"
	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/wire"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// Compile-time assertions: Switch participates in the Matter source
// surface (ADR 0012) as an OnOffPlugInUnit endpoint that contributes
// the OnOff cluster.
var (
	_ interfaces.MatterEndpointSource     = (*Switch)(nil)
	_ interfaces.MatterClusterServer      = (*Switch)(nil)
	_ interfaces.MatterClusterDataVersion = (*Switch)(nil)
)

// Matter Device Type IDs and OnOff cluster IDs follow the Matter 1.5.1
// Application Cluster Specification. They live here next to the
// projection; the cluster-protocol package
// internal/north/matter/cluster/onoff/ may later import them.
// Cluster revision verified against the Matter cluster sweep
// (matter.js HEAD packages/model/src/standard/elements/).
const (
	matterDeviceTypeOnOffPlugInUnit uint16 = 0x010A

	matterClusterOnOff uint32 = 0x0006

	matterAttrOnOff           uint32 = 0x0000
	matterAttrFeatureMap      uint32 = 0xFFFC
	matterAttrClusterRevision uint32 = 0xFFFD

	// LT (Lighting) feature-gated OnOff attributes. OnOffPlugInUnit
	// (0x010A) mandates the LT feature on the OnOff cluster, which in
	// turn makes these four attributes mandatory.
	// matter.js packages/model/src/standard/elements/on-off.element.ts:30-36:
	//   GlobalSceneControl 0x4000 bool   conformance "LT" access "R V"
	//   OnTime             0x4001 uint16 conformance "LT" access "RW VO"
	//   OffWaitTime        0x4002 uint16 conformance "LT" access "RW VO"
	//   StartUpOnOff       0x4003 enum8  conformance "LT" access "RW VM" quality "X N"
	matterAttrGlobalSceneControl uint32 = 0x4000
	matterAttrOnTime             uint32 = 0x4001
	matterAttrOffWaitTime        uint32 = 0x4002
	matterAttrStartUpOnOff       uint32 = 0x4003

	// matterFeatureOnOffLT is the LT (Lighting) FeatureMap bit on the
	// OnOff cluster: constraint "0" → bit 0 (0x01).
	// matter.js on-off.element.ts:24 (Field LT).
	matterFeatureOnOffLT uint32 = 0x01

	matterCmdOff    uint32 = 0x00
	matterCmdOn     uint32 = 0x01
	matterCmdToggle uint32 = 0x02
	// LT (Lighting) feature-gated OnOff commands — mandatory once LT is
	// advertised. matter.js on-off.element.ts:41,46,51 mark all three
	// conformance "LT".
	matterCmdOffWithEffect           uint32 = 0x40
	matterCmdOnWithRecallGlobalScene uint32 = 0x41
	matterCmdOnWithTimedOff          uint32 = 0x42

	matterOnOffClusterRevision uint16 = 6
)

var (
	errMatterUnknownAttribute = errors.New("matter: unknown attribute")
	errMatterUnknownCommand   = errors.New("matter: unknown command")
	errMatterValueType        = errors.New("matter: unexpected value type")
)

// MatterDeviceType implements [interfaces.MatterEndpointSource]. A
// Switch surfaces as Matter OnOffPlugInUnit (0x010A); the endpoint
// assembler may upgrade the device type to OnOffLight (0x0100) when
// the hosting model classifies the channel as a light role.
func (s *Switch) MatterDeviceType() uint16 { return matterDeviceTypeOnOffPlugInUnit }

// MatterClusterServers implements [interfaces.MatterEndpointSource].
// Switch contributes OnOff (the Switch itself) plus the Groups +
// ScenesManagement stubs that are mandatory on the
// OnOffPlugInUnit (0x010A) device-type per matter.js
// packages/model/src/standard/elements/on-off-plug-in-unit.element.ts.
// Energy-aware variants (HmIP-PSM and similar) additionally
// contribute ElectricalPowerMeasurement (0x0090) and
// ElectricalEnergyMeasurement (0x0091) host-clusters when the
// channel exposes a POWER / ENERGY_COUNTER sensor — wired through
// the [Switch.attachedPowerSource] / [Switch.attachedEnergySource]
// hooks the model layer populates at construction.
func (s *Switch) MatterClusterServers() []interfaces.MatterClusterServer {
	servers := []interfaces.MatterClusterServer{
		s,
		wire.Groups{},
		wire.ScenesManagement{},
	}
	if s.attachedPowerSource != nil {
		servers = append(servers, measurement.NewElectricalPowerServer(s.attachedPowerSource))
	}
	if s.attachedEnergySource != nil {
		servers = append(servers, measurement.NewElectricalEnergyServer(s.attachedEnergySource))
	}
	return servers
}

// AttachPowerSource binds an [interfaces.MatterFloatMeasurementSource]
// (typically the Switch channel's POWER generic.Sensor) so the
// Matter projection includes ElectricalPowerMeasurement on the
// Switch endpoint. Calling with nil clears the attachment.
//
// The model layer is expected to call this once at composition; the
// bridge does not re-discover sources at runtime.
func (s *Switch) AttachPowerSource(src interfaces.MatterFloatMeasurementSource) {
	s.attachedPowerSource = src
}

// AttachEnergySource binds the ENERGY_COUNTER source — projects to
// ElectricalEnergyMeasurement (0x0091).
func (s *Switch) AttachEnergySource(src interfaces.MatterFloatMeasurementSource) {
	s.attachedEnergySource = src
}

// MatterClusterID implements [interfaces.MatterClusterServer].
func (s *Switch) MatterClusterID() uint32 { return matterClusterOnOff }

// MatterRead implements [interfaces.MatterClusterServer]. Returns
// (nil, false) when the underlying STATE wire DP has not been
// observed yet — the bridge maps that to a stale-data status.
func (s *Switch) MatterRead(attrID uint32) (any, bool) {
	switch attrID {
	case matterAttrOnOff:
		on, observed := s.IsOn()
		if !observed {
			// Apple Home's HAP-mapper silently drops the entire
			// OnOffPlugInUnit accessory when OnOff=null on the
			// first Subscribe-Initial-Report — the HMOutlet /
			// HMSwitch projection cannot be initialised without
			// a concrete boolean state (verified empirically:
			// bridge pair succeeds, sensors visible, but
			// Bücherregal switch absent). matter.js's OnOff
			// behaviour also defaults to `false` when no observed
			// state is available rather than emitting nullable
			// (the OnOff attribute is NOT spec-nullable —
			// matter.js packages/types/src/clusters/definitions/
			// OnOffCluster.ts:35 declares it as `TlvBoolean`,
			// not TlvNullable). Default to OFF until a real
			// observation lands; the first CCU push or LoadValue
			// then overwrites with the true state.
			return false, true
		}
		return on, true
	case matterAttrGlobalSceneControl:
		// GlobalSceneControl (bool, conformance LT): true after On /
		// OnWithTimedOff / OnWithRecallGlobalScene, false after
		// OffWithEffect; a plain Off leaves it unchanged. Defaults to
		// true. matter.js packages/node/src/behaviors/on-off/OnOffServer.ts:
		// 97-104 (on), :158-169 (offWithEffect).
		return s.globalSceneControl.Load(), true
	case matterAttrOnTime:
		// OnTime (uint16, conformance LT): timed-off countdown.
		// Returns the last written value (default 0 = no timed-off active).
		// matter.js OnOffServer.ts:102.
		return uint16(s.onTime.Load() & 0xFFFF), true
	case matterAttrOffWaitTime:
		// OffWaitTime (uint16, conformance LT): delayed-off wait.
		// Returns the last written value (default 0 = none).
		// matter.js OnOffServer.ts:80.
		return uint16(s.offWaitTime.Load() & 0xFFFF), true
	case matterAttrStartUpOnOff:
		// StartUpOnOff (nullable enum8, conformance LT, quality "X N"):
		// null = "keep last state on startup". (nil, true) encodes TLV null.
		// matter.js OnOffServer.ts:39.
		v := s.startUpOnOff.Load()
		if v == startUpOnOffNull {
			return nil, true
		}
		return uint8(v & 0xFF), true
	case matterAttrFeatureMap:
		// LT (Lighting) feature, bit 0 (0x01). OnOffPlugInUnit (0x010A)
		// mandates LT on the OnOff cluster.
		// matter.js on-off.element.ts:24 (Field LT, constraint "0").
		return matterFeatureOnOffLT, true
	case matterAttrClusterRevision:
		return matterOnOffClusterRevision, true
	default:
		return nil, false
	}
}

// MatterDataVersion implements [interfaces.MatterClusterDataVersion].
// Bumped on every successful MatterWrite / MatterInvoke so
// DataVersionFilter evaluation correctly detects cluster changes;
// controllers cache via this counter and skip the cluster on
// re-reads when it stays unchanged.
func (s *Switch) MatterDataVersion() uint32 { return s.Current() }

// MatterWrite implements [interfaces.MatterClusterServer]. Writable
// attributes on the OnOff cluster with LT feature:
//   - OnOff (0x0000): dispatches to the CCU.
//   - OnTime (0x4001), OffWaitTime (0x4002): uint16 counters accepted and
//     stored in-process; no CCU write-through (no on-timer engine).
//   - StartUpOnOff (0x4003): nullable enum8 stored in-process.
//
// matter.js OnOffServer.ts:80 (offWaitTime reset), :102 (onTime reset),
// :39 (startUpOnOff read-back). on-off.element.ts:31-36 marks all three
// access "RW VO"/"RW VM".
func (s *Switch) MatterWrite(ctx context.Context, attrID uint32, value any, priority hmenum.CommandPriority) error {
	switch attrID {
	case matterAttrOnOff:
		// Guard nil before type-asserting to bool. Matter OnOff has quality "N S"
		// (non-volatile + scene); scene controllers may write nil to reset a
		// scene-tagged attribute. The attribute itself is non-nullable (no quality X)
		// so nil carries no spec-defined meaning here — silently no-op rather than
		// panicking on the type assertion.
		// matter.js packages/model/src/standard/elements/on-off.element.ts:29.
		if value == nil {
			return nil
		}
		on, ok := value.(bool)
		if !ok {
			return fmt.Errorf("%w: OnOff write expected bool, got %T", errMatterValueType, value)
		}
		if err := s.Set(ctx, on, priority); err != nil {
			return err
		}
		s.Bump()
		return nil

	case matterAttrOnTime:
		// OnTime (uint16): on-timer countdown in 1/10 s increments.
		// Accept and store; bridge has no on-timer engine.
		// matter.js OnOffServer.ts:102.
		v, ok := value.(uint16)
		if !ok {
			return fmt.Errorf("%w: OnTime write expected uint16, got %T", errMatterValueType, value)
		}
		s.onTime.Store(uint32(v))
		return nil

	case matterAttrOffWaitTime:
		// OffWaitTime (uint16): delayed-off wait in 1/10 s increments.
		// Accept and store; bridge has no delayed-off engine.
		// matter.js OnOffServer.ts:80.
		v, ok := value.(uint16)
		if !ok {
			return fmt.Errorf("%w: OffWaitTime write expected uint16, got %T", errMatterValueType, value)
		}
		s.offWaitTime.Store(uint32(v))
		return nil

	case matterAttrStartUpOnOff:
		// StartUpOnOff (nullable enum8): null = "keep last state on startup".
		// Accept nil (null) or uint8 enum value and store.
		// matter.js OnOffServer.ts:39.
		if value == nil {
			s.startUpOnOff.Store(startUpOnOffNull)
			return nil
		}
		v, ok := value.(uint8)
		if !ok {
			return fmt.Errorf("%w: StartUpOnOff write expected uint8 or nil, got %T", errMatterValueType, value)
		}
		s.startUpOnOff.Store(uint32(v))
		return nil

	default:
		return fmt.Errorf("%w: 0x%04X", errMatterUnknownAttribute, attrID)
	}
}

// MatterInvoke implements [interfaces.MatterClusterServer]. Implements
// the OFFONLY-free OnOff baseline (Off/On/Toggle) plus the three
// LT-mandatory commands (OffWithEffect 0x40, OnWithRecallGlobalScene
// 0x41, OnWithTimedOff 0x42). The bridge has no dimming-effect / scene /
// on-timer engine, so the LT commands collapse to plain On/Off — the
// device-type conformance only requires that they be accepted without
// error.
func (s *Switch) MatterInvoke(ctx context.Context, cmdID uint32, _ any, priority hmenum.CommandPriority) (any, error) {
	var err error
	switch cmdID {
	case matterCmdOff:
		err = s.TurnOff(ctx, priority)
	case matterCmdOn:
		err = s.TurnOn(ctx, priority)
		if err == nil {
			s.globalSceneControl.Store(true)
		}
	case matterCmdToggle:
		cur, observed := s.IsOn()
		if !observed || !cur {
			err = s.TurnOn(ctx, priority)
			if err == nil {
				s.globalSceneControl.Store(true)
			}
		} else {
			err = s.TurnOff(ctx, priority)
		}
	case matterCmdOffWithEffect:
		// OffWithEffect (LT, mandatory): no dimming-effect engine on a
		// plain switch, so the effect identifier/variant are ignored and
		// the switch is turned off. on-off.element.ts:41. matter.js
		// OnOffServer.ts:158-169 also clears GlobalSceneControl here —
		// a plain Off never touches it.
		err = s.TurnOff(ctx, priority)
		if err == nil {
			s.globalSceneControl.Store(false)
		}
	case matterCmdOnWithRecallGlobalScene:
		// OnWithRecallGlobalScene (LT, mandatory): no scene engine, so
		// recall collapses to a plain On. on-off.element.ts:46.
		err = s.TurnOn(ctx, priority)
		if err == nil {
			s.globalSceneControl.Store(true)
		}
	case matterCmdOnWithTimedOff:
		// OnWithTimedOff (LT, mandatory): no on-timer, so the timed-off
		// semantics are dropped and the switch is turned on.
		// on-off.element.ts:51. matter.js OnOffServer.ts:224 ends
		// onWithTimedOff() with on(), which also sets GlobalSceneControl.
		err = s.TurnOn(ctx, priority)
		if err == nil {
			s.globalSceneControl.Store(true)
		}
	default:
		return nil, fmt.Errorf("%w: 0x%02X", errMatterUnknownCommand, cmdID)
	}
	if err != nil {
		return nil, err
	}
	s.Bump()
	return nil, nil
}

// MatterReportable implements [interfaces.MatterClusterServer]. The
// OnOff attribute fires a Matter report on every state change; no
// other attribute on this projection is reportable.
func (s *Switch) MatterReportable() []uint32 {
	return []uint32{matterAttrOnOff}
}

// MatterAttributes lists every OnOff (0x0006) attribute the server
// implements via MatterRead. Apple Home's HAP service rebuild reads
// the full attribute set; without this the dispatcher falls back to
// MatterReportable's single attribute. The OnOffPlugInUnit (0x010A)
// device type mandates the LT (Lighting) feature, so the four
// LT-gated attributes — GlobalSceneControl (0x4000), OnTime (0x4001),
// OffWaitTime (0x4002), StartUpOnOff (0x4003) — are enumerated.
// matter.js on-off.element.ts:30-36. Options (0x000F) is a historical
// Zigbee-Cluster-Library attribute that Matter dropped from OnOff;
// chip's zzz_generated AttributeIds.h likewise omits it.
func (s *Switch) MatterAttributes() []uint32 {
	return []uint32{
		matterAttrOnOff,
		matterAttrGlobalSceneControl,
		matterAttrOnTime,
		matterAttrOffWaitTime,
		matterAttrStartUpOnOff,
	}
}

// MatterAcceptedCommands implements [interfaces.MatterClusterCommandLister]
// for the OnOff cluster (0x0006). Enumerates the OnOff baseline plus the
// three LT-mandatory commands so AcceptedCommandList is populated for
// chip-tool / Apple Home conformance reads. matter.js on-off.element.ts:
// Off (0x00, M), On (0x01), Toggle (0x02), OffWithEffect (0x40, LT),
// OnWithRecallGlobalScene (0x41, LT), OnWithTimedOff (0x42, LT).
func (s *Switch) MatterAcceptedCommands() []uint32 {
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
func (s *Switch) MatterGeneratedCommands() []uint32 {
	return nil
}

// Compile-time assertions: Switch implements the attribute + command
// listers the dispatcher uses to populate the global metadata attributes.
var (
	_ interfaces.MatterClusterAttributeLister = (*Switch)(nil)
	_ interfaces.MatterClusterCommandLister   = (*Switch)(nil)
)
