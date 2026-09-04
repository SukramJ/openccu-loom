// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package generic

import (
	"strings"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// switchDPToSensor names the (device-model prefix, wire parameter) pairs
// whose descriptor classification has to be overridden: the parameter is a
// derived, read-only surface even though the CCU advertises it writable.
//
// The premise is exact, and it is the descriptor that is misleading rather
// than us. The eTRV climate channel declares LEVEL without a subtype, which
// binds the read/write/event factory, so the legacy descriptor really does
// carry OPERATIONS = READ|WRITE|EVENT = 7 (HMIPServer
// de.eq3.cbcs.devicedescription.channelspecification.ChannelTypeReader#getParameterMapKeyFromNode,
// …stateparameter.GeneralStateParameterFactory#createLevel,
// de.eq3.cbcs.legacy.bidcos.rpc.internal.DeviceUtil#addDesriptionOfParameter).
// The read/write direction is settled by the CCU's own operator surface
// instead: heating_control.fn renders LEVEL as a bare `<span>…%</span>`
// percentage readout and never writes it
// (../OpenCCU-Base/www/rega/esp/controls/heating_control.fn:363-372), while
// the writable controls on that page are the setpoint and the active
// profile. The parameter is not disabled — it carries
// `HEATING_CLIMATECONTROL_TRANSCEIVER.LEVEL.Control=HEATING_CONTROL_HMIP.LEVEL`
// (../OpenCCU-Base/opt/HmIP/legacy-parameter-definition.config:410) — it is
// simply never written by the CCU itself.
//
// UNVERIFIED: whether the valve rejects a LEVEL write. The legacy layer
// imposes no LEVEL guard on this channel type and forwards an unrecognised
// write to the HmIP backend, so the outcome is valve firmware no readable
// source states. The demotion rests on the CCU's operator surface, not on a
// proven rejection.
//
// Scope. Only radiator-thermostat channel-type versions declare LEVEL at
// all — the wall-thermostat versions declare none — so the rule is not too
// wide; and "HmIP-HEATING" is verbatim the heating group's virtual device
// type (HMIPServer
// de.eq3.ccu.groupdevice.hmip.service.internal.HmIPGroupDefinitionProvider),
// whose thermostat channel merges the TRV channel-1 state description and
// therefore inherits LEVEL. Whether the family reaches wider than the
// "HmIP-eTRV" prefix is UNVERIFIED: the CCU's paramset-id mapping lists an
// OEM label ("Thermostat AA") against the eTRV paramset, but that mapping
// keys label lookup, not the channel type, so it does not establish that
// such a device has a LEVEL to demote. What would settle it is the
// device-label → channel-type-version binding, which no readable CCU source
// carries.
//
// Key is the device-model prefix (case-insensitive); value is the wire
// parameter that is forced to sensor.
var switchDPToSensor = map[string]hmenum.Parameter{
	"HmIP-eTRV":    hmenum.ParameterLevel,
	"HmIP-HEATING": hmenum.ParameterLevel,
}

// IsForceSensorParameter reports whether the (model, parameter)
// pair must be classified as a read-only sensor regardless of the
// CCU's operations descriptor.
//
// The model match is a case-insensitive prefix match, and the
// case-insensitivity is load-bearing rather than defensive: the CCU's own
// eTRV-family list ships both spellings on adjacent lines —
// `HMIP-eTRV-2` and `HmIP-eTRV-2`
// (../OpenCCU-Base/opt/HmIP/legacy-parameter-definition.config:12-13).
//
// Used by:
//
//   - MQTT discovery: the per-parameter component classifier
//     overrides Switch / Number with Sensor.
//   - REST / WS adapters: surfaces marked here are rendered
//     non-writable.
func IsForceSensorParameter(model string, parameter hmenum.Parameter) bool {
	if model == "" {
		return false
	}
	lowered := strings.ToLower(model)
	for prefix, param := range switchDPToSensor {
		if param != parameter {
			continue
		}
		if strings.HasPrefix(lowered, strings.ToLower(prefix)) {
			return true
		}
	}
	return false
}
