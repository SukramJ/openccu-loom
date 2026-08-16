// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package diagevent keeps a bounded, in-memory trace of the Matter
// events that explain a failed pairing.
//
// The existing diagnostics report state: which sessions are open, what
// mDNS advertises, which endpoints exist. State answers "what is wrong
// now" and cannot answer "what happened thirty seconds ago", which is
// the question an operator has after a controller refused to pair and
// then went quiet. The daemon logged those moments and nothing else —
// reaching them meant having log access, knowing what to grep for, and
// still having the log.
//
// The trace is deliberately small and lossy. It is a diagnostic, not an
// audit trail: the alarm journal and the audit log are the durable
// records, and neither should be competed with by something that sits on
// the receive path.
package diagevent

import (
	"sync"
	"time"
)

// Kind buckets an event so a reader can filter without parsing prose.
//
// loom:reachable:reason="set on every Event the bridge records in securechannel.go and serialised by GET /api/v1/matter/events; a string alias without methods, invisible to the analyzer's type heuristic"
type Kind string

// Kind values.
const (
	// KindPairing covers commissioning: window open and close, PASE
	// refusals, fabric added and removed.
	KindPairing Kind = "pairing"
	// KindSession covers secure sessions opening and closing: a
	// completed CASE handshake, and a peer's CloseSession.
	KindSession Kind = "session"
	// KindDiscovery covers mDNS advertisement changes, such as the
	// re-announce that follows a peer losing its last session.
	KindDiscovery Kind = "discovery"
)

// Severity separates the entries that explain a failure from the ones
// that merely record progress.
//
// loom:reachable:reason="set on every Event the bridge records in securechannel.go and serialised by GET /api/v1/matter/events; a string alias without methods, invisible to the analyzer's type heuristic"
type Severity string

// Severity values.
const (
	SeverityInfo    Severity = "info"
	SeverityWarning Severity = "warning"
	SeverityError   Severity = "error"
)

// Event is one recorded moment.
//
// loom:reachable:reason="constructed by the bridge's PASE, secure-session and mDNS re-announce paths in securechannel.go, returned by Bridge.DiagnosticEvents, and copied out by the GET /api/v1/matter/events handler; a data struct whose fields the REST layer reads, invisible to the analyzer's method-based type heuristic"
type Event struct {
	At       time.Time
	Kind     Kind
	Severity Severity
	// Message is one sentence an operator can act on. It is not a log
	// line: it says what happened, not which function noticed.
	Message string
	// Detail carries the identifiers that make the message specific —
	// fabric index, peer node id, the refusal reason.
	Detail map[string]string
}

// Ring is a fixed-capacity trace. The oldest entry is dropped to make
// room, so a burst of noise cannot push out the entries that follow it.
//
// The zero value is not usable; a nil *Ring is, and does nothing. That
// asymmetry is deliberate: the recording points sit on the Matter
// receive path, taken for every packet, and a diagnostic that can panic
// there is worse than no diagnostic.
//
// loom:reachable:reason="constructed by the composition root in daemon_matter.go (AttachDiagnosticEvents(diagevent.NewRing(...))) before the bridge starts serving, and held by Bridge.diagEvents; the analyzer reaches NewRing but not the type it returns"
type Ring struct {
	mu     sync.RWMutex
	events []Event
	next   int
	full   bool
}

// NewRing returns a ring holding at most capacity events.
//
// A capacity below one is raised to a small usable default rather than
// producing a ring that silently discards everything: the value reaches
// here from configuration, and an unset field must not switch the
// diagnostic off without saying so.
func NewRing(capacity int) *Ring {
	if capacity < 1 {
		capacity = defaultCapacity
	}
	return &Ring{events: make([]Event, capacity)}
}

// defaultCapacity is what an unset or nonsensical capacity becomes —
// enough to cover a pairing attempt and the minute around it.
const defaultCapacity = 200

// Record appends an event, dropping the oldest when full.
//
// Safe on a nil receiver and safe to call concurrently, because both are
// properties the call sites need: they are optional wiring on a
// concurrent receive path.
func (r *Ring) Record(e Event) {
	if r == nil {
		return
	}
	if e.At.IsZero() {
		e.At = time.Now()
	}
	if e.Severity == "" {
		e.Severity = SeverityInfo
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events[r.next] = e
	r.next = (r.next + 1) % len(r.events)
	if r.next == 0 {
		r.full = true
	}
}

// Snapshot returns the recorded events, newest first.
//
// The result is a copy: the caller serialises it while the receive path
// keeps recording.
func (r *Ring) Snapshot() []Event {
	if r == nil {
		return nil
	}
	r.mu.RLock()
	defer r.mu.RUnlock()

	count := r.next
	if r.full {
		count = len(r.events)
	}
	out := make([]Event, 0, count)
	for i := range count {
		idx := (r.next - 1 - i + len(r.events)*2) % len(r.events)
		out = append(out, r.events[idx])
	}
	return out
}
