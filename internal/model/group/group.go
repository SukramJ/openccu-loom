// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

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
}

// wire mirrors the groups.gson serialization emitted by HMServer's
// de.eq3.lib.groupadministration model (Gson, default field names).
type wire struct {
	Groups []wireGroup `json:"groups"`
}

type wireGroup struct {
	ID              int               `json:"id"`
	GroupType       wireType          `json:"groupType"`
	GroupProperties map[string]string `json:"groupProperties"`
	GroupMembers    []wireMember      `json:"groupMembers"`
}

type wireType struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type wireMember struct {
	ID         string   `json:"id"`
	MemberType wireType `json:"memberType"`
}

// groups.gson property keys (the GROUP_PROPERTIE_* constants in
// de.eq3.lib.groupadministration.Group).
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
			Name:                  wg.GroupProperties[propName],
			GroupDeviceName:       wg.GroupProperties[propGroupDeviceName],
			ForbidSingleOperation: strings.EqualFold(wg.GroupProperties[propForbidSingleOperation], "true"),
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
