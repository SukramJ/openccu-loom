// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmproto

// LinkDescription is one entry returned by the CCU's `getLinks`
// XML-RPC call. A link is a direct sender→receiver association
// between two channels (e.g. a button linked to a switch); each side
// maintains its own LINK paramset that parameterises the triggered
// behaviour.
//
// The canonical upstream field shape is:
//
//	SENDER      — sender channel address
//	RECEIVER    — receiver channel address
//	NAME        — display name of the link
//	DESCRIPTION — free-form description
//	FLAGS       — CCU-internal flags (bitmask; typically 0)
//
// Mirrors the contents of `DeviceLink`.
// `central/coordinators/link.py`, minus the frontend-oriented
// enrichment (device/channel names, direction, translations) which
// the REST adapter composes on top.
type LinkDescription struct {
	Sender      string `json:"SENDER"`
	Receiver    string `json:"RECEIVER"`
	Name        string `json:"NAME,omitempty"`
	Description string `json:"DESCRIPTION,omitempty"`
	Flags       int    `json:"FLAGS,omitempty"`
}
