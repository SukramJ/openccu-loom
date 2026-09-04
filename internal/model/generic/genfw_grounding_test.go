// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package generic

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// Guards for the rules in this package whose authority was re-grounded
// against the CCU's own sources. Each test names the source that decides
// the case in its doc comment, so a later reader can tell a measured rule
// from a ported one.

// genFwParamsetRecorder records the paramset a write produced, so a test
// can assert which keys reached the wire call.
type genFwParamsetRecorder struct {
	values map[string]any
	puts   int
}

func (w *genFwParamsetRecorder) SetValue(_ context.Context, _ string, _ hmenum.Parameter, _ any, _ hmenum.CommandPriority) error {
	return nil
}

func (w *genFwParamsetRecorder) PutParamset(_ context.Context, _ string, _ hmenum.ParamsetKey, values map[string]any, _ hmenum.CommandPriority) error {
	w.puts++
	w.values = values
	return nil
}

// TestGenFwToBoolRejectsEmptyString pins that an empty string is NOT a
// boolean.
//
// The CCU's own XML-RPC value library — the one rfd and hs485d link — is
// the decoder for exactly the values we decode. Both of its textual
// boolean readers reject an empty input as a parse failure:
// ../OpenCCU-Base/src/libXmlRpc/src/XmlRpcValue.cpp:425-437 (boolFromXml
// accepts only the decimal tokens 0 and 1, and returns false when strtol
// consumed nothing) and :470-488 (boolFromText accepts exactly the
// 4-byte "true" and the 5-byte "false"). Its emitters never produce an
// empty string either: :439 writes "1"/"0", :490 writes "true"/"false".
//
// So the honest verdict for "" is "this is not a boolean" — (false,
// false) — not the confirmed false we used to return.
func TestGenFwToBoolRejectsEmptyString(t *testing.T) {
	t.Parallel()

	if got, ok := toBool(""); ok {
		t.Fatalf(`toBool("") = (%v, true); the CCU's own decoders reject an empty string, want (false, false)`, got)
	}
	// A bool-typed data point must therefore stay unobserved rather than
	// take a confirmed false from an empty payload.
	if v, ok := coerceWire[bool](""); ok {
		t.Fatalf(`coerceWire[bool]("") = (%v, true), want (false, false)`, v)
	}
	// The named literals keep working — they are the port's set, not the
	// firmware's, and this guard is only about "".
	for _, tc := range []struct {
		in   string
		want bool
	}{{"true", true}, {"1", true}, {"false", false}, {"0", false}} {
		if got, ok := toBool(tc.in); !ok || got != tc.want {
			t.Fatalf("toBool(%q) = (%v, %v), want (%v, true)", tc.in, got, ok, tc.want)
		}
	}
}

// TestGenFwFixRSSIAcceptsBoundaryReadings pins the four RSSI values our
// strict inequalities used to discard even though both CCU stacks
// publish them as ordinary readings.
//
// BidCoS negates an UNSIGNED byte — ../OpenCCU-Base/src/rfd/RFDevice.cpp:502
// (`int rssi=-1*frame.GetByteData(13);`, and GetByteData returns
// `unsigned char`, ../OpenCCU-Base/src/libhsscomm/StructuredFrame.cpp:85),
// likewise ../OpenCCU-Base/src/multimacd/SerialFrame/HmLegacyFrameBidcos/HmLegacyFrameBidcosRxTelegram.cpp:38-40
// and .../LowLevelMacFrame/LowLevelMacFrameRxTelegram.cpp:52-54 — so byte
// 127 arrives as -127 and byte 129 as -129. HmIP negates a SIGNED byte
// (HMIPServer de.eq3.cbcs.server.core.vertx.IncomingHMIPFrameHandler#handleIncomingFrame),
// so byte -127 arrives as +127 and byte -1 as +1.
func TestGenFwFixRSSIAcceptsBoundaryReadings(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		in    int32
		want  int32
		why   string
		valid bool
	}{
		{name: "bidcos_byte_127", in: -127, want: -127, valid: true, why: "BidCoS -1*uint8(127)"},
		{name: "bidcos_byte_129", in: -129, want: -127, valid: true, why: "BidCoS -1*uint8(129)"},
		{name: "hmip_byte_minus_127", in: 127, want: -127, valid: true, why: "HmIP -1*int8(-127)"},
		{name: "hmip_byte_minus_1", in: 1, want: -1, valid: true, why: "HmIP -1*int8(-1)"},
		// The two the firmware itself rejects, kept rejected.
		{name: "hmip_no_signal_marker", in: 128, valid: false, why: "raw byte 0x80, the HmIP no-signal marker"},
		{name: "bidcos_zero_peer", in: 0, valid: false, why: "RSSI_PEER can carry a frame byte of 0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, ok := FixRSSI(tc.in)
			if ok != tc.valid {
				t.Fatalf("FixRSSI(%d) valid=%v, want %v (%s)", tc.in, ok, tc.valid, tc.why)
			}
			if ok && got != tc.want {
				t.Fatalf("FixRSSI(%d) = %d, want %d (%s)", tc.in, got, tc.want, tc.why)
			}
		})
	}
}

