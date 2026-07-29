// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package engine

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// Engine-level defaults. The trigger-time default and ceiling mirror
// the bounded-activation rule of docs/alarm-concept.md §2 (S1): the
// engine never runs an unbounded triggered phase; long alarm phases
// are bounded re-trigger cycles.
const (
	// DefaultTriggerSeconds is the default length of one triggered
	// phase when the mode does not configure one.
	DefaultTriggerSeconds = 180
	// MaxTriggerSeconds is the hard ceiling for one triggered phase.
	MaxTriggerSeconds = 600
	// DefaultRestartLoopBreakerK is the number of restore-driven
	// output re-fires of the same incident after which the incident
	// degrades to optical + notifications only.
	DefaultRestartLoopBreakerK = 3
	// armAfterClosingDebounce is the settle time before a closing
	// sensor completes the exit delay early.
	armAfterClosingDebounce = 5 * time.Second
)

// ZoneConfig is the per-zone configuration document stored in
// alarm_zones.config_json. It is edited and persisted as a whole.
type ZoneConfig struct {
	// Modes configures the armable protection levels of the zone.
	// Only modes present here can be armed.
	Modes map[hmenum.AlarmMode]ModeConfig `json:"modes"`
	// PostTrigger decides what happens when the trigger time elapses.
	PostTrigger hmenum.AlarmPostTriggerPolicy `json:"post_trigger,omitempty"`
	// AutoRearmSeconds re-arms the zone to its pre-incident mode this
	// many quiet seconds after a post-trigger disarm (docs/alarm-concept.md
	// §15 row 22). 0 disables; only meaningful with PostTrigger==disarm.
	// The countdown resets on any member-sensor activity.
	AutoRearmSeconds int `json:"auto_rearm_s,omitempty"`
	// CentralLoss decides how the armed zone reacts when a whole
	// central is lost.
	CentralLoss hmenum.AlarmCentralLossPolicy `json:"central_loss,omitempty"`
	// Blockers maps sensor-health classes onto arming policies.
	Blockers BlockerPolicies `json:"blockers"`
	// CodePolicy decides when arm/disarm/silence require an alarm code
	// (docs/alarm-concept.md §11).
	CodePolicy CodePolicy `json:"code_policy,omitempty"`
	// HazardOutputs is the always-on hazard-class output policy
	// (docs/alarm-concept.md §6.1/§7). The zero value is loud.
	HazardOutputs OutputPolicy `json:"hazard_outputs,omitempty"`
	// PanicOutputs is the always-on panic-class output policy. The zero
	// value is loud; a silent panic (per-sensor PanicSilent or an
	// explicit silent PanicTrigger) forces Silent for that activation.
	PanicOutputs OutputPolicy `json:"panic_outputs,omitempty"`
	// Schedules lists daily arm schedules and reminders for the zone
	// (docs/alarm-concept.md §15 row 19). The schedule service computes
	// each entry's next fire time and recomputes every chain on Reload.
	Schedules []AlarmSchedule `json:"schedules,omitempty"`
}

// AlarmSchedule is one per-zone arm schedule / reminder entry
// (docs/alarm-concept.md §15 row 19).
type AlarmSchedule struct {
	// Time is the fire time of day, 24h "HH:MM", evaluated in the
	// daemon's local time zone.
	Time string `json:"time"`
	// Days restricts the schedule to specific weekdays using Go's
	// time.Weekday numbering (0=Sunday .. 6=Saturday). Empty fires
	// every day.
	Days []int `json:"days,omitempty"`
	// Mode is the protection mode the schedule expects the zone to be
	// in when it fires.
	Mode hmenum.AlarmMode `json:"mode"`
	// AutoArm arms the zone into Mode when the schedule fires and the
	// zone is not already in it; false raises a reminder instead of
	// arming (docs/alarm-concept.md §15 row 19).
	AutoArm bool `json:"auto_arm,omitempty"`
}

// CodePolicy decides per verb whether an alarm code is required to act
// on an zone (docs/alarm-concept.md §11). The engine consults a
// CodeValidator to resolve the code; a nil validator makes every policy
// inert (codes disabled). Strongly-authenticated operator sources
// (rest-operator, ws-operator, hmcli) bypass the requirement but still
// surface duress when a code is supplied.
type CodePolicy struct {
	// RequireArm gates arming on a valid code (default off).
	RequireArm bool `json:"require_arm,omitempty"`
	// RequireDisarm gates disarming on a valid code. A nil pointer is
	// the default: require a disarm code when the zone has an enabled
	// code (the CodeValidator resolves the "codes exist" half — an
	// empty code against an zone with no codes is permitted, so the
	// requirement can never lock everyone out).
	RequireDisarm *bool `json:"require_disarm,omitempty"`
	// RequireSilence gates silence per source surface (default off per
	// S3; keyed by the source string, e.g. "mqtt").
	RequireSilence map[string]bool `json:"require_silence,omitempty"`
}

