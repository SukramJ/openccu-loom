// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package wiring

import (
	"strings"
	"testing"
)

func orderedSeam(name string, before, after []Mark) Seam {
	return Seam{
		Name:         name,
		Collaborator: "*thing",
		Phase:        PhaseOrdered,
		Before:       before,
		After:        after,
		Why:          "the feature behind it is off",
	}
}

// TestAttachRecordsAViolationWhenTheSeamMissedItsMark is the check the
// whole ordered-seam mechanism exists for: a collaborator handed over
// after the thing that reads it has already started.
//
// Nothing about that fails today — the setter stores the value, returns
// nil, and the feature is simply off. The manifest is what makes it
// answerable.
func TestAttachRecordsAViolationWhenTheSeamMissedItsMark(t *testing.T) {
	t.Parallel()

	m := NewManifest()
	m.Mark(MarkNorthBridgesStarted)

	attached := false
	m.Attach(orderedSeam("webhook.alarm_bus", []Mark{MarkNorthBridgesStarted}, nil),
		func() { attached = true })

	if !attached {
		t.Fatal("a violation must not stop the wiring: refusing to attach turns a reporting " +
			"problem into an outage")
	}
	got := m.Seams()
	if len(got) != 1 {
		t.Fatalf("declared %d seams, want 1", len(got))
	}
	if len(got[0].Violations) != 1 {
		t.Fatalf("seam attached after the mark it must precede reports %d violations, want 1: %+v",
			len(got[0].Violations), got[0].Violations)
	}
	if !strings.Contains(got[0].Violations[0], string(MarkNorthBridgesStarted)) {
		t.Errorf("the violation must name the mark; got %q", got[0].Violations[0])
	}
}

// TestAttachInTheRightOrderReportsNoViolation is the negative control
// for the test above: the same seam, the same mark, attached on the
// correct side.
func TestAttachInTheRightOrderReportsNoViolation(t *testing.T) {
	t.Parallel()

	m := NewManifest()
	m.Attach(orderedSeam("webhook.alarm_bus", []Mark{MarkNorthBridgesStarted}, nil), func() {})
	m.Mark(MarkNorthBridgesStarted)

	got := m.Seams()
	if len(got) != 1 || len(got[0].Violations) != 0 {
		t.Fatalf("correctly ordered seam reports violations: %+v", got)
	}
}

// TestAttachRecordsAViolationWhenAPrerequisiteHasNotRun covers the other
// direction: a seam that reads what a boot step produces, attached
// before that step ran.
func TestAttachRecordsAViolationWhenAPrerequisiteHasNotRun(t *testing.T) {
	t.Parallel()

	m := NewManifest()
	m.Attach(orderedSeam("backup.cache_invalidator", nil, []Mark{MarkSouthboundWired}), func() {})

	got := m.Seams()
	if len(got) != 1 || len(got[0].Violations) != 1 {
		t.Fatalf("seam attached before the mark it must follow reports %v", got)
	}
	if !strings.Contains(got[0].Violations[0], "has not produced yet") {
		t.Errorf("violation should say the prerequisite has not run; got %q", got[0].Violations[0])
	}
}

// TestMarkPassedTwicePanics pins the invariant. A boot sequence that
// crosses a boundary again is not something this package can reason
// about, and accepting it silently would make every constraint evaluated
// afterwards meaningless.
func TestMarkPassedTwicePanics(t *testing.T) {
	t.Parallel()

	m := NewManifest()
	m.Mark(MarkCentralsStarted)
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("passing the same mark twice must panic")
		}
		if !strings.Contains(toString(r), "passed twice") {
			t.Errorf("panic message %q should say the mark was passed twice", toString(r))
		}
	}()
	m.Mark(MarkCentralsStarted)
}

// TestPerCentralSeamWithMarksPanics keeps the two seam kinds apart. An
// observer replays over the centrals already registered, so its call
// site has no order to constrain, and a mark on one would read as a
// checked constraint while checking nothing.
func TestPerCentralSeamWithMarksPanics(t *testing.T) {
	t.Parallel()

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("a per-central seam carrying ordering marks must panic")
		}
	}()
	NewManifest().Declare(Seam{
		Name: "x.y", Collaborator: "*z", Phase: PhasePerCentral,
		Before: []Mark{MarkSouthboundWired}, Why: "w",
	})
}

// TestOrderedSeamWithoutMarksPanics is the mirror: an ordered seam with
// no constraint is either a per-central seam with the wrong phase, or a
// seam whose order does not matter and should not claim otherwise.
func TestOrderedSeamWithoutMarksPanics(t *testing.T) {
	t.Parallel()

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("an ordered seam naming no mark must panic")
		}
	}()
	NewManifest().Declare(Seam{
		Name: "x.y", Collaborator: "*z", Phase: PhaseOrdered, Why: "w",
	})
}

// TestNilManifestAttaches keeps the nil case usable: a subsystem built
// without a manifest wires normally instead of panicking.
func TestNilManifestAttaches(t *testing.T) {
	t.Parallel()

	var m *Manifest
	attached := false
	m.Attach(orderedSeam("x.y", []Mark{MarkSouthboundWired}, nil), func() { attached = true })
	if !attached {
		t.Error("a nil manifest must still run the attach")
	}
	m.Mark(MarkSouthboundWired)
	if got := m.Passed(); got != nil {
		t.Errorf("a nil manifest records nothing, got %v", got)
	}
}

func toString(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if e, ok := v.(error); ok {
		return e.Error()
	}
	return ""
}

// TestPhaseOnceRejectsMarks pins the pair of rules that keep the three
// phases from blurring: an ordered seam must name a mark, and a
// PhaseOnce seam must not.
//
// The second is the one worth a test. A constraint written into a
// PhaseOnce seam would read exactly like a checked one — the marks are
// right there in the diagnostics payload — while nothing evaluated it.
func TestPhaseOnceRejectsMarks(t *testing.T) {
	t.Parallel()

	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("a PhaseOnce seam naming ordering marks must panic")
		}
		if !strings.Contains(toString(r), "written down and not checked") {
			t.Errorf("panic should say the constraint would go unchecked; got %q", toString(r))
		}
	}()
	NewManifest().Declare(Seam{
		Name: "x.y", Collaborator: "*z", Phase: PhaseOnce,
		Before: []Mark{MarkSouthboundWired}, Why: "w",
	})
}

// TestPhaseOnceAttachesWithoutConstraints is the ordinary case: a seam
// with nothing to constrain still gets an entry, because the manifest's
// first promise is that a seam with no entry is unwired.
func TestPhaseOnceAttachesWithoutConstraints(t *testing.T) {
	t.Parallel()

	m := NewManifest()
	attached := false
	m.Attach(Seam{
		Name: "store.audit_overlay", Collaborator: "*sqlite.AuditStore",
		Phase: PhaseOnce, Why: "the change log records nothing",
	}, func() { attached = true })

	if !attached {
		t.Fatal("PhaseOnce must still run the attach")
	}
	got := m.Seams()
	if len(got) != 1 || len(got[0].Violations) != 0 {
		t.Fatalf("a seam with no constraint cannot be in violation: %+v", got)
	}
}

// TestUnknownPhaseIsRejected keeps a typo from producing a seam that no
// rule applies to.
func TestUnknownPhaseIsRejected(t *testing.T) {
	t.Parallel()

	defer func() {
		if r := recover(); r == nil {
			t.Fatal("an unknown phase must panic")
		}
	}()
	NewManifest().Declare(Seam{Name: "x.y", Collaborator: "*z", Phase: "whenever", Why: "w"})
}
