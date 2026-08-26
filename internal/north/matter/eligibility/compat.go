// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package eligibility

import (
	"fmt"
	"sort"
)

// Ecosystem names the controller vendor a fabric belongs to. The
// distinction matters because the ecosystems disagree about which device
// types they support, and a device type one of them refuses is not an
// error the bridge can see — the entity simply never appears there.
// loom:reachable:reason="returned by EcosystemForVendor and carried on CompatFinding, both consumed by the daemon's matterCompatibilityReporter on each GET /api/v1/matter/compatibility; a string alias without methods, invisible to the analyzer's type heuristic"
type Ecosystem string

// Ecosystem values.
const (
	EcosystemApple         Ecosystem = "apple"
	EcosystemGoogle        Ecosystem = "google"
	EcosystemAmazon        Ecosystem = "amazon"
	EcosystemSmartThings   Ecosystem = "smartthings"
	EcosystemAqara         Ecosystem = "aqara"
	EcosystemHomeAssistant Ecosystem = "home_assistant"
	EcosystemUnknown       Ecosystem = "unknown"
)

// vendorEcosystems maps the CSA-assigned vendor id a controller writes
// into its fabric to the ecosystem it belongs to.
//
// The ids come from the CSA vendor registry; a fabric's VendorID is what
// the controller itself declared at commissioning, so this is the only
// reliable way to tell which ecosystem a given fabric represents.
//
// The ids are the ones the CSA Distributed Compliance Ledger carries.
// Two entries here were wrong until 0.60.0 and had been shipped: 0x1037
// is NXP Semiconductors rather than Aqara, and 0x125D is Tuya rather
// than Home Assistant. A wrong id costs twice — the real ecosystem's
// fabric falls through to unknown and is never warned about, and a
// fabric belonging to whoever owns the id is labelled as an ecosystem it
// has nothing to do with.
var vendorEcosystems = map[uint16]Ecosystem{
	0x1349: EcosystemApple, // Apple Home
	0x1384: EcosystemApple, // Apple Keychain — Apple adds a second, management fabric
	0x6006: EcosystemGoogle,
	0x1217: EcosystemAmazon,
	0x110A: EcosystemSmartThings,
	0x115F: EcosystemAqara,
	0x134B: EcosystemHomeAssistant,
}

// EcosystemForVendor classifies a fabric's vendor id.
func EcosystemForVendor(vendorID uint16) Ecosystem {
	if eco, ok := vendorEcosystems[vendorID]; ok {
		return eco
	}
	return EcosystemUnknown
}

// CompatFinding is one warning about how a commissioned ecosystem will
// treat what the bridge exposes.
// loom:reachable:reason="returned by Compat, which the daemon's matterCompatibilityReporter calls on each GET /api/v1/matter/compatibility; a data struct whose fields the REST layer copies out, invisible to the analyzer's method-based type heuristic"
type CompatFinding struct {
	Ecosystem Ecosystem
	Code      string
	Message   string
	// DeviceType is the Matter device type the finding concerns, zero
	// when it applies to the bridge as a whole.
	DeviceType uint16
}

// Device types whose ecosystem support is known to be uneven. Each entry
// below is field-verified rather than read off a compatibility matrix:
// the failure mode is always the same shape — the bridge exposes the
// endpoint correctly and the ecosystem silently omits it.
const (
	deviceTypeWaterValve        = 0x0042
	deviceTypeWaterLeakDetector = 0x0043
)

// amazonEndpointCeiling is the number of bridged endpoints beyond which
// Amazon's bridge support becomes unreliable. It is not a documented
// limit; it is where field reports put the wall.
const amazonEndpointCeiling = 90

// Compat reports what each commissioned ecosystem will do with the
// exposed topology that the bridge itself cannot observe.
//
// The whole class of problem here is invisible from this side: the
// endpoint is assembled, the cluster answers reads, the ecosystem
// commissions successfully — and the device is missing from the app, or
// the whole bridge goes unresponsive. Naming the combination is the only
// way an operator connects the two.
func Compat(fabricVendorIDs []uint16, deviceTypes map[uint16]int, endpointCount int) []CompatFinding {
	var out []CompatFinding

	ecosystems := make(map[Ecosystem]struct{}, len(fabricVendorIDs))
	for _, vid := range fabricVendorIDs {
		ecosystems[EcosystemForVendor(vid)] = struct{}{}
	}

	if _, ok := ecosystems[EcosystemGoogle]; ok && deviceTypes[deviceTypeWaterValve] > 0 {
		out = append(out, CompatFinding{
			Ecosystem:  EcosystemGoogle,
			Code:       "device_type_unsupported",
			DeviceType: deviceTypeWaterValve,
			Message: fmt.Sprintf("%d valve endpoint(s) are exposed. Google Home does not support the "+
				"water-valve device type and will not show them, while Apple and Aqara will.",
				deviceTypes[deviceTypeWaterValve]),
		})
	}
	if _, ok := ecosystems[EcosystemAmazon]; ok {
		if deviceTypes[deviceTypeWaterValve] > 0 {
			out = append(out, CompatFinding{
				Ecosystem:  EcosystemAmazon,
				Code:       "device_type_unsupported",
				DeviceType: deviceTypeWaterValve,
				Message: fmt.Sprintf("%d valve endpoint(s) are exposed. Alexa does not support the "+
					"water-valve device type and will not show them.", deviceTypes[deviceTypeWaterValve]),
			})
		}
		if deviceTypes[deviceTypeWaterLeakDetector] > 0 {
			out = append(out, CompatFinding{
				Ecosystem:  EcosystemAmazon,
				Code:       "device_type_breaks_bridge",
				DeviceType: deviceTypeWaterLeakDetector,
				Message: "A water-leak-detector endpoint is exposed. Alexa becomes unresponsive for the " +
					"entire bridge when it encounters this device type — not just for the endpoint " +
					"itself, so every other device disappears with it.",
			})
		}
		if endpointCount > amazonEndpointCeiling {
			out = append(out, CompatFinding{
				Ecosystem: EcosystemAmazon,
				Code:      "endpoint_count_near_ceiling",
				Message: fmt.Sprintf("%d endpoints are exposed. Alexa becomes unreliable with bridges "+
					"beyond roughly %d and may drop devices without reporting an error; narrowing the "+
					"exposure allowlist is the remedy.", endpointCount, amazonEndpointCeiling),
			})
		}
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Ecosystem != out[j].Ecosystem {
			return out[i].Ecosystem < out[j].Ecosystem
		}
		return out[i].Code < out[j].Code
	})
	return out
}