// requires reports whether verb needs a code for source under this
// policy, before the CodeValidator resolves whether any code exists.
func (p CodePolicy) requires(verb, source string) bool {
	switch verb {
	case codeVerbArm:
		return p.RequireArm
	case codeVerbDisarm:
		if p.RequireDisarm == nil {
			return true
		}
		return *p.RequireDisarm
	case codeVerbSilence:
		return p.RequireSilence[source]
	default:
		return false
	}
}

// ModeConfig configures one protection level of an zone.
type ModeConfig struct {
	// ExitDelaySeconds is the arming countdown; 0 arms immediately.
	ExitDelaySeconds int `json:"exit_delay_s,omitempty"`
	// EntryDelaySeconds is the default pending countdown for sensors
	// flagged use_entry_delay; 0 triggers instantly.
	EntryDelaySeconds int `json:"entry_delay_s,omitempty"`
	// TriggerSeconds bounds one triggered phase; 0 selects
	// DefaultTriggerSeconds. Clamped to MaxTriggerSeconds.
	TriggerSeconds int `json:"trigger_time_s,omitempty"`
	// PreAlarmSeconds runs a pre-alarm phase before the full trigger
	// (docs/alarm-concept.md §15 row 21): the incident opens and only
	// the pre-alarm output classes (chirp / notification / light) fire
	// for this long, then the full policy escalates. 0 disables. A
	// silence during the pre-alarm phase cancels the full escalation.
	PreAlarmSeconds int `json:"pre_alarm_s,omitempty"`
	// MaxRetriggerCycles is the number of additional output cycles
	// per incident after the initial one (0 = fire once).
	MaxRetriggerCycles int `json:"max_retrigger_cycles,omitempty"`
	// Outputs is the mode's output policy (docs/alarm-concept.md §7):
	// loud/silent, indoor/outdoor split, smoke sounders, chirps.
	Outputs OutputPolicy `json:"outputs,omitempty"`
}

// OutputPolicy selects which output classes a mode drives
// (docs/alarm-concept.md §7). The zero value is the loud default:
// every enrolled output fires, no chirps.
type OutputPolicy struct {
	// Silent suppresses every acoustic output class — notifications,
	// optical signal, and alarm light only.
	Silent bool `json:"silent,omitempty"`
	// ExcludeOutdoor keeps outdoor-flagged sirens out of this mode
	// (the HmIP indoor/outdoor split).
	ExcludeOutdoor bool `json:"exclude_outdoor,omitempty"`
	// SmokeSounders enrolls the smoke-detector sounder class for this
	// mode (HmIP "Rauchwarnmelder-Alarm" parity — typically full
	// protection only).
	SmokeSounders bool `json:"smoke_sounders,omitempty"`
	// ArmDisarmChirps plays the confirmation squawk on arm and
	// disarm.
	ArmDisarmChirps bool `json:"arm_disarm_chirps,omitempty"`
	// CountdownTicks plays exit/entry countdown ticks on chirp
	// outputs; ticks thin out first under duty-cycle pressure (S5).
	CountdownTicks bool `json:"countdown_ticks,omitempty"`
}

// BlockerPolicies maps each sensor-health class onto an arming policy
// (docs/alarm-concept.md §5). Empty values fall back to the defaults:
// open/unreachable/sabotage block, low battery warns.
type BlockerPolicies struct {
	Open        hmenum.AlarmBlockerPolicy `json:"open,omitempty"`
	Unreachable hmenum.AlarmBlockerPolicy `json:"unreachable,omitempty"`
	Sabotage    hmenum.AlarmBlockerPolicy `json:"sabotage,omitempty"`
	LowBattery  hmenum.AlarmBlockerPolicy `json:"low_battery,omitempty"`
}

// normalized returns p with defaults applied.
func (p BlockerPolicies) normalized() BlockerPolicies {
	def := func(v, d hmenum.AlarmBlockerPolicy) hmenum.AlarmBlockerPolicy {
		if !v.Valid() {
			return d
		}
		return v
	}
	return BlockerPolicies{
		Open:        def(p.Open, hmenum.AlarmBlockerPolicyBlock),
		Unreachable: def(p.Unreachable, hmenum.AlarmBlockerPolicyBlock),
		Sabotage:    def(p.Sabotage, hmenum.AlarmBlockerPolicyBlock),
		LowBattery:  def(p.LowBattery, hmenum.AlarmBlockerPolicyWarn),
	}
}

