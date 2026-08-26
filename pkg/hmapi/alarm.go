// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hmapi

import (
	"encoding/json"
	"time"
)

// --- Alarm system ---
//
// Wire DTOs for the /api/v1/alarm REST surface and the alarm_panel
// WebSocket family. Config-document fields (AlarmZone.Config,
// AlarmSensor.Config, AlarmOutput.Config) are opaque
// json.RawMessage passthroughs — the alarm engine owns their schema
// and versioning, so this layer neither validates nor reshapes them
// (notes/concepts/alarm-concept.md §14).

// AlarmZone is one alarm zone — an independently armable partition
// with its own arm state, sensor set, and output set.
type AlarmZone struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Position int    `json:"position,omitempty"`
	// Config is the zone's mode/policy document (entry/exit delays,
	// output policy, post-trigger policy, blocker policies).
	Config json.RawMessage `json:"config,omitempty"`
}

// AlarmSensor is one CCU data point enrolled into an alarm zone as a
// sensor input.
type AlarmSensor struct {
	ID             string `json:"id"`
	Central        string `json:"central"`
	InterfaceID    string `json:"interface_id"`
	ChannelAddress string `json:"channel_address"`
	Parameter      string `json:"parameter"`
	// Type is the sensor role: door, window, motion, tamper, hazard,
	// panic.
	Type string `json:"type"`
	Name string `json:"name,omitempty"`
	// Config is the engine-owned per-sensor configuration document
	// (mode matrix, entry-delay flag, bypass eligibility, …).
	Config json.RawMessage `json:"config,omitempty"`
}

// AlarmOutput is one CCU actuator enrolled into an alarm zone as an
// alarm consequence (siren, switched siren, smoke-detector sounder,
// alarm light, chirp emitter, notification target, sysvar mirror).
type AlarmOutput struct {
	ID string `json:"id"`
	// Class is the output driver class: acoustic_siren,
	// switched_siren, smoke_sounder, optical_siren, alarm_light,
	// chirp, notification, sysvar_mirror. The class, not the backing
	// device type, decides which safety invariants apply.
	Class          string `json:"class"`
	Central        string `json:"central"`
	ChannelAddress string `json:"channel_address"`
	Name           string `json:"name,omitempty"`
	// Config is the engine-owned per-output configuration document
	// (per-mode assignment, indoor/outdoor flag, duration/tone, …).
	Config json.RawMessage `json:"config,omitempty"`
}

// AlarmOutputCandidate is one channel whose custom data point can back
// at least one device-backed alarm output class (acoustic_siren,
// optical_siren, switched_siren, smoke_sounder, alarm_light, chirp).
// Derived from the live domain model, so enrolling a candidate always
// resolves to an output driver.
type AlarmOutputCandidate struct {
	Central        string `json:"central"`
	DeviceAddress  string `json:"device_address"`
	DeviceName     string `json:"device_name,omitempty"`
	Model          string `json:"model"`
	ChannelAddress string `json:"channel_address"`
	ChannelNo      int    `json:"channel_no"`
	ChannelName    string `json:"channel_name,omitempty"`
	// Rooms / Functions are the channel's CCU room and function
	// assignments so pickers can filter and label candidates.
	Rooms     []string `json:"rooms,omitempty"`
	Functions []string `json:"functions,omitempty"`
	// Classes are the device-backed output classes this channel can
	// carry, in canonical class order.
	Classes []string `json:"classes"`
	// Kind is the stable custom-DP kind string (widget selection).
	Kind string `json:"kind"`
	// AvailableTones / AvailableLights / AvailableSoundfiles are the
	// device's raw ENUM wire values (acoustic tones and optical
	// patterns for sirens, soundfiles for MP3 players). The parallel
	// *Labels lists carry the localised display strings in the same
	// order; absent when the server has no value translations.
	AvailableTones           []string `json:"available_tones,omitempty"`
	AvailableToneLabels      []string `json:"available_tone_labels,omitempty"`
	AvailableLights          []string `json:"available_lights,omitempty"`
	AvailableLightLabels     []string `json:"available_light_labels,omitempty"`
	AvailableSoundfiles      []string `json:"available_soundfiles,omitempty"`
	AvailableSoundfileLabels []string `json:"available_soundfile_labels,omitempty"`
	// Dimmable reports level support for the alarm_light class.
	Dimmable bool `json:"dimmable,omitempty"`
}

