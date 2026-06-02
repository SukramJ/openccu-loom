// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package payload

// This file holds the typed [InfoPayload] structs every Source
// implementation returns from [Source.InfoPayload]. The structs mirror
// the historic `map[string]any` shapes the source-pipeline already
// produced — JSON marshalling yields the same wire bytes — but the
// fields are now compile-time-checked and IDE-discoverable.
//
// Conventions:
//   - String IDs / labels carry the value verbatim.
//   - Conditional fields use `omitempty` JSON tags and either an empty
//     scalar or a pointer that stays nil when not applicable.
//   - Embedded structs propagate the base fields to subtypes via
//     anonymous embedding (e.g. ColorLightInfo embeds LightInfo).

// --- Climate ---------------------------------------------------------

// ClimateInfo is the identity payload for climate aggregates.
// Key is the canonical custom-DP identifier (`<address>:<channel>:
// <parameter>`). It doubles as the HA `unique_id` and the REST
// identity-key, so the struct emits one wire field instead of two
// identical ones.
type ClimateInfo struct {
	Address   string   `json:"address"`
	Key       string   `json:"key"`
	Kind      string   `json:"kind"`
	Category  string   `json:"category"`
	SubDPKeys []string `json:"sub_dp_keys,omitempty"`
}

// --- Cover / Blind / Garage -----------------------------------------

// CoverInfo is the identity payload for plain covers.
type CoverInfo struct {
	Address   string   `json:"address"`
	Category  string   `json:"category"`
	Key       string   `json:"key,omitempty"`
	SubDPKeys []string `json:"sub_dp_keys,omitempty"`
}

// BlindInfo extends [CoverInfo] with the blind kind marker.
type BlindInfo struct {
	Address  string `json:"address"`
	Category string `json:"category"`
	Kind     string `json:"kind"`
	Key      string `json:"key,omitempty"`
}

// GarageInfo is the identity payload for garage doors.
type GarageInfo struct {
	Address  string `json:"address"`
	Category string `json:"category"`
	Kind     string `json:"kind"`
	Key      string `json:"key"`
}

// --- Light + subtypes -----------------------------------------------

// LightInfo is the base identity payload for any light type.
type LightInfo struct {
	Category string `json:"category"`
	Address  string `json:"address,omitempty"`
	Key      string `json:"key,omitempty"`
	Dimmable bool   `json:"dimmable"`
}

// ColorLightInfo adds the colour-light kind marker.
type ColorLightInfo struct {
	LightInfo
	Kind string `json:"kind"`
}

// ColorTempLightInfo adds the white-point kelvin range.
type ColorTempLightInfo struct {
	LightInfo
	Kind      string `json:"kind"`
	MinKelvin int32  `json:"min_kelvin"`
	MaxKelvin int32  `json:"max_kelvin"`
}

// FixedColorLightInfo adds the fixed-colour kind marker.
type FixedColorLightInfo struct {
	LightInfo
	Kind string `json:"kind"`
}

// EffectLightInfo extends colour-light info with the effect kind.
type EffectLightInfo struct {
	ColorLightInfo
}

// DRGDaliLightInfo extends colour-temp info with the DALI kind.
type DRGDaliLightInfo struct {
	ColorTempLightInfo
}

// RGBWLightInfo extends colour-light info with the active operating
// mode (TunableWhite / RGB / RGBW / PWM).
type RGBWLightInfo struct {
	ColorLightInfo
	Mode string `json:"mode"`
}

// --- Lock ------------------------------------------------------------

// LockInfo is the identity payload for locks.
type LockInfo struct {
	Address   string   `json:"address"`
	Key       string   `json:"key"`
	Category  string   `json:"category"`
	Kind      string   `json:"kind"`
	SubDPKeys []string `json:"sub_dp_keys,omitempty"`
}

// --- Siren / SmokeSiren / SoundPlayer -------------------------------

// SirenInfo is the identity payload for plain sirens.
type SirenInfo struct {
	Address  string `json:"address"`
	Key      string `json:"key"`
	Category string `json:"category"`
}

// SmokeSirenInfo extends [SirenInfo] with the smoke-kind discriminator.
type SmokeSirenInfo struct {
	SirenInfo
	Kind string `json:"kind"`
}

// SoundPlayerInfo extends [SirenInfo] with the sound-player kind.
type SoundPlayerInfo struct {
	SirenInfo
	Kind string `json:"kind"`
}

// --- Switch ----------------------------------------------------------

