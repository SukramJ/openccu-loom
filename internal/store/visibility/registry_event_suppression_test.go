// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package visibility

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestEventSuppressionConsultsEveryMatchingModelKey pins that
// IsParameterIgnoredForDataPointEvent — and the decider step that calls it —
// keeps looking when a matching model key does not carry the parameter.
//
// The scenario needs two keys that are both prefixes of the same model, which
// the shipped single-entry table cannot produce, so the test installs its own
// table for the duration and restores the real one. It must not run in
// parallel: it mutates package state that the other tests in this package
// read.
//
// The assertion runs 100 times because Go randomises map iteration order: a
// loop that returned the first matching key's verdict would answer correctly
// roughly half the time, and 100 draws make a green run on the broken loop a
// 2^-100 event.
func TestEventSuppressionConsultsEveryMatchingModelKey(t *testing.T) {
	const (
		model = "HmIP-XTEST-Alpha"
		short = "HmIP-XTEST"
		long  = "HmIP-XTEST-A"
	)
	param := hmenum.ParameterPressShort

	original := ignoreDevicesForDataPointEvents
	t.Cleanup(func() { ignoreDevicesForDataPointEvents = original })
	ignoreDevicesForDataPointEvents = map[string]map[hmenum.Parameter]struct{}{
		// Matches the model by prefix but carries no parameter at all.
		short: {},
		// Matches the model by prefix and carries the parameter.
		long: {param: {}},
	}

	d := NewParameterDecider(nil)
	for i := range 100 {
		if !IsParameterIgnoredForDataPointEvent(model, param) {
			t.Fatalf("draw %d: IsParameterIgnoredForDataPointEvent(%q, %s) = false, want true — "+
				"the %q key was reached first and its verdict masked the %q key", i, model, param, short, long)
		}
		if !d.IsParameterIgnored(model, "X", channelNoUnknown, hmenum.ParamsetKeyValues, param) {
			t.Fatalf("draw %d: decider reports %s not ignored for %q — "+
				"the event-suppression gate lost the %q entry to the %q entry", i, param, model, long, short)
		}
	}
}
