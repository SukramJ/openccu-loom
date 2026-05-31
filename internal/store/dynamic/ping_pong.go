// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package dynamic

import (
	"sync"
	"time"
)

// =============================================================================
// PongTracker
// =============================================================================

// PongTracker tracks pending or unknown pong tokens with monotonic timestamps
// for TTL expiry.
type PongTracker struct {
	mu        sync.Mutex
	tokens    map[string]struct{}
	seenAt    map[string]time.Time // wall-clock insertion time for TTL
	logged    bool                 // whether a warning was already emitted
	evictions int                  // eviction counter for TrackerStatistics parity
}

// NewPongTracker returns an empty ready-to-use PongTracker.
func NewPongTracker() *PongTracker {
	return &PongTracker{
		tokens: make(map[string]struct{}),
		seenAt: make(map[string]time.Time),
	}
}

// Len returns the number of tracked tokens. Mirrors Python's __len__.
func (pt *PongTracker) Len() int {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	return len(pt.tokens)
}

// Add records a token with its wall-clock insertion time.
func (pt *PongTracker) Add(token string, at time.Time) {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	pt.tokens[token] = struct{}{}
	pt.seenAt[token] = at
}

// Contains reports whether the token is currently tracked.
func (pt *PongTracker) Contains(token string) bool {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	_, ok := pt.tokens[token]
	return ok
}

// Remove deletes a token and its timestamp from the tracker.
func (pt *PongTracker) Remove(token string) {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	delete(pt.tokens, token)
	delete(pt.seenAt, token)
}

// SeenAt returns the insertion time for a token and whether it was found.
func (pt *PongTracker) SeenAt(token string) (time.Time, bool) {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	t, ok := pt.seenAt[token]
	return t, ok
}

// Logged returns the logged flag (true = high-state warning already emitted).
func (pt *PongTracker) Logged() bool {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	return pt.logged
}

// SetLogged sets the logged flag.
func (pt *PongTracker) SetLogged(v bool) {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	pt.logged = v
}

// Clear removes all tokens, timestamps, and resets the logged flag.
func (pt *PongTracker) Clear() {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	pt.tokens = make(map[string]struct{})
	pt.seenAt = make(map[string]time.Time)
	pt.logged = false
}

// RecordEviction increments the internal eviction counter by count.
// This mirrors the TrackerStatistics.record_eviction semantics.
func (pt *PongTracker) RecordEviction(count int) {
	if count <= 0 {
		count = 1
	}
	pt.mu.Lock()
	pt.evictions += count
	pt.mu.Unlock()
}

// ResetStats resets the eviction counter to zero.
func (pt *PongTracker) ResetStats() {
	pt.mu.Lock()
	pt.evictions = 0
	pt.mu.Unlock()
}

// Evictions returns the current eviction count (for metrics / diagnostics).
func (pt *PongTracker) Evictions() int {
	pt.mu.Lock()
	n := pt.evictions
	pt.mu.Unlock()
	return n
}

// Tokens returns a snapshot of all currently tracked token strings.
func (pt *PongTracker) Tokens() []string {
	pt.mu.Lock()
	defer pt.mu.Unlock()
	out := make([]string, 0, len(pt.tokens))
	for t := range pt.tokens {
		out = append(out, t)
	}
	return out
}

// CleanupTracker removes expired entries from the tracker (older than maxAge)
// and enforces a size cap (maxSize). When maxSize is exceeded the oldest
// entries are evicted until len <= maxSize. Returns the number of entries
// removed.
func (pt *PongTracker) CleanupTracker(maxAge time.Duration, maxSize int) int {
	pt.mu.Lock()
	defer pt.mu.Unlock()

	now := time.Now()
	removed := 0

	// : remove TTL-expired entries.
	if maxAge > 0 {
		for token, seenAt := range pt.seenAt {
			if now.Sub(seenAt) > maxAge {
				delete(pt.tokens, token)
				delete(pt.seenAt, token)
				removed++
			}
		}
	}

	// : enforce size limit — evict oldest entries first.
	if maxSize > 0 && len(pt.tokens) > maxSize {
		type entry struct {
			token  string
			seenAt time.Time
		}
		entries := make([]entry, 0, len(pt.seenAt))
		for token, seenAt := range pt.seenAt {
			entries = append(entries, entry{token: token, seenAt: seenAt})
		}
		// Insertion-sort ascending by seenAt (oldest first). The slice is small.
		for i := 1; i < len(entries); i++ {
			for j := i; j > 0 && entries[j].seenAt.Before(entries[j-1].seenAt); j-- {
				entries[j], entries[j-1] = entries[j-1], entries[j]
			}
		}
		evict := len(pt.tokens) - maxSize
		for idx := 0; idx < evict && idx < len(entries); idx++ {
			delete(pt.tokens, entries[idx].token)
			delete(pt.seenAt, entries[idx].token)
			removed++
		}
		pt.evictions += evict
	}

	return removed
}

