// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package wiring lets the composition root declare its wiring as data
// while it performs it, so that "is X wired" becomes a question a test
// can answer exactly rather than approximately (ADR 0065).
//
// The problem it solves is not that the composition root is written
// carelessly. It is that almost nothing about it is decidable: the
// guards that exist match a setter's *name* and so answer "yes, some
// function called something with that name exists" for a seam whose
// collaborator never arrives, or arrives too late. Every audit round
// has found fresh instances of that one class.
//
// A declared seam has no such gap. It is a value in a ledger the
// running daemon carries, so a test asks the daemon what it wired
// instead of asking the source code what it looks like — and a wire
// call that is deleted, skipped by a nil guard, or never reached takes
// its entry with it.
//
// This package deliberately starts with one seam class: the per-central
// registry observer ([OnCentral]). It is the class CLAUDE.md's second
// wiring rule names, it spans the composition root and four north-bound
// packages, and every one of its call sites has the same shape, so
// "declared" and "attached" can be compared exactly. Later phases widen
// the ledger to setter and struct-field seams; the shape of [Seam] is
// chosen with that in mind.
package wiring

import (
	"fmt"
	"sort"
	"sync"
)

// Phase says what kind of seam this is, which decides how its ordering
// is expressed.
//
// loom:reachable:reason="the type of Seam.Phase, set from the PhasePerCentral or PhaseOrdered constant at every declaration site"
type Phase string

const (
	// PhasePerCentral is attached once per central, replayed over the
	// centrals already registered and run again for every later one.
	// Ordering is not a property of such a call site — that is the point
	// of the observer, and why these seams carry no marks.
	PhasePerCentral Phase = "per-central"
	// PhaseOrdered is attached once, at a point in the boot sequence
	// that matters. Its constraints are the [Seam.Before] and
	// [Seam.After] marks.
	PhaseOrdered Phase = "ordered"
)

// Mark names a point the daemon passes during boot. An ordered seam
// states which marks it must precede and which it must follow, and the
// manifest records which had already been passed when the seam was
// actually attached — so "does this run before Y" stops being a property
// of line order in a 900-line function and becomes a comparison.
//
// The marks are few on purpose. Each one is a boundary something
// downstream genuinely depends on, not a convenient label for a place in
// the file.
//
// loom:reachable:reason="the element type of Seam.Before and Seam.After, set from the Mark* constants at the ordered declaration sites and passed to Manifest.Mark from cmd/openccu-loom/daemon.go"
type Mark string

const (
	// MarkCentralsStarted is passed once Registry.StartAll has returned,
	// so every configured central's scheduler and event bus are live.
	MarkCentralsStarted Mark = "centrals.started"
	// MarkSouthboundWired is passed once wireSouthbound has returned:
	// the per-central clients, device pipeline and paramset hydration
	// exist, and the values it produces can be read.
	MarkSouthboundWired Mark = "southbound.wired"
	// MarkNorthBridgesStarted is passed once every north-bound bridge
	// has been started. A bridge reads its collaborators at Start, so a
	// collaborator handed over after this mark is stored and never used.
	MarkNorthBridgesStarted Mark = "northbridges.started"
)

// Seam is one declared piece of wiring.
//
// Why is not decoration. A seam whose absence has no describable
// consequence is a seam nobody needs, and the sentence is what a
// reader of a failing guard needs first.
//
// loom:reachable:reason="constructed as a composite literal at all eighteen OnRegisterDeclared call sites across cmd/ and five internal/ packages, and returned by Manifest.Seams; a type reached only through literals, which the analyzer's heuristic does not follow"
type Seam struct {
	// Name is the stable identifier, in `<subsystem>.<what>` form —
	// e.g. "history.recorder". It is what a guard names and what the
	// diagnostics surface reports, so it outlives renames of the Go
	// function that declares it.
	Name string `json:"name"`
	// Collaborator names the thing being attached, for a reader.
	Collaborator string `json:"collaborator"`
	// Phase says whether this is a per-central observer or a once-only
	// ordered attachment.
	Phase Phase `json:"phase"`
	// Before are the marks this seam must be attached before. Handing a
	// collaborator over after such a mark compiles and runs; what it
	// does not do is take effect.
	Before []Mark `json:"before,omitempty"`
	// After are the marks this seam must be attached after, because it
	// reads something the marked step produces.
	After []Mark `json:"after,omitempty"`
	// Why states, in one sentence, what stops working when this seam
	// is absent.
	Why string `json:"why"`
	// Violations are the constraints that were already broken when the
	// seam was attached, in the manifest's own words. Empty is the
	// normal case; a non-empty list is a wiring defect a running daemon
	// reports about itself.
	Violations []string `json:"violations,omitempty"`
}

