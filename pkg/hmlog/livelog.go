// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmlog

import (
	"log/slog"
	"strings"
	"sync"
	"time"
)

// DefaultLiveLogCapacity is the number of most-recent records the
// [LiveLog] ring retains for backfill and download. ~5 000 records is a
// few MB of JSON — enough to seed a freshly opened log viewer with
// recent context without holding a whole session in memory.
const DefaultLiveLogCapacity = 5000

// defaultLiveSubscriberBuffer is the per-subscriber channel depth. A
// subscriber that falls this far behind has records dropped (it resumes
// via Seq) rather than blocking the logging path.
const defaultLiveSubscriberBuffer = 256

// LogRecord is one structured log line in the [LiveLog] ring. Seq is a
// process-monotonic cursor the log viewer uses to resume a stream
// without gaps or duplicates (it maps onto the SSE Last-Event-ID).
type LogRecord struct {
	Seq    uint64         `json:"seq"`
	Time   time.Time      `json:"time"`
	Level  string         `json:"level"`
	Logger string         `json:"logger,omitempty"`
	Msg    string         `json:"msg"`
	Attrs  map[string]any `json:"attrs,omitempty"`

	// lvl is the numeric level kept for server-side min-level
	// filtering. It is unexported and never serialises.
	lvl slog.Level
}

// liveSubscriber is one attached live-stream consumer.
type liveSubscriber struct {
	ch       chan LogRecord
	minLevel slog.Level
}

// LiveLog is an always-on ring buffer of the most recent structured log
// records plus a fan-out to live-stream subscribers. A [TeeHandler]
// feeds it every record; the diagnostics log endpoints read the ring
// (backfill / download) and subscribe to it (SSE tail).
//
// All exported methods are safe for concurrent use.
type LiveLog struct {
	mu   sync.RWMutex
	ring []LogRecord
	cap  int
	next int    // next write index into ring
	full bool   // ring has wrapped at least once
	seq  uint64 // last assigned sequence number

	subs   map[uint64]*liveSubscriber
	nextID uint64
}

// NewLiveLog allocates a ring with the supplied capacity. A capacity
// <= 0 uses [DefaultLiveLogCapacity].
func NewLiveLog(capacity int) *LiveLog {
	if capacity <= 0 {
		capacity = DefaultLiveLogCapacity
	}
	return &LiveLog{
		ring: make([]LogRecord, capacity),
		cap:  capacity,
		subs: make(map[uint64]*liveSubscriber),
	}
}

// record ingests one slog.Record: it assigns a sequence number, stores
// the record in the ring (evicting the oldest when full) and fans it out
// to every subscriber whose min-level admits it. Delivery is
// send-or-drop so one stalled consumer can never block logging. bound
// carries the handler-bound attributes (e.g. central / interface) that
// do not appear on the raw slog.Record.
func (l *LiveLog) record(r slog.Record, bound []slog.Attr) {
	if l == nil {
		return
	}
	rec := buildLogRecord(r, bound)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.seq++
	rec.Seq = l.seq
	l.ring[l.next] = rec
	l.next++
	if l.next == l.cap {
		l.next = 0
		l.full = true
	}
	for _, s := range l.subs {
		if rec.lvl < s.minLevel {
			continue
		}
		// Non-blocking send under the lock is safe: the default arm
		// never blocks, so Subscribe / Unsubscribe (same lock) stay
		// responsive even under a flood.
		select {
		case s.ch <- rec:
		default:
		}
	}
}

// buildLogRecord converts a slog.Record (plus the handler-bound attrs)
// into a [LogRecord]. The well-known "logger" attribute is promoted to
// its own field; everything else lands in Attrs.
func buildLogRecord(r slog.Record, bound []slog.Attr) LogRecord {
	rec := LogRecord{
		Time:  r.Time.UTC(),
		Level: strings.ToLower(r.Level.String()),
		Msg:   r.Message,
		lvl:   r.Level,
	}
	attrs := make(map[string]any, len(bound)+r.NumAttrs())
	add := func(a slog.Attr) {
		if a.Key == "logger" && a.Value.Kind() == slog.KindString {
			rec.Logger = a.Value.String()
			return
		}
		attrs[a.Key] = attrValue(a.Value)
	}
	for _, a := range bound {
		add(a)
	}
	r.Attrs(func(a slog.Attr) bool {
		add(a)
		return true
	})
	if len(attrs) > 0 {
		rec.Attrs = attrs
	}
	return rec
}

// orderedLocked returns the ring contents oldest-first. The caller holds
// at least the read lock.
func (l *LiveLog) orderedLocked() []LogRecord {
	if !l.full {
		out := make([]LogRecord, l.next)
		copy(out, l.ring[:l.next])
		return out
	}
	out := make([]LogRecord, 0, l.cap)
	out = append(out, l.ring[l.next:]...)
	out = append(out, l.ring[:l.next]...)
	return out
}

// Snapshot returns the most recent records at or above minLevel,
// oldest-first. A limit <= 0 returns the whole (filtered) ring.
func (l *LiveLog) Snapshot(limit int, minLevel slog.Level) []LogRecord {
	l.mu.RLock()
	defer l.mu.RUnlock()
	all := l.orderedLocked()
	if minLevel > slog.LevelDebug {
		filtered := make([]LogRecord, 0, len(all))
		for _, rec := range all {
			if rec.lvl >= minLevel {
				filtered = append(filtered, rec)
			}
		}
		all = filtered
	}
	if limit > 0 && len(all) > limit {
		all = all[len(all)-limit:]
	}
	out := make([]LogRecord, len(all))
	copy(out, all)
	return out
}

// Since returns every retained record with Seq > seq at or above
// minLevel, oldest-first. Used to backfill a resuming stream without
// duplicating what the client already holds.
func (l *LiveLog) Since(seq uint64, minLevel slog.Level) []LogRecord {
	l.mu.RLock()
	defer l.mu.RUnlock()
	all := l.orderedLocked()
	out := make([]LogRecord, 0, len(all))
	for _, rec := range all {
		if rec.Seq <= seq || rec.lvl < minLevel {
			continue
		}
		out = append(out, rec)
	}
	return out
}

// LastSeq returns the sequence number of the most recently recorded
// line (0 when nothing has been logged yet).
func (l *LiveLog) LastSeq() uint64 {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.seq
}

// Subscribe registers a live consumer that receives every subsequent
// record at or above minLevel on the returned channel. The channel is
// buffered; a consumer that falls behind has records dropped (it
// resumes via Seq). The returned cancel detaches the subscriber and
// closes the channel; it is safe to call exactly once.
func (l *LiveLog) Subscribe(minLevel slog.Level) (events <-chan LogRecord, cancel func()) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.nextID++
	id := l.nextID
	sub := &liveSubscriber{
		ch:       make(chan LogRecord, defaultLiveSubscriberBuffer),
		minLevel: minLevel,
	}
	l.subs[id] = sub
	cancel = func() {
		l.mu.Lock()
		defer l.mu.Unlock()
		if _, ok := l.subs[id]; ok {
			delete(l.subs, id)
			close(sub.ch)
		}
	}
	return sub.ch, cancel
}

// Subscribers reports the number of attached live consumers. Intended
// for metrics / tests.
func (l *LiveLog) Subscribers() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return len(l.subs)
}
