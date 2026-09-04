// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package visibility

import (
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestUnIgnoreDoesNotLeakAcrossCentrals proves the decider CAN scope an
// un-ignore entry to one central: a *ParameterDecider is shared by every
// central's DevicePipeline (multi-CCU is first class, ADR 0002), and an entry
// carrying a Central only answers for that central.
//
// It proves nothing about the running daemon. The entry below is constructed
// here with Central set; production never sets that field (see
// [UnIgnoreEntry.Central]), because the composition root unions every
// central's persisted patterns into one stream before parsing. So this is a
// test of the mechanism, not of the wiring — un-ignoring BOOST_TIME on one
// CCU does un-ignore it fleet-wide today. Read
// TestParseUnIgnore_ProducesFleetWideEntries below for what the live path
// actually yields.
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

// TestParseUnIgnore_ProducesFleetWideEntries measures what the live path
// produces, as opposed to what the test above constructs: every entry the
// parser yields carries an empty Central, so the per-central guard in
// matchesUnIgnoreLocked cannot fire on a production entry and one CCU's
// un-ignore decides visibility for every CCU sharing the decider.
//
// The assertion is deliberately on the parser rather than on the decider:
// [Registry.LoadUnIgnore] — the only path the daemon uses to install
// un-ignore rules — installs exactly what [ParseUnIgnore] returns, so this is
// where the central dimension is lost. It fails the moment a caller starts
// stamping the field, which is the point at which the documentation on
// [UnIgnoreEntry.Central] and on IsParameterIgnored has to be rewritten.
func TestParseUnIgnore_ProducesFleetWideEntries(t *testing.T) {
	t.Parallel()

	const param = hmenum.Parameter("BOOST_TIME")
	const model, channelType = "HM-CC-RT-DN", "CLIMATECONTROL_RT_TRANSCEIVER"

	// Control: without any un-ignore rule the parameter is ignored, so the
	// assertion below measures the rule rather than the default.
	if !NewParameterDecider(nil).IsParameterIgnoredForCentral(
		"ccu-02", model, channelType, 1, hmenum.ParamsetKeyValues, param,
	) {
		t.Fatalf("%s is not ignored by default; pick a parameter that is, "+
			"otherwise the leak assertion below proves nothing", param)
	}

	entries, err := ParseUnIgnore(strings.NewReader(string(param) + "\n"))
	if err != nil {
		t.Fatalf("ParseUnIgnore: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("parsed %d entries, want 1", len(entries))
	}
	if entries[0].Central != "" {
		t.Errorf("entry %q carries Central=%q; the parser has no central context, "+
			"so a stamped entry means the surrounding documentation is stale",
			entries[0].Parameter, entries[0].Central)
	}

	// And the consequence, stated as behaviour: an entry the daemon loads
	// answers for a central it was never registered against. Persisting the
	// pattern for ccu-01 alone still un-ignores it on ccu-02.
	d := NewParameterDecider(nil)
	d.LoadUnIgnore(entries)
	if d.IsParameterIgnoredForCentral("ccu-02", model, channelType, 1, hmenum.ParamsetKeyValues, param) {
		t.Errorf("%s is ignored on ccu-02 — un-ignore rules became per-central; "+
			"update the scoping documentation on UnIgnoreEntry.Central and "+
			"IsParameterIgnored with the change", param)
	}
}
