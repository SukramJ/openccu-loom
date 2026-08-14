// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package eligibility

import (
	"slices"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/calculated"
	"github.com/SukramJ/openccu-loom/internal/model/custom/climate"
	"github.com/SukramJ/openccu-loom/internal/model/custom/light"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
)

// TestDPKeyProbesHaveProductionImplementations pins every capability probe
// the candidate-key heuristic performs against the data-point types the
// model actually materialises. A probe that no production type satisfies is
// a permanently dead rung: the key silently falls through to the next one
// while the doc comment keeps promising a derivation that never happens.
// Add a rung to [dpKey] and it has to appear here with a real implementor.
func TestDPKeyProbesHaveProductionImplementations(t *testing.T) {
	t.Parallel()
	// One representative per source kind collectChannelCandidates feeds to
	// dpKey: the custom wrapper on a channel, a calculated DP, a generic DP.
	// Typed nil pointers suffice — a type assertion never dereferences.
	sources := []any{
		(*light.Light)(nil),
		(*climate.Climate)(nil),
		(*calculated.DewPointSensor)(nil),
		(*generic.Float)(nil),
	}
	probes := []struct {
		name    string
		matches func(any) bool
	}{
		{"dataPointKeyed", func(s any) bool { _, ok := s.(dataPointKeyed); return ok }},
		{"dataPointNamed", func(s any) bool { _, ok := s.(dataPointNamed); return ok }},
	}
	for _, probe := range probes {
		if !slices.ContainsFunc(sources, probe.matches) {
			t.Errorf("no production data-point type implements %s — the dpKey rung using it can never run", probe.name)
		}
	}
}
