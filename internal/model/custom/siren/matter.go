// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package siren

import (
	"context"
	"errors"
	"fmt"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster/wire"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// Compile-time assertions: Siren and SmokeSiren participate in the
// Matter source surface (ADR 0012). Siren projects onto an
// OnOffPlugInUnit (0x010A) endpoint with OnOff + BooleanState clusters
// — Matter 1.5.1 has no Siren-cluster covering tone/optical configuration,
// so those features remain MQTT-only per ADR 0012 §5 ("out of Matter
// scope" table). SmokeSiren projects onto a SmokeCOAlarm (0x0076)
// endpoint with the SmokeCOAlarm cluster (0x005C). SoundPlayer is
// excluded from the Matter surface entirely.
var (
	_ interfaces.MatterEndpointSource     = (*Siren)(nil)
	_ interfaces.MatterEndpointSource     = (*SmokeSiren)(nil)
	_ interfaces.MatterClusterDataVersion = (*Siren)(nil)
	_ interfaces.MatterClusterDataVersion = (*SmokeSiren)(nil)
)

// MatterDataVersion implements [interfaces.MatterClusterDataVersion] for Siren.
// Bumped on every successful MatterWrite / MatterInvoke.
func (s *Siren) MatterDataVersion() uint32 { return s.dataVersion.Current() }

// MatterDataVersion implements [interfaces.MatterClusterDataVersion] for SmokeSiren.
// SmokeCOAlarm has no client-writable attributes / commands in 0.1.0; the version
// is reserved for when SelfTestRequest is wired.
func (s *SmokeSiren) MatterDataVersion() uint32 { return s.dataVersion.Current() }

// Matter constants follow the Matter 1.5.1 Application Cluster
// Specification (§1.5 OnOff, §1.7 BooleanState, §2.11 SmokeCOAlarm).
// Cluster revisions verified against the Matter cluster sweep
// (matter.js HEAD packages/model/src/standard/elements/).
const (
	matterDeviceTypeOnOffPlugInUnit uint16 = 0x010A
	matterDeviceTypeSmokeCOAlarm    uint16 = 0x0076

	matterClusterOnOff        uint32 = 0x0006
	matterClusterBooleanState uint32 = 0x0045
	matterClusterSmokeCOAlarm uint32 = 0x005C

	matterAttrOnOffOnOff uint32 = 0x0000

	// LT (Lighting) feature-gated OnOff attributes. OnOffPlugInUnit
	// (0x010A) mandates the LT feature on the OnOff cluster (see
	// matterFeatureOnOffLT below), which in turn makes these four
	// attributes mandatory. matter.js
	// packages/model/src/standard/elements/on-off.element.ts:30-36:
	//   GlobalSceneControl 0x4000 bool   conformance "LT" access "R V"
	//   OnTime             0x4001 uint16 conformance "LT" access "RW VO"
	//   OffWaitTime        0x4002 uint16 conformance "LT" access "RW VO"
	//   StartUpOnOff       0x4003 enum8  conformance "LT" access "RW VM" quality "X N"
	matterAttrOnOffGlobalSceneControl uint32 = 0x4000
	matterAttrOnOffOnTime             uint32 = 0x4001
	matterAttrOnOffOffWaitTime        uint32 = 0x4002
	matterAttrOnOffStartUpOnOff       uint32 = 0x4003

	// matterFeatureOnOffLT is the LT (Lighting) FeatureMap bit on the
	// OnOff cluster: constraint "0" → bit 0 (0x01). matter.js
	// on-off.element.ts:24 (Field LT). OnOffPlugInUnit (0x010A) marks
	// the feature "M" (mandatory) — on-off-plug-in-unit.element.ts:22-25 —
	// so this projection must advertise it (and its four gated
	// attributes + three gated commands below), the same as
	// internal/model/custom/switch/matter.go's Switch projection.
	matterFeatureOnOffLT uint32 = 0x01

	// SmokeCOAlarm attributes (Matter spec 2.11.5).
	// HardwareFaultAlert (0x0006) and EndOfServiceAlert (0x0007) are
	// mandatory per matter.js
	// packages/model/src/standard/elements/smoke-co-alarm.element.ts.
	matterAttrSmokeExpressedState uint32 = 0x0000
	matterAttrSmokeState          uint32 = 0x0001
	matterAttrCOState             uint32 = 0x0002
	matterAttrBatteryAlert        uint32 = 0x0003
	matterAttrHardwareFaultAlert  uint32 = 0x0006 // mandatory — bool, false = no fault
	matterAttrEndOfServiceAlert   uint32 = 0x0007 // mandatory — EndOfServiceEnum: 0=Normal, 1=Expired
	matterAttrTestInProgress      uint32 = 0x0005 // mandatory bool; 0x08 is InterconnectSmokeAlarm (matter.js smoke-co-alarm-cluster.element.ts)

	matterAttrFeatureMap      uint32 = 0xFFFC
	matterAttrClusterRevision uint32 = 0xFFFD

	matterCmdOff uint32 = 0x00
	matterCmdOn  uint32 = 0x01
	// LT (Lighting) feature-gated OnOff commands — mandatory once LT is
	// advertised. matter.js on-off.element.ts:41,46,51 mark all three
	// conformance "LT".
	matterCmdOffWithEffect           uint32 = 0x40
	matterCmdOnWithRecallGlobalScene uint32 = 0x41
	matterCmdOnWithTimedOff          uint32 = 0x42

	matterOnOffClusterRevision        uint16 = 6
	matterBooleanStateClusterRevision uint16 = 2 // matter.js HEAD (@matter/model 0.16.11)
	// matterSmokeCOAlarmClusterRevision pinned to Matter 1.5.1
	// (connectedhomeip tag v1.5.1.0 / smoke-co-alarm-cluster.xml
	// globalAttribute 0xFFFD = 1). The 1.4 Mute-attribute promotion
	// did not bump the cluster revision — the value stays at 1.
	matterSmokeCOAlarmClusterRevision uint16 = 1

	// SmokeCOAlarm AlarmStateEnum (spec 2.11.5.1):
	// 0 = Normal, 1 = Warning, 2 = Critical.
	matterSmokeAlarmNormal   uint8 = 0
	matterSmokeAlarmWarning  uint8 = 1
	matterSmokeAlarmCritical uint8 = 2

	// SmokeCOAlarm FeatureMap bits (spec 2.11.4):
	// bit 0 = SMOKE, bit 1 = CO. HM-SWSD reports smoke only.
	matterSmokeCOFeatureSmoke uint32 = 1 << 0
)

var (
	errMatterUnknownAttribute = errors.New("matter: unknown attribute")
	errMatterUnknownCommand   = errors.New("matter: unknown command")
	errMatterValueType        = errors.New("matter: unexpected value type")
)

// MatterDeviceType implements [interfaces.MatterEndpointSource].
func (s *Siren) MatterDeviceType() uint16 { return matterDeviceTypeOnOffPlugInUnit }

// MatterEligibility marks Siren as partially mappable: OnOff covers the
// alarm on/off command surface; BooleanState is not mounted because it
// is non-conformant on OnOffPlugInUnit (see MatterClusterServers).
// Tone / optical selection has no Matter cluster equivalent and stays
// MQTT-only.
func (s *Siren) MatterEligibility() interfaces.MatterEligibilityVerdict {
	return interfaces.MatterEligibilityVerdict{
		State:      interfaces.MatterEligibilityPartial,
		DeviceType: matterDeviceTypeOnOffPlugInUnit,
		Clusters:   []uint32{matterClusterOnOff},
		Reason:     "Siren tone / optical selection is MQTT-only — OnOff covers alarm on/off; BooleanState omitted (non-conformant on OnOffPlugInUnit 0x010A per matter.js).",
	}
}

// MatterClusterServers returns OnOff (for the alarm-on/off command
// surface, LT feature and all — see sirenOnOffServer) plus the
// mandatory Groups + ScenesManagement stubs for the OnOffPlugInUnit
// (0x010A) device-type per matter.js
// packages/model/src/standard/elements/on-off-plug-in-unit.element.ts.
//
// BooleanState (0x0045) is intentionally absent: it is NOT part of the
// OnOffPlugInUnit cluster set in matter.js or the spec; mounting it is
// a non-conformant addition that strict controllers may reject as
// UnsupportedCluster. The "alarm active" state is surfaced only via the
// OnOff attribute, which is sufficient for the on/off alarm role.
// See docs/parity/by_design.md for the by-design rationale.
//
// Tone / optical selection lives outside Matter — see ADR 0012 §5
// "out of Matter scope" table.
func (s *Siren) MatterClusterServers() []interfaces.MatterClusterServer {
	return []interfaces.MatterClusterServer{
		sirenOnOffServer{s: s},
		wire.Groups{},
		wire.ScenesManagement{},
	}
}

// sirenOnOffServer projects Siren onto the Matter OnOff cluster.
// On/Off commands map to the default Siren TurnOn / TurnOff with the
// device-supplied default OnConfig (capability profile chooses the
// tone / light selection out-of-band).
//
// OnOffPlugInUnit (0x010A) mandates the LT (Lighting) feature on OnOff
// (on-off-plug-in-unit.element.ts:22-25), so this server advertises it
// and implements the four LT-gated attributes plus the three LT-gated
// commands — the same LT baseline
// internal/model/custom/switch/matter.go's Switch projection carries
// for the same device type. HM-ASIR has no on-timer / delayed-off /
// scene engine, so OnTime, OffWaitTime, and the three LT commands
// collapse to the plain On/Off path; GlobalSceneControl is reported
// false — matter.js's schema default for a device that has never
// executed on() (on-off.element.ts:29-30 declares no explicit default,
// so the bool starts false; OnOffServer.ts:102-103 only flips it true
// inside on()).
type sirenOnOffServer struct{ s *Siren }

func (s sirenOnOffServer) MatterClusterID() uint32 { return matterClusterOnOff }

func (s sirenOnOffServer) MatterRead(attrID uint32) (any, bool) {
	switch attrID {
	case matterAttrOnOffOnOff:
		// OnOff is a non-nullable bool per Matter §1.5.6.2 — chip-tool
		// returns CHIP_ERROR_WRONG_TLV_TYPE when it sees TLV null
		// where a bool is required. Default to FALSE on unobserved
		// state so the cluster surface stays spec-clean.
		on, _ := s.s.IsActive()
		return on, true
	case matterAttrOnOffGlobalSceneControl:
		// See the sirenOnOffServer doc comment: static false, this
		// projection has no scene engine to flip it live.
		return false, true
	case matterAttrOnOffOnTime, matterAttrOnOffOffWaitTime:
		// No on-timer / delayed-off engine; report the idle default (0).
		return uint16(0), true
	case matterAttrOnOffStartUpOnOff:
		// Nullable; null = "keep last state on startup" (the only state
		// this bridge tracks). matter.js OnOffServer.ts:39.
		return nil, true
	case matterAttrFeatureMap:
		return matterFeatureOnOffLT, true
	case matterAttrClusterRevision:
		return matterOnOffClusterRevision, true
	default:
		return nil, false
	}
}

func (s sirenOnOffServer) MatterWrite(ctx context.Context, attrID uint32, value any, priority hmenum.CommandPriority) error {
	switch attrID {
	case matterAttrOnOffOnOff:
		on, ok := value.(bool)
		if !ok {
			return fmt.Errorf("%w: OnOff write expected bool, got %T", errMatterValueType, value)
		}
		var err error
		if on {
			// TurnOn with the empty OnConfig: device-defined defaults pick the tone /
			// light selection.
			err = s.s.TurnOn(ctx, OnConfig{}, priority)
		} else {
			err = s.s.TurnOff(ctx, priority)
		}
		if err != nil {
			return err
		}
		s.s.dataVersion.Bump()
		return nil
	case matterAttrOnOffOnTime, matterAttrOnOffOffWaitTime:
		// RW VO (on-off.element.ts:31-32); accepted for conformance —
		// there is no on-timer / delayed-off engine behind these
		// counters on this projection, so the write is a validated no-op.
		if _, ok := matterWriteUint16(value); !ok {
			return fmt.Errorf("%w: OnTime/OffWaitTime write expected uint16, got %T", errMatterValueType, value)
		}
		return nil
	case matterAttrOnOffStartUpOnOff:
		// RW VM, nullable enum 0..2 (on-off.element.ts:33-36); accepted
		// for conformance, not persisted (see the sirenOnOffServer doc
		// comment).
		if value == nil {
			return nil
		}
		v, ok := matterWriteUint8(value)
		if !ok {
			return fmt.Errorf("%w: StartUpOnOff write expected enum8, got %T", errMatterValueType, value)
		}
		if v > 2 {
			return fmt.Errorf("%w: StartUpOnOff constraint 0..2, got %d", errMatterValueType, v)
		}
		return nil
	default:
		return fmt.Errorf("%w: 0x%04X", errMatterUnknownAttribute, attrID)
	}
}

// MinWritePrivilege implements
// [interfaces.MatterClusterAttributeWritePrivilege]: StartUpOnOff is
// RW VM per on-off.element.ts:34 (write access Manage); the countdown
// attributes stay at the RW VO default.
func (s sirenOnOffServer) MinWritePrivilege(attrID uint32) uint8 {
	if attrID == matterAttrOnOffStartUpOnOff {
		return 4 // Manage
	}
	return 3 // Operate
}

func (s sirenOnOffServer) MatterInvoke(ctx context.Context, cmdID uint32, _ any, priority hmenum.CommandPriority) (any, error) {
	var err error
	switch cmdID {
	case matterCmdOff:
		err = s.s.TurnOff(ctx, priority)
	case matterCmdOn:
		err = s.s.TurnOn(ctx, OnConfig{}, priority)
	case matterCmdOffWithEffect:
		// OffWithEffect (LT, mandatory): no dimming-effect engine on a
		// siren, so the effect identifier/variant are ignored and the
		// alarm is turned off. on-off.element.ts:41.
		err = s.s.TurnOff(ctx, priority)
	case matterCmdOnWithRecallGlobalScene:
		// OnWithRecallGlobalScene (LT, mandatory): no scene engine, so
		// recall collapses to a plain On. on-off.element.ts:46.
		err = s.s.TurnOn(ctx, OnConfig{}, priority)
	case matterCmdOnWithTimedOff:
		// OnWithTimedOff (LT, mandatory): no on-timer, so the timed-off
		// semantics are dropped and the alarm is turned on.
		// on-off.element.ts:51.
		err = s.s.TurnOn(ctx, OnConfig{}, priority)
	default:
		return nil, fmt.Errorf("%w: 0x%02X", errMatterUnknownCommand, cmdID)
	}
	if err != nil {
		return nil, err
	}
	s.s.dataVersion.Bump()
	return nil, nil
}

func (s sirenOnOffServer) MatterReportable() []uint32 {
	return []uint32{matterAttrOnOffOnOff}
}

// MatterAttributes lists every OnOff (0x0006) attribute the siren
// server implements via MatterRead. Apple Home's HAP service rebuild
// reads the full attribute set; without this the dispatcher falls back
// to MatterReportable's single attribute. OnOffPlugInUnit (0x010A)
// mandates the LT (Lighting) feature, so the four LT-gated attributes
// are enumerated. matter.js on-off.element.ts:30-36.
func (s sirenOnOffServer) MatterAttributes() []uint32 {
	return []uint32{
		matterAttrOnOffOnOff,
		matterAttrOnOffGlobalSceneControl,
		matterAttrOnOffOnTime,
		matterAttrOnOffOffWaitTime,
		matterAttrOnOffStartUpOnOff,
	}
}

// MatterAcceptedCommands implements [interfaces.MatterClusterCommandLister]
// for the OnOff cluster (0x0006). Enumerates the OnOff baseline plus the
// three LT-mandatory commands so AcceptedCommandList is populated for
// chip-tool / Apple Home conformance reads. matter.js on-off.element.ts:
// Off (0x00, M), On (0x01), OffWithEffect (0x40, LT),
// OnWithRecallGlobalScene (0x41, LT), OnWithTimedOff (0x42, LT). Toggle
// (0x02) is absent — Siren has no toggle command in its wire surface.
func (s sirenOnOffServer) MatterAcceptedCommands() []uint32 {
	return []uint32{
		matterCmdOff,
		matterCmdOn,
		matterCmdOffWithEffect,
		matterCmdOnWithRecallGlobalScene,
		matterCmdOnWithTimedOff,
	}
}

// MatterGeneratedCommands implements [interfaces.MatterClusterCommandLister]
// for the OnOff cluster (0x0006). OnOff commands have no response payload.
func (s sirenOnOffServer) MatterGeneratedCommands() []uint32 { return nil }

// Compile-time assertions: sirenOnOffServer implements the attribute +
// command listers plus the write-privilege provider the dispatcher
// uses to populate the global metadata attributes and enforce ACL.
var (
	_ interfaces.MatterClusterAttributeLister         = sirenOnOffServer{}
	_ interfaces.MatterClusterCommandLister           = sirenOnOffServer{}
	_ interfaces.MatterClusterAttributeWritePrivilege = sirenOnOffServer{}
)

// MatterDeviceType implements [interfaces.MatterEndpointSource].
func (s *SmokeSiren) MatterDeviceType() uint16 { return matterDeviceTypeSmokeCOAlarm }

// MatterClusterServers returns the SmokeCOAlarm cluster.
func (s *SmokeSiren) MatterClusterServers() []interfaces.MatterClusterServer {
	return []interfaces.MatterClusterServer{smokeCOServer{s: s}}
}

// smokeCOServer projects SmokeSiren onto the SmokeCOAlarm cluster.
// Maps the HM SmokeAlarmStatus enum onto AlarmStateEnum:
//
//	IDLE_OFF / IDLE_ON       → Normal (0)
//	SECONDARY_ALARM          → Warning (1) — peer is alarming
//	PRIMARY_ALARM / INTRUSION → Critical (2) — local alarm fires
type smokeCOServer struct{ s *SmokeSiren }

func (s smokeCOServer) MatterClusterID() uint32 { return matterClusterSmokeCOAlarm }

func smokeStatusToAlarmState(st SmokeAlarmStatus) uint8 {
	switch st {
	case SmokeStatusPrimaryAlarm, SmokeStatusIntrusion:
		return matterSmokeAlarmCritical
	case SmokeStatusSecondaryAlarm:
		return matterSmokeAlarmWarning
	default:
		return matterSmokeAlarmNormal
	}
}

func (s smokeCOServer) MatterRead(attrID uint32) (any, bool) {
	switch attrID {
	case matterAttrSmokeExpressedState:
		// Value temporarily unavailable (e.g. CCU circuit-breaker open): return
		// (nil, true) so the dispatcher encodes TLV null + Success. See
		// climate/matter.go for the full rationale.
		st, ok := s.s.Status()
		if !ok {
			return nil, true
		}
		// ExpressedState mirrors SmokeState for smoke-only devices.
		return smokeStatusToAlarmState(st), true
	case matterAttrSmokeState:
		st, ok := s.s.Status()
		if !ok {
			return nil, true
		}
		return smokeStatusToAlarmState(st), true
	case matterAttrCOState:
		// HM-SWSD has no CO sensor — return Normal so Matter clients
		// reading both attributes get a coherent picture.
		return matterSmokeAlarmNormal, true
	case matterAttrBatteryAlert:
		// Battery condition is exposed via Power Source cluster on the
		// host endpoint (P1, see ADR 0012 §6 generic table). The
		// SmokeCOAlarm BatteryAlert attribute mirrors but is not
		// authoritative; report Normal until the Power-Source-cluster
		// reflection is wired.
		return matterSmokeAlarmNormal, true
	case matterAttrHardwareFaultAlert:
		// HardwareFaultAlert: false = no hardware fault detected.
		// matter.js smoke-co-alarm.element.ts: mandatory bool attribute.
		return false, true
	case matterAttrEndOfServiceAlert:
		// EndOfServiceAlert: 0 = Normal (not expired).
		// matter.js smoke-co-alarm.element.ts EndOfServiceEnum: 0=Normal, 1=Expired.
		return uint8(0), true
	case matterAttrTestInProgress:
		return false, true
	case matterAttrFeatureMap:
		return matterSmokeCOFeatureSmoke, true
	case matterAttrClusterRevision:
		return matterSmokeCOAlarmClusterRevision, true
	default:
		return nil, false
	}
}

func (s smokeCOServer) MatterWrite(_ context.Context, attrID uint32, _ any, _ hmenum.CommandPriority) error {
	return fmt.Errorf("%w: 0x%04X", errMatterUnknownAttribute, attrID)
}

func (s smokeCOServer) MatterInvoke(_ context.Context, cmdID uint32, _ any, _ hmenum.CommandPriority) (any, error) {
	// SmokeCOAlarm has cluster commands (SelfTestRequest etc.) but
	// HM-SWSD exposes none of them through the wire layer. Reject
	// every cmdID; a SelfTest mapping is possible if a HM equivalent
	// surfaces.
	return nil, fmt.Errorf("%w: 0x%02X", errMatterUnknownCommand, cmdID)
}

func (s smokeCOServer) MatterReportable() []uint32 {
	return []uint32{matterAttrSmokeExpressedState, matterAttrSmokeState}
}

// MatterAttributes lists every SmokeCOAlarm (0x005C) attribute the
// server implements via MatterRead. Apple Home's HAP service rebuild
// reads the full attribute set; without this the dispatcher falls back
// to MatterReportable's two-attribute surface.
func (s smokeCOServer) MatterAttributes() []uint32 {
	return []uint32{
		matterAttrSmokeExpressedState,
		matterAttrSmokeState,
		matterAttrCOState,
		matterAttrBatteryAlert,
		matterAttrHardwareFaultAlert,
		matterAttrEndOfServiceAlert,
		matterAttrTestInProgress,
	}
}

// matterWriteUint16 coerces an OnOff LT attribute-write value (OnTime /
// OffWaitTime) into uint16. The IM write layer delivers decoded TLV
// unsigned ints as uint64; the narrower uint16 case keeps in-package
// callers working.
func matterWriteUint16(value any) (uint16, bool) {
	switch v := value.(type) {
	case uint64:
		return uint16(v & 0xFFFF), true
	case uint16:
		return v, true
	default:
		return 0, false
	}
}

// matterWriteUint8 coerces a StartUpOnOff attribute-write value into uint8.
func matterWriteUint8(value any) (uint8, bool) {
	switch v := value.(type) {
	case uint64:
		return uint8(v & 0xFF), true
	case uint8:
		return v, true
	default:
		return 0, false
	}
}
