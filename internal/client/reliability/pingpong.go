// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package reliability

import (
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/clock"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmreliability"
)

// PingPongConfig configures a [PingPongTracker].
type PingPongConfig struct {
	// PendingTTL bounds how long an unmatched PING stays pending before
	// it is reported as a PingPongMismatchPending anomaly.
	PendingTTL time.Duration

	// UnknownTTL bounds how long an unmatched PONG lingers before it
	// is reported as a PingPongMismatchUnknown anomaly.
	UnknownTTL time.Duration

	// MaxEntries caps the pending and unknown tables independently.
	// Oldest 20 % is evicted when the cap is reached.
	MaxEntries int

	// MismatchThreshold is the count of expired pending+unknown entries above
	// which [Stats] reports the connection as degraded. When left zero the
	// constructor fills it with the parity default below; pass a negative value
	// to opt out of alarm escalation entirely.
	MismatchThreshold int

	// JournalSize bounds the in-memory journal of recent ping/pong
	// events kept for diagnostics. Zero disables the journal.
	JournalSize int

	// Clock overrides the wall clock. Nil falls back to the real wall
	// clock via [clock.New]. Mirrors the pattern used by [ThrottleConfig].
	Clock clock.Clock
}

// PingPongTracker correlates outbound PINGs with inbound PONGs.
//
// Rules:
// - RecordPing stores the sent identifier with a timestamp.
// - RecordPong matches it. Orphan PONGs are filed in the Unknown
// table.
// - Sweep garbage-collects entries older than TTL and emits them as
// anomalies.
type PingPongTracker struct {
	cfg PingPongConfig
	clk clock.Clock

	mu      sync.Mutex
	pending map[string]time.Time
	unknown map[string]time.Time

	// Counters and stats.
	matched   int
	mismatch  int // expired entries cumulative
	totalSent int
	totalRecv int

	// RTT samples. minRTT/maxRTT are zero until at least one match
	// has been observed; avgRTT is computed lazily in [Stats].
	rttSum   time.Duration
	rttCount int
	rttMin   time.Duration
	rttMax   time.Duration

	// Journal: ring buffer of recent events for diagnostics.
	journal     []JournalEntry
	journalHead int
	journalLen  int

	// Optional hook fired when [Sweep] surfaces a mismatch. Used to
	// record an incident in the persistent store.
	onMismatch func(Mismatch)

	// Optional hook fired by [RecordPing] / [RecordPong] when the pending /
	// unknown count crosses the [PingPongConfig.MismatchThreshold]. The caller
	// wires this to publish a PingPongMismatchEvent (and a SystemStatusChanged
	// issue) on the central event bus.
	//
	// `kind` distinguishes pending-overflow from unknown-overflow. `count` is
	// the current size of the affected set.
	onPublish func(kind hmenum.PingPongMismatchType, count int)

	// Optional gate for [RecordPing]: when set and returns true, the PING is
	// *not* tracked.
	hasConnectionIssue func() bool

	// retryAt tracks tokens for which a reconcile-retry has been scheduled.
	// Prevents duplicate retries for the same token.
	retryAt map[string]struct{}
}

// Defaults mirror
// `PING_PONG_MISMATCH_COUNT_TTL` (300 s) from const.py:316-317.
// PingPongTTL is sourced from [hmreliability]; the local
// MismatchThreshold stays here because it has no cross-cutting
// counterpart yet.
var (
	defaultPingPongTTL               = hmreliability.PingPongTTL
	defaultPingPongMismatchThreshold = 15
)

// NewPingPongTracker returns a ready tracker. Zero-valued PendingTTL,
// UnknownTTL and MismatchThreshold are filled with parity defaults
// (300 s / 300 s / 15) — pass negative values to disable a setting.
func NewPingPongTracker(cfg PingPongConfig) *PingPongTracker {
	if cfg.PendingTTL == 0 {
		cfg.PendingTTL = defaultPingPongTTL
	}
	if cfg.UnknownTTL == 0 {
		cfg.UnknownTTL = defaultPingPongTTL
	}
	if cfg.MismatchThreshold == 0 {
		cfg.MismatchThreshold = defaultPingPongMismatchThreshold
	}
	if cfg.MaxEntries <= 0 {
		cfg.MaxEntries = 100
	}
	clk := cfg.Clock
	if clk == nil {
		clk = clock.New()
	}
	t := &PingPongTracker{
		cfg:     cfg,
		clk:     clk,
		pending: make(map[string]time.Time),
		unknown: make(map[string]time.Time),
	}
	if cfg.JournalSize > 0 {
		t.journal = make([]JournalEntry, cfg.JournalSize)
	}
	return t
}

