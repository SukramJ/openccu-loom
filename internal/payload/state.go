// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package payload

// This file holds the typed [StatePayload] structs every Source
// implementation returns from [Source.StatePayload]. The structs
// mirror the historic `map[string]any` shapes the source-pipeline
// already produced — JSON marshalling yields the same wire bytes —
// but the fields are now compile-time-checked.
//
// Pointer fields signal "field absent unless observed" — Go's
// json encoder elides them via `omitempty` when nil. Always-present
// fields with a sensible default (e.g. Climate.HVACMode = "off")
// stay scalar.

// --- Color helper (shared by light subtypes) ------------------------

// ColorHS is the HA-canonical hue/saturation tuple. Sent as
// `{"h": 220, "s": 80}`.
type ColorHS struct {
	H float64 `json:"h"`
	S float64 `json:"s"`
}

// --- Climate ---------------------------------------------------------

// ClimateState is the live climate state. Several fields default to
// HA-friendly sentinels ("off"/"none"/"idle") so HA's value_template
// filters never resolve to undefined.
//
// Action is omitted entirely for thermostats without an activity
// source (display-only devices like HmIP-STHD) — the discovery payload
// skips the action_topic for those, so no template ever reads the key.
//
// Layer-Trennung: identity fields (Address, UniqueID) live in
// [ClimateInfo]. The HA-Discovery json_attributes_template derives
// the `value_state` UI label from `state_uncertain` directly.
type ClimateState struct {
	StateUncertain bool   `json:"state_uncertain"`
	HVACMode       string `json:"hvac_mode"`
	PresetMode     string `json:"preset_mode"`
	Action         string `json:"action,omitempty"`
	// CurrentTemperature / SetTemperature / CurrentHumidity mirror the
	// channel's measurement field DPs so REST/WS consumers (external
	// clients, SPA tiles) can populate a climate card from the CDP
	// state alone. The MQTT plane keeps using the per-DP slot topics
	// (ADR 0011); these fields are additive for the aggregate state.
	CurrentTemperature       *float64       `json:"current_temperature,omitempty"`
	SetTemperature           *float64       `json:"set_temperature,omitempty"`
	CurrentHumidity          *float64       `json:"current_humidity,omitempty"`
	TemperatureOffset        *string        `json:"temperature_offset,omitempty"`
	OptimumStartStop         any            `json:"optimum_start_stop,omitempty"`
	AvailableProfiles        []any          `json:"available_profiles,omitempty"`
	CurrentScheduleProfile   string         `json:"current_schedule_profile,omitempty"`
	DeviceActiveProfileIndex *int           `json:"device_active_profile_index,omitempty"`
	ScheduleAPIVersion       string         `json:"schedule_api_version,omitempty"`
	ScheduleData             map[string]any `json:"schedule_data,omitempty"`
}

// --- Cover / Blind / Garage -----------------------------------------

// CoverState is the live cover state.
type CoverState struct {
	State           string   `json:"state"`
	CurrentPosition *int     `json:"current_position,omitempty"`
	Level           *float64 `json:"level,omitempty"`
	Direction       string   `json:"direction,omitempty"`
}

// BlindState extends [CoverState] with the tilt axis.
type BlindState struct {
	State               string   `json:"state"`
	CurrentPosition     int      `json:"current_position"`
	Level               *float64 `json:"level,omitempty"`
	CurrentTiltPosition int      `json:"current_tilt_position"`
	TiltLevel           float64  `json:"tilt_level"`
	Direction           string   `json:"direction,omitempty"`
}

// GarageState is the live garage-door state.
type GarageState struct {
	State           string `json:"state"`
	CurrentPosition int    `json:"current_position"`
	DoorState       string `json:"door_state,omitempty"`
}

// --- Light + subtypes -----------------------------------------------

// LightState is the base live state for any light type.
type LightState struct {
	State      string `json:"state"`
	Brightness *int   `json:"brightness,omitempty"`
}

// ColorLightState adds HS-colour fields.
type ColorLightState struct {
	LightState
	ColorMode string   `json:"color_mode,omitempty"`
	Color     *ColorHS `json:"color,omitempty"`
}

// ColorTempLightState adds the colour-temperature kelvin field.
type ColorTempLightState struct {
	LightState
	ColorMode       string `json:"color_mode,omitempty"`
	ColorTempKelvin *int   `json:"color_temp_kelvin,omitempty"`
}

// FixedColorLightState carries the fixed colour plus its label.
type FixedColorLightState struct {
	LightState
	ColorMode  string   `json:"color_mode,omitempty"`
	Color      *ColorHS `json:"color,omitempty"`
	FixedColor string   `json:"fixed_color,omitempty"`
}

// EffectLightState adds the current effect label.
type EffectLightState struct {
	ColorLightState
	Effect string `json:"effect,omitempty"`
}

