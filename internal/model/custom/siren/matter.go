// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package siren

import (
	"context"
	"slices"

	onoffdef "github.com/SukramJ/go-fabric/cluster/spec/onoff"
	smokecoalarmdef "github.com/SukramJ/go-fabric/cluster/spec/smokecoalarm"
	fabricdevice "github.com/SukramJ/go-fabric/device"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
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

// Compile-time assertions: Siren and SmokeSiren participate in the
// Matter source surface (ADR 0012). Siren projects onto an
// OnOffPlugInUnit (0x010A) endpoint with the OnOff cluster (BooleanState
// is not conformant there) — Matter has no Siren cluster covering
// tone/optical configuration,
// so those features remain MQTT-only per ADR 0012 §5 ("out of Matter
// scope" table). SmokeSiren projects onto a SmokeCOAlarm (0x0076)
// endpoint with the SmokeCOAlarm cluster (0x005C). SoundPlayer projects
// onto a Speaker (0x0022) endpoint — see sound_matter.go.
var (
	_ interfaces.MatterDeviceSource   = (*Siren)(nil)
	_ interfaces.MatterDeviceSource   = (*SmokeSiren)(nil)
	_ interfaces.MatterChangeNotifier = (*Siren)(nil)
	_ interfaces.MatterChangeNotifier = (*SmokeSiren)(nil)
)

// OnMatterValueChanged implements [interfaces.MatterChangeNotifier] for Siren.
// The Matter surface is OnOff, driven by the acoustic and optical
// active-state DPs; fan both into the callback so a siren that starts
// or stops outside Apple (triggered by a CCU program or the device itself)
// dirty-marks the endpoint and reaches Apple's Subscribe. Tone/light
// selection stays MQTT-only (no Matter cluster), so those DPs are not wired.
func (s *Siren) OnMatterValueChanged(cb func()) func() {
	if s == nil || cb == nil {
		return func() {}
	}
	return custom.CombineUnsubs(
		s.acousticActive.OnMatterValueChanged(cb),
		s.opticalActive.OnMatterValueChanged(cb),
	)
}

// OnMatterValueChanged implements [interfaces.MatterChangeNotifier] for
// SmokeSiren. The SmokeCOAlarm cluster's ExpressedState/alarm attributes are
// driven by the status DP; wiring it means a smoke/CO alarm raised at the
// device reaches Apple's Subscribe rather than only surfacing after a manual
// re-read.
func (s *SmokeSiren) OnMatterValueChanged(cb func()) func() {
	if s == nil || cb == nil {
		return func() {}
	}
	return custom.CombineUnsubs(
		s.status.OnMatterValueChanged(cb),
	)
}

// MatterDeviceType implements [interfaces.MatterDeviceSource].
func (s *Siren) MatterDeviceType() uint16 { return uint16(fabricdevice.OnOffPlugInUnitID) }

// MatterEligibility marks Siren as partially mappable: OnOff covers the
// alarm on/off command surface; BooleanState is not mounted because it
// is non-conformant on OnOffPlugInUnit.
// Tone / optical selection has no Matter cluster equivalent and stays
// MQTT-only.
func (s *Siren) MatterEligibility() interfaces.MatterEligibilityVerdict {
	return interfaces.MatterEligibilityVerdict{
		State:      interfaces.MatterEligibilityPartial,
		DeviceType: uint16(fabricdevice.OnOffPlugInUnitID),
		Clusters:   []uint32{onoffdef.ClusterID},
		Reason:     "Siren tone / optical selection is MQTT-only — OnOff covers alarm on/off; BooleanState omitted (non-conformant on OnOffPlugInUnit 0x010A per matter.js).",
	}
}

// MatterDevice implements [interfaces.MatterDeviceSource]: an
// OnOffPlugInUnit with the OnOff cluster declared, built once per siren.
// OnOffPlugInUnit (0x010A) mandates the LT (Lighting) feature on OnOff
// (on-off-plug-in-unit.element.ts:27-30), so the declaration selects it;
// the LT attributes, the timed commands and the scene paths are the kit
// server's. The Groups and ScenesManagement clusters the device type
// mandates are stack state the endpoint assembler mounts.
//
// The Sink collapses every switch-on onto [Siren.TurnOn] with the empty
// [OnConfig] — the device-defined defaults pick the tone and the light —
// and every switch-off onto [Siren.TurnOff], at [matterDispatchPriority].
// An OnWithTimedOff ends the alarm after OnTime through the server's
// countdown (OnOffServer.ts:216-232), whose off reaches the same Sink. The
// acoustic and optical ACOUSTIC_ALARM_ACTIVE / OPTICAL_ALARM_ACTIVE
// confirmations feed the CCU's state back through
// [generic.SetMatterOnOff], so a CCU program raising the alarm reaches a
// subscribed controller.
//
// The server starts from the observed activity, false while nothing was
// observed: OnOff is a non-nullable bool (Matter §1.5.6.2; chip-tool
// answers CHIP_ERROR_WRONG_TLV_TYPE to TLV null there).
func (s *Siren) MatterDevice(opts fabricdevice.Options) (*fabricdevice.Device, error) {
	return s.matterDevice.Device(func() (*fabricdevice.Device, error) {
		active, _ := s.IsActive()
		d, err := generic.NewMatterDevice(s, uint16(fabricdevice.OnOffPlugInUnitID), opts,
			generic.MatterOnOffDeclaration(true, active, func(ctx context.Context, on bool) error {
				if on {
					return s.TurnOn(ctx, OnConfig{}, matterDispatchPriority)
				}
				return s.TurnOff(ctx, matterDispatchPriority)
			}))
		if err != nil {
			return nil, err
		}
		syncActive := func(_, _ bool) {
			active, _ := s.IsActive()
			generic.SetMatterOnOff(d, active)
		}
		if s.acousticActive != nil {
			_ = s.acousticActive.OnConfirmedUpdate(syncActive)
		}
		if s.opticalActive != nil {
			_ = s.opticalActive.OnConfirmedUpdate(syncActive)
		}
		// A confirmation that landed between the initial read and the
		// hook registration is not lost: the current value is stored
		// again, which is a no-op when nothing changed.
		if active, observed := s.IsActive(); observed {
			generic.SetMatterOnOff(d, active)
		}
		return d, nil
	})
}

// MatterDeviceType implements [interfaces.MatterDeviceSource].
func (s *SmokeSiren) MatterDeviceType() uint16 {
	return uint16(fabricdevice.SmokeCoAlarmID)
}

// MatterDevice implements [interfaces.MatterDeviceSource]: a SmokeCoAlarm
// with the SmokeCOAlarm cluster declared over this siren's status, built
// once so the server's event baseline survives reassembly. HM-SWSD
// carries a smoke sensor only, so the CO feature and every
// CO-conformance attribute stay off the wire.
func (s *SmokeSiren) MatterDevice(opts fabricdevice.Options) (*fabricdevice.Device, error) {
	return s.matterDevice.Device(func() (*fabricdevice.Device, error) {
		return generic.NewMatterDevice(s, uint16(fabricdevice.SmokeCoAlarmID), opts,
			smokecoalarmdef.Declare{
				StateSource: smokeAlarmSource{s: s},
				Features:    smokecoalarmdef.FeatureSmokeAlarm,
			})
	})
}

// alarmRefresher is the part of the SmokeCOAlarm server the siren drives
// itself: re-reading its source and emitting the events the change calls
// for.
type alarmRefresher interface{ Refresh() }

// refreshMatterAlarm lets the alarm server diff the status it now reads
// against the previous one and emit the SmokeAlarm / AllClear events the
// change calls for. Before the first assembly there is no device and
// nothing to tell.
func (s *SmokeSiren) refreshMatterAlarm() {
	d := s.matterDevice.Current()
	if d == nil {
		return
	}
	if srv, ok := d.Server(smokecoalarmdef.ClusterID).(alarmRefresher); ok {
		srv.Refresh()
	}
}

// smokeAlarmSource is the alarm server's host port over a SmokeSiren.
// Only SmokeState is the device's to report; ExpressedState is derived by
// the server from it, and every other attribute keeps the matter.js initial
// state (no CO sensor, no battery or fault report on this channel). An
// unread status reports Normal: every SmokeCoAlarm attribute is
// non-nullable.
type smokeAlarmSource struct{ s *SmokeSiren }

// SmokeCOState implements [smokecoalarmdef.StateSource].
func (src smokeAlarmSource) SmokeCOState() smokecoalarmdef.State {
	var st smokecoalarmdef.State
	if status, ok := src.s.Status(); ok {
		st.SmokeState = smokeStatusToAlarmState(status)
	}
	return st
}

// smokeStatusToAlarmState maps the HM status onto AlarmStateEnum.
//
// Which labels mean smoke is not decided here: it is read from
// [hmenum.SmokeDetectorAlarmStatusSmokeLabels], the one place the domain
// answers that question, so this plane cannot drift from the safety
// classifier and the derived SMOKE_ALARM sensor. It did: INTRUSION_ALARM was
// reported as Critical, i.e. as a fire, although it means the installation
// drove this smoke detector as a *siren* for a burglar alarm — a command the
// domain sent, not a detection the device made. Matter's SmokeState is
// "whether the device's smoke sensor is currently triggering a smoke alarm"
// (matter.js packages/types/src/clusters/smoke-co-alarm.d.ts:150), so an
// intrusion belongs at Normal however loud the sounder is.
//
// Only the severity of a genuine smoke label is decided locally.
func smokeStatusToAlarmState(st SmokeAlarmStatus) smokecoalarmdef.AlarmStateEnum {
	if !slices.Contains(hmenum.SmokeDetectorAlarmStatusSmokeLabels(), string(st)) {
		return smokecoalarmdef.AlarmStateNormal
	}
	if st == SmokeStatusSecondaryAlarm {
		return smokecoalarmdef.AlarmStateWarning
	}
	return smokecoalarmdef.AlarmStateCritical
}