// AlarmRemoteKeyCandidate is one channel that emits the key-press
// events remote-key code bindings dispatch on (PRESS_SHORT /
// PRESS_LONG) — a physical remote-control or wall-button key.
type AlarmRemoteKeyCandidate struct {
	Central        string `json:"central"`
	DeviceAddress  string `json:"device_address"`
	DeviceName     string `json:"device_name,omitempty"`
	Model          string `json:"model"`
	ChannelAddress string `json:"channel_address"`
	ChannelNo      int    `json:"channel_no"`
	ChannelName    string `json:"channel_name,omitempty"`
	// Parameters are the press parameters this key offers, in
	// dispatch order (PRESS_SHORT before PRESS_LONG).
	Parameters []string `json:"parameters"`
}

// AlarmIncidentRef is the open-incident reference nested in
// AlarmZoneStatus. Present only while the zone's state is triggered.
type AlarmIncidentRef struct {
	ID       string `json:"id"`
	Silenced bool   `json:"silenced"`
}

// AlarmCountdown is a running exit/entry delay countdown nested in
// AlarmZoneStatus. Present only while a delay is active.
type AlarmCountdown struct {
	// Kind is "exit_delay" or "entry_delay".
	Kind       string `json:"kind"`
	RemainingS int    `json:"remaining_s"`
	TotalS     int    `json:"total_s"`
}

// AlarmModeReadiness is the per-mode ready-to-arm verdict for one
// alarm zone.
type AlarmModeReadiness struct {
	// Ready is true when the mode can be armed without force.
	Ready bool `json:"ready"`
	// Blockers lists sensor ids currently blocking the arm into this
	// mode.
	Blockers []string `json:"blockers,omitempty"`
	// Warnings lists sensor ids with non-blocking health warnings for
	// this mode.
	Warnings []string `json:"warnings,omitempty"`
}

// AlarmZoneStatus is one alarm zone's live status, as returned by
// GET /alarm/state.
type AlarmZoneStatus struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// State is the arm-state-machine state: disarmed, arming, armed,
	// pending, triggered.
	State string `json:"state"`
	// Mode is the currently active (or, while arming, target)
	// protection mode. Empty when disarmed.
	Mode string `json:"mode,omitempty"`
	// Bypassed lists sensor ids currently bypassed for the
	// active/pending arm.
	Bypassed  []string          `json:"bypassed,omitempty"`
	Incident  *AlarmIncidentRef `json:"incident,omitempty"`
	Countdown *AlarmCountdown   `json:"countdown,omitempty"`
	// Readiness is keyed by mode name.
	Readiness      map[string]AlarmModeReadiness `json:"readiness,omitempty"`
	WalkTestActive bool                          `json:"walktest_active"`
}

// AlarmArmRequest is the body of POST /alarm/zones/{id}/arm.
type AlarmArmRequest struct {
	// Mode is the target protection mode: perimeter, full, night,
	// vacation, custom.
	Mode string `json:"mode"`
	// Force arms despite readiness blockers, where the engine's
	// blocker policy allows overriding.
	Force bool `json:"force,omitempty"`
	// SkipDelay skips the configured exit delay and arms immediately.
	SkipDelay bool `json:"skip_delay,omitempty"`
	// Bypass lists sensor ids to bypass for this arm attempt.
	Bypass []string `json:"bypass,omitempty"`
	// Code is the alarm code supplied with the arm, when the zone's
	// code policy requires one (or to surface a duress code). Empty when
	// none was supplied. Never logged or persisted in cleartext.
	Code string `json:"code,omitempty"`
}

// AlarmVerbRequest is the optional body of the code-carrying verbs
// (POST /alarm/zones/{id}/disarm | silence | acknowledge). The body is
// optional — an absent body disarms/silences without a code, honoring
// the zone's code policy and the S3/S6 operator-bypass rules
// (notes/concepts/alarm-concept.md §11). Code is never logged or persisted in
// cleartext.
type AlarmVerbRequest struct {
	Code string `json:"code,omitempty"`
}

