// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package payload

import "github.com/SukramJ/openccu-loom/pkg/hmenum"

// --- Generic (per-parameter wire-DP) Config -------------------------

// GenericConfig is the typed [ConfigPayload] for per-parameter wire
// DPs (every channel data point that maps 1:1 onto a CCU parameter).
// EventBridge fills it from [ParameterData]; the bridge serialises
// it as the retained /config body and the discovery builder reads
// its fields for min/max/options/unit_of_measurement.
type GenericConfig struct {
	Unit      string               `json:"unit,omitempty"`
	Type      hmenum.ParameterType `json:"type,omitempty"`
	Paramset  hmenum.ParamsetKey   `json:"paramset,omitempty"`
	Min       *float64             `json:"min,omitempty"`
	Max       *float64             `json:"max,omitempty"`
	Default   any                  `json:"default,omitempty"`
	ValueList []string             `json:"value_list,omitempty"`
	// ValueLabels carries the localised display strings for ValueList,
	// index-aligned with it. The raw tokens stay in ValueList — they are
	// what a write has to carry back to the CCU — so a consumer can show
	// the label and still address the value.
	ValueLabels []string `json:"value_labels,omitempty"`
	Label       string   `json:"label,omitempty"`
	// LabelOmitted is true when the embedded translation_custom
	// table maps this parameter to an explicit empty string (e.g.
	// `"state": ""` in translation_custom/parameters_*.json). That
	// signals "primary parameter, render no entity name" — HA picks
	// the device name alone for friendly_name and entity_id, the
	// same effect HA-native integrations achieve via
	// `_attr_translation_key` + an HA-translation `name: ""` entry.
	LabelOmitted bool     `json:"label_omitted,omitempty"`
	SourceParams []string `json:"source,omitempty"`
}

// --- Climate ---------------------------------------------------------

// ClimateConfig is the typed [ConfigPayload] for climate aggregates
// (HmIP-BWTH, HmIP-eTRV, …). HVAC modes and preset modes are emitted
// only when the source advertises them.
type ClimateConfig struct {
	MinTemp         float64  `json:"min_temp"`
	MaxTemp         float64  `json:"max_temp"`
	TempStep        float64  `json:"temp_step"`
	TemperatureUnit string   `json:"temperature_unit"`
	HVACModes       []string `json:"hvac_modes,omitempty"`
	PresetModes     []string `json:"preset_modes,omitempty"`
}

// --- Cover / Blind / Garage -----------------------------------------

// CoverConfig describes a shutter (single-axis position).
type CoverConfig struct {
	InvertedControl bool `json:"inverted_control"`
	SupportsStop    bool `json:"supports_stop"`
	SupportsTilt    bool `json:"supports_tilt"`
}

// BlindConfig describes a blind (position + tilt). SupportsTilt is
// always true — kept on the struct to mirror the wire shape.
type BlindConfig struct {
	InvertedControl bool `json:"inverted_control"`
	SupportsStop    bool `json:"supports_stop"`
	SupportsTilt    bool `json:"supports_tilt"`
}

// GarageConfig describes a garage door.
type GarageConfig struct {
	SupportsStop bool `json:"supports_stop"`
	SupportsVent bool `json:"supports_vent"`
}

// --- Light + subtypes -----------------------------------------------

// LightConfig is the base for plain dimmable / switchable lights.
type LightConfig struct {
	Dimmable          bool `json:"dimmable"`
	SupportsColor     bool `json:"supports_color"`
	SupportsColorTemp bool `json:"supports_color_temp"`
	SupportsEffects   bool `json:"supports_effects"`
}

// ColorTempLightConfig adds the white-point kelvin range.
type ColorTempLightConfig struct {
	LightConfig
	MinKelvin int32 `json:"min_kelvin"`
	MaxKelvin int32 `json:"max_kelvin"`
}

// EffectLightConfig adds the available effect labels.
type EffectLightConfig struct {
	LightConfig
	Effects []string `json:"effects,omitempty"`
}

// RGBWLightConfig combines colour-temp + effects (HmIP-BSL).
type RGBWLightConfig struct {
	LightConfig
	MinKelvin int32    `json:"min_kelvin"`
	MaxKelvin int32    `json:"max_kelvin"`
	Effects   []string `json:"effects,omitempty"`
}

// --- Lock ------------------------------------------------------------

// LockConfig captures the per-lock capability flags.
type LockConfig struct {
	SupportsOpen bool `json:"supports_open"`
}

// --- Siren / SmokeSiren / SoundPlayer -------------------------------

// SirenConfig describes a generic siren entity.
type SirenConfig struct {
	SupportsAcoustic bool     `json:"supports_acoustic"`
	SupportsOptical  bool     `json:"supports_optical"`
	SupportsDuration bool     `json:"supports_duration"`
	AvailableTones   []string `json:"available_tones,omitempty"`
	AvailableLights  []string `json:"available_lights,omitempty"`
}

// SmokeSirenConfig pins the kind discriminator for smoke variants.
type SmokeSirenConfig struct {
	Kind string `json:"kind"` // always "smoke"
}

// SoundPlayerConfig extends Siren with sound-file controls.
type SoundPlayerConfig struct {
	SirenConfig
	AvailableSoundfiles  []string `json:"available_soundfiles,omitempty"`
	AvailableRepetitions []string `json:"available_repetitions,omitempty"`
}

// --- Switch ----------------------------------------------------------

// SwitchConfig is the typed marker for switch custom-DPs.
type SwitchConfig struct {
	Category string `json:"category"` // always "switch"
}

// --- TextDisplay -----------------------------------------------------

// TextDisplayConfig flags the write-only nature of text-display
// custom-DPs.
type TextDisplayConfig struct {
	WriteOnly bool `json:"write_only"` // always true
}

// --- Valve (Irrigation / Modulating) --------------------------------

// IrrigationValveConfig pins the kind discriminator.
type IrrigationValveConfig struct {
	Kind string `json:"kind"` // always "irrigation"
}

// ModulatingValveConfig pins the kind discriminator.
type ModulatingValveConfig struct {
	Kind string `json:"kind"` // always "modulating"
}

// --- Hub-side (program / sysvar / aggregates) -----------------------

// ProgramConfig is the typed [ConfigPayload] for CCU programs.
type ProgramConfig struct {
	EnabledDefault bool `json:"enabled_default"`
}

// SysvarConfig is the typed [ConfigPayload] for CCU system variables.
type SysvarConfig struct {
	EnabledDefault bool `json:"enabled_default"`
	Writable       bool `json:"writable"`
}

// --- Unit + InterfaceClient (top-level services) -------------

// CentralConfig is the operator-tunable configuration of the
// central. Today the central exposes few runtime-tunable knobs; the
// struct shape lets adapters refer to the bucket without
// special-casing.
type CentralConfig struct {
	Name string `json:"name"`
}

// InterfaceClientConfig captures the capability profile the client
// runs with. Detailed reliability tuning (throttle / retry / circuit
// policy) lives in the operator config file — the bucket here is the
// observable capability summary only.
type InterfaceClientConfig struct {
	RPCCallback    bool `json:"rpc_callback"`
	PingPong       bool `json:"ping_pong"`
	ListDevices    bool `json:"list_devices"`
	GetAllPrograms bool `json:"get_all_programs"`
	GetAllSysvars  bool `json:"get_all_sysvars"`
}
