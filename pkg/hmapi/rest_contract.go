// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmapi

import (
	"encoding/json"
	"errors"
	"time"
)

// --- Backup ---

// BackupEntry is one entry in the backup list.
type BackupEntry struct {
	ID        string    `json:"id"`
	Central   string    `json:"central"`
	Bytes     int64     `json:"bytes"`
	CreatedAt time.Time `json:"created_at"`
}

// --- Central links ---

// CentralLinksReport summarises one create/remove call. Touched is the
// number of channels for which the CCU accepted the report-value-usage
// call, Skipped the count of channels without press events (so they
// were left alone), Failed the count of channels where the CCU
// returned an error.
type CentralLinksReport struct {
	Touched int `json:"touched"`
	Skipped int `json:"skipped"`
	Failed  int `json:"failed"`
}

// CentralLinksStatus describes whether the device is eligible for
// central click-event routing. Channels enumerates the eligible
// channels so the SPA can offer a per-channel toggle next to the
// device-wide one — mirroring the CCU channel-config dialog, which
// scopes the switch to the single opened channel.
type CentralLinksStatus struct {
	Supported        bool                        `json:"supported"`
	Reason           string                      `json:"reason,omitempty"`
	EligibleChannels int                         `json:"eligible_channels,omitempty"`
	Channels         []CentralLinksChannelStatus `json:"channels,omitempty"`
}

// CentralLinksChannelStatus describes one channel's suitability for
// central click-event routing. Eligible is true when the channel
// exposes PRESS_SHORT / PRESS_LONG and can therefore drive central
// click events.
type CentralLinksChannelStatus struct {
	Address  string `json:"address"`
	Number   int    `json:"number"`
	Eligible bool   `json:"eligible"`
}

// ErrCentralLinksUnsupported is returned by adapters when the device
// is on an interface that has no concept of central event routing
// (CUxD, virtual devices, …). Surfaced as 422 to make the SPA show
// "not applicable on this device" instead of a generic upstream error.
var ErrCentralLinksUnsupported = errors.New("central-links: device interface does not support central links")

// ErrCentralLinksChannelNotFound is returned when a channel-scoped
// create/remove/status request names a channel address that the device
// does not carry. Surfaced as 422 so the SPA shows a targeted
// validation error rather than a generic upstream failure.
var ErrCentralLinksChannelNotFound = errors.New("central-links: channel not found on device")

// --- Config ---

// ConfigSnapshot is whatever the domain layer publishes as a
// sanitized view of the effective configuration. Fields are
// deliberately omitempty so the daemon can grow the shape without
// breaking clients.
type ConfigSnapshot struct {
	Locale        string            `json:"locale,omitempty"`
	Centrals      []ConfigCentral   `json:"centrals,omitempty"`
	CallbackPorts ConfigPorts       `json:"callback_ports,omitzero"`
	Features      map[string]bool   `json:"features,omitempty"`
	Extras        map[string]string `json:"extras,omitempty"`
	// Policies surfaces static daemon-side behaviour switches that
	// external clients (HA in particular) ask about: which hub
	// content gets surfaced, whether invisible devices show up,
	// etc. The current MVP exposes a fixed policy set; future
	// revisions may add operator-configurable knobs without
	// breaking the wire shape. Keys are stable; values are the
	// current effective setting. See `/config` description in
	// openapi.yaml for the enumerated keys.
	Policies map[string]bool `json:"policies,omitempty"`
}

// ConfigCentral describes one configured CCU.
type ConfigCentral struct {
	Name       string   `json:"name"`
	Host       string   `json:"host"`
	Interfaces []string `json:"interfaces"`
}

// ConfigPorts surfaces the effective callback server ports.
type ConfigPorts struct {
	XMLRPC int `json:"xmlrpc,omitempty"`
	BINRPC int `json:"binrpc,omitempty"`
}

// --- Custom data points ---

// ErrUnknownOperation is returned by InvokeCustomDP when `operation`
// is not in the dispatch table for the data point's category.
var ErrUnknownOperation = errors.New("custom_dp: unknown operation")

// ErrBadParam is returned when a required param is missing or out of range.
var ErrBadParam = errors.New("custom_dp: bad parameter")

// --- Diagnostics ---