// JournalEventKind labels a single ping/pong journal entry.
type JournalEventKind string

// JournalEventKind values.
const (
	JournalEventSent    JournalEventKind = "sent"
	JournalEventMatched JournalEventKind = "matched"
	JournalEventUnknown JournalEventKind = "unknown"
	JournalEventExpired JournalEventKind = "expired"
	JournalEventEvicted JournalEventKind = "evicted"
)

// JournalEntry is one timestamped observation in the tracker's
// diagnostic journal. RTT is non-zero only for [JournalEventMatched]
// entries.
type JournalEntry struct {
	When time.Time
	Kind JournalEventKind
	ID   string
	RTT  time.Duration
}

// Stats summarises the tracker's view on connection health. Mirrors
// The read side
type Stats struct {
	Pending           int
	Unknown           int
	TotalSent         int
	TotalReceived     int
	MatchedTotal      int
	MismatchTotal     int
	MismatchThreshold int

	// RTT statistics for matched pairs. All three are zero when no
	// match has been observed yet.
	MinRTT time.Duration
	MaxRTT time.Duration
	AvgRTT time.Duration

	// Severity is "ok" when neither pending nor unknown counts hit
	// the threshold; "degraded" once any individual table crosses
	// it; "critical" when both cross simultaneously.
	Severity string
}

// SuccessRate returns matched / total in [0, 1]. Returns 0 when no
// pings have been sent.
func (s Stats) SuccessRate() float64 {
	if s.TotalSent == 0 {
		return 0
	}
	return float64(s.MatchedTotal) / float64(s.TotalSent)
}

// Clear empties the pending and unknown tables and resets all counters.
// Call this when the connection is restored so stale PING/PONG tokens
// from the previous session don't inflate the mismatch counters.
//
// The journal is preserved — clearing the state is not the same as
// clearing the history.
//
// Mirrors the Python reference implementation's `store/dynamic/ping_pong.py:107`.
func (t *PingPongTracker) Clear() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pending = make(map[string]time.Time)
	t.unknown = make(map[string]time.Time)
	t.matched = 0
	t.mismatch = 0
	t.totalSent = 0
	t.totalRecv = 0
	t.rttSum = 0
	t.rttCount = 0
	t.rttMin = 0
	t.rttMax = 0
}

// RecordPing notes that id was sent at the current clock reading.
//
// - The connection-issue gate (when configured) returns the PING unrecorded
// so the daemon does not accumulate false pendings during a known CCU outage.
// - The threshold-publish hook fires on every other PING once the pending
// count is above [PingPongConfig.MismatchThreshold] callers wire this to
// publish a PingPongMismatchEvent.
func (t *PingPongTracker) RecordPing(id string) {
	// Read the gate through the accessor rather than the field: the gate is
	// installed after the client is already published into the central's
	// client registry, so the periodic connection check can call this while
	// SetConnectionIssueGate writes the same field.
	if t.HasConnectionIssue() {
		// Skip tracking: connection is known down.
		return
	}
	t.mu.Lock()
	now := t.clk.Now()
	t.pending[id] = now
	t.totalSent++
	t.appendJournalLocked(JournalEntry{When: now, Kind: JournalEventSent, ID: id})
	t.enforceCap(t.pending, JournalEventEvicted)
	count := len(t.pending)
	publishHook := t.onPublish
	threshold := t.cfg.MismatchThreshold
	t.mu.Unlock()

	// Threshold crossing — emit on every even count above the
	// Threshold (mirrors
	// handle_send_ping, plus the unconditional emit when crossing).
	if publishHook != nil && threshold > 0 && count > threshold && (count%2 == 0 || count == threshold+1) {
		publishHook(hmenum.PingPongMismatchPending, count)
	}
}