// AlarmArmAccepted is the 200 response of POST /alarm/zones/{id}/arm.
type AlarmArmAccepted struct {
	// State is the resulting zone state: arming or armed.
	State string `json:"state"`
	// Bypassed lists sensor ids actually bypassed for this arm.
	Bypassed []string `json:"bypassed,omitempty"`
	// ExitDelayS is the exit delay in seconds the zone is now
	// counting down; 0 when armed immediately.
	ExitDelayS int `json:"exit_delay_s,omitempty"`
}

// AlarmJournalEntry is one entry in the alarm engine's append-only
// event journal, as returned by GET /alarm/journal.
type AlarmJournalEntry struct {
	ID     int64     `json:"id"`
	When   time.Time `json:"when"`
	ZoneID string    `json:"zone_id"`
	// Class buckets the entry for the ?class= query filter: arm,
	// disarm, trigger, silence, bypass, fault, test, config.
	Class string `json:"class"`
	// Event is the stable machine-readable event token within the
	// class (e.g. "armed", "force_armed", "silenced").
	Event string `json:"event"`
	// Actor is the identity that caused the entry (operator account,
	// keypad identity, code name, or an engine-internal actor);
	// empty when unattributed.
	Actor string `json:"actor,omitempty"`
	// Source is the surface the action came from: rest, ws, mqtt,
	// hmcli, keypad, engine.
	Source string `json:"source,omitempty"`
	// IncidentID references the related incident; 0 when none.
	IncidentID int64 `json:"incident_id,omitempty"`
	// Details is an engine-owned, event-class-specific detail
	// document.
	Details json.RawMessage `json:"details,omitempty"`
}

// AlarmWalkTestSensor is one sensor's walk-test coverage row, nested
// in AlarmWalkTestStatus.
type AlarmWalkTestSensor struct {
	ID   string `json:"id"`
	Name string `json:"name,omitempty"`
	// Tested is true once this sensor reported at least one
	// activation during the current walk-test session.
	Tested          bool       `json:"tested"`
	LastTriggeredAt *time.Time `json:"last_triggered_at,omitempty"`
}

// AlarmWalkTestStatus is the live status of a walk-test session on
// one alarm zone, as returned by GET /alarm/zones/{id}/walktest.
type AlarmWalkTestStatus struct {
	Active    bool                  `json:"active"`
	StartedAt *time.Time            `json:"started_at,omitempty"`
	Sensors   []AlarmWalkTestSensor `json:"sensors"`
}

// AlarmOutputTestRequest is the body of POST /alarm/outputs/{id}/test.
type AlarmOutputTestRequest struct {
	// OpticalOnly fires only the output's optical/visual indication,
	// suppressing sound. Ignored for outputs with no audible element.
	OpticalOnly bool `json:"optical_only,omitempty"`
}

// AlarmPanelEntity is the alarm-control-panel entity projection: the
// HA-facing view of one alarm zone (or the aggregate master panel),
// identical across REST, WebSocket, and MQTT.
type AlarmPanelEntity struct {
	UniqueID       string   `json:"unique_id"`
	ZoneID         string   `json:"zone_id"`
	Name           string   `json:"name"`
	Category       string   `json:"category"`
	State          string   `json:"state"`
	SupportedModes []string `json:"supported_modes,omitempty"`
	Available      bool     `json:"available"`
	Master         bool     `json:"master,omitempty"`
	// CodeArmRequired / CodeDisarmRequired carry the zone's effective
	// per-verb code policy: the zone-config policy AND an applicable
	// enabled pin code exists — exactly the requirement the engine
	// enforces, so a client prompts for a code precisely when one is
	// needed. The master aggregate carries the any-zone-requires union.
	CodeArmRequired    bool `json:"code_arm_required"`
	CodeDisarmRequired bool `json:"code_disarm_required"`
}

// AlarmCodePerms are the per-code verb permissions.
type AlarmCodePerms struct {
	Arm     bool `json:"arm"`
	Disarm  bool `json:"disarm"`
	Silence bool `json:"silence"`
}