// ReliabilityState is one (central, interface) reliability row. State holds
// the InterfaceClient's live state payload, marshalled as-is.
//
// Use of `any` for State is justified here: the payload shape depends on
// which reliability sub-state (circuit breaker, retry, throttle, ...) is
// reporting, so this DTO is a pass-through JSON envelope rather than a
// typed model — the caller only ever re-serialises it to the REST/WS
// response, never inspects it in Go.
type ReliabilityState struct {
	Central      string `json:"central"`
	Interface    string `json:"interface"`
	CircuitState int    `json:"circuit_state"`
	State        any    `json:"state,omitempty"`
}

// DiagnosticsEvent is one tapped event-bus record.
//
// Use of `any` for Event is justified here: the recorder taps every event
// type published on the bus (see internal/central/events), whose payload
// shapes are unrelated Go structs. This DTO exists to relay the already-
// decoded event to the diagnostics endpoint as-is, not to interpret it.
type DiagnosticsEvent struct {
	TS    string `json:"ts"`
	Type  string `json:"type"`
	Event any    `json:"event,omitempty"`
}

// RPCRecordingStatus is one central's recorder status.
type RPCRecordingStatus struct {
	Central string `json:"central"`
	Active  bool   `json:"active"`
	// Entries is the number of distinct recorded call slots
	// (rpc_type + method + params).
	Entries int `json:"entries"`
	// EndsAt is the auto-stop deadline (RFC3339) while recording; empty when
	// idle.
	EndsAt string `json:"ends_at,omitempty"`
	// Randomize reports whether this recording's export is anonymised.
	Randomize bool `json:"randomize,omitempty"`
}

// --- Incidents ---

// Incident is one diagnostic entry surfaced at `/incidents`.
type Incident struct {
	ID        string    `json:"id"`
	When      time.Time `json:"when"`
	Component string    `json:"component"`
	Severity  string    `json:"severity"`
	Summary   string    `json:"summary"`
	Detail    string    `json:"detail,omitempty"`
}

// --- Interfaces ---

// InterfaceState is one entry in `GET /interfaces`.
type InterfaceState struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Connected bool   `json:"connected"`
	Interface string `json:"interface"`
	CentralID string `json:"central_id,omitempty"`
	Host      string `json:"host,omitempty"`
	Note      string `json:"note,omitempty"`
	// DutyCycle is the interface's transmit duty cycle in percent (0..100)
	// for BidCos radio interfaces, sourced from the CCU's
	// listBidcosInterfaces poll. Nil (absent) when unknown or when the
	// interface carries no BidCos gateway (e.g. HmIP-RF, whose device-level
	// DUTY_CYCLE data points provide the value instead).
	DutyCycle *int `json:"duty_cycle,omitempty"`
	// CarrierSense is the interface's receive carrier-sense load in percent
	// (0..100). Nil (absent) when the CCU does not report it, which is the
	// common case over the JSON-RPC surface.
	CarrierSense *int `json:"carrier_sense,omitempty"`
}

// --- Links ---

// Link is the enriched view of a direct link between two channels.
type Link struct {
	Sender                   string `json:"sender_address"`
	Receiver                 string `json:"receiver_address"`
	Name                     string `json:"name,omitempty"`
	Description              string `json:"description,omitempty"`
	Flags                    int    `json:"flags,omitempty"`
	SenderDeviceName         string `json:"sender_device_name,omitempty"`
	SenderDeviceModel        string `json:"sender_device_model,omitempty"`
	SenderChannelType        string `json:"sender_channel_type,omitempty"`
	SenderChannelTypeLabel   string `json:"sender_channel_type_label,omitempty"`
	SenderChannelName        string `json:"sender_channel_name,omitempty"`
	ReceiverDeviceName       string `json:"receiver_device_name,omitempty"`
	ReceiverDeviceModel      string `json:"receiver_device_model,omitempty"`
	ReceiverChannelType      string `json:"receiver_channel_type,omitempty"`
	ReceiverChannelTypeLabel string `json:"receiver_channel_type_label,omitempty"`
	ReceiverChannelName      string `json:"receiver_channel_name,omitempty"`
	PeerAddress              string `json:"peer_address"`
	PeerDeviceName           string `json:"peer_device_name,omitempty"`
	PeerDeviceModel          string `json:"peer_device_model,omitempty"`
	Direction                string `json:"direction"`
	// CentralName and InterfaceID identify the owning CCU and interface
	// of the link. They are populated only by the global links overview
	// (`GET /api/v1/links`); the per-device listing leaves them empty so
	// its response stays byte-identical.
	CentralName string `json:"central_name,omitempty"`
	InterfaceID string `json:"interface_id,omitempty"`
}