// =============================================================================
// PingPongEventType
// =============================================================================

// PingPongEventType labels a single entry in the diagnostic journal.
type PingPongEventType string

// PingPongEventType values.
const (
	// PingPongEventTypePingSent is recorded when a PING is dispatched.
	PingPongEventTypePingSent PingPongEventType = "PING_SENT"
	// PingPongEventTypePongReceived is recorded when a matching PONG arrives.
	PingPongEventTypePongReceived PingPongEventType = "PONG_RECEIVED"
	// PingPongEventTypePongUnknown is recorded when an unmatched PONG arrives.
	PingPongEventTypePongUnknown PingPongEventType = "PONG_UNKNOWN"
	// PingPongEventTypePongExpired is recorded when a PING's TTL elapses.
	PingPongEventTypePongExpired PingPongEventType = "PONG_EXPIRED"
)

// =============================================================================
// PingPongDiagJournal
// =============================================================================

// PingPongDiagEvent is one immutable entry in the diagnostic ring buffer.
type PingPongDiagEvent struct {
	// Timestamp is the wall-clock time the event was recorded.
	Timestamp time.Time
	// EventType classifies the event.
	EventType PingPongEventType
	// Token is the ping/pong token (truncated to last 20 chars for display).
	Token string
	// RTTms is round-trip time in milliseconds; non-zero only for
	// PingPongEventTypePongReceived.
	RTTms float64
}

// TimestampISO returns the event timestamp in ISO 8601 / RFC 3339 form.
func (e PingPongDiagEvent) TimestampISO() string {
	return e.Timestamp.Format(time.RFC3339)
}

// ToMap converts the event to a JSON-serialisable map.
func (e PingPongDiagEvent) ToMap() map[string]any {
	m := map[string]any{
		"time":          e.Timestamp.Format(time.RFC3339Nano),
		"timestamp_iso": e.TimestampISO(),
		"type":          string(e.EventType),
		"token":         e.Token,
	}
	if e.EventType == PingPongEventTypePongReceived {
		m["rtt_ms"] = e.RTTms
	}
	return m
}

// PingPongDiagJournalConfig configures a [PingPongDiagJournal].
type PingPongDiagJournalConfig struct {
	// MaxEntries caps the ring buffer. Defaults to 100.
	MaxEntries int
	// MaxAge is the time-based eviction window. Defaults to 30 min.
	MaxAge time.Duration
}

// PingPongDiagJournal is a fixed-size ring buffer of ping/pong diagnostic
// events with time-based eviction and RTT statistics.
type PingPongDiagJournal struct {
	cfg PingPongDiagJournalConfig
	mu  sync.Mutex

	events     []PingPongDiagEvent
	rttSamples []float64 // kept to last 50 samples; updated on PONG_RECEIVED
}

// NewPingPongDiagJournal creates a ready journal with the given config.
func NewPingPongDiagJournal(cfg PingPongDiagJournalConfig) *PingPongDiagJournal {
	if cfg.MaxEntries <= 0 {
		cfg.MaxEntries = 100
	}
	if cfg.MaxAge <= 0 {
		cfg.MaxAge = 30 * time.Minute
	}
	return &PingPongDiagJournal{cfg: cfg}
}

// RTTSamples returns a copy of the collected RTT samples in milliseconds.
func (j *PingPongDiagJournal) RTTSamples() []float64 {
	j.mu.Lock()
	defer j.mu.Unlock()
	if len(j.rttSamples) == 0 {
		return nil
	}
	out := make([]float64, len(j.rttSamples))
	copy(out, j.rttSamples)
	return out
}

// Events returns a copy of all diagnostic events currently in the buffer.
func (j *PingPongDiagJournal) Events() []PingPongDiagEvent {
	j.mu.Lock()
	defer j.mu.Unlock()
	if len(j.events) == 0 {
		return nil
	}
	out := make([]PingPongDiagEvent, len(j.events))
	copy(out, j.events)
	return out
}

// Clear removes all events and RTT samples.
func (j *PingPongDiagJournal) Clear() {
	j.mu.Lock()
	j.events = nil
	j.rttSamples = nil
	j.mu.Unlock()
}

// RecordPingSent logs a PING being dispatched.
func (j *PingPongDiagJournal) RecordPingSent(token string) {
	j.addEvent(PingPongEventTypePingSent, token, 0)
}

// RecordPongReceived logs a successful PONG with its RTT in milliseconds.
func (j *PingPongDiagJournal) RecordPongReceived(token string, rttMs float64) {
	j.addEvent(PingPongEventTypePongReceived, token, rttMs)
}

// RecordPongExpired logs a PING whose TTL elapsed without a matching PONG.
func (j *PingPongDiagJournal) RecordPongExpired(token string) {
	j.addEvent(PingPongEventTypePongExpired, token, 0)
}