// DRGDaliLightState reuses ColorTempLightState without additions.
type DRGDaliLightState struct {
	ColorTempLightState
}

// RGBWLightState carries colour + optional kelvin, depending on the
// active operating mode at emission time. Setters choose which fields
// to populate; omitempty handles the absence of the unused fields.
type RGBWLightState struct {
	LightState
	ColorMode       string   `json:"color_mode,omitempty"`
	Color           *ColorHS `json:"color,omitempty"`
	ColorTempKelvin *int     `json:"color_temp_kelvin,omitempty"`
}

// --- Lock ------------------------------------------------------------

// LockState is the live lock state. All HA-relevant fields are
// always present so HA value templates never see `undefined`.
type LockState struct {
	StateUncertain bool   `json:"state_uncertain"`
	LockState      string `json:"lock_state"`
	IsLocked       bool   `json:"is_locked"`
	Direction      string `json:"direction"`
	IsLocking      bool   `json:"is_locking"`
	IsUnlocking    bool   `json:"is_unlocking"`
	IsJammed       bool   `json:"is_jammed"`
}

// --- Siren / SmokeSiren / SoundPlayer -------------------------------

// SirenState is the live siren state ("on"/"off").
type SirenState struct {
	State string `json:"state"`
}

// SmokeSirenState mirrors [SirenState].
type SmokeSirenState struct {
	State string `json:"state"`
}

// SoundPlayerState mirrors [SirenState].
type SoundPlayerState struct {
	State string `json:"state"`
}

// --- Switch ----------------------------------------------------------

// SwitchState is the live switch state — `is_on` only emitted once
// the channel has produced an observed value.
type SwitchState struct {
	IsOn *bool `json:"is_on,omitempty"`
}

// --- TextDisplay -----------------------------------------------------

// TextDisplayState carries the available option lists (icons, sounds,
// background / text colours, alignments, repetitions, intervals) when the
// model exposes them. Consumers build per-option pickers from these lists;
// each is omitted when the underlying VALUE_LIST was not captured.
type TextDisplayState struct {
	AvailableIcons            []any `json:"available_icons,omitempty"`
	AvailableSounds           []any `json:"available_sounds,omitempty"`
	AvailableBackgroundColors []any `json:"available_background_colors,omitempty"`
	AvailableTextColors       []any `json:"available_text_colors,omitempty"`
	AvailableAlignments       []any `json:"available_alignments,omitempty"`
	AvailableRepetitions      []any `json:"available_repetitions,omitempty"`
	AvailableIntervals        []any `json:"available_intervals,omitempty"`
}

// --- Valve (Irrigation / Modulating) --------------------------------

// IrrigationValveState is the live irrigation-valve state.
type IrrigationValveState struct {
	IsOpen bool `json:"is_open"`
}

// ModulatingValveState carries the current opening level.
type ModulatingValveState struct {
	CurrentLevel    *float64 `json:"current_level,omitempty"`
	CurrentLevelPct float64  `json:"current_level_pct"`
}

// --- Hub-side ---------------------------------------------------------

// ProgramState is the live program state.
type ProgramState struct {
	StateUncertain bool  `json:"state_uncertain"`
	IsActive       *bool `json:"is_active,omitempty"`
	// ExecuteAvailable reports whether running the program would do
	// anything: a program the CCU has deactivated ignores its triggers and
	// refuses a manual run.
	//
	// The daemon answers this rather than leaving each consumer to derive
	// it, because it is CCU semantics, not a presentation choice — and the
	// consumers that surface a program as two controls (an active toggle
	// and an execute action) otherwise each have to rediscover that the
	// second one depends on the first.
	//
	// Toggling activity stays available regardless: it is what brings a
	// deactivated program back, so gating it would leave no way back.
	ExecuteAvailable  bool   `json:"execute_available"`
	LastExecuted      string `json:"last_executed,omitempty"`
	LastResultSuccess *bool  `json:"last_result_success,omitempty"`
}

// SysvarState is the live sysvar state. Value and PreviousValue are
// emitted only after the first observed read.
type SysvarState struct {
	StateUncertain bool   `json:"state_uncertain"`
	ValueType      string `json:"value_type"`
	Value          any    `json:"value,omitempty"`
	PreviousValue  any    `json:"previous_value,omitempty"`
}

// UpdateState carries the latest firmware-update tracker snapshot.
type UpdateState struct {
	Available            bool   `json:"available"`
	CurrentFirmware      string `json:"current_firmware,omitempty"`
	LatestFirmware       string `json:"latest_firmware,omitempty"`
	UpdateAvailable      bool   `json:"update_available,omitempty"`
	CheckScriptAvailable bool   `json:"check_script_available,omitempty"`
}