// Manifest is the ledger of seams a daemon has actually wired.
//
// The zero value is not usable; construct with [NewManifest]. A nil
// *Manifest accepts declarations and drops them, so a subsystem
// constructed without one (tests, the CLI) wires normally instead of
// panicking — the daemon always has one, and the guard that reads it
// reads the daemon's.
//
// loom:reachable:reason="held as central.Registry.manifest, built by NewManifest there and served through Registry.Manifest() to the GET /diagnostics/wiring handler; a type reached only through a struct field and a constructor, which the analyzer's heuristic does not follow"
type Manifest struct {
	mu    sync.RWMutex
	seams map[string]Seam
	// passed holds the marks the daemon has gone by, in order. An
	// ordered seam's constraints are evaluated against this set at the
	// moment it attaches, which is the only moment at which the answer
	// is a fact rather than a reading of the source.
	passed []Mark
}

// NewManifest returns an empty manifest.
func NewManifest() *Manifest {
	return &Manifest{seams: make(map[string]Seam)}
}

// Mark records that the daemon has passed m.
//
// It panics when the same mark is passed twice: a boot sequence that
// crosses one of these boundaries again is not a mark this package can
// reason about, and silently accepting it would make every constraint
// evaluated afterwards meaningless.
//
// invariant: each mark is passed at most once per manifest.
func (m *Manifest) Mark(mark Mark) {
	if m == nil {
		return
	}
	if mark == "" {
		panic("wiring: empty mark")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, p := range m.passed {
		if p == mark {
			panic(fmt.Sprintf("wiring: mark %q passed twice", mark))
		}
	}
	m.passed = append(m.passed, mark)
}

// Passed reports the marks the daemon has gone by, in order.
func (m *Manifest) Passed() []Mark {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	return append([]Mark(nil), m.passed...)
}

// Attach declares s, checks its ordering constraints against the marks
// passed so far, and then runs attach.
//
// This is the ordered counterpart of the per-central observer seam. The
// constraint it checks is the one the audits keep finding broken and
// which no name-matching guard can express: a collaborator handed over
// after the thing that reads it has already started. That compiles, it
// runs, the setter reports nothing — and the feature behind the seam is
// simply off. The webhook bridge is the worked example: it reads its
// alarm and security buses once, at Start, so a bus set afterwards is
// stored and never subscribed, and no alarm event is ever forwarded.
//
// A violation does not stop the wiring. Refusing to attach would turn a
// reporting problem into an outage, and the value of the manifest is
// that a running daemon can be asked; the violation is recorded on the
// seam, served by the diagnostics surface, and failed on by a test.
func (m *Manifest) Attach(s Seam, attach func()) {
	if m != nil {
		s.Violations = m.violations(s)
	}
	m.Declare(s)
	if attach != nil {
		attach()
	}
}

// violations reports the constraints of s that are already broken.
func (m *Manifest) violations(s Seam) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	passed := make(map[Mark]struct{}, len(m.passed))
	for _, p := range m.passed {
		passed[p] = struct{}{}
	}
	var out []string
	for _, mark := range s.Before {
		if _, gone := passed[mark]; gone {
			out = append(out, fmt.Sprintf(
				"attached after %q, which it must precede; the collaborator is stored but whatever reads it has already run",
				mark,
			))
		}
	}
	for _, mark := range s.After {
		if _, gone := passed[mark]; !gone {
			out = append(out, fmt.Sprintf(
				"attached before %q, which it must follow; it reads something that step has not produced yet",
				mark,
			))
		}
	}
	return out
}

// Declare records s.
//
// It panics on an incomplete seam or a duplicate name, because both are
// programming errors in wiring code that runs once at boot: a duplicate
// name means two subsystems answer to one identifier and the ledger can
// no longer say which of them is missing.
//
// invariant: every declared seam is complete and uniquely named.
func (m *Manifest) Declare(s Seam) {
	if m == nil {
		return
	}
	switch {
	case s.Name == "":
		panic("wiring: seam declared without a name")
	case s.Collaborator == "":
		panic(fmt.Sprintf("wiring: seam %q declared without a collaborator", s.Name))
	case s.Phase == "":
		panic(fmt.Sprintf("wiring: seam %q declared without a phase", s.Name))
	case s.Why == "":
		panic(fmt.Sprintf("wiring: seam %q declared without a reason; a seam whose absence has no consequence is a seam nobody needs", s.Name))
	case s.Phase == PhasePerCentral && (len(s.Before) > 0 || len(s.After) > 0):
		panic(fmt.Sprintf("wiring: per-central seam %q carries ordering marks; the observer replays over the centrals already registered, so its call site has no order to constrain", s.Name))
	case s.Phase == PhaseOrdered && len(s.Before) == 0 && len(s.After) == 0:
		panic(fmt.Sprintf("wiring: ordered seam %q names no mark; an ordered seam with no constraint is a per-central seam with the wrong phase, or a seam whose order does not matter and should say so", s.Name))
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, dup := m.seams[s.Name]; dup {
		panic(fmt.Sprintf("wiring: seam %q declared twice", s.Name))
	}
	m.seams[s.Name] = s
}

// Seams returns every declared seam in name order.
func (m *Manifest) Seams() []Seam {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Seam, 0, len(m.seams))
	for name := range m.seams {
		out = append(out, m.seams[name])
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Has reports whether a seam with that name was declared.
func (m *Manifest) Has(name string) bool {
	if m == nil {
		return false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.seams[name]
	return ok
}