// TestGenFwCleanupUnitMatchesWholeUnitsOnly pins that a compound unit
// keeps its suffix.
//
// The CCU normalises units by whole-string equality, never by substring:
// ../OpenCCU-Base/www/config/easymodes/etc/uiElements.tcl:174-216 is a
// chain of `==` / `string equal` tests, and
// ../OpenCCU-Base/www/rega/esp/programs.fn:606-614 is two more. The
// compound units that exist in the shipped corpus are
// ../OpenCCU-Base/src/devicetypes/rftypes/rf_es_tx_wm.xml:282
// (METER_CONSTANT_GAS, `unit="m3/Imp."`, same at
// rf_es_tx_wm_le_v1_0.xml:171) and
// ../OpenCCU-Base/opt/HmIP/legacy-parameter-definition.config:352
// (`ENERGIE_METER_TRANSMITTER.GAS_FLOW.Unit=m3/h`). Neither matches any
// firmware branch, so the CCU renders both verbatim.
func TestGenFwCleanupUnitMatchesWholeUnitsOnly(t *testing.T) {
	t.Parallel()

	cases := []struct {
		param hmenum.Parameter
		raw   string
		want  string
	}{
		{param: "METER_CONSTANT_GAS", raw: "m3/Imp.", want: "m3/Imp."},
		{param: "GAS_FLOW", raw: "m3/h", want: "m3/h"},
		{param: "GAS_ENERGY_COUNTER", raw: "m3", want: "m³"},
	}
	for _, tc := range cases {
		if got := CleanupUnit(tc.param, tc.raw); got != tc.want {
			t.Errorf("CleanupUnit(%s, %q) = %q, want %q", tc.param, tc.raw, got, tc.want)
		}
	}
}

// TestGenFwCleanupUnitDegreeIsAnAngle pins that `degree` is an angle,
// not a temperature.
//
// ../OpenCCU-Base/www/rega/esp/programs.fn:611-614 maps it to `&deg;`,
// and uiElements.tcl:198-200 maps HmIP's `_Grad_` to `&#176;` — both the
// degree sign, not Celsius. The only parameters declaring `unit="degree"`
// are WIND_DIRECTION and WIND_DIRECTION_RANGE
// (../OpenCCU-Base/src/devicetypes/rftypes/rf_hm-wds100-c6-o-2.xml:155,163
// and rf_ks550.xml:148,156) — angles, which is why the mapping only ever
// showed as a latent trap: fixUnitByParam already overrides those two.
func TestGenFwCleanupUnitDegreeIsAnAngle(t *testing.T) {
	t.Parallel()

	// A parameter with no per-parameter override, so the table decides.
	if got := CleanupUnit("SOME_ANGLE", "degree"); got != "°" {
		t.Errorf(`CleanupUnit(_, "degree") = %q, want "°" (an angle, not Celsius)`, got)
	}
	if got := CleanupUnit("SOME_ANGLE", "_Grad_"); got != "°" {
		t.Errorf(`CleanupUnit(_, "_Grad_") = %q, want "°"`, got)
	}
	// The two parameters that actually declare it keep their override.
	for _, p := range []hmenum.Parameter{"WIND_DIRECTION", "WIND_DIRECTION_RANGE"} {
		if got := CleanupUnit(p, "degree"); got != "°" {
			t.Errorf("CleanupUnit(%s, \"degree\") = %q, want \"°\"", p, got)
		}
	}
}

// TestGenFwCleanupUnitStripsDeclaredQuotes pins that the quote-stripping
// rule survives the move to whole-string matching.
//
// ../OpenCCU-Base/opt/HmIP/legacy-parameter-definition.config:333 and
// :653 declare the literal two-character value `""` on the right-hand
// side of `.Unit=`, which is why stripping has to stay a character-level
// strip rather than a table lookup.
func TestGenFwCleanupUnitStripsDeclaredQuotes(t *testing.T) {
	t.Parallel()

	if got := CleanupUnit("SOME_PARAM", `""`); got != "" {
		t.Errorf(`CleanupUnit(_, "\"\"") = %q, want ""`, got)
	}
	if got := CleanupUnit("SOME_PARAM", `"m3/h"`); got != "m3/h" {
		t.Errorf(`CleanupUnit(_, "\"m3/h\"") = %q, want "m3/h"`, got)
	}
}