// LinkableChannel is one candidate returned by
// GET /channels/{no}/linkable-channels.
type LinkableChannel struct {
	Address          string `json:"address"`
	ChannelType      string `json:"channel_type,omitempty"`
	ChannelTypeLabel string `json:"channel_type_label,omitempty"`
	ChannelName      string `json:"channel_name,omitempty"`
	DeviceAddress    string `json:"device_address"`
	DeviceName       string `json:"device_name,omitempty"`
	DeviceModel      string `json:"device_model,omitempty"`
}

// --- Schedules ---

// ClimateSchedule is the schedule payload returned by GET
// /devices/{addr}/schedule. The shape carries both flavours so the SPA can
// render a single response — "kind" disambiguates:
//
//   - "climate" — `profiles` (P1..P6) is populated and `simple_entries` is
//     empty. Used by thermostats with `P<n>_*` paramsets.
//   - "simple"  — `simple_entries` is populated and `profiles` is empty.
//     Used by switches / covers / lights with `<NN>_WP_*` paramsets
//     (HmIP-PSM, HmIP-FSM, …).
//
// `Domain` further specialises a "simple" schedule so the SPA can pick the
// matching editor widgets ("switch" → on/off toggle, "light" → slider + ramp,
// "cover" → two sliders, "lock" → action dropdown).
type ClimateSchedule struct {
	Channel       ScheduleChannelRef `json:"channel"`
	Kind          string             `json:"kind"`
	Domain        string             `json:"domain,omitempty"`
	ActiveProfile string             `json:"active_profile,omitempty"`
	// ActiveProfileIndex is the 0-based integer index of the currently
	// active climate profile as reported by the CCU. Nil when the device
	// does not report a numeric active-profile index (e.g. simple-schedule
	// devices). The SPA uses this to pre-select the profile tab.
	ActiveProfileIndex *int                      `json:"active_profile_index,omitempty"`
	Profiles           map[string]ClimateProfile `json:"profiles,omitempty"`
	SimpleEntries      []SimpleScheduleEntry     `json:"simple_entries,omitempty"`
}

// SimpleScheduleEntry is one switching slot for a non-climate device.
// Up to 24 such slots per channel.
//
// Trigger composition: the slot fires when the `condition` evaluates
// to true. Conditions combine `time` (fixed HH:MM) with optional
// astro events (`sunrise` / `sunset` ± `astro_offset_minutes`).
//
// Target composition: `target_channels` selects which actor channels
// the slot drives (e.g. "1_1" for channel 1, function 1). Empty list
// means "the CCU's default target".
//
// Action composition: `level` is the value the channel is set to at
// the trigger instant. Optional `level_2` carries cover slat
// position. `duration` keeps the actor at this level for a fixed
// time (auto-revert), `ramp_time` controls the dimmer ramp.
type SimpleScheduleEntry struct {
	// SlotNo is 1..24 — preserved so a partial update keeps unrelated
	// slots intact on the CCU.
	SlotNo int `json:"slot_no"`

	// --- Trigger -----------------------------------------------
	Weekdays []string `json:"weekdays"`
	// Time is the fixed switching time in 24-hour HH:MM. Required
	// even for astro conditions because the CCU stores it always.
	Time string `json:"time"`
	// Condition is one of:
	//   "fixed_time"                  — fire at Time only.
	//   "astro"                       — fire at astro event ± offset.
	//   "fixed_if_before_astro" / "astro_if_before_fixed"
	//   "fixed_if_after_astro"  / "astro_if_after_fixed"
	//   "earliest_of_fixed_and_astro" / "latest_of_fixed_and_astro"
	// Defaults to "fixed_time" when empty / unknown.
	Condition string `json:"condition,omitempty"`
	// AstroType is "sunrise" or "sunset". Required for any astro-
	// involving condition; ignored for "fixed_time".
	AstroType string `json:"astro_type,omitempty"`
	// AstroOffsetMinutes shifts the astro event (-720..+720).
	AstroOffsetMinutes int `json:"astro_offset_minutes,omitempty"`

	// --- Target ------------------------------------------------
	// TargetChannels addresses output sub-channels in "X_Y" notation
	// (X=1..8 actor channel, Y=1..3 function). Empty list = CCU
	// default routing.
	TargetChannels []string `json:"target_channels,omitempty"`

	// --- Action ------------------------------------------------
	Level  float64  `json:"level"`
	Level2 *float64 `json:"level_2,omitempty"`
	// Duration / RampTime are human-readable strings: "10s", "5min",
	// "1h", "100ms", "500ms", "2s", … The (de)serializer maps them
	// onto the CCU's TimeBase + factor pair.
	Duration string `json:"duration,omitempty"`
	RampTime string `json:"ramp_time,omitempty"`

	// --- Lock-only fields ---------------------------------------
	// LockMode is "door_lock" or "user_permission" — picks how the
	// rest of the lock-domain fields are encoded on the wire.
	// LockMode = "door_lock":
	//   * LockAction sets the LEVEL+DURATION pair via the standard
	//     KeyMatic encoding.
	//   * Permission must be empty.
	// LockMode = "user_permission":
	//   * Permission ("granted" / "not_granted") sets LEVEL.
	//   * DURATION is forced to (HOUR_1, 31).
	//   * LockAction must be empty.
	LockMode   string `json:"lock_mode,omitempty"`
	LockAction string `json:"lock_action,omitempty"`
	Permission string `json:"permission,omitempty"`
}

