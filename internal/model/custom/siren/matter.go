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
// surface) plus the mandatory Groups + ScenesManagement stubs for the
// OnOffPlugInUnit (0x010A) device-type per matter.js
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
	case matterAttrFeatureMap:
		return uint32(0), true
	case matterAttrClusterRevision:
		return matterOnOffClusterRevision, true
	default:
		return nil, false
	}
}

func (s sirenOnOffServer) MatterWrite(ctx context.Context, attrID uint32, value any, priority hmenum.CommandPriority) error {
	if attrID != matterAttrOnOffOnOff {
		return fmt.Errorf("%w: 0x%04X", errMatterUnknownAttribute, attrID)
	}
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
}

func (s sirenOnOffServer) MatterInvoke(ctx context.Context, cmdID uint32, _ any, priority hmenum.CommandPriority) (any, error) {
	var err error
	switch cmdID {
	case matterCmdOff:
		err = s.s.TurnOff(ctx, priority)
	case matterCmdOn:
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
// to MatterReportable's single attribute.
func (s sirenOnOffServer) MatterAttributes() []uint32 {
	return []uint32{matterAttrOnOffOnOff}
}

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
