// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package backends

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/parameter"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestHmBkCombinedParameterSetMatchesWritePath pins the two membership tests
// that a running daemon actually consults for the combined-parameter set.
//
// parameter.IsConvertable decides whether a WRITE is decomposed into the
// command tracker (the optimistic-value path); IsCombinedParameter
// decides whether an incoming CALLBACK is decomposed into its constituent
// data points. They are the two halves of one feature and have to carry the
// same membership: a parameter added to one alone is written as sub-parameters
// but never decomposed on the way back (or the reverse), and the optimistic
// value is silently dropped.
//
// The pre-existing set guard pairs internal/parameter with the
// internal/model/value mirror, which no production path reads; this one pairs
// internal/parameter with the callback decoder that does.
func TestHmBkCombinedParameterSetMatchesWritePath(t *testing.T) {
	t.Parallel()

	if len(parameter.ConvertableParameters) == 0 {
		t.Fatal("parameter.ConvertableParameters is empty — the checks below would pass vacuously")
	}

	for p := range parameter.ConvertableParameters {
		if !IsCombinedParameter(string(p)) {
			t.Errorf("parameter %q is convertable on the write path (parameter.IsConvertable) "+
				"but IsCombinedParameter reports false, so the callback is never decomposed", p)
		}
	}

	// And the reverse direction, over the callback decoder's own declared
	// set, so an entry added to the backends side alone is caught too.
	for _, p := range CombinedParameters {
		if !parameter.IsConvertable(p) {
			t.Errorf("parameter %q is decomposed on the callback path "+
				"(CombinedParameters) but is not convertable on the write path", p)
		}
		if !IsCombinedParameter(string(p)) {
			t.Errorf("IsCombinedParameter(%q) is false for a parameter in "+
				"CombinedParameters — the predicate and its declared set disagree", p)
		}
	}

	if got, want := len(CombinedParameters), len(parameter.ConvertableParameters); got != want {
		t.Errorf("combined-parameter set sizes differ: backends has %d, internal/parameter has %d", got, want)
	}

	// A parameter that is neither, so the test cannot pass by accepting
	// everything.
	if IsCombinedParameter(string(hmenum.ParameterState)) {
		t.Error("STATE must not be classified as a combined parameter")
	}
}
