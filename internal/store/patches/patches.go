// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package patches

import (
	"encoding/json"
	"log/slog"
	"strconv"
	"strings"
	"sync"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// Patch is one idempotent modification applied to a parameter
// descriptor. The [Apply] function mutates the data in place and
// returns true when it actually changed something.
//
// ChannelNo (optional) and Reason fields added to mirror the Python
// reference implementation's channel_no + reason fields.
type Patch struct {
	Model     string // "" = any model
	Parameter hmenum.Parameter
	Paramset  hmenum.ParamsetKey // "" = any paramset
	// ChannelNo restricts the patch to a specific channel number.
	// nil matches all channels (Python: channel_no = None).
	ChannelNo *int
	// Reason is an optional human-readable justification for the patch.
	Reason string
	// Ticket is an optional reference to the upstream issue or PR that motivated
	// the patch (e.g. For audit purposes only; not used at runtime.
	Ticket string
	Apply  func(pd *hmproto.ParameterData) bool
}

// Registry stores the list of active patches.
type Registry struct {
	mu      sync.RWMutex
	patches []Patch
}

// NewRegistry returns a registry pre-populated with the built-ins.
func NewRegistry() *Registry {
	r := &Registry{}
	r.patches = append(r.patches, builtIns()...)
	return r
}

// Register appends a patch.
func (r *Registry) Register(p Patch) {
	r.mu.Lock()
	r.patches = append(r.patches, p)
	r.mu.Unlock()
}

// ApplyTo runs every matching patch against pd for the given channel address.
// The channelAddress is used to extract the channel number for ChannelNo-scoped patches.
// Returns the number of patches that actually modified the descriptor.
//
// channel-no matching added; device_type pre-filter by Model.
func (r *Registry) ApplyTo(model string, paramset hmenum.ParamsetKey, parameter hmenum.Parameter, pd *hmproto.ParameterData) int {
	return r.applyToWithChannel(model, paramset, parameter, pd, -1)
}

// applyToWithChannel is the internal implementation that accepts a channel
// number. channelNo == -1 means "no channel / don't filter by channel".
//
// first-match (most-specific-first) semantics. Exact match: channel_no +
// paramset + parameter 2. Any channel: nil channel_no + paramset + parameter
// 3. Any paramset: channel_no + nil paramset + parameter 4. Any channel &
// paramset: nil + nil + parameter
//
// Each probe returns the first matching patch in registration order; once a
// probe tier finds a match no lower tier is consulted.
func (r *Registry) applyToWithChannel(model string, paramset hmenum.ParamsetKey, parameter hmenum.Parameter, pd *hmproto.ParameterData, channelNo int) int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if pd == nil {
		return 0
	}

	// Resolve whether channelNo is known (>= 0).
	chKnown := channelNo >= 0

	// Find first matching patch across the four priority tiers.
	// Tier matching: most-specific first (channel+paramset → no-channel+paramset
	// → channel+no-paramset → no-channel+no-paramset).
	// A patch with Model != "" must also match the model.
	type tierKey struct {
		hasChannel  bool
		hasParamset bool
	}
	tiers := []tierKey{
		{true, true},   // exact: channel + paramset
		{false, true},  // any channel, exact paramset
		{true, false},  // exact channel, any paramset
		{false, false}, // any channel, any paramset
	}

	for _, tier := range tiers {
		for _, p := range r.patches {
			// Model filter.
			if p.Model != "" && !strings.EqualFold(p.Model, model) {
				continue
			}
			// Parameter filter.
			if p.Parameter != "" && p.Parameter != parameter {
				continue
			}
			// Tier: channel dimension.
			if tier.hasChannel {
				// This tier requires an exact channel match.
				if !chKnown || p.ChannelNo == nil || *p.ChannelNo != channelNo {
					continue
				}
			} else {
				// This tier requires no channel restriction (nil ChannelNo).
				if p.ChannelNo != nil {
					continue
				}
			}
			// Tier: paramset dimension.
			if tier.hasParamset {
				// This tier requires an exact paramset match.
				if p.Paramset == "" || p.Paramset != paramset {
					continue
				}
			} else {
				// This tier requires no paramset restriction (empty Paramset).
				if p.Paramset != "" {
					continue
				}
			}
			// Found the most-specific match for this parameter: apply and return.
			if p.Apply != nil && p.Apply(pd) {
				if p.Reason != "" {
					slog.Debug(
						"paramset patch applied",
						"model", model,
						"paramset", paramset,
						"parameter", parameter,
						"reason", p.Reason,
					)
				}
				return 1
			}
			// Patch matched but did not change anything — still counts as
			// "found a match", so we do not fall through to less-specific tiers.
			return 0
		}
	}
	return 0
}

