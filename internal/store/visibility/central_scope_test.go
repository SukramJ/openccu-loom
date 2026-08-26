// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package visibility

import (
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestUnIgnoreDoesNotLeakAcrossCentrals pins the fix for the cache-coherency
// finding: a *ParameterDecider is shared by every central's DevicePipeline
// (multi-CCU is first class, ADR 0002), so an un-ignore entry registered for
// one central must not decide visibility for another. Before the fix the
// decider held one flat, central-agnostic unIgnore list: un-ignoring
// BOOST_TIME for central "A" made it visible on central "B" too, even though
// the REST/SPA surface presents un-ignore as a per-central control.
func TestUnIgnoreDoesNotLeakAcrossCentrals(t *testing.T) {
	t.Parallel()

	const param = hmenum.Parameter("BOOST_TIME")
	d := NewParameterDecider(nil)
	d.LoadUnIgnore([]UnIgnoreEntry{{Parameter: param, IsSimple: true, Central: "central-a"}})

	if d.IsParameterIgnoredForCentral("central-a", "HM-CC-RT-DN", "CLIMATECONTROL_RT_TRANSCEIVER", 1, hmenum.ParamsetKeyValues, param) {
		t.Error("central-a: BOOST_TIME still reported as ignored after its own un-ignore entry was loaded")
	}
	if !d.IsParameterIgnoredForCentral("central-b", "HM-CC-RT-DN", "CLIMATECONTROL_RT_TRANSCEIVER", 1, hmenum.ParamsetKeyValues, param) {
		t.Error("central-b: BOOST_TIME leaked as un-ignored from central-a's un-ignore entry (cross-central cache-coherency defect)")
	}
}
