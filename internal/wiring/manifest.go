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

// Phase says when a seam's collaborator is attached relative to
// south-bound bring-up. It is the property the audits keep finding
// broken: a caller that exists but runs before the value it needs.
//
// loom:reachable:reason="the type of Seam.Phase, set from the PhasePerCentral constant at every declaration site"
type Phase string

const (
	// PhasePerCentral is attached once per central, replayed over the
	// centrals already registered and run again for every later one.
	// Ordering relative to south-bound bring-up is therefore not a
	// property of the call site — that is the point of the observer.
	PhasePerCentral Phase = "per-central"
	// PhaseBeforeSouthbound must be attached before south-bound
	// bring-up starts, because it observes something bring-up emits.
	PhaseBeforeSouthbound Phase = "before-southbound"
	// PhaseAfterSouthbound must be attached after south-bound bring-up
	// completes, because it reads state bring-up produces.
	PhaseAfterSouthbound Phase = "after-southbound"
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
	// Phase is the ordering constraint.
	Phase Phase `json:"phase"`
	// Why states, in one sentence, what stops working when this seam
	// is absent.
	Why string `json:"why"`
}

// Manifest is the ledger of seams a daemon has actually wired.
//
// The zero value is not usable; construct with [NewManifest]. A nil
// *Manifest accepts declarations and drops them, so a subsystem
// constructed without one (tests, the CLI) wires normally instead of
// panicking — the daemon always has one, and the guard that reads it
// reads the daemon's.
type Manifest struct {
	mu    sync.RWMutex
	seams map[string]Seam
}

// NewManifest returns an empty manifest.
func NewManifest() *Manifest {
	return &Manifest{seams: make(map[string]Seam)}
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
	for _, s := range m.seams {
		out = append(out, s)
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