// AlarmMessageRow is one entry inside the alarm-messages aggregate.
type AlarmMessageRow struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	DeviceName  string   `json:"device_name"`
	Address     string   `json:"address"`
	StateValue  string   `json:"state_value"`
	Timestamp   string   `json:"timestamp"`
	Counter     int      `json:"counter"`
	LastTrigger string   `json:"last_trigger"`
	Rooms       []string `json:"rooms"`
}

// AlarmMessagesState is the live alarm-messages aggregate state.
type AlarmMessagesState struct {
	Count    int               `json:"count"`
	Items    []AlarmMessageRow `json:"items"`
	Observed bool              `json:"observed"`
}

// ServiceMessageRow is one entry inside the service-messages aggregate.
type ServiceMessageRow struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Address     string   `json:"address"`
	DeviceName  string   `json:"device_name"`
	Type        string   `json:"type"`
	Description string   `json:"description"`
	Priority    int      `json:"priority"`
	Timestamp   string   `json:"timestamp"`
	Counter     int      `json:"counter"`
	Rooms       []string `json:"rooms"`
	Functions   []string `json:"functions"`
	Quittable   bool     `json:"quittable"`
}

// ServiceMessagesState is the live service-messages aggregate state.
type ServiceMessagesState struct {
	Count          int                 `json:"count"`
	QuittableCount int                 `json:"quittable_count"`
	Items          []ServiceMessageRow `json:"items"`
	Observed       bool                `json:"observed"`
}

// InstallModeState is the live install-mode countdown snapshot.
type InstallModeState struct {
	Active           bool `json:"active"`
	SecondsRemaining int  `json:"seconds_remaining"`
	Observed         bool `json:"observed"`
}

// ConnectivityInterfaceRow is one per-interface reachability entry.
type ConnectivityInterfaceRow struct {
	InterfaceID string `json:"interface_id"`
	Reachable   bool   `json:"reachable"`
}

// ConnectivityState is the live connectivity-aggregate snapshot.
type ConnectivityState struct {
	AllReachable bool                       `json:"all_reachable"`
	Interfaces   []ConnectivityInterfaceRow `json:"interfaces"`
	Observed     bool                       `json:"observed"`
}

// InboxDeviceRow is one device entry inside the inbox aggregate.
// FirstSeen is the Unix-epoch second the device first appeared in
// the inbox — emitted as a JSON number to match the historic wire
// shape (consumers parse it numerically).
type InboxDeviceRow struct {
	Address      string `json:"address"`
	Model        string `json:"model"`
	Serial       string `json:"serial"`
	FirstSeen    int64  `json:"first_seen"`
	Manufacturer string `json:"manufacturer"`
}

// InboxState is the live device-inbox aggregate snapshot.
type InboxState struct {
	Count    int              `json:"count"`
	Devices  []InboxDeviceRow `json:"devices"`
	Observed bool             `json:"observed"`
}

// MetricsSample is one captured metric value.
type MetricsSample struct {
	Value any    `json:"value"`
	When  string `json:"when"`
}

// MetricsState is the live hub-metrics snapshot. Keyed by metric kind
// (`callbacks`, `events`, …); each entry carries the most recent sample.
type MetricsState map[string]MetricsSample

// HubState is the live hub-root snapshot.
type HubState struct {
	ProgramCount int `json:"program_count"`
	SysvarCount  int `json:"sysvar_count"`
}

// --- Unit + InterfaceClient (top-level services) -------------

// CentralState is the live runtime status of the central. The
// state-machine bucket and the registered-device count are the two
// metrics northbound adapters consume — health page, connectivity
// badge, REST /info endpoint.
type CentralState struct {
	State       string `json:"state,omitempty"`
	DeviceCount int    `json:"device_count,omitempty"`
}

// InterfaceClientState is the live state of one interface client.
// Aggregated counters (`total_requests`, `executed_requests`,
// `pending_requests`) let the MQTT bridge publish one connectivity
// topic per interface without crossing the metrics layer.
type InterfaceClientState struct {
	State            string `json:"state"`
	Closed           bool   `json:"closed"`
	TotalRequests    int64  `json:"total_requests"`
	ExecutedRequests int64  `json:"executed_requests"`
	PendingRequests  int64  `json:"pending_requests"`
	LastFailureAt    string `json:"last_failure_at,omitempty"`
	LastCallbackAt   string `json:"last_callback_at,omitempty"`
}

// --- Device / Channel / Generic --------------------------------------

// ChannelState is the live channel state. Channel is a container —
// no per-channel runtime state today, so the struct stays empty
// (publishers return nil).
type ChannelState struct{}

// GenericDataPointState carries the live wire-DP state. Value is
// emitted only after the first observed read.
type GenericDataPointState struct {
	Available   bool   `json:"available"`
	Value       any    `json:"value,omitempty"`
	ModifiedAt  string `json:"modified_at,omitempty"`
	RefreshedAt string `json:"refreshed_at,omitempty"`
	Status      string `json:"status,omitempty"`
}