// TestGenFwSwitchOnTimeZeroIsCarriedNotCollapsed pins that an explicit
// zero on-time still travels as an ON_TIME key.
//
// It is a regression pin, not a firmware requirement: on BidCos the two
// shapes are equivalent, because the LEVEL_SET frame carries
// `omit_if="0"` on its ON_TIME field
// (../OpenCCU-Base/src/devicetypes/rftypes/rf_s_644.xml:230, semantics at
// ../OpenCCU-Base/src/libhsscomm/FrameDescription.h:60 FLAG_OMIT), so an
// encoded 0 vanishes from the radio frame either way. What the pin
// protects is that the caller's explicit zero is not silently reshaped
// into a different wire call.
func TestGenFwSwitchOnTimeZeroIsCarriedNotCollapsed(t *testing.T) {
	t.Parallel()

	w := &genFwParamsetRecorder{}
	s := NewSwitch(switchCfg())
	s.Writer = w

	if err := s.TurnOnWithTimer(context.Background(), 0, hmenum.CommandPriorityCritical); err != nil {
		t.Fatalf("TurnOnWithTimer(0): %v", err)
	}
	if w.puts != 1 {
		t.Fatalf("PutParamset calls = %d, want 1 (an explicit zero must not collapse to a plain STATE write)", w.puts)
	}
	got, ok := w.values[string(hmenum.ParameterOnTime)]
	if !ok {
		t.Fatalf("paramset %v carries no ON_TIME key", w.values)
	}
	if f, isFloat := got.(float64); !isFloat || f != 0 {
		t.Fatalf("ON_TIME = %#v, want float64(0)", got)
	}
}

// TestGenFwForceSensorMatchIsCaseInsensitive pins that the eTRV model
// match ignores case.
//
// It is load-bearing, not incidental: the CCU's own eTRV-family list
// ships both spellings — ../OpenCCU-Base/opt/HmIP/legacy-parameter-definition.config:12
// `HMIP-eTRV-2=hmip-etrv` sits directly above :13 `HmIP-eTRV-2=hmip-etrv`.
func TestGenFwForceSensorMatchIsCaseInsensitive(t *testing.T) {
	t.Parallel()

	for _, model := range []string{"HmIP-eTRV-2", "HMIP-eTRV-2", "hmip-etrv-b-2 r4m", "HmIP-HEATING"} {
		if !IsForceSensorParameter(model, hmenum.ParameterLevel) {
			t.Errorf("IsForceSensorParameter(%q, LEVEL) = false, want true", model)
		}
	}
	// A wall thermostat declares no LEVEL at all, so nothing to demote.
	if IsForceSensorParameter("HmIP-WTH-2", hmenum.ParameterLevel) {
		t.Error(`IsForceSensorParameter("HmIP-WTH-2", LEVEL) = true, want false`)
	}
}

// TestGenFwValuesCloseWindowIsFixedAndUngrounded characterises the
// optimistic-confirmation tolerance so that any change to it is
// deliberate.
//
// The window is a fixed ±0.005, and it is NOT the CCU's rule. The CCU
// quantises every scaled FLOAT parameter to 1/factor
// (../OpenCCU-Base/src/libhsscomm/HSSTypeConversionFloatInteger.cpp:56-66
// on write, :69-77 on the echo), and the factor is per parameter:
// SET_TEMPERATURE is factor 2 → step 0.5 °C
// (../OpenCCU-Base/src/devicetypes/rftypes/rf_cc_rt_dn.xml:2392-2401),
// ON_TIME / RAMP_TIME are factor 10, LEVEL on some dimmers is factor 200
// → step 0.005. The two assertions below are the two directions in which
// the fixed window disagrees with that.
func TestGenFwValuesCloseWindowIsFixedAndUngrounded(t *testing.T) {
	t.Parallel()

	// Too tight: SET_TEMPERATURE snaps 20.3 to 20.5, and the confirmation
	// reads as a mismatch.
	if valuesClose(20.3, 20.5) {
		t.Error("valuesClose(20.3, 20.5) = true; the fixed window is ±0.005, so this must be a mismatch")
	}
	// Too loose: a factor-200 LEVEL can execute 0.500 and 0.505 as
	// distinct steps, and the fixed window swallows the difference.
	if !valuesClose(0.500, 0.503) {
		t.Error("valuesClose(0.500, 0.503) = false; the fixed window is ±0.005, so these compare equal")
	}
}
