// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package engine

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// Engine-level defaults. The trigger-time default and ceiling mirror
// the bounded-activation rule of notes/concepts/alarm-concept.md §2 (S1): the
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
	// many quiet seconds after a post-trigger disarm (notes/concepts/alarm-concept.md
	// §15 row 22). 0 disables; only meaningful with PostTrigger==disarm.
	// The countdown resets on any member-sensor activity.
	AutoRearmSeconds int `json:"auto_rearm_s,omitempty"`
	// CentralLoss decides how the armed zone reacts when a whole
	// central is lost.
	CentralLoss hmenum.AlarmCentralLossPolicy `json:"central_loss,omitempty"`
	// Blockers maps sensor-health classes onto arming policies.
	Blockers BlockerPolicies `json:"blockers"`
	// CodePolicy decides when arm/disarm/silence require an alarm code
	// (notes/concepts/alarm-concept.md §11).
	CodePolicy CodePolicy `json:"code_policy,omitempty"`
	// HazardOutputs is the always-on hazard-class output policy
	// (notes/concepts/alarm-concept.md §6.1/§7). The zero value is loud.
	HazardOutputs OutputPolicy `json:"hazard_outputs,omitempty"`
	// PanicOutputs is the always-on panic-class output policy. The zero
	// value is loud; a silent panic (per-sensor PanicSilent or an
	// explicit silent PanicTrigger) forces Silent for that activation.
	PanicOutputs OutputPolicy `json:"panic_outputs,omitempty"`
	// Schedules lists daily arm schedules and reminders for the zone
	// (notes/concepts/alarm-concept.md §15 row 19). The schedule service computes
	// each entry's next fire time and recomputes every chain on Reload.
	Schedules []AlarmSchedule `json:"schedules,omitempty"`
}

// AlarmSchedule is one per-zone arm schedule / reminder entry
// (notes/concepts/alarm-concept.md §15 row 19).
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
	// arming (notes/concepts/alarm-concept.md §15 row 19).
	AutoArm bool `json:"auto_arm,omitempty"`
}

// CodePolicy decides per verb whether an alarm code is required to act
// on an zone (notes/concepts/alarm-concept.md §11). The engine consults a
// CodeValidator to resolve the code; a nil validator makes every policy
// inert (codes disabled). Strongly-authenticated operator sources
// (rest-operator, ws-operator, hmcli) bypass the requirement but still
// surface duress when a code is supplied.
// loom:reachable:reason="field ZoneConfig.CodePolicy, decoded from every persisted zone; RTA scores call edges, so a type used only structurally is invisible to it"
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
	//
	// It reaches the anonymous planes only. resolveCode drops the
	// requirement for every pre-authenticated source — the operator
	// surfaces (CodeSourceRESTOperator, CodeSourceWSOperator,
	// CodeSourceHmcli) carry a session, and CodeSourceKeypad /
	// CodeSourceRemote are authenticated by the slot or binding match
	// and carry no PIN that could be typed — so an entry keyed on one of
	// those is accepted, persisted and inert. Pinned by
	// TestRequireSilenceGatesOnlyAnonymousSources.
	//
	// Of the anonymous sources the engine could gate, only "mqtt"
	// reaches a silence verb at all: "sysvar" arms and disarms, and the
	// keypad intent router likewise never silences. So "mqtt" is the one
	// key that changes anything today, which is what the alarm policies
	// view offers — pinned from both sides by
	// TestSilenceGatesAreOfferedOnlyWhereTheyBite.
	RequireSilence map[string]bool `json:"require_silence,omitempty"`
}

// requires reports whether verb needs a code for source under this
// policy, before the CodeValidator resolves whether any code exists.
func (p CodePolicy) requires(verb, source string) bool {
	switch verb {
	case CodeVerbArm:
		return p.RequireArm
	case CodeVerbDisarm:
		if p.RequireDisarm == nil {
			return true
		}
		return *p.RequireDisarm
	case CodeVerbSilence:
		return p.RequireSilence[source]
	default:
		return false
	}
}

// ModeConfig configures one protection level of an zone.
// loom:reachable:reason="value type of ZoneConfig.Modes, decoded from every persisted zone; RTA scores call edges, so a type used only structurally is invisible to it"
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
	// (notes/concepts/alarm-concept.md §15 row 21): the incident opens and only
	// the pre-alarm output classes (chirp / notification / light) fire
	// for this long, then the full policy escalates. 0 disables. A
	// silence during the pre-alarm phase cancels the full escalation.
	PreAlarmSeconds int `json:"pre_alarm_s,omitempty"`
	// MaxRetriggerCycles is the number of additional output cycles
	// per incident after the initial one (0 = fire once).
	MaxRetriggerCycles int `json:"max_retrigger_cycles,omitempty"`
	// Outputs is the mode's output policy (notes/concepts/alarm-concept.md §7):
	// loud/silent, indoor/outdoor split, smoke sounders, chirps.
	Outputs OutputPolicy `json:"outputs,omitempty"`
}

