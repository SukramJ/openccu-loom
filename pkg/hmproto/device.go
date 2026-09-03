// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hmproto

import (
	"encoding/json"
	"strings"

	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// LinkRoles holds the role-name tokens carried in CCU's LINK_SOURCE_ROLES /
// LINK_TARGET_ROLES fields. The CCU emits these as a single space-separated
// string (e.g. `""`, `"CONDITIONAL_SWITCH"`, or `"KEYMATIC SWITCH"`); some
// snapshot exports emit a JSON array instead. Decoding tolerates both shapes;
// encoding always produces the array form.
type LinkRoles []string

// UnmarshalJSON decodes either a JSON string (space-separated tokens)
// or a JSON array of strings into the LinkRoles slice. An empty
// string and an empty array both yield a nil slice.
func (r *LinkRoles) UnmarshalJSON(data []byte) error {
	if len(data) == 0 || string(data) == "null" {
		*r = nil
		return nil
	}
	if data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		s = strings.TrimSpace(s)
		if s == "" {
			*r = nil
			return nil
		}
		fields := strings.Fields(s)
		out := make([]string, 0, len(fields))
		for _, f := range fields {
			if f != "" {
				out = append(out, f)
			}
		}
		*r = out
		return nil
	}
	var arr []string
	if err := json.Unmarshal(data, &arr); err != nil {
		return err
	}
	*r = arr
	return nil
}

// MarshalJSON renders the roles as a JSON array of strings so the
// snapshot/round-trip stays canonical regardless of input shape.
func (r LinkRoles) MarshalJSON() ([]byte, error) {
	if r == nil {
		return []byte("null"), nil
	}
	return json.Marshal([]string(r))
}

// DeviceDescription is the CCU's listDevices payload entry, normalised
// to Go types. CCU field names are uppercase; JSON tags preserve them
// for round-trip parity.
type DeviceDescription struct {
	Address   string   `json:"ADDRESS"`
	Type      string   `json:"TYPE,omitempty"`
	RFAddress *int     `json:"RF_ADDRESS,omitempty"`
	Children  []string `json:"CHILDREN,omitempty"`
	Parent    string   `json:"PARENT,omitempty"`
	Index     *int     `json:"INDEX,omitempty"`

	// AESKey is 0/1 on the wire; *int distinguishes "absent" from "zero".
	AESKey *int `json:"AES_KEY,omitempty"`

	Firmware string `json:"FIRMWARE,omitempty"`
	Roaming  *bool  `json:"ROAMING,omitempty"`
	RXMode   int    `json:"RX_MODE,omitempty"`

	Paramsets []string `json:"PARAMSETS,omitempty"`
	Flags     int      `json:"FLAGS,omitempty"`

	Group   string `json:"GROUP,omitempty"`
	Team    string `json:"TEAM,omitempty"`
	TeamTag string `json:"TEAM_TAG,omitempty"`
	Serial  string `json:"SERIAL,omitempty"`

	// Interface is the interface ID the CCU reports. Some older CCU
	// versions omit the field; our normaliser sets it to the dispatching
	// interface when reading descriptions fresh.
	Interface string `json:"INTERFACE,omitempty"`

	// Version is a per-device schema version bumped by the CCU when
	// paramsets change; missing for older firmwares.
	Version *int `json:"VERSION,omitempty"`

	// DirectLinkDeactivated is device-specific; preserved verbatim.
	DirectLinkDeactivated *bool `json:"DIRECT_LINK_DEACTIVATED,omitempty"`

	// Subtype is the CCU's SUBTYPE field (e.g. "DIMMER", "SWITCH"). Present on
	// some device families.
	Subtype string `json:"SUBTYPE,omitempty"`

	// ParentType is the CCU's PARENT_TYPE field — the TYPE of the parent device
	// when this description refers to a channel.
	ParentType string `json:"PARENT_TYPE,omitempty"`

	// Direction is the CCU's DIRECTION field (0 = none, 1 = sender, 2 =
	// receiver). Used for LINK paramset routing.
	Direction *int `json:"DIRECTION,omitempty"`

	// AvailableFirmware, Firmware-related feedback fields — optional.
	AvailableFirmware string `json:"AVAILABLE_FIRMWARE,omitempty"`
	FirmwareUpdatable *bool  `json:"UPDATABLE,omitempty"`
	FirmwareState     string `json:"FIRMWARE_STATE,omitempty"`
	UpdateState       string `json:"UPDATE_STATE,omitempty"`
	// FirmwareUpdateState is the HmIP firmware-update lifecycle phase
	// (UP_TO_DATE / NEW_FIRMWARE_AVAILABLE / READY_FOR_UPDATE / …). This is
	// the field that gates whether an update is installable — distinct from
	// FIRMWARE_STATE (firmware health) and UPDATE_STATE.
	FirmwareUpdateState string `json:"FIRMWARE_UPDATE_STATE,omitempty"`

	// LinkSourceRoles describes the link-source role strings for this channel.
	// Used for LINK paramset routing.
	LinkSourceRoles LinkRoles `json:"LINK_SOURCE_ROLES,omitempty"`

	// LinkTargetRoles describes the link-target role strings.
	LinkTargetRoles LinkRoles `json:"LINK_TARGET_ROLES,omitempty"`

	// TeamChannels is the list of channel addresses that are part
	// of a team.
	TeamChannels []string `json:"TEAM_CHANNELS,omitempty"`

	// Extra captures any field we do not model explicitly. The
	// normaliser copies keys we know about out of Extra and into their
	// typed slots; anything unknown stays here and is preserved on the
	// wire round-trip.
	Extra map[string]json.RawMessage `json:"-"`
}

// IsDevice reports whether the description refers to a full device
// (i.e. it has no PARENT). Channels are children of devices.
func (d *DeviceDescription) IsDevice() bool { return d.Parent == "" }

// IsChannel reports whether the description is for a channel.
func (d *DeviceDescription) IsChannel() bool { return d.Parent != "" }

// ChannelNo returns the channel number derived from the ADDRESS field (e.g.
// "VCU1234567:3" → 3). Returns -1 when the address has no colon suffix or
// parsing fails.
//
// Delegates to [hmtypes.ChannelNo] so the address grammar — which colon
// separates the device part, and what counts as a numeric suffix — is
// decided in one place. That helper takes the first separator and accepts
// what strconv.Atoi accepts, so an address carrying two colons has no
// channel number here rather than the last segment's.
func (d *DeviceDescription) ChannelNo() int {
	if n, ok := hmtypes.ChannelNo(d.Address); ok {
		return n
	}
	return -1
}
