// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hub

import (
	"strings"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/datapoint"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// --- HubDataPoint base type tests ---

// TestHubDataPointInitialStateUncertain verifies that a freshly created
// HubDataPoint reports state_uncertain = true (matches
// GenericHubDataPoint.__init__ which sets self._state_uncertain = True).
func TestHubDataPointInitialStateUncertain(t *testing.T) {
	dp := HubDataPoint{Name: "test"}
	if !dp.StateUncertain() {
		t.Fatal("fresh HubDataPoint must start state_uncertain=true")
	}
}

// TestHubDataPointMarkCertainAndUncertain verifies the two internal
// lifecycle transitions: markCertain clears the flag; markUncertain
// sets it again (models optimistic-write path in Sysvar).
func TestHubDataPointMarkCertainAndUncertain(t *testing.T) {
	dp := HubDataPoint{Name: "x"}
	if !dp.StateUncertain() {
		t.Fatal("expected initial uncertain=true")
	}
	dp.markCertain()
	if dp.StateUncertain() {
		t.Fatal("after markCertain() uncertain must be false")
	}
	dp.markUncertain()
	if !dp.StateUncertain() {
		t.Fatal("after markUncertain() uncertain must be true again")
	}
}

// TestHubDataPointSignature verifies that Signature() returns the Name.
func TestHubDataPointSignature(t *testing.T) {
	dp := HubDataPoint{Name: "MyVar"}
	if got := dp.Signature(); got != "MyVar" {
		t.Fatalf("Signature()=%q want %q", got, "MyVar")
	}
}

// TestHubDataPointerPolymorphism verifies that both *Sysvar and *Program
// satisfy the HubDataPointer interface so north-bound adapters can use
// a single []HubDataPointer slice to iterate both types.
func TestHubDataPointerPolymorphism(t *testing.T) {
	sv := &Sysvar{HubDataPoint: HubDataPoint{Name: "Presence"}, ValueType: hmenum.HubValueTypeLogic}
	pg := &Program{HubDataPoint: HubDataPoint{Name: "Evening"}, ID: "p1"}

	// Both must satisfy the interface at compile time (checked by
	// the variable assignments below) and at runtime.
	var _ HubDataPointer = sv
	var _ HubDataPointer = pg

	// Collect them polymorphically.
	items := []HubDataPointer{sv, pg}
	names := make([]string, 0, len(items))
	for _, item := range items {
		names = append(names, item.Signature())
	}
	if len(names) != 2 || names[0] != "Presence" || names[1] != "Evening" {
		t.Fatalf("polymorphic Signature() = %v, want [Presence Evening]", names)
	}
}

// TestSysvarStateUncertainClearedOnValue verifies that OnValue clears
// The HubDataPoint.StateUncertain flag
// GenericSysvarDataPoint.write_value(state_uncertain = False).
func TestSysvarStateUncertainClearedOnValue(t *testing.T) {
	sv := &Sysvar{HubDataPoint: HubDataPoint{Name: "sv"}}
	if !sv.StateUncertain() {
		t.Fatal("fresh Sysvar must be state_uncertain=true")
	}
	sv.OnValue(hmtypes.BoolValue(true))
	if sv.StateUncertain() {
		t.Fatal("after OnValue, Sysvar must report state_uncertain=false")
	}
}

// TestProgramStateUncertainClearedOnExecution verifies that
// OnExecution clears the HubDataPoint.StateUncertain flag, matching
// Update_data semantics.
func TestProgramStateUncertainClearedOnExecution(t *testing.T) {
	pg := &Program{HubDataPoint: HubDataPoint{Name: "prog"}, ID: "p1"}
	if !pg.StateUncertain() {
		t.Fatal("fresh Program must be state_uncertain=true")
	}
	pg.OnExecution(true, hmenum.ProgramTriggerAPI)
	if pg.StateUncertain() {
		t.Fatal("after OnExecution, Program must report state_uncertain=false")
	}
}

// TestHubDataPointConcurrentMarkCertainUncertain verifies that
// concurrent markCertain/markUncertain calls do not race. The
// race detector will surface any unsynchronised access.
func TestHubDataPointConcurrentMarkCertainUncertain(t *testing.T) {
	dp := HubDataPoint{Name: "race"}
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if i%2 == 0 {
				dp.markCertain()
			} else {
				dp.markUncertain()
			}
			_ = dp.StateUncertain()
		}(i)
	}
	wg.Wait()
}

