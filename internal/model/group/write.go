// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package group

// Write-side domain types for heating-group administration. The mutation
// itself runs through the CCU's HMServer jpages endpoints (see
// docs/adr/0055-groups-jpages-proxy.md); these types are the transport-
// independent shapes the domain exchanges with the REST/WS layer.

// Type is one group type a new group can be created as (e.g. the HmIP
// heating-group type). LabelKey is the CCU translation key for the type's
// label, not a resolved string.
type Type struct {
	ID       string
	LabelKey string
}

// MemberCandidate is one device/channel that can be assigned to a group of a
// given type.
type MemberCandidate struct {
	// Address is the member channel/device address (e.g. "000ABC…:1").
	Address string
	// Serial is the CCU-reported serial number for the member.
	Serial string
	// Type is the member kind (e.g. "SENSOR_WINDOW", "SWITCH_ACTUATOR").
	Type string
}

// SuitableMembers splits the devices for a group type into the ones that can
// be assigned now and the leftover ones that do not fit.
type SuitableMembers struct {
	Assignable []MemberCandidate
	Leftover   []MemberCandidate
}

// CreateInput describes a new group to create.
type CreateInput struct {
	TypeID                string
	Name                  string
	ForbidSingleOperation bool
	// MemberIDs are the member channel/device addresses to assign.
	MemberIDs []string
}

// UpdateInput describes the desired state of an existing group. TypeID is
// carried through unchanged (a group's type is immutable after creation).
type UpdateInput struct {
	TypeID                string
	Name                  string
	ForbidSingleOperation bool
	MemberIDs             []string
}
