// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmapi

// --- Areas ---
//
// Wire DTOs for the /api/v1/areas REST surface. Areas are
// operator-defined room groupings above the CCU's flat, per-central
// room list (a floor, a shed, a terrace roof) — distinct from alarm
// zones (notes/concepts/alarm-concept.md §14), which are independently armable
// partitions.

// Area is one operator-defined room grouping.
type Area struct {
	// ID is server-generated on create; ignored in the create body.
	ID       string        `json:"id"`
	Name     string        `json:"name"`
	Position int           `json:"position,omitempty"`
	Rooms    []AreaRoomRef `json:"rooms,omitempty"`
}

// AreaRoomRef is one CCU room assignment of an area.
type AreaRoomRef struct {
	Central string `json:"central"`
	Room    string `json:"room"`
}