// ApplyParamset applies all matching patches to every parameter in ps for the
// given channel address. This is the ingestion-time entry point called by
// ParamsetRegistry.Add. Returns the total number of field changes across all
// parameters.
//
// _address_parameter_cache pre-filtering by device_type is handled here via
// the Model field on each Patch.
func (r *Registry) ApplyParamset(model, channelAddress string, paramset hmenum.ParamsetKey, ps hmproto.Paramset) int {
	if len(ps) == 0 {
		return 0
	}
	// Pre-check: does this model have any patches at all?
	r.mu.RLock()
	hasAny := false
	for _, p := range r.patches {
		if p.Model == "" || strings.EqualFold(p.Model, model) {
			hasAny = true
			break
		}
	}
	r.mu.RUnlock()
	if !hasAny {
		return 0
	}

	_, channelNo, _ := hmtypes.SplitChannelAddress(channelAddress)
	total := 0
	for param := range ps {
		pd := ps[param]
		n := r.applyToWithChannel(model, paramset, hmenum.Parameter(param), &pd, channelNo)
		if n > 0 {
			ps[param] = pd
			total += n
		}
	}
	return total
}

// Len reports the patch count.
func (r *Registry) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.patches)
}

// HasPatches reports whether any patches are registered.
func (r *Registry) HasPatches() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.patches) > 0
}

// powerMeterSwitchModels lists every device id that shares one
// ENERGY_COUNTER declaration. They are the nine ids in the
// <supported_types> block of
// ../OpenCCU-Base/src/devicetypes/rftypes/rf_es_pmsw.xml (:9-40); the file
// declares ENERGY_COUNTER once (:194-203) and every id inherits it, so a
// patch keyed on one id would repair one device out of nine.
var powerMeterSwitchModels = []string{
	"HM-ES-PMSw1-Pl",
	"HM-ES-PMSw1-Pl-DN-R1",
	"HM-ES-PMSw1-Pl-DN-R2",
	"HM-ES-PMSw1-Pl-DN-R3",
	"HM-ES-PMSw1-Pl-DN-R4",
	"HM-ES-PMSw1-Pl-DN-R5",
	"HM-ES-PMSw1-DR",
	"HM-ES-PMSw1-SM",
	"HM-ES-PMSwX",
}

// Virtual-heating-group setpoint bounds, as the CCU declares them.
//
// ../OpenCCU-Base/opt/HMServer/groups/groupdefinitions.xml:504 carries
// `<status_parameter … control="HEATING_CONTROL.SETPOINT" default="20.0"
// max="30.0" min="5.0" name="SET_TEMPERATURE" operations="7" type="FLOAT"
// unit="°C">` inside `<channel id="1" type="CLIMATECONTROL_RT_TRANSCEIVER">`
// of the file's single group, whose `<virtual_device_type>` is HM-CC-VG-1
// (:8113).
//
// The group is deliberately narrower than the devices it aggregates:
// HM-CC-RT-DN's own SET_TEMPERATURE is min="4.5" max="30.5"
// (../OpenCCU-Base/src/devicetypes/rftypes/rf_cc_rt_dn.xml:2395). Writing
// the member device's range onto the group widens the accepted setpoint by
// half a kelvin at each end.
var (
	heatingGroupSetpointMin = json.RawMessage(`5.0`)
	heatingGroupSetpointMax = json.RawMessage(`30.0`)
)