// RecordPong reports whether the PONG matched an outstanding PING. Unmatched
// PONGs move to the Unknown table. The returned RTT is the elapsed time
// between [RecordPing] and [RecordPong] for matched pairs; unmatched calls
// return zero.
func (t *PingPongTracker) RecordPong(id string) (matched bool, rtt time.Duration) {
	t.mu.Lock()
	now := t.clk.Now()
	t.totalRecv++
	var (
		threshold       = t.cfg.MismatchThreshold
		publishHook     = t.onPublish
		emitKind        hmenum.PingPongMismatchType
		emitCount       int
		shouldEmit      bool
		matchedThisCall bool
		measuredRTT     time.Duration
	)
	if sentAt, ok := t.pending[id]; ok {
		delete(t.pending, id)
		t.matched++
		measuredRTT = now.Sub(sentAt)
		t.rttSum += measuredRTT
		t.rttCount++
		if t.rttMin == 0 || measuredRTT < t.rttMin {
			t.rttMin = measuredRTT
		}
		if measuredRTT > t.rttMax {
			t.rttMax = measuredRTT
		}
		t.appendJournalLocked(JournalEntry{When: now, Kind: JournalEventMatched, ID: id, RTT: measuredRTT})
		matchedThisCall = true
		// Pending shrinks on match; emit if still above threshold.
		if publishHook != nil && threshold > 0 && len(t.pending) > threshold {
			emitKind = hmenum.PingPongMismatchPending
			emitCount = len(t.pending)
			shouldEmit = true
		}
	} else {
		t.unknown[id] = now
		t.appendJournalLocked(JournalEntry{When: now, Kind: JournalEventUnknown, ID: id})
		t.enforceCap(t.unknown, JournalEventEvicted)
		// Unknown grows; emit when above threshold.
		if publishHook != nil && threshold > 0 && len(t.unknown) > threshold {
			emitKind = hmenum.PingPongMismatchUnknown
			emitCount = len(t.unknown)
			shouldEmit = true
		}
	}
	t.mu.Unlock()

	if shouldEmit {
		publishHook(emitKind, emitCount)
	}
	return matchedThisCall, measuredRTT
}

// Mismatch describes one expired pending or unknown entry.
type Mismatch struct {
	ID   string
	Kind hmenum.PingPongMismatchType
	When time.Time
}

// Sweep purges expired entries and returns the mismatches found.
// Each mismatch is dispatched to the configured [SetMismatchHook]
// (after dropping the lock) so the caller's incident-recording stays
// outside the critical section.
func (t *PingPongTracker) Sweep() []Mismatch {
	now := t.clk.Now()
	t.mu.Lock()

	var out []Mismatch
	for id, when := range t.pending {
		if now.Sub(when) >= t.cfg.PendingTTL {
			out = append(out, Mismatch{ID: id, Kind: hmenum.PingPongMismatchPending, When: when})
			delete(t.pending, id)
			t.mismatch++
			t.appendJournalLocked(JournalEntry{When: now, Kind: JournalEventExpired, ID: id})
		}
	}
	for id, when := range t.unknown {
		if now.Sub(when) >= t.cfg.UnknownTTL {
			out = append(out, Mismatch{ID: id, Kind: hmenum.PingPongMismatchUnknown, When: when})
			delete(t.unknown, id)
			t.mismatch++
			t.appendJournalLocked(JournalEntry{When: now, Kind: JournalEventExpired, ID: id})
		}
	}
	hook := t.onMismatch
	t.mu.Unlock()

	if hook != nil {
		for _, m := range out {
			hook(m)
		}
	}
	return out
}

// PendingCount returns the size of the pending set.
func (t *PingPongTracker) PendingCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.pending)
}

// UnknownCount returns the size of the unknown set.
func (t *PingPongTracker) UnknownCount() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.unknown)
}