// TestHubDataPointFieldsPromoted verifies that embedding promotes
// Name and Description to direct field access on Sysvar and Program.
func TestHubDataPointFieldsPromoted(t *testing.T) {
	sv := &Sysvar{HubDataPoint: HubDataPoint{Name: "MyVar", Description: "a variable"}}
	if sv.Name != "MyVar" {
		t.Fatalf("sv.Name = %q, want MyVar", sv.Name)
	}
	if sv.Description != "a variable" {
		t.Fatalf("sv.Description = %q, want 'a variable'", sv.Description)
	}
	pg := &Program{HubDataPoint: HubDataPoint{Name: "MyProg", Description: "a program"}, ID: "p1"}
	if pg.Name != "MyProg" {
		t.Fatalf("pg.Name = %q, want MyProg", pg.Name)
	}
	if pg.Description != "a program" {
		t.Fatalf("pg.Description = %q, want 'a program'", pg.Description)
	}
}

// TestHubDataPointEnabledDefault verifies that the EnabledDefault field is
// promoted and settable from both types.
func TestHubDataPointEnabledDefault(t *testing.T) {
	sv := &Sysvar{HubDataPoint: HubDataPoint{Name: "sv", EnabledDefault: true}}
	if !sv.EnabledDefault {
		t.Fatal("EnabledDefault should be true on Sysvar")
	}
	pg := &Program{HubDataPoint: HubDataPoint{Name: "pg", EnabledDefault: false}, ID: "p1"}
	if pg.EnabledDefault {
		t.Fatal("EnabledDefault should be false on Program")
	}
}

// --- Phase 5B: BaseDataPoint migration tests ---

// TestHubDataPointSatisfiesBaseDataPoint is a compile-time assertion
// that *HubDataPoint, *Sysvar, and *Program all satisfy the
// datapoint.BaseDataPoint interface via embedded BaseDataPointFields
// promotion. The test also exercises the three methods at runtime so
// the contract is not purely static.
func TestHubDataPointSatisfiesBaseDataPoint(t *testing.T) {
	t.Parallel()

	// compile-time interface satisfaction
	var _ datapoint.BaseDataPoint = &HubDataPoint{}
	var _ datapoint.BaseDataPoint = &Sysvar{}
	var _ datapoint.BaseDataPoint = &Program{}

	// runtime: methods must be callable and return sensible values
	sv := NewSysvar("ccu-01", "Anwesenheit", "Who is home", hmenum.HubValueTypeLogic, nil)
	if sv.UniqueID() == "" {
		t.Fatal("Sysvar.UniqueID() must not be empty")
	}
	if !sv.Visible() {
		t.Fatal("Sysvar.Visible() must default to true")
	}
	if !sv.EnabledByDefault() {
		t.Fatal("Sysvar.EnabledByDefault() must be true when EnabledDefault=true")
	}

	pg := NewProgram("ccu-01", "P1", "Evening", "Evening routine", false, nil)
	if pg.UniqueID() == "" {
		t.Fatal("Program.UniqueID() must not be empty")
	}
	if !pg.Visible() {
		t.Fatal("Program.Visible() must default to true")
	}
}

// TestSysvarUniqueIDFormat verifies the canonical UniqueID format
// "central::name" (empty address segment for hub DPs) produced by
// NewBaseDataPointFields when called from NewHubDataPoint.
func TestSysvarUniqueIDFormat(t *testing.T) {
	t.Parallel()
	sv := NewSysvar("ccu-home", "Anwesenheit", "", hmenum.HubValueTypeLogic, nil)
	id := sv.UniqueID()
	// expected: "ccu-home::Anwesenheit"
	if !strings.HasPrefix(id, "ccu-home:") {
		t.Fatalf("UniqueID() = %q, want prefix %q", id, "ccu-home:")
	}
	if !strings.HasSuffix(id, ":Anwesenheit") {
		t.Fatalf("UniqueID() = %q, want suffix %q", id, ":Anwesenheit")
	}
}

// TestProgramUniqueIDFormat verifies that NewProgram produces a
// UniqueID with the correct "central::programName" form.
func TestProgramUniqueIDFormat(t *testing.T) {
	t.Parallel()
	pg := NewProgram("ccu-01", "ISE_ID_42", "Abendmodus", "Evening mode", false, nil)
	id := pg.UniqueID()
	if !strings.Contains(id, "ccu-01") {
		t.Fatalf("UniqueID() = %q must contain central name %q", id, "ccu-01")
	}
	if !strings.HasSuffix(id, ":Abendmodus") {
		t.Fatalf("UniqueID() = %q, want suffix %q", id, ":Abendmodus")
	}
}

