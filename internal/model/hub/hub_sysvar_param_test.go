// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hub

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// --- Hub ClearPrograms / ClearSysvars ---

func TestHubClearPrograms(t *testing.T) {
	t.Parallel()
	h := NewHub("ccu1")
	w := &stubProgram{}
	p := NewProgram("ccu1", "p1", "P1", "", false, w)
	h.PutProgram(p)

	if len(h.Programs()) != 1 {
		t.Fatal("program not registered")
	}
	h.ClearPrograms()
	if len(h.Programs()) != 0 {
		t.Fatal("ClearPrograms must empty the map")
	}
	// Repeated clear is idempotent.
	h.ClearPrograms()
}

func TestHubClearSysvars(t *testing.T) {
	t.Parallel()
	h := NewHub("ccu1")
	sv := NewSysvar("ccu1", "myvar", "desc", hmenum.HubValueTypeLogic, nil)
	h.PutSysvar(sv)

	if len(h.Sysvars()) != 1 {
		t.Fatal("sysvar not registered")
	}
	h.ClearSysvars()
	if len(h.Sysvars()) != 0 {
		t.Fatal("ClearSysvars must empty the map")
	}
	// Repeated clear is idempotent.
	h.ClearSysvars()
}

// --- HubDataPoint Channel / SetChannel ---

func TestHubDataPointChannelRoundtrip(t *testing.T) {
	t.Parallel()
	dp := NewHubDataPoint("ccu1", "myvar", "desc", true)

	if got := dp.Channel(); got != "" {
		t.Fatalf("fresh Channel() = %q, want empty", got)
	}
	dp.SetChannel("ch:1")
	if got := dp.Channel(); got != "ch:1" {
		t.Fatalf("after SetChannel() = %q, want ch:1", got)
	}
}

// --- sysvarParamValue branches (via NewSysvar + set_value service) ---

func TestSysvarRegisterSysvarServicesLogic(t *testing.T) {
	t.Parallel()
	w := &stubSysvar{}
	sv := NewSysvar("ccu1", "sv1", "desc", hmenum.HubValueTypeLogic, w)

	// Exercise the registered "set_value" service via Invoke.
	err := sv.Invoke(context.Background(), "set_value", map[string]any{"value": true}, hmenum.CommandPriorityHigh)
	if err != nil {
		t.Fatalf("set_value(true) err=%v", err)
	}
}

func TestSysvarParamValueAllTypes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		vt      hmenum.HubValueType
		raw     any
		wantErr bool
	}{
		{"logic/bool-true", hmenum.HubValueTypeLogic, true, false},
		{"logic/bool-false", hmenum.HubValueTypeLogic, false, false},
		{"logic/float-1", hmenum.HubValueTypeLogic, float64(1), false},
		{"logic/float-0", hmenum.HubValueTypeLogic, float64(0), false},
		{"logic/string-true", hmenum.HubValueTypeLogic, "true", false},
		{"logic/string-false", hmenum.HubValueTypeLogic, "FALSE", false},
		{"logic/string-bad", hmenum.HubValueTypeLogic, "maybe", false},
		{"logic/bad-type", hmenum.HubValueTypeLogic, 42, true},
		{"float/float64", hmenum.HubValueTypeFloat, float64(3.14), false},
		{"float/int", hmenum.HubValueTypeFloat, 42, false},
		{"float/string", hmenum.HubValueTypeFloat, "1.5", false},
		{"float/bad", hmenum.HubValueTypeFloat, true, true},
		{"integer/float64", hmenum.HubValueTypeInteger, float64(7), false},
		{"integer/int", hmenum.HubValueTypeInteger, 3, false},
		{"integer/string", hmenum.HubValueTypeInteger, "42", false},
		{"integer/bad", hmenum.HubValueTypeInteger, "abc", false},
		{"string/str", hmenum.HubValueTypeString, "hello", false},
		{"string/bool", hmenum.HubValueTypeString, true, false},
		{"string/bad", hmenum.HubValueTypeString, []int{1}, true},
		{"list/slice-any", hmenum.HubValueTypeList, []any{"a", "b"}, false},
		{"list/slice-str", hmenum.HubValueTypeList, []string{"x"}, false},
		{"list/single-str", hmenum.HubValueTypeList, "one", false},
		{"list/bad", hmenum.HubValueTypeList, 99, true},
		// When the HubValueType is unknown the fallback infers the type from the
		// raw Go value (bool / float64 / int / string). A plain string raw value
		// succeeds; a slice (no matching Go primitive) still errors.
		{"unknown-vt/string-ok", hmenum.HubValueType("BOGUS"), "x", false},
		{"unknown-vt/slice-err", hmenum.HubValueType("BOGUS"), []int{1}, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			_, err := sysvarParamValue(c.vt, c.raw)
			if c.wantErr && err == nil {
				t.Fatalf("expected error, got nil")
			}
			if !c.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// TestServiceRouteAndDirectSetAgreeOnStrings is the guard behind moving
// string coercion out of [sysvarParamValue].
//
// The service route (set_value with a JSON string) and a direct
// [Sysvar.Set] must reach the same verdict: the same wire value, or the
// same rejection. They did not. The service route's boolean table was
// case-sensitive and knew neither "yes" nor "t", so it refused values the
// write path accepts; its numeric tables used fmt.Sscanf, which reads
// "12abc" as 12 and bounds nothing, so it accepted values the write path
// refuses.
//
// The tokens below are the ones that used to disagree, plus one that
// always did agree as the control.
func TestServiceRouteAndDirectSetAgreeOnStrings(t *testing.T) {
	t.Parallel()

	for _, c := range []struct {
		name      string
		valueType hmenum.HubValueType
		raw       string
	}{
		{"logic accepts a token only the wire path knew", hmenum.HubValueTypeLogic, "yes"},
		{"logic accepts mixed case", hmenum.HubValueTypeLogic, "tRuE"},
		{"logic refuses a non-token", hmenum.HubValueTypeLogic, "maybe"},
		{"float refuses a trailing-garbage number", hmenum.HubValueTypeFloat, "12abc"},
		{"integer refuses a trailing-garbage number", hmenum.HubValueTypeInteger, "7xyz"},
		{"integer accepts a plain number", hmenum.HubValueTypeInteger, "42"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()

			viaService := &capturingSysvarWriter{}
			svc := NewSysvar("c1", "v", "", c.valueType, viaService)
			pv, convErr := sysvarParamValue(c.valueType, any(c.raw))
			var svcErr error
			if convErr != nil {
				svcErr = convErr
			} else {
				svcErr = svc.Set(ctx, pv)
			}

			viaDirect := &capturingSysvarWriter{}
			direct := NewSysvar("c1", "v", "", c.valueType, viaDirect)
			directErr := direct.Set(ctx, hmtypes.StringValue(c.raw))

			if (svcErr == nil) != (directErr == nil) {
				t.Fatalf("service route err=%v but direct Set err=%v — the two routes disagree", svcErr, directErr)
			}
			if svcErr == nil && viaService.value != viaDirect.value {
				t.Errorf("service route wrote %#v, direct Set wrote %#v", viaService.value, viaDirect.value)
			}
		})
	}
}
