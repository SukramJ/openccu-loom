// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Guards for the built-in patches whose values must match what the CCU
// firmware declares. Each test names the firmware source it pins.

package patches

import (
	"encoding/json"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
)

// patchFwBound decodes a MIN / MAX raw value into its numeric value and
// reports whether the wire form was a JSON string rather than a number.
// The distinction is the whole point of the HM-CC-VG-1 patch: the CCU
// serves the group's bounds as strings.
func patchFwBound(t *testing.T, raw json.RawMessage) (value float64, quoted bool) {
	t.Helper()
	if len(raw) == 0 {
		t.Fatalf("bound is absent")
	}
	quoted = raw[0] == '"'
	var n json.Number
	if err := json.Unmarshal(raw, &n); err != nil {
		t.Fatalf("bound %s is not numeric: %v", raw, err)
	}
	f, err := n.Float64()
	if err != nil {
		t.Fatalf("bound %s does not convert to float: %v", raw, err)
	}
	return f, quoted
}

// patchFwVirtualGroupSetTemperature builds the VALUES paramset the CCU
// serves for HM-CC-VG-1 channel 1, with the caller's MIN / MAX wire form.
func patchFwVirtualGroupSetTemperature(minRaw, maxRaw string) hmproto.Paramset {
	return hmproto.Paramset{
		"SET_TEMPERATURE": hmproto.ParameterData{
			Type: hmenum.ParameterTypeFloat,
			Min:  json.RawMessage(minRaw),
			Max:  json.RawMessage(maxRaw),
		},
	}
}

// TestPatchFwVirtualGroupSetTemperatureCoercesToDeclaredBounds pins the
// patch to the bounds the CCU declares for the virtual heating group.
//
// ../OpenCCU-Base/opt/HMServer/groups/groupdefinitions.xml:504 declares
// SET_TEMPERATURE min="5.0" max="30.0" default="20.0" unit="°C" inside
// <channel id="1" type="CLIMATECONTROL_RT_TRANSCEIVER"> of the group whose
// <virtual_device_type> is HM-CC-VG-1 (:8113). 4.5 / 30.5 are the member
// device's own bounds
// (../OpenCCU-Base/src/devicetypes/rftypes/rf_cc_rt_dn.xml:2395) and must
// not leak into the group.
func TestPatchFwVirtualGroupSetTemperatureCoercesToDeclaredBounds(t *testing.T) {
	t.Parallel()

	// The wire form the CCU actually serves: MIN and MAX as strings.
	ps := patchFwVirtualGroupSetTemperature(`"5.0"`, `"30.0"`)
	if changes := NewRegistry().ApplyParamset("HM-CC-VG-1", "VCU0000001:1", hmenum.ParamsetKeyValues, ps); changes == 0 {
		t.Fatal("string-typed bounds must be coerced")
	}

	pd := ps["SET_TEMPERATURE"]
	minV, minQuoted := patchFwBound(t, pd.Min)
	maxV, maxQuoted := patchFwBound(t, pd.Max)
	if minQuoted || maxQuoted {
		t.Errorf("bounds must be coerced to JSON numbers, got MIN=%s MAX=%s", pd.Min, pd.Max)
	}
	if minV != 5.0 {
		t.Errorf("MIN=%v, want 5 — groupdefinitions.xml:504 declares min=\"5.0\"", minV)
	}
	if maxV != 30.0 {
		t.Errorf("MAX=%v, want 30 — groupdefinitions.xml:504 declares max=\"30.0\"", maxV)
	}
}

