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

// BufferConfig sizes the event buffer. Ported from matter.js HEAD
// packages/protocol/src/events/OccurrenceManager.ts (BufferConfig): one
// buffer across all priorities, harvested down to MinEventAllowance when it
// grows past MaxEventAllowance, with a floor under the non-critical classes
// so a burst of Debug traffic cannot starve Info entirely.
//
// The floors deliberately cover only Info and Debug. Critical has none
// because it needs none: the harvest drops Critical last, so it keeps
// whatever the other two classes leave — which is what Matter §10.6.6 asks
// for when it requires Critical events to survive.
type BufferConfig struct {
	// MinEventAllowance is the size the buffer is harvested down to.
	MinEventAllowance int
	// MaxEventAllowance is the size at which harvesting starts.
	MaxEventAllowance int
	// MinInfoAllowance is the number of most-recent Info records the
	// harvest will not touch while any droppable record of a lower class
	// remains.
	MinInfoAllowance int
	// MinDebugAllowance is the same floor for Debug records.
	MinDebugAllowance int
}

// Default buffer sizing. The shape is matter.js's; the numbers are scaled
// down from its 10 000 / 11 000 / 2 000 / 2 000, which target a generic node
// rather than a daemon that also holds a full CCU device model. The
// divergence is recorded in notes/parity/by_design.md.
//
// The predecessor of this buffer was three fixed FIFOs (critical 64, info 32,
// debug 16). A per-class cap drops a Critical record while the Info class
// sits empty: one CCU interface flap flips every bridged device's Reachable
// at once, and 64 of those were enough to evict the StartUp and BootReason
// events a controller reads at Subscribe-Initial.
const (
	defaultMinEventAllowance = 2000
	defaultMaxEventAllowance = 2200
	defaultMinInfoAllowance  = 400
	defaultMinDebugAllowance = 200
)

// DefaultBufferConfig returns the sizing [NewEventLog] uses.
func DefaultBufferConfig() BufferConfig {
	return BufferConfig{
		MinEventAllowance: defaultMinEventAllowance,
		MaxEventAllowance: defaultMaxEventAllowance,
		MinInfoAllowance:  defaultMinInfoAllowance,
		MinDebugAllowance: defaultMinDebugAllowance,
	}
}

// normalized returns cfg with any unset or contradictory field replaced by a
// usable value, so a caller cannot construct a buffer that harvests to a size
// above the one that triggers harvesting (which would loop) or to zero.
func (c BufferConfig) normalized() BufferConfig {
	if c.MinEventAllowance <= 0 {
		c.MinEventAllowance = defaultMinEventAllowance
	}
	if c.MaxEventAllowance <= c.MinEventAllowance {
		c.MaxEventAllowance = c.MinEventAllowance + max(1, c.MinEventAllowance/10)
	}
	if c.MinInfoAllowance < 0 {
		c.MinInfoAllowance = 0
	}
	if c.MinDebugAllowance < 0 {
		c.MinDebugAllowance = 0
	}
	return c
}

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
	mu   sync.RWMutex
	next uint64 // next event number; incremented before use so first event is 1
	// occurrences holds every buffered record across all priorities, in
	// append order and therefore ascending by Number. One list rather than
	// one per class is what lets the harvest spend a full-buffer budget on
	// the least valuable records wherever they sit. Mirrors matter.js
	// OccurrenceManager.ts #occurrences.
	occurrences []EventRecord
	buf         BufferConfig

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

// NewEventLog constructs an EventLog with [DefaultBufferConfig].
func NewEventLog() *EventLog {
	return NewEventLogWithBuffer(DefaultBufferConfig())
}

// NewEventLogWithBuffer constructs an EventLog with custom buffer sizing.
// Unset or contradictory fields fall back to the defaults; see
// [BufferConfig.normalized].
func NewEventLogWithBuffer(cfg BufferConfig) *EventLog {
	return &EventLog{buf: cfg.normalized()}
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
	l.occurrences = append(l.occurrences, rec)
	l.harvestLocked()
	return rec.Number
}

