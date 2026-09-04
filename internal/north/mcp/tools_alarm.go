// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mcp

import (
	"context"
	"fmt"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/SukramJ/openccu-loom/internal/alarm/engine"
	"github.com/SukramJ/openccu-loom/internal/model/security"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// AlarmReader is the read half of the alarm projection. Satisfied by
// *alarm.Service via the engine; nil leaves every alarm tool
// unregistered, matching how the other optional seams behave.
type AlarmReader interface {
	Zones() []engine.ZoneSnapshot
	TriggeredMotionSensors(zoneID string) []engine.TriggeredMotionSensor
}

// AlarmController is the write half. It is registered only when
// AllowWrites is set, so the default read-only posture cannot arm or
// disarm anything.
//
// Arming takes no code argument on purpose: a code is a human
// authorization factor, and an assistant that could supply one would
// defeat the point of having it. Zones whose policy requires a code
// therefore refuse an MCP arm, which is the correct outcome rather
// than a limitation to work around.
type AlarmController interface {
	Arm(ctx context.Context, zoneID string, mode hmenum.AlarmMode) error
	Disarm(ctx context.Context, zoneID string) error
	ResetMotion(ctx context.Context, zoneID string) (reset, failed int)
}

// SecurityReader projects the Security & Safety domain snapshot.
type SecurityReader interface {
	Snapshot() security.Snapshot
}

// --- alarm read tools -------------------------------------------------

type listAlarmZonesIn struct{}

type alarmZoneSummary struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	State string `json:"state" jsonschema:"disarmed, arming, armed, pending or triggered"`
	Mode  string `json:"mode,omitempty" jsonschema:"the armed mode, empty while disarmed"`
	// TriggeredMotion counts the latched motion detectors a reset would
	// clear. Non-zero on a disarmed zone usually explains why an arm
	// refuses.
	TriggeredMotion int      `json:"triggered_motion"`
	Bypassed        []string `json:"bypassed,omitempty"`
	IncidentID      int64    `json:"incident_id,omitempty"`
	CountdownKind   string   `json:"countdown_kind,omitempty"`
	CountdownS      int      `json:"countdown_seconds,omitempty"`
}

type listAlarmZonesOut struct {
	Zones []alarmZoneSummary `json:"zones"`
}

func registerListAlarmZones(s *mcpsdk.Server, d Deps) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "list_alarm_zones",
		Description: "List every alarm zone with its arm state, active countdown and the number of latched motion detectors.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, _ listAlarmZonesIn) (*mcpsdk.CallToolResult, listAlarmZonesOut, error) {
		snaps := d.Alarm.Zones()
		out := listAlarmZonesOut{Zones: make([]alarmZoneSummary, 0, len(snaps))}
		for i := range snaps {
			z := &snaps[i]
			out.Zones = append(out.Zones, alarmZoneSummary{
				ID:              z.ID,
				Name:            z.Name,
				State:           string(z.State),
				Mode:            string(z.Mode),
				TriggeredMotion: len(d.Alarm.TriggeredMotionSensors(z.ID)),
				Bypassed:        z.Bypassed,
				IncidentID:      z.IncidentID,
				CountdownKind:   countdownKind(z.TimerKind),
				CountdownS:      countdownSeconds(z.TimerKind, z.TimerRemaining),
			})
		}
		return nil, out, nil
	})
}

type listTriggeredMotionIn struct {
	ZoneID string `json:"zone_id,omitempty" jsonschema:"optional alarm zone to scope the list; omit for every zone"`
}

type triggeredMotionSummary struct {
	SensorID       string `json:"sensor_id"`
	ZoneID         string `json:"zone_id"`
	Name           string `json:"name,omitempty"`
	ChannelAddress string `json:"channel_address"`
	Parameter      string `json:"parameter"`
}

type listTriggeredMotionOut struct {
	Sensors []triggeredMotionSummary `json:"sensors"`
}

func registerListTriggeredMotion(s *mcpsdk.Server, d Deps) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name: "list_triggered_motion",
		Description: "List the motion detectors that are currently latched and can be cleared. " +
			"A latched detector reads as open and can block arming.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in listTriggeredMotionIn) (*mcpsdk.CallToolResult, listTriggeredMotionOut, error) {
		sensors := d.Alarm.TriggeredMotionSensors(in.ZoneID)
		out := listTriggeredMotionOut{Sensors: make([]triggeredMotionSummary, 0, len(sensors))}
		for _, s := range sensors {
			out.Sensors = append(out.Sensors, triggeredMotionSummary{
				SensorID:       s.SensorID,
				ZoneID:         s.ZoneID,
				Name:           s.Name,
				ChannelAddress: s.ChannelAddress,
				Parameter:      s.Parameter,
			})
		}
		return nil, out, nil
	})
}

// --- security read tool -----------------------------------------------

type getSecurityStatusIn struct{}

type securityClassSummary struct {
	Class    string `json:"class"`
	Active   bool   `json:"active"`
	Severity string `json:"severity,omitempty"`
	Sources  int    `json:"sources"`
}

type securityFaultSummary struct {
	ID       string `json:"id"`
	Class    string `json:"class"`
	Reason   string `json:"reason"`
	Severity string `json:"severity"`
	Name     string `json:"name,omitempty"`
	// Acknowledged reports that an operator has seen the fault. It never
	// means the condition cleared — the fault stands either way.
	Acknowledged bool `json:"acknowledged"`
}