// ScheduleChannelRef identifies the owning channel so the SPA can
// cross-reference with the device detail page without carrying the
// URL params around separately.
type ScheduleChannelRef struct {
	Address string `json:"address"`
	Number  int    `json:"number"`
	Device  string `json:"device_address"`
}

// ClimateProfile is one named profile (P1..P6) with the seven
// weekday slots. Missing weekdays are valid — the thermostat falls
// back to its base temperature.
type ClimateProfile struct {
	Weekdays map[string]ClimateWeekday `json:"weekdays"`
}

// ClimateWeekday is the simplified weekday form (base + periods), as
// opposed to the 13-slot CCU wire format. The adapter does the
// conversion in both directions.
type ClimateWeekday struct {
	BaseTemperature float64         `json:"base_temperature"`
	Periods         []ClimatePeriod `json:"periods"`
}

// ClimatePeriod is one non-base-temperature stretch.
type ClimatePeriod struct {
	StartTime   string  `json:"start_time"`
	EndTime     string  `json:"end_time"`
	Temperature float64 `json:"temperature"`
}

// --- UI Schema ---

// ErrUISchemaNotFound signals the device or channel cannot be located.
var ErrUISchemaNotFound = errors.New("ui-schema: channel not found")

// UISchemaRequest aggregates every option a UI-schema lookup
// understands. Bundling them into a struct keeps the call site
// readable as the parameter list grows (currently: paramset, peer,
// locale, expert).
type UISchemaRequest struct {
	Address  string
	Channel  int
	Paramset string
	Peer     string
	Locale   string
	// Expert disables the easymode filter that hides untranslated
	// MASTER parameters. SPA users opt in via the "Expert"-Toggle in
	// the ChannelPanel; default is the curated, friendly view.
	Expert bool
}

// UISchema is the root object returned by the endpoint.
type UISchema struct {
	Channel          UISchemaChannel           `json:"channel"`
	Groups           []UISchemaGroup           `json:"groups,omitempty"`
	ParameterOrder   []string                  `json:"parameter_order,omitempty"`
	Parameters       []UISchemaParameter       `json:"parameters"`
	Visibility       []UISchemaVisibility      `json:"visibility,omitempty"`
	CrossValidations []UISchemaCrossValidation `json:"cross_validations,omitempty"`
	Profile          *UISchemaProfile          `json:"profile,omitempty"`
	// SubsetGroups are easymode "scene"-style multi-parameter choices
	// (e.g. "Light: warm/cool/off") — picking one option patches all
	// member parameters at once. Surfaced separately so the SPA can
	// render the picker above the regular parameter grid.
	SubsetGroups []UISchemaSubsetGroup `json:"subset_groups,omitempty"`
	// ModelDescription is the localised human-readable device model name
	// (e.g. "Funk-Schaltsteckdose"). Empty when the device model cannot
	// be resolved from the translation catalogue.
	ModelDescription string `json:"model_description,omitempty"`
	// DeviceIcon is the icon identifier for the device type. The SPA maps
	// it onto an icon resource. Empty when no icon is registered.
	DeviceIcon string `json:"device_icon,omitempty"`
}

// UISchemaSubsetGroup wraps one resolved easymode subset definition as
// labelled options. The SPA renders it as a labelled dropdown that,
// when the user picks an option, emits a multi-parameter patch.
type UISchemaSubsetGroup struct {
	ID              string              `json:"id"`
	Label           string              `json:"label"`
	MemberParams    []string            `json:"member_params"`
	CurrentOptionID *int                `json:"current_option_id,omitempty"`
	Options         []UISchemaSubsetOpt `json:"options"`
}