// OutputPolicy selects which output classes a mode drives
// (notes/concepts/alarm-concept.md §7). The zero value is the loud default:
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
// (notes/concepts/alarm-concept.md §5). Empty values fall back to the defaults:
// open/unreachable/sabotage block, low battery warns.
// loom:reachable:reason="field ZoneConfig.Blockers, decoded from every persisted zone; RTA scores call edges, so a type used only structurally is invisible to it"
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
// alarm_sensors.config_json (notes/concepts/alarm-concept.md §6.2).
// loom:reachable:reason="the sensor half of a persisted zone, read on every sensor event; RTA scores call edges, so a type used only structurally is invisible to it"
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
	// ActiveValues names the enumerated values that count as an
	// activation. It exists because the default rule — "active unless
	// the value sits at index 0" — is wrong for enumerations that carry
	// more than one kind of non-idle state.
	//
	// The load-bearing case is SMOKE_DETECTOR_ALARM_STATUS, whose value
	// list is [IDLE_OFF, PRIMARY_ALARM, INTRUSION_ALARM,
	// SECONDARY_ALARM] — that order verbatim from the firmware's own
	// enum (HMIPServer
	// de.eq3.cbcs.devicedescription.channelspecification.stateparameter.SmokeDetectorAlarmStatus,
	// whose #getNames() fills the VALUE_LIST in ordinal order). Under the
	// default rule INTRUSION_ALARM counts as a smoke detection, while it
	// means the installation drove that detector as a siren for a
	// burglary — the alarm system reading back its own output as an
	// input.
	//
	// Two premises under the default rule, held apart because they rest
	// on different authority:
	//
	// The positional half is firmware. The integer an ENUM delivers on
	// the legacy XML-RPC surface IS its index in the declared VALUE_LIST:
	// the description's list and the wire ordinal come from one
	// getEnumStrings() array (HMIPServer
	// de.eq3.cbcs.legacy.bidcos.rpc.internal.DeviceUtil#createParameterDescription
	// and #convertParameterValue), and the shipped configuration turns
	// that substitution on
	// (../OpenCCU-Base/etc/config_templates/crRFD.conf:47,
	// Legacy.Parameter.ReplaceEnumValueWithOrdinal=true). Note the
	// physical radio bytes are not the ordinals — a window state maps
	// {CLOSED, TILTED, OPEN} onto {0, 100, 200} — so this flag is what
	// makes an index scan correct at all. On BidCos the same holds by a
	// different route: an option list is emitted as VALUE_LIST[i] with
	// MIN 0 and MAX size-1
	// (../OpenCCU-Base/src/libhsscomm/HSSLogicalTypeOption.cpp).
	//
	// "Index 0 means idle" is NOT a firmware rule and is UNVERIFIED as a
	// general one. The strongest statement the sources make is
	// DEFAULT=MIN=first entry for the HmIP state-parameter enums, which
	// is a default, not an idle semantic; for BidCos option lists they
	// say nothing at all. It happens to hold for every enumeration a
	// sensor can currently be enrolled on — CLOSED, DRY, NO_ERROR,
	// IDLE_OFF all sit at 0 — so the rule is safe in today's scope and
	// unbacked one step wider. A new enumerated family becoming
	// enrollable has to be re-checked against its own value list; a
	// firmware shipping an alarm state at position 0 would break this
	// silently, and only naming the values explicitly here defends
	// against it.
	//
	// Empty selects exactly the previous behaviour, so an existing
	// enrollment keeps its meaning; a value is only ever narrowed by an
	// explicit operator choice.
	ActiveValues []string `json:"active_values,omitempty"`
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
	// disarmed (notes/concepts/alarm-concept.md §15 row 23); never during a walk
	// test.
	Chime bool `json:"chime,omitempty"`
	// HoldTimeSeconds requires an activation to persist this long
	// before it counts (notes/concepts/alarm-concept.md §6.2) — the debounce for
	// twitchy PIRs and doors rattling in wind. 0 counts instantly.
	// Applies to the arm-state path only, never to always-on
	// hazard/panic sensors.
	HoldTimeSeconds int `json:"hold_time,omitempty"`
	// Group names an optional cross-zoning group: a grouped sensor only
	// triggers when a second distinct member of the same group
	// activates within the cross-zone window (notes/concepts/alarm-concept.md
	// §6.2). Empty disables grouping.
	Group string `json:"group,omitempty"`
	// PanicSilent marks a panic-class always-on sensor whose activation
	// fires the panic policy with acoustic outputs suppressed (silent
	// panic / duress panic — notifications only).
	PanicSilent bool `json:"panic_silent,omitempty"`
}

// RequiresAlwaysOn reports whether a sensor of this type must bypass the arm
// state machine.
//
// A hazard sensor that is not always-on only fires while its zone is armed in
// one of its listed modes — and with the empty mode list that is normal for a
// smoke detector, it never fires at all. The rule is the domain's because more
// than one surface depends on it: the REST write path couples the two so the
// failure cannot be configured, and the input loader warns when a stored row
// arrives without it. Two spellings of one safety invariant is not a thing to
// keep.
func RequiresAlwaysOn(sensorType hmenum.AlarmSensorType) bool {
	return sensorType == hmenum.AlarmSensorTypeHazard
}

// AlwaysOnViolated reports whether cfg contradicts [RequiresAlwaysOn] for a
// sensor of this type.
func AlwaysOnViolated(sensorType hmenum.AlarmSensorType, cfg SensorConfig) bool {
	return RequiresAlwaysOn(sensorType) && !cfg.AlwaysOn
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