// harvestLocked drops the least valuable records once the buffer has grown
// past MaxEventAllowance, bringing it back to MinEventAllowance. A no-op
// below that threshold, so the cost is paid once per MaxEventAllowance -
// MinEventAllowance appends rather than on every one.
//
// Ported from matter.js HEAD
// packages/protocol/src/events/OccurrenceManager.ts #dropOldOccurrences.
// Two rules carry the behaviour:
//
//   - Classes are harvested in the order Debug, Info, Critical, so a
//     Critical record is dropped only once nothing else can be. This is what
//     keeps the boot-once StartUp / BootReason events readable while
//     ordinary traffic churns through the buffer.
//   - Within Debug and Info the most recent MinInfoAllowance /
//     MinDebugAllowance records are off limits while any older droppable
//     record remains, so a Debug flood cannot take the whole Info class with
//     it. Critical has no such floor and needs none — it is harvested last.
//
// Within one class the oldest record goes first.
func (l *EventLog) harvestLocked() {
	if len(l.occurrences) <= l.buf.MaxEventAllowance {
		return
	}
	toDrop := len(l.occurrences) - l.buf.MinEventAllowance
	if toDrop <= 0 {
		return
	}

	// Walk backwards to find, per protected class, the index at which its
	// floor begins: everything from there on is the most-recent run the
	// harvest must leave alone.
	floors := map[EventPriority]int{
		EventPriorityInfo:  l.buf.MinInfoAllowance,
		EventPriorityDebug: l.buf.MinDebugAllowance,
	}
	protectedFrom := map[EventPriority]int{
		EventPriorityInfo:  len(l.occurrences),
		EventPriorityDebug: len(l.occurrences),
	}
	counted := map[EventPriority]int{}
	for i := len(l.occurrences) - 1; i >= 0; i-- {
		p := l.occurrences[i].Priority
		floor, guarded := floors[p]
		if !guarded || counted[p] >= floor {
			continue
		}
		counted[p]++
		protectedFrom[p] = i
	}

	drop := make([]bool, len(l.occurrences))
	dropped := 0
	for _, p := range []EventPriority{EventPriorityDebug, EventPriorityInfo, EventPriorityCritical} {
		limit, guarded := protectedFrom[p]
		if !guarded {
			// Critical: the whole buffer is in reach, floors do not apply.
			limit = len(l.occurrences)
		}
		for i := 0; i < limit && dropped < toDrop; i++ {
			if l.occurrences[i].Priority == p && !drop[i] {
				drop[i] = true
				dropped++
			}
		}
		if dropped >= toDrop {
			break
		}
	}

	kept := l.occurrences[:0]
	for i, rec := range l.occurrences {
		if !drop[i] {
			kept = append(kept, rec)
		}
	}
	l.occurrences = kept
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

// Query returns all EventRecords whose Number >= minNumber and that
// match the supplied (endpoint, cluster, eventID) filter.
//
// The lower bound is INCLUSIVE: minNumber is the EventFilterIB.EventMin
// the controller sent, and Matter §10.6.9 treats EventMin as the minimum
// EventNumber of interest — the record whose Number == EventMin is
// returned, not skipped. Mirrors matter.js
// packages/protocol/src/events/OccurrenceManager.ts
// #findMinEventNumberIndex ("first event number that is greater than or
// equal to eventMin") and chip src/app/EventManagement.cpp
// IncludeEventInReport (drops only mCurrentEventNumber < mStartingEventNumber).
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

	// One buffer in append order, so the result is already ascending by
	// Number — the order the wire requires (see [BuildEventReports]).
	var out []EventRecord
	for _, rec := range l.occurrences {
		if matchRecord(rec, endpoint, cluster, eventID, minNumber) {
			out = append(out, rec)
		}
	}
	return out
}

// matchRecord returns true when rec matches the wildcard-aware filter
// and has a Number >= minNumber (EventMin is an inclusive lower bound —
// see [EventLog.Query]).
func matchRecord(rec EventRecord, endpoint uint16, cluster, eventID uint32, minNumber uint64) bool {
	if rec.Number < minNumber {
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