// TestPatchFwVirtualGroupSetTemperatureLeavesNumericBoundsAlone pins the
// rule that the CCU is the authority on its own bounds. When MIN / MAX
// already arrive as numbers there is nothing to coerce, and the patch must
// neither widen nor narrow them.
func TestPatchFwVirtualGroupSetTemperatureLeavesNumericBoundsAlone(t *testing.T) {
	t.Parallel()

	ps := patchFwVirtualGroupSetTemperature(`5.0`, `30.0`)
	if changes := NewRegistry().ApplyParamset("HM-CC-VG-1", "VCU0000001:1", hmenum.ParamsetKeyValues, ps); changes != 0 {
		t.Fatalf("numeric bounds must be left untouched, got %d changes (MIN=%s MAX=%s)",
			changes, ps["SET_TEMPERATURE"].Min, ps["SET_TEMPERATURE"].Max)
	}

	pd := ps["SET_TEMPERATURE"]
	if minV, _ := patchFwBound(t, pd.Min); minV != 5.0 {
		t.Errorf("MIN=%v, want 5 (unchanged)", minV)
	}
	if maxV, _ := patchFwBound(t, pd.Max); maxV != 30.0 {
		t.Errorf("MAX=%v, want 30 (unchanged)", maxV)
	}
}

// TestPatchFwVirtualGroupSetTemperatureFallsBackOnUnusableRange covers the
// degenerate 0/0 range the patch was originally written for. The 0/0
// observation itself is unverified against the CCU sources, but a setpoint
// whose MIN equals its MAX carries no usable range, so the fallback value
// is taken from the group definition rather than invented.
func TestPatchFwVirtualGroupSetTemperatureFallsBackOnUnusableRange(t *testing.T) {
	t.Parallel()

	ps := patchFwVirtualGroupSetTemperature(`0`, `0`)
	if changes := NewRegistry().ApplyParamset("HM-CC-VG-1", "VCU0000001:1", hmenum.ParamsetKeyValues, ps); changes == 0 {
		t.Fatal("a 0/0 range must be replaced")
	}

	pd := ps["SET_TEMPERATURE"]
	if minV, _ := patchFwBound(t, pd.Min); minV != 5.0 {
		t.Errorf("MIN=%v, want 5", minV)
	}
	if maxV, _ := patchFwBound(t, pd.Max); maxV != 30.0 {
		t.Errorf("MAX=%v, want 30", maxV)
	}
}

// TestPatchFwRGBWSaturationOperationsUntouched pins the absence of a
// SATURATION OPERATIONS patch for HmIP-RGBW. The HmIP server registers
// SATURATION exactly once, with read|write|event, and the legacy XML-RPC
// descriptor copies that byte verbatim; the one override file that can
// rewrite a parameter description can set only TabOrder, Unit, Control and
// Disabled. A descriptor without the EVENT bit is therefore not a shape a
// current CCU can produce, and we must not fabricate one.
func TestPatchFwRGBWSaturationOperationsUntouched(t *testing.T) {
	t.Parallel()

	pd := &hmproto.ParameterData{
		Type:       hmenum.ParameterTypeFloat,
		Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
	}
	changes := NewRegistry().ApplyTo("HmIP-RGBW", hmenum.ParamsetKeyValues, hmenum.ParameterSaturation, pd)
	if changes != 0 {
		t.Fatalf("SATURATION OPERATIONS must not be patched, got %d changes (operations=%d)", changes, pd.Operations)
	}
	if pd.Operations.IsEvent() {
		t.Errorf("EVENT bit was set by a patch; operations=%d", pd.Operations)
	}
}