// UISchemaSubsetOpt is one labelled value-set inside a subset group.
type UISchemaSubsetOpt struct {
	ID     int            `json:"id"`
	Label  string         `json:"label"`
	Values map[string]any `json:"values"`
}

// UISchemaChannel identifies the channel whose schema is being
// served.
type UISchemaChannel struct {
	Address  string `json:"address"`
	Number   int    `json:"number"`
	Type     string `json:"type"`
	Label    string `json:"label,omitempty"`
	Device   string `json:"device_address"`
	Paramset string `json:"paramset"`
}

// UISchemaGroup is one semantic parameter group (e.g. "timing") with
// its localised label.
type UISchemaGroup struct {
	ID         string   `json:"id"`
	Label      string   `json:"label"`
	Parameters []string `json:"parameters"`
}

// UISchemaParameter is the complete, renderable descriptor for a
// single parameter.
//
// The Link* fields are only populated for LINK-paramset renderings.
// They carry the classification derived from the parameter name: keypress
// group (SHORT/LONG/COMMON), functional category, paired time-base
// linking, and preset pickers. The SPA uses them to render SHORT/
// LONG sub-sections, percent sliders, "hidden by default" toggles,
// and TIME_BASE/FACTOR preset dropdowns.
type UISchemaParameter struct {
	Name             string                   `json:"name"`
	Label            string                   `json:"label,omitempty"`
	Help             string                   `json:"help,omitempty"`
	Type             string                   `json:"type"`
	Unit             string                   `json:"unit,omitempty"`
	Min              json.RawMessage          `json:"min,omitempty"`
	Max              json.RawMessage          `json:"max,omitempty"`
	Default          json.RawMessage          `json:"default,omitempty"`
	ValueList        []UISchemaValueListEntry `json:"value_list,omitempty"`
	Operations       UISchemaParameterOps     `json:"operations"`
	Flags            UISchemaParameterFlags   `json:"flags"`
	Control          string                   `json:"control,omitempty"`
	Value            any                      `json:"value,omitempty"`
	Observed         bool                     `json:"observed"`
	ModifiedAt       string                   `json:"modified_at,omitempty"`
	GroupID          string                   `json:"group_id,omitempty"`
	Preset           string                   `json:"preset,omitempty"`
	Category         string                   `json:"category,omitempty"`
	KeypressGroup    string                   `json:"keypress_group,omitempty"`
	DisplayAsPercent bool                     `json:"display_as_percent,omitempty"`
	HasLastValue     bool                     `json:"has_last_value,omitempty"`
	HiddenByDefault  bool                     `json:"hidden_by_default,omitempty"`
	TimePairID       string                   `json:"time_pair_id,omitempty"`
	TimeSelectorType string                   `json:"time_selector_type,omitempty"`
	TimePresets      []UISchemaTimePreset     `json:"time_presets,omitempty"`
	// Presets are EasyMode value chips for ENUM/INTEGER/FLOAT parameters. Each
	// entry carries a localised label and the value to apply when the user
	// clicks the chip.
	Presets []UISchemaPreset `json:"presets,omitempty"`
	// AllowCustomValue is true when this parameter accepts values outside the
	// preset list (the user can type an arbitrary value in addition to the
	// preset chips). False when the preset list is exhaustive.
	AllowCustomValue bool `json:"allow_custom_value,omitempty"`
	// SubsetGroupID names the subset group this parameter belongs to,
	// if any. The SPA hides member parameters behind the group's picker widget
	// and writes the group's selected option instead of the raw value.
	SubsetGroupID string `json:"subset_group_id,omitempty"`
}

// UISchemaPreset is one EasyMode preset chip.
type UISchemaPreset struct {
	Label string          `json:"label"`
	Value json.RawMessage `json:"value"`
}

// UISchemaTimePreset is one preset entry for a TIME_BASE/TIME_FACTOR
// picker. The SPA binds the paired parameters to (Base, Factor).
type UISchemaTimePreset struct {
	Base   int    `json:"base"`
	Factor int    `json:"factor"`
	Label  string `json:"label"`
}

// UISchemaValueListEntry is one entry of an enum-style value list
// with its localised label.
type UISchemaValueListEntry struct {
	Value int    `json:"value"`
	Key   string `json:"key"`
	Label string `json:"label,omitempty"`
}

