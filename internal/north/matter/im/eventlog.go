// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package im

import (
	"sync"
	"time"
)

// EventRecord is one persisted event entry.
// Mirrors matter.js packages/protocol/src/interaction/EventHandler.ts::EventRecord.
//
// EpochMS is milliseconds since the Unix epoch (Matter §10.6.6.1
// EpochTimestamp is POSIX milliseconds — matter.js TlvPosixMs); Priority
// mirrors the [EventPriority] constants.
type EventRecord struct {
	// Number is the bridge-wide monotonic event counter stamped onto
	// every emitted event. Matter §10.6.6.5 requires the number to be
	// globally monotonic across the node; a single bridge-wide counter
	// satisfies that constraint and is simpler than per-cluster counters.
	Number uint64
	// Priority is the Matter §10.6.6.1 priority class.
	Priority EventPriority
	// Endpoint is the Matter endpoint ID that emitted the event.
	Endpoint uint16
	// Cluster is the Matter cluster ID.
	Cluster uint32
	// EventID is the event ID within the cluster.
	EventID uint32
	// EpochMS is milliseconds since the Unix epoch (Matter §10.6.6.1
	// EpochTimestamp, POSIX milliseconds).
	EpochMS uint64
	// Payload is the cluster-specific event struct (e.g. StartUpEvent,
	// ReachableChangedEvent). Encoding is handled by the cluster-specific
	// value writer in [bridge/reply.go]. `any` is justified: event
	// payloads vary per (cluster, event) pair and cannot be expressed in
	// a single Go type without fragmenting the interface.
	Payload any
}

// capCritical / capInfo / capDebug are the default per-priority bucket
// capacities. Matter §10.6.6.6 EventList sizing: Critical events SHALL
// be persisted across reboots; this buffer holds the most recent N per
// priority class. Oldest entries are evicted in FIFO order when a
// bucket is full.
const (
	capCritical = 64
	capInfo     = 32
	capDebug    = 16
)

// EventLog buffers emitted events for retrospective Read-Event queries.
// Bounded: keeps the most recent N entries per priority bucket.
// Older entries are evicted in FIFO order. Thread-safe.
//
// Matter §10.6.6 requires Critical events to be persisted; this
// implementation is an in-memory buffer (survives the session, not
// reboots). A persistent layer can be layered on top by draining the
// buffer at shutdown and replaying on boot — v1.1 leaves that for a
// later milestone.
//
// Mirrors matter.js packages/protocol/src/interaction/EventHandler.ts.
type EventLog struct {
	mu       sync.RWMutex
	next     uint64        // next event number; incremented before use so first event is 1
	critical []EventRecord // bounded FIFO, cap = capCrit
	info     []EventRecord // bounded FIFO, cap = capInfo
	debug    []EventRecord // bounded FIFO, cap = capDebug
	capCrit  int
	capInfo  int
	capDebug int

	// persistCeiling / persistEpoch / persistFn implement the
	// crash-safe monotonic EventNumber (Matter §7.14.2.1: event
	// numbers SHALL be monotonic and SHALL NOT reset on reboot —
	// controllers use EventMin filters keyed on the last number they
	// saw, so a reset makes them silently drop every fresh event).
	// Whenever the counter reaches persistCeiling, a new ceiling
	// (next + persistEpoch) is persisted BEFORE the number is handed
	// out; after a crash the log reseeds from the ceiling, skipping at
	// most one epoch of numbers but never reusing one. Mirrors chip's
	// EventManagement counter-epoch pattern
	// (CHIP_DEVICE_CONFIG_EVENT_ID_COUNTER_EPOCH) and the durable
	// numbering matter.js delegates to its EventStore
	// (OccurrenceManager.ts).
	persistCeiling uint64
	persistEpoch   uint64
	persistFn      func(ceiling uint64)
}

// NewEventLog constructs an EventLog with the default priority-bucket
// capacities (Critical=64, Info=32, Debug=16).
func NewEventLog() *EventLog {
	return &EventLog{
		capCrit:  capCritical,
		capInfo:  capInfo,
		capDebug: capDebug,
	}
}

// newEventLogWithCaps constructs an EventLog with custom capacities.
// Used by tests to force eviction with small caps.
func newEventLogWithCaps(critical, info, debug int) *EventLog {
	return &EventLog{
		capCrit:  critical,
		capInfo:  info,
		capDebug: debug,
	}
}