// AlarmCode is one alarm code as returned by GET /alarm/codes. The
// argon2id hash and the cleartext PIN are NEVER serialized onto this
// surface (notes/concepts/alarm-concept.md §11, §16): a code projection carries
// only identity, permissions, scope, validity, and lifecycle metadata.
type AlarmCode struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Kind is the code class: pin, keypad_slot, remote_key.
	Kind string `json:"kind"`
	// Duress marks a PIN that disarms normally but fires a silent duress
	// alarm. Only meaningful for the pin kind.
	Duress bool           `json:"duress,omitempty"`
	Perms  AlarmCodePerms `json:"perms"`
	// Zones restricts the code to a subset of zones; an empty list means
	// every zone.
	Zones []string `json:"zones,omitempty"`
	// Binding is the engine-owned hardware-binding document for the
	// keypad_slot / remote_key kinds; absent for pin codes.
	Binding json.RawMessage `json:"binding,omitempty"`
	// ValidFromMS / ValidUntilMS are the optional validity window in Unix
	// milliseconds; 0 leaves the bound open (guest codes).
	ValidFromMS  int64 `json:"valid_from_ms,omitempty"`
	ValidUntilMS int64 `json:"valid_until_ms,omitempty"`
	Enabled      bool  `json:"enabled"`
	CreatedMS    int64 `json:"created_ms,omitempty"`
	UpdatedMS    int64 `json:"updated_ms,omitempty"`
}

// AlarmCodeRequest is the body of POST/PUT /alarm/codes[/{id}]. It
// mirrors AlarmCode plus the write-only PIN: the cleartext PIN is
// accepted on the way in (hashed to argon2id by the codes facade before
// persistence) and is NEVER echoed back on any read surface. An empty
// PIN on an update leaves the stored hash unchanged.
type AlarmCodeRequest struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	// PIN is the cleartext code, write-only, for the pin kind. Omitted on
	// update to keep the existing hash.
	PIN          string          `json:"pin,omitempty"`
	Duress       bool            `json:"duress,omitempty"`
	Perms        AlarmCodePerms  `json:"perms"`
	Zones        []string        `json:"zones,omitempty"`
	Binding      json.RawMessage `json:"binding,omitempty"`
	ValidFromMS  int64           `json:"valid_from_ms,omitempty"`
	ValidUntilMS int64           `json:"valid_until_ms,omitempty"`
	Enabled      bool            `json:"enabled"`
}

// AlarmSource is one data point that contributed to an incident or
// blocked an arm. It is the REST projection of the domain source
// reference: identity plus enough context to deep-link into the device
// view without a second lookup.
type AlarmSource struct {
	// Ref is the stable routing key
	// `<central>|<interface_id>|<channel_address>|<parameter>`.
	Ref            string `json:"ref"`
	Central        string `json:"central,omitempty"`
	InterfaceID    string `json:"interface_id,omitempty"`
	ChannelAddress string `json:"channel_address,omitempty"`
	DeviceAddress  string `json:"device_address,omitempty"`
	Parameter      string `json:"parameter,omitempty"`
	// SensorID is the enrolled alarm-sensor row; empty when the source
	// is not an enrolled sensor.
	SensorID string `json:"sensor_id,omitempty"`
	Name     string `json:"name,omitempty"`
	// SensorType is the alarm role (motion, opening, hazard, panic, …).
	SensorType string `json:"sensor_type,omitempty"`
	// Class is the Security & Safety hazard/fault class.
	Class string `json:"class,omitempty"`
	// Cause is the incident cause token the contribution arrived under.
	Cause string    `json:"cause,omitempty"`
	At    time.Time `json:"at"`
}

