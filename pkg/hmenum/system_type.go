// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hmenum

import "strings"

// SystemType names the kind of Homematic system a central talks to. It
// selects the central's south profile: where readiness, events, metadata and
// system management come from. The wire value is what an operator writes in
// the central's `system_type` config key.
type SystemType string

// SystemType values.
const (
	// SystemTypeAuto asks the daemon to detect the system at bring-up and
	// persist what it found.
	SystemTypeAuto SystemType = "auto"
	// SystemTypeCCU is a CCU with ReGaHss and the WebUI JSON-RPC (eQ-3
	// CCU2/CCU3, OpenCCU, RaspberryMatic). The empty value means this type,
	// so a configuration written before the key existed keeps its meaning.
	SystemTypeCCU SystemType = "ccu"
	// SystemTypeOpenCCULite is an openccu-lite system: no ReGaHss, no
	// JSON-RPC; everything goes through the occulited HTTP API.
	SystemTypeOpenCCULite SystemType = "openccu-lite"
)

// String returns the wire representation.
func (s SystemType) String() string { return string(s) }

// Normalize folds case and surrounding whitespace and maps the empty value to
// [SystemTypeCCU]. An unknown value is returned unchanged (trimmed and
// lower-cased) so a validator can name it; [SystemType.Valid] tells it apart.
func (s SystemType) Normalize() SystemType {
	n := SystemType(strings.ToLower(strings.TrimSpace(string(s))))
	if n == "" {
		return SystemTypeCCU
	}
	return n
}

// Valid reports whether the normalized value is one of the defined types.
func (s SystemType) Valid() bool {
	switch s.Normalize() {
	case SystemTypeAuto, SystemTypeCCU, SystemTypeOpenCCULite:
		return true
	default:
		return false
	}
}
