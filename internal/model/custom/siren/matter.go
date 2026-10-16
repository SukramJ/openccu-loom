// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package siren

import (
	"context"
	"fmt"
	"slices"
	"sync"

	"github.com/SukramJ/go-fabric/cluster/alarm"
	"github.com/SukramJ/go-fabric/cluster/onoff"
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
// OnOffPlugInUnit (0x010A) endpoint with OnOff + BooleanState clusters
// — Matter 1.5.1 has no Siren-cluster covering tone/optical configuration,
// so those features remain MQTT-only per ADR 0012 §5 ("out of Matter
// scope" table). SmokeSiren projects onto a SmokeCOAlarm (0x0076)
// endpoint with the SmokeCOAlarm cluster (0x005C). SoundPlayer projects
// onto a Speaker (0x0022) endpoint — see sound_matter.go.
var (
	_ interfaces.MatterEndpointSource     = (*Siren)(nil)
	_ interfaces.MatterEndpointSource     = (*SmokeSiren)(nil)
	_ interfaces.MatterClusterDataVersion = (*Siren)(nil)
	_ interfaces.MatterClusterDataVersion = (*SmokeSiren)(nil)
	_ interfaces.MatterChangeNotifier     = (*Siren)(nil)
	_ interfaces.MatterChangeNotifier     = (*SmokeSiren)(nil)
)

// MatterDataVersion implements [interfaces.MatterClusterDataVersion] for Siren.
// Bumped on every successful MatterWrite / MatterInvoke.
func (s *Siren) MatterDataVersion() uint32 { return s.dataVersion.Current() }

// OnMatterValueChanged implements [interfaces.MatterChangeNotifier] for Siren.
// The Matter surface is OnOff + BooleanState driven by the acoustic and
// optical active-state DPs; fan both into the callback so a siren that starts
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

// MatterDataVersion implements [interfaces.MatterClusterDataVersion] for SmokeSiren.
// SmokeCOAlarm has no client-writable attributes / commands in 0.1.0; the version
// is reserved for when SelfTestRequest is wired.
func (s *SmokeSiren) MatterDataVersion() uint32 { return s.dataVersion.Current() }

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

// MatterDeviceType implements [interfaces.MatterEndpointSource].
func (s *Siren) MatterDeviceType() uint16 { return fabricdevice.OnOffPlugInUnitID }

// MatterEligibility marks Siren as partially mappable: OnOff covers the
// alarm on/off command surface; BooleanState is not mounted because it
// is non-conformant on OnOffPlugInUnit (see MatterClusterServers).
// Tone / optical selection has no Matter cluster equivalent and stays
// MQTT-only.
func (s *Siren) MatterEligibility() interfaces.MatterEligibilityVerdict {
	return interfaces.MatterEligibilityVerdict{
		State:      interfaces.MatterEligibilityPartial,
		DeviceType: fabricdevice.OnOffPlugInUnitID,
		Clusters:   []uint32{onoff.ClusterID},
		Reason:     "Siren tone / optical selection is MQTT-only — OnOff covers alarm on/off; BooleanState omitted (non-conformant on OnOffPlugInUnit 0x010A per matter.js).",
	}
}

// MatterClusterServers implements [interfaces.MatterEndpointSource]: the
// OnOff cluster, served by go-fabric's [onoff.Server] (ADR 0084 phase 2).
// OnOffPlugInUnit (0x010A) mandates the LT (Lighting) feature on OnOff
// (on-off-plug-in-unit.element.ts:22-25), so the server advertises it;
// the LT attributes, the timed commands and the scene paths are the
// stack's. The Groups and ScenesManagement clusters the device type
// mandates are stack state the endpoint assembler mounts.
func (s *Siren) MatterClusterServers() []interfaces.MatterClusterServer {
	return []interfaces.MatterClusterServer{s.matterOnOffServer()}
}

// matterOnOffServer builds the siren's OnOff server once. The Sink
// collapses every switch-on onto [Siren.TurnOn] with the empty
// [OnConfig] — the device-defined defaults pick the tone and the light —
// and every switch-off onto [Siren.TurnOff], at [matterDispatchPriority].
// An OnWithTimedOff now ends the alarm after OnTime through the stack's
// countdown (OnOffServer.ts:102), whose off reaches the same Sink. The
// acoustic and optical ACOUSTIC_ALARM_ACTIVE / OPTICAL_ALARM_ACTIVE
// confirmations feed the CCU's state back through [onoff.Server.Set],
// the one path that stores without asking the Sink, so a CCU program
// raising the alarm reaches a subscribed controller.
//
// The server starts from the observed activity, false while nothing was
// observed: OnOff is a non-nullable bool (Matter §1.5.6.2; chip-tool
// answers CHIP_ERROR_WRONG_TLV_TYPE to TLV null there).
func (s *Siren) matterOnOffServer() *onoff.Server {
	return s.matterOnOff.Server(func() *onoff.Server {
		active, _ := s.IsActive()
		srv := generic.MustMatterOnOffServer(generic.MatterOnOffConfig{
			Lighting:    true,
			DataVersion: &s.dataVersion,
			Initial:     active,
			Sink: func(ctx context.Context, on bool) error {
				if on {
					return s.TurnOn(ctx, OnConfig{}, matterDispatchPriority)
				}
				return s.TurnOff(ctx, matterDispatchPriority)
			},
		})
		syncActive := func(_, _ bool) {
			active, _ := s.IsActive()
			srv.Set(active)
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
			srv.Set(active)
		}
		return srv
	})
}

// MatterDeviceType implements [interfaces.MatterEndpointSource].
func (s *SmokeSiren) MatterDeviceType() uint16 { return fabricdevice.SmokeCoAlarmID }

// MatterClusterServers returns the SmokeCOAlarm cluster: go-fabric's
// alarm.Server over this siren's status, built once so the stack's event
// baseline and data version survive reassembly.
func (s *SmokeSiren) MatterClusterServers() []interfaces.MatterClusterServer {
	srv := s.matterAlarm.get(func() *alarm.Server {
		srv, err := alarm.NewServer(alarm.Config{
			Source: smokeAlarmSource{s: s},
			// HM-SWSD carries a smoke sensor only, so the CO feature and
			// every CO-conformance attribute stay off the wire.
			Features:    alarm.FeatureSmokeAlarm,
			DataVersion: &s.dataVersion,
		})
		if err != nil {
			// invariant: the configuration is a constant SMOKE-only
			// selection the cluster's conformance accepts.
			panic(fmt.Sprintf("siren: smoke alarm server: %v", err))
		}
		return srv
	})
	return []interfaces.MatterClusterServer{srv}
}

// refreshMatterAlarm lets the alarm server diff the status it now reads
// against the previous one and emit the SmokeAlarm / AllClear events the
// change calls for. Before the first assembly there is no server and
// nothing to tell.
func (s *SmokeSiren) refreshMatterAlarm() {
	if srv := s.matterAlarm.current(); srv != nil {
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

// SmokeCOState implements alarm.StateSource.
func (src smokeAlarmSource) SmokeCOState() alarm.State {
	var st alarm.State
	if status, ok := src.s.Status(); ok {
		st.SmokeState = smokeStatusToAlarmState(status)
	}
	return st
}

// matterAlarmHolder builds the alarm server once and hands the same value
// back on every later assembly.
type matterAlarmHolder struct {
	mu  sync.Mutex
	srv *alarm.Server
}

func (h *matterAlarmHolder) get(build func() *alarm.Server) *alarm.Server {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.srv == nil {
		h.srv = build()
	}
	return h.srv
}

func (h *matterAlarmHolder) current() *alarm.Server {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.srv
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
func smokeStatusToAlarmState(st SmokeAlarmStatus) alarm.AlarmState {
	if !slices.Contains(hmenum.SmokeDetectorAlarmStatusSmokeLabels(), string(st)) {
		return alarm.AlarmNormal
	}
	if st == SmokeStatusSecondaryAlarm {
		return alarm.AlarmWarning
	}
	return alarm.AlarmCritical
}