// TestSysvarSetForcedUsageNoCreate verifies that SetForcedUsage with
// DataPointUsageNoCreate flips Visible() to false, matching
func TestSysvarSetForcedUsageNoCreate(t *testing.T) {
	t.Parallel()
	sv := NewSysvar("ccu-01", "Hidden", "", hmenum.HubValueTypeLogic, nil)
	if !sv.Visible() {
		t.Fatal("before SetForcedUsage, Visible() must be true")
	}
	sv.SetForcedUsage(hmenum.DataPointUsageNoCreate)
	if sv.Visible() {
		t.Fatal("after SetForcedUsage(NoCreate), Visible() must be false")
	}
	if sv.EnabledByDefault() {
		t.Fatal("after SetForcedUsage(NoCreate), EnabledByDefault() must be false")
	}
}

// TestHubDataPointPublisherWiringNilSafe verifies that PublishUpdate
// with no publisher installed is a silent no-op and does not panic.
func TestHubDataPointPublisherWiringNilSafe(t *testing.T) {
	t.Parallel()
	sv := NewSysvar("ccu-01", "NoPublisher", "", hmenum.HubValueTypeLogic, nil)
	// No publisher installed — must not panic.
	sv.PublishUpdate(nil, hmtypes.BoolValue(true)) //nolint:staticcheck // nil ctx intentional in no-op test
}

// TestHubDataPointEnabledByDefaultRespectsField verifies that
// EnabledByDefault() returns the EnabledDefault field value when no
// forced usage is set, and delegates to BaseDataPointFields logic when
// a forced usage is present.
func TestHubDataPointEnabledByDefaultRespectsField(t *testing.T) {
	t.Parallel()

	// EnabledDefault = false, no forced usage → must return false
	dp := HubDataPoint{Name: "x", EnabledDefault: false}
	if dp.EnabledByDefault() {
		t.Fatal("EnabledByDefault() must be false when EnabledDefault=false and no forced usage")
	}

	// EnabledDefault = true, no forced usage → must return true
	dp2 := HubDataPoint{Name: "y", EnabledDefault: true}
	if !dp2.EnabledByDefault() {
		t.Fatal("EnabledByDefault() must be true when EnabledDefault=true and no forced usage")
	}

	// EnabledDefault = true, forced NoCreate → must return false
	dp3 := HubDataPoint{Name: "z", EnabledDefault: true}
	dp3.SetForcedUsage(hmenum.DataPointUsageNoCreate)
	if dp3.EnabledByDefault() {
		t.Fatal("EnabledByDefault() must be false when forced usage is NoCreate, even if EnabledDefault=true")
	}
}

// TestProgramIsInternalFlag verifies that M-4 is implemented:
// NewProgram propagates the isInternal flag and the field is readable.
func TestProgramIsInternalFlag(t *testing.T) {
	t.Parallel()

	regular := NewProgram("ccu-01", "P1", "Lights Off", "", false, nil)
	if regular.IsInternal {
		t.Fatal("regular program must not be marked internal")
	}

	internal := NewProgram("ccu-01", "Tmp_Internal", "Tmp prog", "", true, nil)
	if !internal.IsInternal {
		t.Fatal("Tmp_* program with isInternal=true must be marked internal")
	}
}

// TestHubDataPointerSatisfiesBaseDataPoint verifies that HubDataPointer
// is a superset of datapoint.BaseDataPoint — any value that satisfies
// HubDataPointer also satisfies BaseDataPoint.
func TestHubDataPointerSatisfiesBaseDataPoint(t *testing.T) {
	t.Parallel()
	sv := NewSysvar("ccu-01", "test", "", hmenum.HubValueTypeLogic, nil)
	pg := NewProgram("ccu-01", "P1", "prog", "", false, nil)

	// HubDataPointer includes BaseDataPoint by embedding; confirm both
	// types satisfy both interfaces.
	var _ HubDataPointer = sv
	var _ HubDataPointer = pg
	var _ datapoint.BaseDataPoint = sv
	var _ datapoint.BaseDataPoint = pg
}