// Stats returns a coherent snapshot of counters plus an aggregate severity
// that callers can route into the health tracker.
func (t *PingPongTracker) Stats() Stats {
	t.mu.Lock()
	defer t.mu.Unlock()
	pending := len(t.pending)
	unknown := len(t.unknown)
	severity := "ok"
	if t.cfg.MismatchThreshold > 0 {
		thOver := 0
		if pending >= t.cfg.MismatchThreshold {
			thOver++
		}
		if unknown >= t.cfg.MismatchThreshold {
			thOver++
		}
		switch thOver {
		case 1:
			severity = "degraded"
		case 2:
			severity = "critical"
		}
	}
	var avg time.Duration
	if t.rttCount > 0 {
		avg = t.rttSum / time.Duration(t.rttCount)
	}
	return Stats{
		Pending:           pending,
		Unknown:           unknown,
		TotalSent:         t.totalSent,
		TotalReceived:     t.totalRecv,
		MatchedTotal:      t.matched,
		MismatchTotal:     t.mismatch,
		MismatchThreshold: t.cfg.MismatchThreshold,
		MinRTT:            t.rttMin,
		MaxRTT:            t.rttMax,
		AvgRTT:            avg,
		Severity:          severity,
	}
}

// SetMismatchHook installs a callback fired for every [Mismatch] that [Sweep]
// surfaces. Pass nil to detach. Callers wire this to an IncidentRecorder so
// connection-health regressions land in the incident store.
func (t *PingPongTracker) SetMismatchHook(fn func(Mismatch)) {
	t.mu.Lock()
	t.onMismatch = fn
	t.mu.Unlock()
}

// SetPublishHook installs a callback fired by [RecordPing] [RecordPong] when
// the pending or unknown count exceeds [PingPongConfig.MismatchThreshold].
//
// Callers typically wire this to publish a [hmevent.PingPongMismatchEvent] on
// the central event bus. Pass nil to detach.
func (t *PingPongTracker) SetPublishHook(fn func(kind hmenum.PingPongMismatchType, count int)) {
	t.mu.Lock()
	t.onPublish = fn
	t.mu.Unlock()
}

// SetConnectionIssueGate installs a predicate consulted by [RecordPing]: when
// it returns true, the PING is recorded as a no-op (no pending entry, no
// journal). Used during a known CCU outage to prevent false-alarm pending
// mismatches.
func (t *PingPongTracker) SetConnectionIssueGate(fn func() bool) {
	t.mu.Lock()
	t.hasConnectionIssue = fn
	t.mu.Unlock()
}

// HasConnectionIssue returns true when the configured connection-issue gate
// exists and currently reports a connection problem.
func (t *PingPongTracker) HasConnectionIssue() bool {
	t.mu.Lock()
	gate := t.hasConnectionIssue
	t.mu.Unlock()
	return gate != nil && gate()
}

// Size returns the combined count of pending + unknown entries.
func (t *PingPongTracker) Size() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.pending) + len(t.unknown)
}

// AllowedDelta returns the configured MismatchThreshold.
func (t *PingPongTracker) AllowedDelta() int {
	return t.cfg.MismatchThreshold
}

// RetryReconcilePong attempts to reconcile a previously-unknown PONG with a
// late pending PING. Cleans up expired entries first, then if the token is
// now in pending (arrived late), removes it from both tables and emits
// publish events. If the token is not in pending, it remains in unknown until
// TTL expiry.
func (t *PingPongTracker) RetryReconcilePong(token string) {
	now := t.clk.Now()
	t.mu.Lock()

	// Evict expired entries from both tables before reconciling.
	for id, when := range t.pending {
		if now.Sub(when) >= t.cfg.PendingTTL {
			delete(t.pending, id)
			t.mismatch++
			t.appendJournalLocked(JournalEntry{When: now, Kind: JournalEventExpired, ID: id})
		}
	}
	for id, when := range t.unknown {
		if now.Sub(when) >= t.cfg.UnknownTTL {
			delete(t.unknown, id)
			t.mismatch++
			t.appendJournalLocked(JournalEntry{When: now, Kind: JournalEventExpired, ID: id})
		}
	}

	if _, inPending := t.pending[token]; inPending {
		// Remove from pending.
		delete(t.pending, token)
		// If still in unknown, remove from there too.
		delete(t.unknown, token)
		t.matched++
		t.appendJournalLocked(JournalEntry{When: now, Kind: JournalEventMatched, ID: token})
	}
	publishHook := t.onPublish
	pendingLen := len(t.pending)
	unknownLen := len(t.unknown)
	threshold := t.cfg.MismatchThreshold
	t.mu.Unlock()

	// Re-publish events to reflect updated counts.
	if publishHook != nil && threshold > 0 {
		if pendingLen > threshold {
			publishHook(hmenum.PingPongMismatchPending, pendingLen)
		}
		if unknownLen > threshold {
			publishHook(hmenum.PingPongMismatchUnknown, unknownLen)
		}
	}
}