// SwitchInfo is the identity payload for switch custom-DPs.
type SwitchInfo struct {
	Address  string `json:"address"`
	Key      string `json:"key"`
	Category string `json:"category"`
}

// --- TextDisplay -----------------------------------------------------

// TextDisplayInfo is the identity payload for text-display custom-DPs.
type TextDisplayInfo struct {
	Address  string `json:"address"`
	Key      string `json:"key"`
	Category string `json:"category"`
}

// --- Valve (Irrigation / Modulating) --------------------------------

// IrrigationValveInfo is the identity payload for irrigation valves.
type IrrigationValveInfo struct {
	Address  string `json:"address"`
	Key      string `json:"key"`
	Category string `json:"category"`
	Kind     string `json:"kind"`
}

// ModulatingValveInfo is the identity payload for modulating valves.
type ModulatingValveInfo struct {
	Address  string `json:"address"`
	Key      string `json:"key"`
	Category string `json:"category"`
	Kind     string `json:"kind"`
}

// --- Hub-side ---------------------------------------------------------

// ProgramInfo is the identity payload for CCU programs.
type ProgramInfo struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Category    string `json:"category"`
	UniqueID    string `json:"unique_id"`
	IsInternal  bool   `json:"is_internal"`
}

// SysvarInfo is the identity payload for CCU system variables.
type SysvarInfo struct {
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Category    string   `json:"category"`
	UniqueID    string   `json:"unique_id"`
	ValueType   string   `json:"value_type"`
	Unit        string   `json:"unit"`
	Vid         int      `json:"vid"`
	IsExtended  bool     `json:"is_extended"`
	ValueList   []string `json:"value_list,omitempty"`
	Min         any      `json:"min,omitempty"`
	Max         any      `json:"max,omitempty"`
}

// UpdateInfo is the identity payload for an update tracker.
type UpdateInfo struct {
	Category string `json:"category"`
}

// AlarmMessagesInfo is the identity payload for the alarm-messages aggregate.
type AlarmMessagesInfo struct {
	Category string `json:"category"`
}

// ServiceMessagesInfo is the identity payload for the service-messages aggregate.
type ServiceMessagesInfo struct {
	Category string `json:"category"`
}

// InstallModeInfo is the identity payload for an install-mode tracker.
type InstallModeInfo struct {
	Category    string `json:"category"`
	InterfaceID string `json:"interface_id"`
}

// ConnectivityInfo is the identity payload for the connectivity aggregate.
type ConnectivityInfo struct {
	Category string `json:"category"`
}

// InboxInfo is the identity payload for the device-inbox aggregate.
type InboxInfo struct {
	Category string `json:"category"`
}

// MetricsInfo is the identity payload for the hub-metrics aggregate.
type MetricsInfo struct {
	Category string `json:"category"`
}

// HubInfo is the identity payload for the hub root.
type HubInfo struct {
	CentralName string `json:"central_name"`
	Category    string `json:"category"`
}

// --- Unit + InterfaceClient (top-level services) -------------

// CentralInfo is the identity payload for a central. HA-canonical
// keys (`sw_version`, `serial_number`, `configuration_url`) are used
// directly so the body can flow straight into the MQTT-Discovery
// hub-device block without per-key renaming. Empty fields are omitted
// — they are "not yet observed" rather than real data.
type CentralInfo struct {
	Name             string `json:"name"`
	Model            string `json:"model,omitempty"`
	SWVersion        string `json:"sw_version,omitempty"`
	Hostname         string `json:"hostname,omitempty"`
	SerialNumber     string `json:"serial_number,omitempty"`
	ConfigurationURL string `json:"configuration_url,omitempty"`
	IsHaApp          bool   `json:"is_ha_app,omitempty"`
}

// InterfaceClientInfo is the identity payload for one interface client
// (HmIP-RF, BidCos-RF, …).
type InterfaceClientInfo struct {
	Central   string `json:"central"`
	Interface string `json:"interface"`
}

// --- Device / Channel / Generic --------------------------------------

// DeviceInfoChannelRow summarises one channel in the device-info
// snapshot published to the retained `<addr>/info` MQTT topic.
type DeviceInfoChannelRow struct {
	ChannelNo    int      `json:"channel_no"`
	Type         string   `json:"type"`
	ParamsetKeys []string `json:"paramset_keys"`
	CustomDPs    []string `json:"custom_dps,omitempty"`
}

