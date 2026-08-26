// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Package group models Homematic heating groups (HmIP / BidCos
// "Heizungsgruppen") for the read-only group surface.
//
// A heating group is a virtual device on the VirtualDevices interface
// whose members are wired together through a type-specific direct-link
// matrix. The CCU keeps the member roster and group metadata in
// /etc/config/groups.gson; the JSON-RPC method CCU.getHeatingGroupList
// returns that file verbatim (as a JSON string). This package parses
// that payload into a typed, transport-independent view. Mutating a
// group runs through the CCU's HMServer jpages endpoints, not this
// package — see docs/adr/0055-groups-jpages-proxy.md.
package group

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Group is one heating group as it appears in groups.gson.
type Group struct {
	// ID is the numeric group id the CCU assigns.
	ID int
	// Name is the operator-facing group name (groupProperties.NAME).
	Name string
	// GroupDeviceName is the label of the backing virtual device
	// (groupProperties.GROUP_DEVICE_NAME); often empty.
	GroupDeviceName string
	// ForbidSingleOperation reports the "operate only via group" flag
	// (groupProperties.FORBID_SINGLE_OPERATION).
	ForbidSingleOperation bool
	// TypeID is the group-type key (groupType.id), e.g. the HmIP
	// heating-group type identifier.
	TypeID string
	// TypeLabel is the CCU-provided, translatable label for the type
	// (groupType.label). It may be a translation key rather than a
	// resolved string.
	TypeLabel string
	// Members are the devices/channels wired into the group.
	Members []Member
}

// Member is one device or channel belonging to a group.
type Member struct {
	// Address is the member's device or channel address
	// (member.id, e.g. "000ABC0123456789:1").
	Address string
	// TypeID is the member-type key (member.memberType.id).
	TypeID string

	// The fields below are resolved from the live device model by the adapter
	// (they are not part of the groups.gson wire shape) so the overview can show
	// each member by name instead of its bare address. Empty when the member is
	// not (yet) in the model.

	// DeviceName is the CCU-assigned name of the member's parent device.
	DeviceName string
	// DeviceModel is the parent device model (e.g. "HmIP-STHD").
	DeviceModel string
	// ChannelName is the CCU-assigned channel name, when the member is a channel.
	ChannelName string
	// Rooms are the member's assigned rooms (channel's, falling back to device).
	Rooms []string
}

// wire mirrors the groups.gson serialization the CCU emits from its
// getHeatingGroupList method (Gson, default field names).
type wire struct {
	Groups []wireGroup `json:"groups"`
}

type wireGroup struct {
	ID        int      `json:"id"`
	GroupType wireType `json:"groupType"`
	// GroupProperties values are decoded lazily: a real CCU returns
	// FORBID_SINGLE_OPERATION as a JSON boolean while NAME / GROUP_DEVICE_NAME
	// are strings, so a fixed map[string]string would fail the whole unmarshal.
	// json.RawMessage keeps each value verbatim; propString / propBool coerce.
	GroupProperties map[string]json.RawMessage `json:"groupProperties"`
	GroupMembers    []wireMember               `json:"groupMembers"`
}

// propString returns a group property as a string, tolerating a JSON string
// ("foo") or any scalar (bool / number) rendered as its literal text.
func propString(m map[string]json.RawMessage, key string) string {
	raw, ok := m[key]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return strings.TrimSpace(string(raw))
}

// propBool returns a group property as a bool, tolerating a JSON boolean
// (true/false — the real CCU format) or the string "true"/"false".
func propBool(m map[string]json.RawMessage, key string) bool {
	raw, ok := m[key]
	if !ok {
		return false
	}
	var b bool
	if err := json.Unmarshal(raw, &b); err == nil {
		return b
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.EqualFold(s, "true")
	}
	return false
}

type wireType struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type wireMember struct {
	ID         string   `json:"id"`
	MemberType wireType `json:"memberType"`
}

// groups.gson property-map keys, as they appear verbatim in the
// getHeatingGroupList payload.
const (
	propName                  = "NAME"
	propGroupDeviceName       = "GROUP_DEVICE_NAME"
	propForbidSingleOperation = "FORBID_SINGLE_OPERATION"
)

// ParseGroupList decodes the payload CCU.getHeatingGroupList returns.
// That method reads /etc/config/groups.gson and hands it back as a JSON
// string, so `raw` is the file's JSON text; a missing file yields the
// sentinel "-1". Both the sentinel and an empty payload decode to an
// empty (non-nil) slice, so callers never special-case "no groups yet".
func ParseGroupList(raw string) ([]Group, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "-1" {
		return []Group{}, nil
	}
	var w wire
	if err := json.Unmarshal([]byte(trimmed), &w); err != nil {
		return nil, fmt.Errorf("parse groups.gson: %w", err)
	}
	out := make([]Group, 0, len(w.Groups))
	for _, wg := range w.Groups {
		g := Group{
			ID:                    wg.ID,
			Name:                  propString(wg.GroupProperties, propName),
			GroupDeviceName:       propString(wg.GroupProperties, propGroupDeviceName),
			ForbidSingleOperation: propBool(wg.GroupProperties, propForbidSingleOperation),
			TypeID:                wg.GroupType.ID,
			TypeLabel:             wg.GroupType.Label,
			Members:               make([]Member, 0, len(wg.GroupMembers)),
		}
		for _, wm := range wg.GroupMembers {
			g.Members = append(g.Members, Member{
				Address: wm.ID,
				TypeID:  wm.MemberType.ID,
			})
		}
		out = append(out, g)
	}
	return out, nil
}
