// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package wire

import (
	"errors"

	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// Thermostat command IDs per Matter §4.3.9.
const (
	ThermostatCmdSetpointRaiseLower  uint32 = 0x00
	ThermostatCmdSetWeeklySchedule   uint32 = 0x01
	ThermostatCmdGetWeeklySchedule   uint32 = 0x02
	ThermostatCmdClearWeeklySchedule uint32 = 0x03
)

// SetpointRaiseLower mode values per Matter §4.3.9.1.
const (
	ThermostatSetpointModeHeat uint8 = 0
	ThermostatSetpointModeCool uint8 = 1
	ThermostatSetpointModeBoth uint8 = 2
)

// ErrThermostatMalformed is returned for malformed Thermostat command
// payloads.
var ErrThermostatMalformed = errors.New("wire: Thermostat command malformed")

// SetpointRaiseLowerRequest mirrors Matter §4.3.9.1.
type SetpointRaiseLowerRequest struct {
	Mode   uint8 // 0 Heat / 1 Cool / 2 Both
	Amount int8  // tenths of °C, signed
}

// DecodeSetpointRaiseLower parses SetpointRaiseLower payloads.
func DecodeSetpointRaiseLower(payload []byte) (SetpointRaiseLowerRequest, error) {
	var req SetpointRaiseLowerRequest
	if err := walkContext(payload, ErrThermostatMalformed, func(tag uint32, el tlv.Element) {
		switch tag {
		case 0:
			req.Mode = uint8(el.Uint & 0xFF) // 8-bit enum per spec
		case 1:
			req.Amount = int8(el.Int) //nolint:gosec // 8-bit signed per spec; see #20
		}
	}); err != nil {
		return req, err
	}
	return req, nil
}