// codeIDWireCeiling is the largest value the CODE_ID wire field can carry.
// The HmIP server decodes the CODE_STATUS byte as CODE_STATE =
// (valueData[0] & 0xFF) >> 5 and CODE_ID = valueData[0] & 0x1F
// (HMIPServer de.eq3.cbcs.server.core.framehandling.HMIPApplicationHandler),
// so the low five bits span 0..31 whatever the descriptor declares.
const codeIDWireCeiling = 31

// builtIns returns the factory-shipped patches.
//
// There is deliberately no patch for HmIP-RGBW SATURATION. The HmIP server
// registers SATURATION exactly once, with read|write|event, and the legacy
// XML-RPC path copies that OPERATIONS byte into the parameter description
// verbatim; the single override file that can rewrite a description sets
// only TabOrder, Unit, Control and Disabled, and a disabled parameter is
// dropped from the description rather than served with reduced operations.
// The two shapes a current CCU can produce for SATURATION are therefore
// "present with OPERATIONS=7" and "absent" — restoring an EVENT bit that
// is never missing would only mask a descriptor read that raced device
// bring-up.
func builtIns() []Patch {
	ch0 := 0
	ch1 := 1
	out := make([]Patch, 0, len(powerMeterSwitchModels)+2)

	// ENERGY_COUNTER carries unit="Wh" in the shipped device XML
	// (../OpenCCU-Base/src/devicetypes/rftypes/rf_es_pmsw.xml:196), and the
	// 10x factor lives in the conversion, so the logical value delivered
	// over XML-RPC is already in Wh. The unit is a per-parameter fact, not
	// a device-wide one: the sibling counters on the same channel declare
	// W, mA, V and Hz, and the same channel type on HM-ES-TX-WM carries
	// Wh, kWh and m3 counters side by side.
	//
	// The trigger condition is a defensive guard, and its premise is
	// unverified for the shipped stack: HSSLogicalType::GetDescription
	// (../OpenCCU-Base/src/libhsscomm/HSSLogicalType.cpp:56) writes UNIT
	// into every parameter description unconditionally, and it is the only
	// UNIT assignment in that library, so a current rfd cannot serve this
	// parameter without a unit. The guard remains for fronts that present
	// the device themselves rather than through rfd; it is a no-op
	// whenever a UNIT arrives.
	for _, model := range powerMeterSwitchModels {
		out = append(out, Patch{
			Model:     model,
			Parameter: hmenum.Parameter("ENERGY_COUNTER"),
			Paramset:  hmenum.ParamsetKeyValues,
			Reason:    "ENERGY_COUNTER is declared unit=Wh; supply it when the descriptor carries none",
			Apply: func(pd *hmproto.ParameterData) bool {
				if pd.Unit == "" {
					pd.Unit = "Wh"
					return true
				}
				return false
			},
		})
	}

	// HM-CC-VG-1 virtual heating group, channel 1 SET_TEMPERATURE.
	//
	// What this patch repairs is the *type*, not the range. HMServer
	// de.eq3.ccu.groupdevice.service.GroupDeviceHandler#createParamsetDescriptions
	// copies the group parameter's min/max straight into the RpcStruct, and
	// both accessors are String-typed on HMServer
	// de.eq3.ccu.virtualdevice.service.internal.bidcos.BidCosVirtualChannelParameter
	// — so MIN and MAX reach us as XML-RPC strings ("5.0") rather than
	// doubles, and every consumer that expects a JSON number sees a string.
	//
	// Three rules, in order:
	//
	//   - a bound that already arrives as a JSON number stays untouched.
	//     The CCU is the authority on its own bounds; widening or
	//     narrowing them here would substitute our opinion for its answer.
	//   - a bound that arrives as a quoted number is coerced to that same
	//     number. The value the CCU sent is preserved.
	//   - a missing or unusable range falls back to the group definition
	//     above. "Unusable" covers the 0/0 range the original patch was
	//     written for: a setpoint whose MIN equals its MAX constrains
	//     nothing. That 0/0 observation is itself unverified — no path in
	//     the group definition or in the description-building code
	//     produces it — so the fallback value is read from the firmware
	//     rather than chosen.
	//nolint:gocritic // appendCombine: each patch carries its own multi-paragraph
	// derivation above it; folding the two calls into one argument list would
	// put those blocks inside an expression and separate each from its patch.
	out = append(out, Patch{
		Model:     "HM-CC-VG-1",
		Parameter: hmenum.ParameterSetTemperature,
		Paramset:  hmenum.ParamsetKeyValues,
		ChannelNo: &ch1,
		Reason:    "CCU serves virtual-group SET_TEMPERATURE bounds as strings",
		Apply: func(pd *hmproto.ParameterData) bool {
			minValue, minRewrite, minOK := numericBound(pd.Min)
			maxValue, maxRewrite, maxOK := numericBound(pd.Max)
			if !minOK || !maxOK || minValue == maxValue {
				pd.Min = heatingGroupSetpointMin
				pd.Max = heatingGroupSetpointMax
				return true
			}
			changed := false
			if minRewrite {
				pd.Min = formatBound(minValue)
				changed = true
			}
			if maxRewrite {
				pd.Max = formatBound(maxValue)
				changed = true
			}
			return changed
		},
	})

	// HmIP-FWI fingerprint reader, MAINTENANCE channel CODE_ID.
	//
	// The CCU's MAX=21 is deliberate and device-specific: the HmIP server
	// registers CODE_ID three times under distinct keys — generic (0, 30),
	// keypad (1, 8), and the FWI variant (1, 21) — via HMIPServer
	// de.eq3.cbcs.devicedescription.channelspecification.stateparameter.GeneralStateParameterFactory#createCodeId,
	// and the CCU's own FWI maintenance control renders CODE_ID == 21 as
	// the bell button
	// (../OpenCCU-Base/www/rega/esp/controls/maintenanceFWI.fn:82-84), i.e.
	// 1..20 are the finger codes and 21 is the doorbell.
	//
	// What justifies raising MAX anyway is the wire, not the descriptor:
	// CODE_ID is the low five bits of the CODE_STATUS byte and the event
	// path applies no range check, so a raw value above the declared MAX
	// is observable. Our read-path validity gate compares the observed
	// value against MIN/MAX and suppresses the entity when it falls
	// outside, which is how an out-of-range observation makes the sensor
	// go unavailable.
	//
	// What 31 *means* is unverified in those words. The firmware assigns
	// it no meaning; its idle indicator is a different parameter,
	// CODE_STATE = 0 (CodeState.IDLE), and the CCU's own control reads
	// CODE_ID only while CODE_STATE == 1. Reading "31 = no active code"
	// out of the wire width is our convention, not the CCU's rule.
	//
	// The cost of the raise is that MIN/MAX also gates writes, so values
	// 22..codeIDWireCeiling now pass our check and are then rejected by
	// the CCU. Driving the entity off CODE_STATE — what the CCU's own
	// surface does — would settle both halves; that lives with the data
	// point, not here.
	//
	// The raise never lowers a declared MAX and never writes one where the
	// CCU declared none.
	out = append(out, Patch{
		Model:     "HmIP-FWI",
		Parameter: hmenum.ParameterCodeID,
		Paramset:  hmenum.ParamsetKeyValues,
		ChannelNo: &ch0,
		Reason:    "CODE_ID is a 5-bit wire field; raw values above the declared MAX are observable",
		Ticket:    "#3238",
		Apply: func(pd *hmproto.ParameterData) bool {
			declared, _, ok := numericBound(pd.Max)
			if !ok || declared >= codeIDWireCeiling {
				return false
			}
			pd.Max = json.RawMessage(strconv.Itoa(codeIDWireCeiling))
			return true
		},
	})

	return out
}

// numericBound decodes a MIN / MAX descriptor field.
//
// ok reports whether the field carries a number at all. rewrite reports
// whether the wire form was a JSON string holding that number, which is the
// shape a caller has to replace to hand downstream consumers a real number.
func numericBound(raw json.RawMessage) (value float64, rewrite, ok bool) {
	if len(raw) == 0 {
		return 0, false, false
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0, false, false
	}
	f, err := n.Float64()
	if err != nil {
		return 0, false, false
	}
	return f, raw[0] == '"', true
}

// formatBound renders a coerced bound as a JSON number literal.
func formatBound(v float64) json.RawMessage {
	return json.RawMessage(strconv.FormatFloat(v, 'f', -1, 64))
}