// SensorConfig is the per-sensor configuration document stored in
// alarm_sensors.config_json (docs/alarm-concept.md §6.2).
type SensorConfig struct {
	// Modes lists the protection levels the sensor participates in.
	Modes []hmenum.AlarmMode `json:"modes"`
	// UseExitDelay lets the sensor be active while leaving; without
	// it, activation during the exit delay triggers instantly.
	UseExitDelay bool `json:"use_exit_delay,omitempty"`
	// UseEntryDelay routes activation to pending instead of an
	// instant trigger.
	UseEntryDelay bool `json:"use_entry_delay,omitempty"`
	// EntryDelayOverrideSeconds replaces the mode's entry delay for
	// this sensor when non-nil.
	EntryDelayOverrideSeconds *int `json:"entry_delay_override_s,omitempty"`
	// AlwaysOn marks hazard/panic-class sensors that bypass the
	// arm-state machine. Accepted and persisted here; the always-on
	// trigger path ships with the hazard/panic slice.
	AlwaysOn bool `json:"always_on,omitempty"`
	// AllowOpenAfterArming lets the sensor remain open through
	// arming; only a re-activation after clearing triggers.
	AllowOpenAfterArming bool `json:"allow_open_after_arming,omitempty"`
	// ArmAfterClosing completes the exit delay early when the sensor
	// closes (debounced).
	ArmAfterClosing bool `json:"arm_after_closing,omitempty"`
	// BypassAuto excludes the sensor until the next disarm when it
	// would block an arm, instead of failing the arm.
	BypassAuto bool `json:"bypass_auto,omitempty"`
	// TriggerWhenUnavailable treats vanishing while armed as an
	// activation (default: warn only).
	TriggerWhenUnavailable bool `json:"trigger_when_unavailable,omitempty"`
	// Chime plays the door-chime tone on activation while the zone is
	// disarmed (docs/alarm-concept.md §15 row 23); never during a walk
	// test.
	Chime bool `json:"chime,omitempty"`
	// HoldTimeSeconds requires an activation to persist this long
	// before it counts (docs/alarm-concept.md §6.2) — the debounce for
	// twitchy PIRs and doors rattling in wind. 0 counts instantly.
	// Applies to the arm-state path only, never to always-on
	// hazard/panic sensors.
	HoldTimeSeconds int `json:"hold_time,omitempty"`
	// Group names an optional cross-zoning group: a grouped sensor only
	// triggers when a second distinct member of the same group
	// activates within the cross-zone window (docs/alarm-concept.md
	// §6.2). Empty disables grouping.
	Group string `json:"group,omitempty"`
	// PanicSilent marks a panic-class always-on sensor whose activation
	// fires the panic policy with acoustic outputs suppressed (silent
	// panic / duress panic — notifications only).
	PanicSilent bool `json:"panic_silent,omitempty"`
}

// InMode reports whether the sensor participates in mode.
func (c SensorConfig) InMode(mode hmenum.AlarmMode) bool {
	for _, m := range c.Modes {
		if m == mode {
			return true
		}
	}
	return false
}

// ParseZoneConfig decodes an alarm_zones.config_json document.
func ParseZoneConfig(raw string) (ZoneConfig, error) {
	var cfg ZoneConfig
	if raw == "" {
		raw = "{}"
	}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return ZoneConfig{}, fmt.Errorf("engine: parse zone config: %w", err)
	}
	if cfg.Modes == nil {
		cfg.Modes = map[hmenum.AlarmMode]ModeConfig{}
	}
	if !cfg.PostTrigger.Valid() {
		cfg.PostTrigger = hmenum.AlarmPostTriggerReturnToArmed
	}
	if !cfg.CentralLoss.Valid() {
		cfg.CentralLoss = hmenum.AlarmCentralLossAlert
	}
	cfg.Blockers = cfg.Blockers.normalized()
	return cfg, nil
}

// ParseSensorConfig decodes an alarm_sensors.config_json document.
func ParseSensorConfig(raw string) (SensorConfig, error) {
	var cfg SensorConfig
	if raw == "" {
		raw = "{}"
	}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return SensorConfig{}, fmt.Errorf("engine: parse sensor config: %w", err)
	}
	return cfg, nil
}

// triggerDuration returns the bounded length of one triggered phase.
func (m ModeConfig) triggerDuration() time.Duration {
	s := m.TriggerSeconds
	if s <= 0 {
		s = DefaultTriggerSeconds
	}
	if s > MaxTriggerSeconds {
		s = MaxTriggerSeconds
	}
	return time.Duration(s) * time.Second
}

// entryDelay returns the pending countdown for a sensor, honoring the
// per-sensor override.
func (m ModeConfig) entryDelay(sensor SensorConfig) time.Duration {
	s := m.EntryDelaySeconds
	if sensor.EntryDelayOverrideSeconds != nil {
		s = *sensor.EntryDelayOverrideSeconds
	}
	if s < 0 {
		s = 0
	}
	return time.Duration(s) * time.Second
}