// UISchemaParameterOps mirrors the OCCU OPERATIONS bitmask.
type UISchemaParameterOps struct {
	Read  bool `json:"read"`
	Write bool `json:"write"`
	Event bool `json:"event"`
	// Determine is the DETERMINE bit (0x08): the parameter's live value
	// can be read from the device on demand. The MASTER editor renders a
	// "Determine" button for such fields. Omitted when unset so the
	// addition is backward-compatible.
	Determine bool `json:"determine,omitempty"`
}

// UISchemaParameterFlags mirrors the OCCU FLAGS bitmask.
type UISchemaParameterFlags struct {
	Visible  bool `json:"visible"`
	Internal bool `json:"internal"`
	Service  bool `json:"service"`
}

// UISchemaVisibility encodes one conditional-visibility rule.
// When Trigger equals TriggerValue, Show-listed parameters are visible
// and Hide-listed parameters are invisible.
type UISchemaVisibility struct {
	Show         []string `json:"show"`
	Hide         []string `json:"hide,omitempty"`
	Trigger      string   `json:"trigger"`
	TriggerValue any      `json:"trigger_value"`
}

// UISchemaCrossValidation is one cross-parameter rule already
// resolved to a localised error message.
//
// Param is set for single-parameter rules (e.g. "gte" / "lte" with one
// reference). MinParam and MaxParam are set for "between" rules.
// ParamA and ParamB carry the two-parameter form. Only the fields
// relevant to the specific Rule value are non-empty.
type UISchemaCrossValidation struct {
	ID              string   `json:"id"`
	Rule            string   `json:"rule"`
	ParamA          string   `json:"param_a,omitempty"`
	ParamB          string   `json:"param_b,omitempty"`
	Param           string   `json:"param,omitempty"`
	MinParam        string   `json:"min_param,omitempty"`
	MaxParam        string   `json:"max_param,omitempty"`
	AppliesToParams []string `json:"applies_to_params"`
	Error           string   `json:"error,omitempty"`
}

// UISchemaProfile wraps the receiver-profile catalogue for the
// channel's type (after alias resolution). The Raw payload is the
// JSON from the profile catalogue, carried verbatim to avoid a lossy
// Go mirror.
//
// For LINK paramsets the adapter pre-filters Raw to only the sender-
// channel-type subtree (SenderType) and pre-computes the best-match
// ActiveProfileID from the current values. Zero means "Expert" / no
// preset matches.
type UISchemaProfile struct {
	ReceiverType    string          `json:"receiver_type"`
	SenderType      string          `json:"sender_type,omitempty"`
	ActiveProfileID int             `json:"active_profile_id,omitempty"`
	Raw             json.RawMessage `json:"raw,omitempty"`
}

// --- Device replace ---

// ReplaceCandidate is one already-paired device a new (inbox) device may
// replace, as returned by `GET /devices/{addr}/replace-candidates`. The
// interface daemon (rfd / hs485d) computes type / channel compatibility;
// ModelMatches is true when the candidate's model equals the new
// device's, letting the SPA badge an exact swap apart from a compatible
// cross-type one.
type ReplaceCandidate struct {
	Address      string `json:"address"`
	Name         string `json:"name,omitempty"`
	Model        string `json:"model,omitempty"`
	Interface    string `json:"interface,omitempty"`
	Central      string `json:"central,omitempty"`
	ModelMatches bool   `json:"model_matches"`
}

// --- Device communication test ---

// CommunicationTestResult is the outcome of a per-device communication /
// function test (POST /devices/{addr}/test): the CCU sends a radio test
// frame to the device and waits for its ACK. Passed is true when the
// device's last-completed-test time advanced past the test start within
// the poll window; TimedOut is true when the window elapsed first.
type CommunicationTestResult struct {
	Passed      bool      `json:"passed"`
	StartedAt   time.Time `json:"started_at"`
	CompletedAt time.Time `json:"completed_at,omitempty"`
	DurationMs  int64     `json:"duration_ms"`
	TimedOut    bool      `json:"timed_out"`
}

// --- Device team assignment ---

// TeamCandidate is one team-channel a device channel may be assigned to,
// returned by GET /devices/{addr}/channels/{no}/team-candidates. The
// candidate list is filtered to channels sharing the target's team tag;
// Current marks the channel's currently-assigned team.
type TeamCandidate struct {
	Address string `json:"address"`
	Name    string `json:"name,omitempty"`
	TeamTag string `json:"team_tag,omitempty"`
	Current bool   `json:"current"`
}