// Append records an event with a freshly-allocated monotonic Number and
// the current wall-clock epoch timestamp, then returns the assigned Number.
// The record is placed in the priority bucket corresponding to rec.Priority;
// the oldest entry in that bucket is evicted when the bucket is full.
func (l *EventLog) Append(rec EventRecord) uint64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.next++
	rec.Number = l.next
	// Persist a fresh ceiling BEFORE handing the number out so a crash
	// can never reuse it (see the persistCeiling field doc). The write
	// happens at most once per epoch, not per event.
	if l.persistFn != nil && l.next >= l.persistCeiling {
		l.persistCeiling = l.next + l.persistEpoch
		l.persistFn(l.persistCeiling)
	}
	if rec.EpochMS == 0 {
		rec.EpochMS = uint64(time.Now().UnixMilli()) //nolint:gosec // time.Now() is non-negative; see #20
	}
	switch rec.Priority {
	case EventPriorityCritical:
		l.critical = appendEvict(l.critical, rec, l.capCrit)
	case EventPriorityInfo:
		l.info = appendEvict(l.info, rec, l.capInfo)
	default: // Debug
		l.debug = appendEvict(l.debug, rec, l.capDebug)
	}
	return rec.Number
}

// SeedNumber raises the event-number counter to at least base. Called
// at boot with the persisted ceiling so numbering resumes past every
// number that may have been handed out before the previous shutdown
// (Matter §7.14.2.1 monotonicity). A base at or below the current
// counter is ignored — the counter never moves backwards.
func (l *EventLog) SeedNumber(base uint64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if base > l.next {
		l.next = base
	}
}

// SetCounterPersistence wires the durable half of the EventNumber
// counter: persist is invoked (under the log's lock, at most once per
// epoch) with the new ceiling whenever the counter reaches the current
// one. epoch <= 0 selects the default 0x10000, chip's
// CHIP_DEVICE_CONFIG_EVENT_ID_COUNTER_EPOCH. Pass nil to detach.
func (l *EventLog) SetCounterPersistence(persist func(ceiling uint64), epoch uint64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if epoch == 0 {
		epoch = 0x10000
	}
	l.persistFn = persist
	l.persistEpoch = epoch
	// Force a persist on the next Append so the ceiling reflects the
	// freshly-seeded counter even when no prior ceiling existed.
	l.persistCeiling = l.next
}

// appendEvict appends rec to buf; if len(buf) > limit after append, the
// oldest entry (index 0) is evicted (FIFO). Returns the updated slice.
func appendEvict(buf []EventRecord, rec EventRecord, limit int) []EventRecord {
	buf = append(buf, rec)
	if len(buf) > limit {
		// Shift left — drop the oldest entry.
		copy(buf, buf[1:])
		buf = buf[:limit]
	}
	return buf
}

// Query returns all EventRecords whose Number > minNumber and that
// match the supplied (endpoint, cluster, eventID) filter.
//
// Wildcard semantics follow chip-tool / Apple MTRDevice conventions:
//   - endpoint == 0xFFFF → match any endpoint
//   - cluster  == 0xFFFFFFFF → match any cluster
//   - eventID  == 0xFFFFFFFF → match any event
//
// Results are returned in ascending Number order. Callers typically
// pass minNumber=0 to retrieve all buffered events.
func (l *EventLog) Query(endpoint uint16, cluster, eventID uint32, minNumber uint64) []EventRecord {
	l.mu.RLock()
	defer l.mu.RUnlock()

	// Collect from all three buckets then sort by Number.
	var out []EventRecord
	for _, rec := range l.critical {
		if matchRecord(rec, endpoint, cluster, eventID, minNumber) {
			out = append(out, rec)
		}
	}
	for _, rec := range l.info {
		if matchRecord(rec, endpoint, cluster, eventID, minNumber) {
			out = append(out, rec)
		}
	}
	for _, rec := range l.debug {
		if matchRecord(rec, endpoint, cluster, eventID, minNumber) {
			out = append(out, rec)
		}
	}
	// Sort by Number ascending. Records within each bucket are already
	// monotonic; merging three sorted slices with a simple sort here is
	// correct (bucket sizes are bounded and small).
	sortByNumber(out)
	return out
}

// matchRecord returns true when rec matches the wildcard-aware filter
// and has a Number > minNumber.
func matchRecord(rec EventRecord, endpoint uint16, cluster, eventID uint32, minNumber uint64) bool {
	if rec.Number <= minNumber {
		return false
	}
	if endpoint != 0xFFFF && rec.Endpoint != endpoint {
		return false
	}
	if cluster != 0xFFFFFFFF && rec.Cluster != cluster {
		return false
	}
	if eventID != 0xFFFFFFFF && rec.EventID != eventID {
		return false
	}
	return true
}

// sortByNumber sorts recs in-place by EventRecord.Number ascending.
// Uses insertion sort — bucket sizes are bounded (≤ 64+32+16=112)
// so insertion sort is faster than sort.Slice's overhead for small n.
func sortByNumber(recs []EventRecord) {
	for i := 1; i < len(recs); i++ {
		key := recs[i]
		j := i - 1
		for j >= 0 && recs[j].Number > key.Number {
			recs[j+1] = recs[j]
			j--
		}
		recs[j+1] = key
	}
}