type getSecurityStatusOut struct {
	Severity string                 `json:"severity"`
	Classes  []securityClassSummary `json:"classes"`
	Faults   []securityFaultSummary `json:"faults"`
}

func registerGetSecurityStatus(s *mcpsdk.Server, d Deps) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name: "get_security_status",
		Description: "Overall Security & Safety state: the folded severity, the hazard classes that have known sources, " +
			"and the standing faults.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, _ getSecurityStatusIn) (*mcpsdk.CallToolResult, getSecurityStatusOut, error) {
		snap := d.Security.Snapshot()
		out := getSecurityStatusOut{
			Severity: string(snap.Severity),
			Classes:  make([]securityClassSummary, 0, len(snap.Classes)),
			Faults:   make([]securityFaultSummary, 0, len(snap.Faults)),
		}
		for class, st := range snap.Classes {
			out.Classes = append(out.Classes, securityClassSummary{
				Class:    string(class),
				Active:   st.Active,
				Severity: string(st.Severity),
				Sources:  len(st.Sources),
			})
		}
		sortByClass(out.Classes)
		for i := range snap.Faults {
			f := &snap.Faults[i]
			out.Faults = append(out.Faults, securityFaultSummary{
				ID:           f.ID,
				Class:        string(f.Class),
				Reason:       string(f.Reason),
				Severity:     string(f.Severity),
				Name:         f.Source.Name,
				Acknowledged: f.AcknowledgedAtMS != 0,
			})
		}
		return nil, out, nil
	})
}

// --- alarm write tools ------------------------------------------------

type armAlarmZoneIn struct {
	ZoneID string `json:"zone_id" jsonschema:"the alarm zone id from list_alarm_zones"`
	Mode   string `json:"mode" jsonschema:"perimeter, full, night, vacation or custom"`
}

type armAlarmZoneOut struct {
	Armed bool `json:"armed"`
}

func registerArmAlarmZone(s *mcpsdk.Server, d Deps) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name: "arm_alarm_zone",
		Description: "Arm one alarm zone. Fails when a sensor blocks the arm — read list_alarm_zones and " +
			"list_triggered_motion first. Zones whose policy requires a code cannot be armed from here.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in armAlarmZoneIn) (*mcpsdk.CallToolResult, armAlarmZoneOut, error) {
		mode := hmenum.AlarmMode(in.Mode)
		if !mode.Armed() {
			return nil, armAlarmZoneOut{}, fmt.Errorf("mode %q is not an armed mode", in.Mode)
		}
		if err := d.AlarmControl.Arm(ctx, in.ZoneID, mode); err != nil {
			return nil, armAlarmZoneOut{}, err
		}
		return nil, armAlarmZoneOut{Armed: true}, nil
	})
}

type disarmAlarmZoneIn struct {
	ZoneID string `json:"zone_id" jsonschema:"the alarm zone id from list_alarm_zones"`
}

type disarmAlarmZoneOut struct {
	Disarmed bool `json:"disarmed"`
}

func registerDisarmAlarmZone(s *mcpsdk.Server, d Deps) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "disarm_alarm_zone",
		Description: "Disarm one alarm zone. Zones whose policy requires a disarm code cannot be disarmed from here.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in disarmAlarmZoneIn) (*mcpsdk.CallToolResult, disarmAlarmZoneOut, error) {
		if err := d.AlarmControl.Disarm(ctx, in.ZoneID); err != nil {
			return nil, disarmAlarmZoneOut{}, err
		}
		return nil, disarmAlarmZoneOut{Disarmed: true}, nil
	})
}

type resetMotionIn struct {
	ZoneID string `json:"zone_id,omitempty" jsonschema:"optional alarm zone to scope the reset; omit to clear every zone"`
}

type resetMotionOut struct {
	Reset  int `json:"reset"`
	Failed int `json:"failed"`
}

func registerResetMotion(s *mcpsdk.Server, d Deps) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name: "reset_motion",
		Description: "Clear the latched motion detectors of one zone, or of every zone. " +
			"Detectors that are not triggered are left alone.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in resetMotionIn) (*mcpsdk.CallToolResult, resetMotionOut, error) {
		reset, failed := d.AlarmControl.ResetMotion(ctx, in.ZoneID)
		return nil, resetMotionOut{Reset: reset, Failed: failed}, nil
	})
}

// sortByClass keeps the class list deterministic for a reasoning
// client, which otherwise sees Go's randomised map order.
func sortByClass(in []securityClassSummary) {
	for i := 1; i < len(in); i++ {
		for j := i; j > 0 && in[j].Class < in[j-1].Class; j-- {
			in[j], in[j-1] = in[j-1], in[j]
		}
	}
}

// countdownKind reports the timer kind only when it is one the countdown
// contract admits (exit_delay / entry_delay). The engine owns that question;
// this plane used to pass any kind through, so a zone in pre-alarm or trigger
// reported a countdown kind no client declares.
func countdownKind(kind string) string {
	if !engine.IsCountdownTimerKind(kind) {
		return ""
	}
	return kind
}

// countdownSeconds is [countdownKind]'s remaining-value counterpart.
func countdownSeconds(kind string, remaining time.Duration) int {
	if !engine.IsCountdownTimerKind(kind) {
		return 0
	}
	return int(remaining.Seconds())
}