// ScheduleUnknownPongRetry schedules a one-shot call to
// [RetryReconcilePong] after delay for the given token. The retry is
// coalesced: if a retry for the same token is already pending, the
// call is a no-op.
//
// The fn parameter is the scheduling function (e.g. a wrapper around
// time.AfterFunc). Passing nil disables scheduling — the entry will
// simply expire via TTL. This mirrors the Python reference
// implementation's _schedule_unknown_pong_retry
// (store/dynamic/ping_pong.py:387).
func (t *PingPongTracker) ScheduleUnknownPongRetry(token string, delay time.Duration, schedule func(token string, delay time.Duration, retry func(string))) {
	if schedule == nil {
		return
	}
	t.mu.Lock()
	if t.retryAt == nil {
		t.retryAt = make(map[string]struct{})
	}
	if _, already := t.retryAt[token]; already {
		t.mu.Unlock()
		return
	}
	t.retryAt[token] = struct{}{}
	t.mu.Unlock()

	schedule(token, delay, func(tok string) {
		t.RetryReconcilePong(tok)
		t.mu.Lock()
		delete(t.retryAt, tok)
		t.mu.Unlock()
	})
}

// Journal returns a copy of the in-memory diagnostic journal in
// chronological order (oldest first). Empty when [PingPongConfig.JournalSize]
// is zero.
func (t *PingPongTracker) Journal() []JournalEntry {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.journalLen == 0 {
		return nil
	}
	out := make([]JournalEntry, 0, t.journalLen)
	start := (t.journalHead - t.journalLen + len(t.journal)) % len(t.journal)
	for i := range t.journalLen {
		out = append(out, t.journal[(start+i)%len(t.journal)])
	}
	return out
}

// appendJournalLocked records an event into the ring buffer. Caller
// must hold t.mu. No-op when the journal is disabled.
func (t *PingPongTracker) appendJournalLocked(e JournalEntry) {
	if len(t.journal) == 0 {
		return
	}
	t.journal[t.journalHead] = e
	t.journalHead = (t.journalHead + 1) % len(t.journal)
	if t.journalLen < len(t.journal) {
		t.journalLen++
	}
}

// enforceCap drops the oldest 20 % of entries when the table exceeds
// MaxEntries. Prevents unbounded growth under pathological packet loss.
// kind labels the eviction reason in the journal.
func (t *PingPongTracker) enforceCap(m map[string]time.Time, kind JournalEventKind) {
	if len(m) <= t.cfg.MaxEntries {
		return
	}
	toDrop := len(m) / 5
	// Two-pass: first find the cut-off, then drop.
	type entry struct {
		id string
		at time.Time
	}
	entries := make([]entry, 0, len(m))
	for id, at := range m {
		entries = append(entries, entry{id, at})
	}
	// Partial sort: we only need the oldest toDrop. A linear scan for
	// each of them is cheap (toDrop is small).
	for i := range toDrop {
		oldestIdx := i
		for j := i + 1; j < len(entries); j++ {
			if entries[j].at.Before(entries[oldestIdx].at) {
				oldestIdx = j
			}
		}
		entries[i], entries[oldestIdx] = entries[oldestIdx], entries[i]
		delete(m, entries[i].id)
		t.appendJournalLocked(JournalEntry{When: t.clk.Now(), Kind: kind, ID: entries[i].id})
	}
}