// RecordPongUnknown logs an orphan PONG (no matching PING was pending).
func (j *PingPongDiagJournal) RecordPongUnknown(token string) {
	j.addEvent(PingPongEventTypePongUnknown, token, 0)
}

// CountEventsByType counts events of eventType recorded in the last minutes.
func (j *PingPongDiagJournal) CountEventsByType(eventType PingPongEventType, minutes int) int {
	if minutes <= 0 {
		minutes = 5
	}
	cutoff := time.Now().Add(-time.Duration(minutes) * time.Minute)
	j.mu.Lock()
	defer j.mu.Unlock()
	count := 0
	for _, e := range j.events {
		if e.EventType == eventType && e.Timestamp.After(cutoff) {
			count++
		}
	}
	return count
}

// GetSuccessRate returns PONG_RECEIVED / PING_SENT over the last minutes.
// Returns 1.0 when no pings have been sent (nothing to fail).
func (j *PingPongDiagJournal) GetSuccessRate(minutes int) float64 {
	pings := j.CountEventsByType(PingPongEventTypePingSent, minutes)
	if pings == 0 {
		return 1.0
	}
	pongs := j.CountEventsByType(PingPongEventTypePongReceived, minutes)
	return float64(pongs) / float64(pings)
}

// GetRecentEvents returns the last limit events as JSON-serialisable maps.
func (j *PingPongDiagJournal) GetRecentEvents(limit int) []map[string]any {
	if limit <= 0 {
		limit = 50
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	start := 0
	if len(j.events) > limit {
		start = len(j.events) - limit
	}
	out := make([]map[string]any, 0, len(j.events)-start)
	for _, e := range j.events[start:] {
		out = append(out, e.ToMap())
	}
	return out
}

// GetRTTStatistics returns aggregate RTT statistics over collected samples.
func (j *PingPongDiagJournal) GetRTTStatistics() map[string]any {
	j.mu.Lock()
	defer j.mu.Unlock()
	if len(j.rttSamples) == 0 {
		return map[string]any{
			"samples": 0,
			"avg_ms":  nil,
			"min_ms":  nil,
			"max_ms":  nil,
		}
	}
	var sum, mn, mx float64
	mn = j.rttSamples[0]
	mx = j.rttSamples[0]
	for _, v := range j.rttSamples {
		sum += v
		if v < mn {
			mn = v
		}
		if v > mx {
			mx = v
		}
	}
	avg := sum / float64(len(j.rttSamples))
	return map[string]any{
		"samples": len(j.rttSamples),
		"avg_ms":  round2(avg),
		"min_ms":  round2(mn),
		"max_ms":  round2(mx),
	}
}

// GetDiagnostics returns a full diagnostics snapshot for HA diagnostics.
func (j *PingPongDiagJournal) GetDiagnostics() map[string]any {
	j.mu.Lock()
	total := len(j.events)
	j.mu.Unlock()
	return map[string]any{
		"total_events":    total,
		"max_entries":     j.cfg.MaxEntries,
		"max_age_seconds": j.cfg.MaxAge.Seconds(),
		"rtt_statistics":  j.GetRTTStatistics(),
		"recent_events":   j.GetRecentEvents(20),
	}
}

// addEvent is the internal write path: evicts stale entries, enforces size
// cap, appends the new event, and (for PONG_RECEIVED) updates RTT samples.
func (j *PingPongDiagJournal) addEvent(eventType PingPongEventType, token string, rttMs float64) {
	now := time.Now()
	// Truncate token display to last 20 characters (mirrors Python side).
	display := token
	if len(display) > 20 {
		display = display[len(display)-20:]
	}
	e := PingPongDiagEvent{
		Timestamp: now,
		EventType: eventType,
		Token:     display,
		RTTms:     rttMs,
	}

	j.mu.Lock()
	defer j.mu.Unlock()

	// Time-based eviction: drop entries older than MaxAge.
	cutoff := now.Add(-j.cfg.MaxAge)
	keep := 0
	for keep < len(j.events) && j.events[keep].Timestamp.Before(cutoff) {
		keep++
	}
	if keep > 0 {
		j.events = j.events[keep:]
	}

	// Size-based eviction: cap at MaxEntries.
	for len(j.events) >= j.cfg.MaxEntries {
		j.events = j.events[1:]
	}

	j.events = append(j.events, e)

	// Update RTT samples for PONG_RECEIVED events; cap at 50.
	if eventType == PingPongEventTypePongReceived {
		j.rttSamples = append(j.rttSamples, rttMs)
		if len(j.rttSamples) > 50 {
			j.rttSamples = j.rttSamples[1:]
		}
	}
}

// round2 rounds a float to 2 decimal places.
func round2(v float64) float64 {
	return float64(int(v*100+0.5)) / 100
}