// AlarmIncident is one alarm episode of a zone.
type AlarmIncident struct {
	ID     int64  `json:"id"`
	ZoneID string `json:"zone_id"`
	// Mode is the protection mode active when the incident opened.
	Mode string `json:"mode"`
	// Cause is the machine-readable cause token that opened it.
	Cause string `json:"cause,omitempty"`
	// CauseSensorID / CauseSensorName identify the source that opened
	// the incident, when it was a sensor.
	CauseSensorID   string `json:"cause_sensor_id,omitempty"`
	CauseSensorName string `json:"cause_sensor_name,omitempty"`
	// Sources lists every data point that contributed, oldest first —
	// not just the one that opened the incident. A second detector
	// firing while the siren already sounds appears here and nowhere
	// else.
	Sources []AlarmSource `json:"sources,omitempty"`
	// StartedAt is when the incident opened.
	StartedAt time.Time `json:"started_at"`
	// ClosedAt is when it closed; zero while it is still running.
	ClosedAt time.Time `json:"closed_at,omitzero"`
	// CloseReason is disarm, post_trigger or incident_lost.
	CloseReason string `json:"close_reason,omitempty"`
	// Silenced reports whether an operator silenced the incident, and
	// SilencedAt / SilencedBy record when and by whom.
	Silenced   bool      `json:"silenced"`
	SilencedAt time.Time `json:"silenced_at,omitzero"`
	SilencedBy string    `json:"silenced_by,omitempty"`
	// RetriggerCycles counts the output cycles the incident drove;
	// AcousticSeconds the acoustic budget it consumed.
	RetriggerCycles int `json:"retrigger_cycles"`
	AcousticSeconds int `json:"acoustic_seconds"`
	// Open reports whether the incident is still running.
	Open bool `json:"open"`
}

// AlarmSensorCandidate is one data point a zone can enrol as an alarm
// sensor, with the pre-fill a picker needs to enrol it correctly.
type AlarmSensorCandidate struct {
	Central        string   `json:"central"`
	InterfaceID    string   `json:"interface_id"`
	DeviceAddress  string   `json:"device_address"`
	DeviceName     string   `json:"device_name,omitempty"`
	Model          string   `json:"model,omitempty"`
	ChannelAddress string   `json:"channel_address"`
	ChannelNo      int      `json:"channel_no"`
	ChannelName    string   `json:"channel_name,omitempty"`
	ChannelType    string   `json:"channel_type,omitempty"`
	Parameter      string   `json:"parameter"`
	Rooms          []string `json:"rooms,omitempty"`
	Functions      []string `json:"functions,omitempty"`
	// SensorType is the suggested alarm role.
	SensorType string `json:"sensor_type,omitempty"`
	// SecurityClass is the hazard/fault class of the data point.
	SecurityClass string `json:"security_class,omitempty"`
	// ValueList is the parameter's enumeration vocabulary, empty for a
	// boolean parameter.
	ValueList []string `json:"value_list,omitempty"`
	// ValueLabels are the localized renderings of ValueList, in the
	// same order, when a translation exists.
	ValueLabels []string `json:"value_labels,omitempty"`
	// ActiveValues is the recommended active-value selection. It is set
	// only where the default "anything but index 0" rule would be
	// wrong.
	ActiveValues []string `json:"active_values,omitempty"`
	// Recommended marks the data point to prefer when a device offers
	// several for the same purpose.
	Recommended bool `json:"recommended,omitempty"`
	// Deprioritised marks a workable data point with a better sibling;
	// Reason names why.
	Deprioritised bool   `json:"deprioritised,omitempty"`
	Reason        string `json:"reason,omitempty"`
	// Enrolled reports whether the data point is already enrolled, and
	// ZoneID names the zone holding it.
	Enrolled bool   `json:"enrolled,omitempty"`
	ZoneID   string `json:"zone_id,omitempty"`
}

// AlarmTriggeredMotionSensor is one latched motion detector that the
// reset verb can clear.
type AlarmTriggeredMotionSensor struct {
	SensorID string `json:"sensor_id"`
	ZoneID   string `json:"zone_id"`
	Name     string `json:"name,omitempty"`
	// ChannelAddress and Parameter identify the sensor's own data
	// point, not the RESET_MOTION one the reset writes.
	ChannelAddress string `json:"channel_address"`
	Parameter      string `json:"parameter"`
}

// AlarmMotionResetResult reports one reset pass. Reset + Failed is the
// number of detectors attempted; Sensors names them.
//
// A per-device failure is reported here rather than as an HTTP error:
// the verb ran, and an operator needs to see that three of four
// detectors cleared.
type AlarmMotionResetResult struct {
	Reset   int                          `json:"reset"`
	Failed  int                          `json:"failed"`
	Sensors []AlarmTriggeredMotionSensor `json:"sensors"`
}