// TestPatchFwEnergyCounterUnitCoversEveryDeclaredModel pins the UNIT patch
// to the full set of ids that share the declaration it repairs.
// ../OpenCCU-Base/src/devicetypes/rftypes/rf_es_pmsw.xml lists nine ids in
// its <supported_types> block (:9-40) and declares ENERGY_COUNTER once
// (:194-203) for all of them.
func TestPatchFwEnergyCounterUnitCoversEveryDeclaredModel(t *testing.T) {
	t.Parallel()

	for _, model := range []string{
		"HM-ES-PMSw1-Pl",
		"HM-ES-PMSw1-Pl-DN-R1",
		"HM-ES-PMSw1-Pl-DN-R2",
		"HM-ES-PMSw1-Pl-DN-R3",
		"HM-ES-PMSw1-Pl-DN-R4",
		"HM-ES-PMSw1-Pl-DN-R5",
		"HM-ES-PMSw1-DR",
		"HM-ES-PMSw1-SM",
		"HM-ES-PMSwX",
	} {
		pd := &hmproto.ParameterData{Type: hmenum.ParameterTypeFloat}
		changes := NewRegistry().ApplyTo(model, hmenum.ParamsetKeyValues, hmenum.Parameter("ENERGY_COUNTER"), pd)
		if changes == 0 {
			t.Errorf("%s: ENERGY_COUNTER UNIT patch did not fire", model)
			continue
		}
		if pd.Unit != "Wh" {
			t.Errorf("%s: UNIT=%q, want Wh — rf_es_pmsw.xml:196 declares unit=\"Wh\"", model, pd.Unit)
		}
	}
}

// TestPatchFwEnergyCounterUnitNeverOverwritesADeclaredUnit keeps the patch
// defensive: whenever the CCU does send a UNIT it wins, because the device
// XML is the authority and the sibling counters on the same channel carry
// different units (W, mA, V, Hz; kWh and m3 on the sibling device file).
func TestPatchFwEnergyCounterUnitNeverOverwritesADeclaredUnit(t *testing.T) {
	t.Parallel()

	pd := &hmproto.ParameterData{Type: hmenum.ParameterTypeFloat, Unit: "kWh"}
	if changes := NewRegistry().ApplyTo("HM-ES-PMSw1-Pl", hmenum.ParamsetKeyValues, hmenum.Parameter("ENERGY_COUNTER"), pd); changes != 0 {
		t.Fatalf("a declared UNIT must win, got %d changes", changes)
	}
	if pd.Unit != "kWh" {
		t.Errorf("UNIT=%q, want kWh (unchanged)", pd.Unit)
	}
}

// TestPatchFwFwiCodeIDNeverNarrowsOrInventsAMax pins the two things the
// CODE_ID patch must not do. The CCU declares MIN=1 MAX=21 for the FWI
// variant deliberately, and 21 is the bell button on the CCU's own
// maintenance control (../OpenCCU-Base/www/rega/esp/controls/maintenanceFWI.fn:82).
// The patch raises MAX to the 5-bit wire ceiling so an observed raw value
// survives the read-path range gate — it must not lower a higher declared
// MAX, and it must not write a MAX where the CCU declared none.
func TestPatchFwFwiCodeIDNeverNarrowsOrInventsAMax(t *testing.T) {
	t.Parallel()

	// A MAX above the wire ceiling is the CCU's business, not ours.
	wide := hmproto.Paramset{
		"CODE_ID": hmproto.ParameterData{Type: hmenum.ParameterTypeInteger, Min: json.RawMessage(`1`), Max: json.RawMessage(`63`)},
	}
	if changes := NewRegistry().ApplyParamset("HmIP-FWI", "VCU4820995:0", hmenum.ParamsetKeyValues, wide); changes != 0 {
		t.Errorf("patch must not narrow a declared MAX, got %d changes (MAX=%s)", changes, wide["CODE_ID"].Max)
	}
	if got := string(wide["CODE_ID"].Max); got != "63" {
		t.Errorf("MAX=%s, want 63 (unchanged)", got)
	}

	// No declared MAX at all: the CCU set no ceiling, so neither do we.
	none := hmproto.Paramset{
		"CODE_ID": hmproto.ParameterData{Type: hmenum.ParameterTypeInteger, Min: json.RawMessage(`1`)},
	}
	if changes := NewRegistry().ApplyParamset("HmIP-FWI", "VCU4820995:0", hmenum.ParamsetKeyValues, none); changes != 0 {
		t.Errorf("patch must not invent a MAX, got %d changes (MAX=%s)", changes, none["CODE_ID"].Max)
	}
	if got := string(none["CODE_ID"].Max); got != "" {
		t.Errorf("MAX=%s, want absent", got)
	}
}
