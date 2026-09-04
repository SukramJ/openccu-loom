// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/model/value"
	"github.com/SukramJ/openccu-loom/internal/parameter"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestConvertableParameterSetsAgree pins every declaration of one rule —
// which parameter names carry a combined wire encoding — to the same
// membership.
//
// Two of the declarations gate a running daemon:
//
//   - parameter.ConvertableParameters decides on the WRITE path whether a
//     value is decomposed into the command tracker
//     (internal/client/interface_client_orchestration.go,
//     InterfaceClient.WriteUnconfirmedValue → CommandTracker.AddCombinedParameter).
//   - backends.IsCombinedParameter / backends.CombinedParameters decide on
//     the CALLBACK path whether an incoming value is decomposed into its
//     constituent data points (internal/central/adapter/callback_handlers.go),
//     and back backends.ParseCombinedParameter, which is also the decoder the
//     write path's command tracker resolves optimistic values through.
//
// A parameter listed on one of those two alone is written as sub-parameters
// and never reassembled on the way back, or — the other direction — routed
// into AddCombinedParameter, where ParseCombinedParameter's `default:
// return nil, false` drops the optimistic record entirely.
//
// The third declaration, internal/model/value.ConvertableParameters, gates
// nothing: a grep for value.IsConvertableParameter and
// value.ConvertableParameters over all non-test .go files in the tree returns
// only the declaring file itself. It is checked here because it exists and
// would otherwise drift unobserved, not because a daemon consults it — and it
// is the reason this guard used to prove less than it claimed: binding the
// live write set to a test-only mirror leaves the live callback set free to
// disagree with both.
func TestConvertableParameterSetsAgree(t *testing.T) {
	if len(parameter.ConvertableParameters) == 0 {
		t.Fatal("parameter.ConvertableParameters is empty — every loop below would pass vacuously")
	}

	// Live write path → live callback path. This is the pairing that decides
	// whether a combined write is reassembled when the CCU echoes it back.
	for p := range parameter.ConvertableParameters {
		if !backends.IsCombinedParameter(string(p)) {
			t.Errorf("parameter %q is convertable on the write path (parameter.IsConvertable) but "+
				"backends.IsCombinedParameter reports false: the write is decomposed into the command "+
				"tracker while the callback carrying it back is published as one opaque value", p)
		}
	}
	for _, p := range backends.CombinedParameters {
		if !parameter.IsConvertable(p) {
			t.Errorf("parameter %q is decomposed on the callback path (backends.CombinedParameters) but "+
				"parameter.IsConvertable reports false, so the write side never tracks it", p)
		}
	}
	if got, want := len(backends.CombinedParameters), len(parameter.ConvertableParameters); got != want {
		t.Errorf("combined-parameter set sizes differ: internal/client/backends has %d, internal/parameter has %d", got, want)
	}

	// The test-only model mirror, both directions.
	for _, p := range value.ConvertableParameters {
		if !parameter.IsConvertable(p) {
			t.Errorf("parameter %q is in internal/model/value.ConvertableParameters but parameter.IsConvertable reports false", p)
		}
		if !value.IsConvertableParameter(p) {
			t.Errorf("internal/model/value.IsConvertableParameter(%q) is false for a parameter in its own ConvertableParameters list", p)
		}
	}
	for p := range parameter.ConvertableParameters {
		if !value.IsConvertableParameter(p) {
			t.Errorf("parameter %q is convertable per internal/parameter but missing from internal/model/value.ConvertableParameters", p)
		}
	}
	if got, want := len(value.ConvertableParameters), len(parameter.ConvertableParameters); got != want {
		t.Errorf("convertable-parameter set sizes differ: internal/model/value has %d, internal/parameter has %d", got, want)
	}

	// A non-convertable parameter must be rejected by all three, so the test
	// cannot pass vacuously on a set that accepts everything.
	if parameter.IsConvertable(hmenum.ParameterState) ||
		value.IsConvertableParameter(hmenum.ParameterState) ||
		backends.IsCombinedParameter(string(hmenum.ParameterState)) {
		t.Errorf("STATE must not be classified convertable by any of the three declarations")
	}
}
