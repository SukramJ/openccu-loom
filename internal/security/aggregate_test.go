// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package security

import (
	"fmt"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/security"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// addAggSource registers one classified source directly on the
// aggregate, bypassing the index builder so a test can pin the class
// and relevance it needs without standing up a device model.
func addAggSource(a *aggregate, central string, channelSuffix int, class hmenum.SecurityClass, relevant bool) (key string, ref hmevent.SecuritySourceRef) {
	ref = hmevent.NewSecuritySourceRef(central, "HmIP-RF", fmt.Sprintf("ADDR%d:1", channelSuffix), "STATE")
	ref.Class = class
	key = ref.Ref
	a.sources[key] = &indexedSource{ref: ref, class: class, relevant: relevant}
	return key, ref
}

// activateAggSource registers a relevant, currently-active source of the
// given class at an arbitrary activation time.
func activateAggSource(a *aggregate, central string, channelSuffix int, class hmenum.SecurityClass) {
	key, _ := addAggSource(a, central, channelSuffix, class, true)
	a.active[key] = int64(channelSuffix)
}

// TestAggregateSetActiveReportsMovementOnlyOnChange verifies setActive
// returns true exactly on a real transition and false on a repeated
// call with the same value, and that the activation timestamp is
// recorded and removed correctly.
func TestAggregateSetActiveReportsMovementOnlyOnChange(t *testing.T) {
	a := newAggregate()

	if !a.setActive("k1", true, 100) {
		t.Fatal("first activation must report movement")
	}
	if got := a.active["k1"]; got != 100 {
		t.Errorf("active[k1] = %d, want 100", got)
	}
	if a.setActive("k1", true, 200) {
		t.Error("repeated activation (still active) must not report movement")
	}
	if got := a.active["k1"]; got != 100 {
		t.Errorf("active[k1] = %d, want 100 (must not be overwritten by a no-op call)", got)
	}

	if !a.setActive("k1", false, 300) {
		t.Fatal("deactivation must report movement")
	}
	if _, on := a.active["k1"]; on {
		t.Error("k1 must be absent from active map after deactivation")
	}
	if a.setActive("k1", false, 400) {
		t.Error("repeated deactivation (still inactive) must not report movement")
	}
}

// TestAggregateClassStateCountsOnlyRelevantSourcesOfClass verifies Known
// counts only the relevant sources of the requested class: a
// non-relevant source of the same class, and a relevant source of a
// different class, must not contribute.
func TestAggregateClassStateCountsOnlyRelevantSourcesOfClass(t *testing.T) {
	a := newAggregate()
	addAggSource(a, "ccu1", 1, hmenum.SecurityClassSmoke, true)
	addAggSource(a, "ccu1", 2, hmenum.SecurityClassSmoke, true)
	addAggSource(a, "ccu1", 3, hmenum.SecurityClassSmoke, false) // not relevant: must not count
	addAggSource(a, "ccu1", 4, hmenum.SecurityClassWater, true)  // different class: must not count

	st := a.classState(hmenum.SecurityClassSmoke)
	if st.Known != 2 {
		t.Errorf("Known = %d, want 2", st.Known)
	}
}

// TestAggregateClassStateSourcesSortedByAtMS verifies the active source
// list of a class is ordered oldest-first regardless of insertion order.
func TestAggregateClassStateSourcesSortedByAtMS(t *testing.T) {
	a := newAggregate()
	k1, ref1 := addAggSource(a, "ccu1", 1, hmenum.SecurityClassSmoke, true)
	k2, ref2 := addAggSource(a, "ccu1", 2, hmenum.SecurityClassSmoke, true)
	k3, ref3 := addAggSource(a, "ccu1", 3, hmenum.SecurityClassSmoke, true)
	a.active[k2] = 300
	a.active[k1] = 100
	a.active[k3] = 200

	st := a.classState(hmenum.SecurityClassSmoke)
	if len(st.Sources) != 3 {
		t.Fatalf("len(Sources) = %d, want 3", len(st.Sources))
	}
	wantOrder := []string{ref1.Ref, ref3.Ref, ref2.Ref} // AtMS 100, 200, 300
	for i, want := range wantOrder {
		if st.Sources[i].Ref != want {
			t.Errorf("Sources[%d].Ref = %q, want %q", i, st.Sources[i].Ref, want)
		}
	}
}

// TestAggregateClassStateCentralsDedupedAndSorted verifies Centrals lists
// every contributing central exactly once, alphabetically.
func TestAggregateClassStateCentralsDedupedAndSorted(t *testing.T) {
	a := newAggregate()
	k1, _ := addAggSource(a, "ccuB", 1, hmenum.SecurityClassSmoke, true)
	k2, _ := addAggSource(a, "ccuA", 2, hmenum.SecurityClassSmoke, true)
	k3, _ := addAggSource(a, "ccuA", 3, hmenum.SecurityClassSmoke, true) // duplicate central
	a.active[k1] = 100
	a.active[k2] = 200
	a.active[k3] = 300

	st := a.classState(hmenum.SecurityClassSmoke)
	want := []string{"ccuA", "ccuB"}
	if len(st.Centrals) != len(want) {
		t.Fatalf("Centrals = %v, want %v", st.Centrals, want)
	}
	for i := range want {
		if st.Centrals[i] != want[i] {
			t.Errorf("Centrals[%d] = %q, want %q", i, st.Centrals[i], want[i])
		}
	}
}

// TestAggregateSeverityPrecedence pins the load-bearing fold order:
// smoke (critical) outranks water (alarm) outranks tamper (warning)
// outranks technical (info); an unhealthy engine alone contributes a
// warning; an empty aggregate is ok.
func TestAggregateSeverityPrecedence(t *testing.T) {
	t.Run("empty aggregate yields ok", func(t *testing.T) {
		a := newAggregate()
		if got := a.severity(); got != hmenum.SecuritySeverityOK {
			t.Errorf("severity() = %q, want %q", got, hmenum.SecuritySeverityOK)
		}
	})

	t.Run("unhealthy engine alone yields warning", func(t *testing.T) {
		a := newAggregate()
		a.engineHealthy = false
		if got := a.severity(); got != hmenum.SecuritySeverityWarning {
			t.Errorf("severity() = %q, want %q", got, hmenum.SecuritySeverityWarning)
		}
	})

	t.Run("smoke outranks water, tamper and technical", func(t *testing.T) {
		a := newAggregate()
		activateAggSource(a, "ccu1", 1, hmenum.SecurityClassSmoke)
		activateAggSource(a, "ccu1", 2, hmenum.SecurityClassWater)
		activateAggSource(a, "ccu1", 3, hmenum.SecurityClassTamper)
		activateAggSource(a, "ccu1", 4, hmenum.SecurityClassTechnical)
		if got := a.severity(); got != hmenum.SecuritySeverityCritical {
			t.Errorf("severity() = %q, want %q", got, hmenum.SecuritySeverityCritical)
		}
	})

	t.Run("water outranks tamper and technical", func(t *testing.T) {
		a := newAggregate()
		activateAggSource(a, "ccu1", 1, hmenum.SecurityClassWater)
		activateAggSource(a, "ccu1", 2, hmenum.SecurityClassTamper)
		activateAggSource(a, "ccu1", 3, hmenum.SecurityClassTechnical)
		if got := a.severity(); got != hmenum.SecuritySeverityAlarm {
			t.Errorf("severity() = %q, want %q", got, hmenum.SecuritySeverityAlarm)
		}
	})

	t.Run("tamper outranks technical", func(t *testing.T) {
		a := newAggregate()
		activateAggSource(a, "ccu1", 1, hmenum.SecurityClassTamper)
		activateAggSource(a, "ccu1", 2, hmenum.SecurityClassTechnical)
		if got := a.severity(); got != hmenum.SecuritySeverityWarning {
			t.Errorf("severity() = %q, want %q", got, hmenum.SecuritySeverityWarning)
		}
	})

	t.Run("technical alone yields info", func(t *testing.T) {
		a := newAggregate()
		activateAggSource(a, "ccu1", 1, hmenum.SecurityClassTechnical)
		if got := a.severity(); got != hmenum.SecuritySeverityInfo {
			t.Errorf("severity() = %q, want %q", got, hmenum.SecuritySeverityInfo)
		}
	})
}

// TestAggregateSnapshotOmitsClassWithNoKnownSources verifies a class the
// index knows nothing about is absent from the map — not present with
// Active=false — so an installation without gas detectors never
// advertises a permanently-off gas alarm.
func TestAggregateSnapshotOmitsClassWithNoKnownSources(t *testing.T) {
	a := newAggregate()
	activateAggSource(a, "ccu1", 1, hmenum.SecurityClassSmoke)

	snap := a.snapshot()
	if _, ok := snap.Classes[hmenum.SecurityClassSmoke]; !ok {
		t.Error("Classes must contain smoke (Known>0)")
	}
	if _, ok := snap.Classes[hmenum.SecurityClassGas]; ok {
		t.Error("Classes must not contain gas (Known==0): key must be absent, not present-and-false")
	}
}

// TestAggregateDropCentralRemovesOnlyThatCentralsTraces verifies the
// multi-CCU teardown: dropCentral removes exactly one central's sources,
// active entries and faults, leaving another central's state untouched.
// Without this, a removed CCU pins its class permanently active.
func TestAggregateDropCentralRemovesOnlyThatCentralsTraces(t *testing.T) {
	a := newAggregate()
	k1, _ := addAggSource(a, "ccu1", 1, hmenum.SecurityClassSmoke, true)
	a.active[k1] = 100
	k2, _ := addAggSource(a, "ccu2", 2, hmenum.SecurityClassSmoke, true)
	a.active[k2] = 200

	a.faults["f1"] = &security.Fault{ID: "f1", Source: hmevent.SecuritySourceRef{Central: "ccu1", Ref: "f1ref"}}
	a.faults["f2"] = &security.Fault{ID: "f2", Source: hmevent.SecuritySourceRef{Central: "ccu2", Ref: "f2ref"}}

	a.dropCentral("ccu1")

	if _, ok := a.sources[k1]; ok {
		t.Error("ccu1 source must be removed")
	}
	if _, ok := a.active[k1]; ok {
		t.Error("ccu1 active entry must be removed")
	}
	if _, ok := a.faults["f1"]; ok {
		t.Error("ccu1 fault must be removed")
	}

	if _, ok := a.sources[k2]; !ok {
		t.Error("ccu2 source must remain")
	}
	if _, ok := a.active[k2]; !ok {
		t.Error("ccu2 active entry must remain")
	}
	if _, ok := a.faults["f2"]; !ok {
		t.Error("ccu2 fault must remain")
	}
}

// TestAggregateSnapshotDeepCopiesZones verifies mutating a returned
// zone's ByClass or Sources does not reach back into the aggregate's
// own state.
func TestAggregateSnapshotDeepCopiesZones(t *testing.T) {
	a := newAggregate()
	a.zones["z1"] = security.ZoneState{
		ID:   "z1",
		Slug: "z1",
		ByClass: map[hmenum.SecurityClass][]string{
			hmenum.SecurityClassIntrusion: {"Sensor A"},
		},
		Sources: []hmevent.SecuritySourceRef{{Ref: "r1"}},
	}

	snap := a.snapshot()
	zone := snap.Zones["z1"]
	zone.ByClass[hmenum.SecurityClassIntrusion][0] = "MUTATED"
	zone.Sources[0].Ref = "mutated"

	original := a.zones["z1"]
	if original.ByClass[hmenum.SecurityClassIntrusion][0] != "Sensor A" {
		t.Errorf("original ByClass was mutated through the snapshot: %v", original.ByClass)
	}
	if original.Sources[0].Ref != "r1" {
		t.Errorf("original Sources was mutated through the snapshot: %v", original.Sources)
	}
}