// DeviceInfo is the identity payload for a CCU device. Field tags
// mirror the historical map keys (e.g. `serial_number` instead of
// `address`) so JSON-wire bytes stay identical.
//
// Central, SWVersion, and Channels are populated at publish time by
// the EventBridge (not by Device.InfoPayload) because they require
// runtime context (central name, firmware tracker, channel list) that
// the device model itself does not own.
type DeviceInfo struct {
	InterfaceID   string                 `json:"interface_id"`
	Interface     string                 `json:"interface"`
	Address       string                 `json:"serial_number"`
	Model         string                 `json:"model"`
	ModelLabel    string                 `json:"model_label,omitempty"`
	ModelIcon     string                 `json:"model_icon,omitempty"`
	SubModel      string                 `json:"sub_model,omitempty"`
	Name          string                 `json:"name,omitempty"`
	Manufacturer  string                 `json:"manufacturer,omitempty"`
	ProductGroup  string                 `json:"product_group,omitempty"`
	Rooms         []string               `json:"rooms,omitempty"`
	Functions     []string               `json:"functions,omitempty"`
	Room          string                 `json:"room,omitempty"`
	Function      string                 `json:"function,omitempty"`
	IseID         int                    `json:"ise_id,omitempty"`
	SchemaVersion int                    `json:"schema_version,omitempty"`
	HasSubDevices bool                   `json:"has_sub_devices"`
	Central       string                 `json:"central,omitempty"`
	SWVersion     string                 `json:"sw_version,omitempty"`
	Channels      []DeviceInfoChannelRow `json:"channels,omitempty"`
}

// DeviceConfig captures device-level configuration data.
type DeviceConfig struct {
	RxModes   []string `json:"rx_modes,omitempty"`
	Updatable bool     `json:"updatable,omitempty"`
}

// DeviceState carries the live device state.
type DeviceState struct {
	Available           bool   `json:"available"`
	Firmware            string `json:"firmware,omitempty"`
	AvailableFirmware   string `json:"available_firmware,omitempty"`
	FirmwareUpdateState string `json:"firmware_update_state,omitempty"`
}

// ChannelInfo is the identity payload for a single channel.
type ChannelInfo struct {
	Address        string   `json:"address"`
	ChannelNo      int      `json:"channel_no"`
	Type           string   `json:"type"`
	Name           string   `json:"name,omitempty"`
	Rooms          []string `json:"rooms,omitempty"`
	Functions      []string `json:"functions,omitempty"`
	Room           string   `json:"room,omitempty"`
	GroupNo        int      `json:"group_no,omitempty"`
	IsGroupMaster  bool     `json:"is_group_master,omitempty"`
	IsInMultiGroup bool     `json:"is_in_multi_group,omitempty"`
	SubDeviceName  string   `json:"sub_device_name,omitempty"`
}

// ChannelConfig captures channel-level configuration.
type ChannelConfig struct {
	OperationMode string `json:"operation_mode,omitempty"`
	ParamsetIn    string `json:"paramset_in,omitempty"`
}

// GenericDataPointInfo is the identity payload for any wire-level DP.
type GenericDataPointInfo struct {
	UniqueID    string `json:"unique_id"`
	Key         string `json:"key"`
	Parameter   string `json:"parameter"`
	ParamsetKey string `json:"paramset_key"`
	Address     string `json:"address"`
	Category    string `json:"category"`
	Kind        string `json:"kind"`
	HasEvents   bool   `json:"has_events"`
	IsReadable  bool   `json:"is_readable"`
	IsWritable  bool   `json:"is_writable"`
	Operations  int    `json:"operations"`
	Flags       int    `json:"flags"`
	Central     string `json:"central,omitempty"`
	DeviceModel string `json:"device_model,omitempty"`
}

// GenericDataPointConfig carries the wire-DP descriptor.
type GenericDataPointConfig struct {
	Usage          string   `json:"usage"`
	EnabledDefault bool     `json:"enabled_default"`
	Unit           string   `json:"unit,omitempty"`
	Default        string   `json:"default,omitempty"`
	Min            string   `json:"min,omitempty"`
	Max            string   `json:"max,omitempty"`
	Special        []byte   `json:"special,omitempty"`
	ValueList      []string `json:"value_list,omitempty"`
	// Multiplier is only emitted when the DP declares a non-trivial
	// scaling factor (m != 0 && m != 1.0).
	Multiplier float64 `json:"multiplier,omitempty"`
}
