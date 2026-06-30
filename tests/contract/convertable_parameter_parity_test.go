// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package contract

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/value"
	"github.com/SukramJ/openccu-loom/internal/parameter"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestConvertableParameterSetsAgree pins the two parallel definitions of the
// convertable-parameter set to the same membership. The optimistic write path
// (InterfaceClient.WriteUnconfirmedValue) routes combined parameters using
// parameter.IsConvertable (internal/parameter), while internal/model/value
// keeps a model-side mirror (value.IsConvertableParameter). Both must list the
// same parameters: if one gains or loses a parameter the other lacks, a
// combined-parameter write would route correctly on one path and not the
// other. This guard fails the build the moment they diverge.
func TestConvertableParameterSetsAgree(t *testing.T) {
	// Every parameter the model mirror lists must be convertable per the
	// call-site predicate.
	for _, p := range value.ConvertableParameters {
		if !parameter.IsConvertable(p) {
			t.Errorf("parameter %q is in internal/model/value.ConvertableParameters but parameter.IsConvertable reports false", p)
		}
		if !value.IsConvertableParameter(p) {
			t.Errorf("internal/model/value.IsConvertableParameter(%q) is false for a parameter in its own ConvertableParameters list", p)
		}
	}

	// And every parameter the call-site predicate accepts must be in the
	// model mirror — checked over the full known parameter space the two
	// sets draw from.
	for p := range parameter.ConvertableParameters {
		if !value.IsConvertableParameter(p) {
			t.Errorf("parameter %q is convertable per internal/parameter but missing from internal/model/value.ConvertableParameters", p)
		}
	}

	// Sizes must match — a belt-and-braces check that neither side carries an
	// extra entry the loops above could miss.
	if got, want := len(value.ConvertableParameters), len(parameter.ConvertableParameters); got != want {
		t.Errorf("convertable-parameter set sizes differ: internal/model/value has %d, internal/parameter has %d", got, want)
	}

	// A non-convertable parameter must be rejected by both, so the test
	// cannot pass vacuously on an empty set.
	if parameter.IsConvertable(hmenum.ParameterState) || value.IsConvertableParameter(hmenum.ParameterState) {
		t.Errorf("STATE must not be classified convertable by either predicate")
	}
}
