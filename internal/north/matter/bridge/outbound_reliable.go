// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package bridge

import (
	"context"
	"errors"
	"math/rand/v2"
	"net"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/transport/mrp"
)

// ErrAckWaitTimeout is returned by [outboundReliableTracker.WaitForAck]
// when the deadline expires before the peer ACKs the tracked counter.
// Subscribe-initial chunking uses this to enforce Matter §10.6.6's
// per-chunk handshake: the sender must wait for a `StatusResponse(SUCCESS)`
// (which piggybacks an MRP ACK on the chunk's counter) before shipping
// the next chunk. matter.js mirrors the same flow with
// `await this.waitForSuccess("DataReport")` in
// `InteractionMessenger.ts:721`.
var ErrAckWaitTimeout = errors.New("bridge: outbound ack wait: timeout")

// outboundReliableTracker bookkeeps reliable outbound messages the
// bridge has shipped (ongoing subscription reports today;
// command-style IM responses tomorrow) so an inbound HasAck-bearing
// datagram can clear the corresponding pending counter and a
// scheduler can re-send entries whose ACK never arrived. Independent
// of [mrp.AckTracker] which goes in the opposite direction (we owe
// peer an ACK for an inbound reliable).
//
// The pending map is keyed on (SessionID, MessageCounter). Matter
// §4.12.3 says the ACK carries the acknowledged message's counter in
// the protocol-header AckCounter field, but that counter is only
// unique WITHIN a session — two concurrent sessions each seed their
// MRP counter from an independent random (Matter §4.5.4), so a bare
// counter key lets session A's ACK clear session B's pending entry
// when their counters happen to coincide. Keying on the session too
// mirrors matter.js's per-session ExchangeManager bookkeeping
// (ExchangeManager.ts:287). The inbound ACK path supplies the session
// from the received message header.
type outboundKey struct {
	sessionID uint16
	counter   uint32
}

type outboundReliableTracker struct {
	mu      sync.Mutex
	pending map[outboundKey]*outboundEntry
	// waiters lets callers block on Ack via [WaitForAck] — used by the
	// Subscribe-initial chunked send path so the bridge mirrors
	// matter.js's per-chunk handshake (one ReportData → wait for
	// StatusResponse(SUCCESS) → next ReportData). Closed by [Ack] /
	// [Tick] when the entry is resolved or abandoned.
	waiters map[outboundKey]chan error
	// rtt holds the recent first-try round-trips for diagnostics.
	rtt mrpRTTWindow
	// baseIntervalFor resolves the peer-appropriate MRP base interval
	// for the session (peer-advertised active/idle interval selected
	// by peer activity — matter.js MRP.ts:129). nil, or a session the
	// resolver does not know, falls back to the spec idle default.
	// Set once at construction; read without the mutex.
	baseIntervalFor func(sessionID uint16, now time.Time) time.Duration
}

// outboundEntry captures one in-flight reliable datagram and the
// retransmit schedule. The exchangeID is recorded alongside the
// counter so [outboundReliableTracker.AbandonExchange] can drop every
// pending entry of an exchange in one shot — used by the CASE Sigma3
// receive path to stop redundant Sigma2 retransmits once the
// commissioner has progressed past Sigma2. The sessionID feeds the
// per-peer MRP interval selection on every retransmit.
type outboundEntry struct {
	counter    uint32
	sessionID  uint16
	exchangeID uint16
	datagram   []byte
	dest       *net.UDPAddr
	retries    int
	nextSendAt time.Time
	// sentWall is the wall-clock reading when the datagram first left, used
	// only to report the peer's round-trip in diagnostics. It is deliberately
	// NOT the injected `now` the retransmit schedule runs on: that clock is a
	// test seam and may be a fixed instant, which would make every reported
	// round-trip zero. Nothing in the MRP state machine reads this field.
	sentWall time.Time
}

// newOutboundReliableTracker returns a fresh, empty tracker.
// baseIntervalFor may be nil — every send then uses the spec idle
// default as its base interval.
func newOutboundReliableTracker(baseIntervalFor func(sessionID uint16, now time.Time) time.Duration) *outboundReliableTracker {
	return &outboundReliableTracker{
		pending:         make(map[outboundKey]*outboundEntry),
		waiters:         make(map[outboundKey]chan error),
		baseIntervalFor: baseIntervalFor,
	}
}

// baseInterval resolves the MRP base interval for sessionID, falling
// back to the spec SESSION_IDLE_INTERVAL default when no resolver is
// wired (tests) or the session is unknown (session 0 / PASE).
func (t *outboundReliableTracker) baseInterval(sessionID uint16, now time.Time) time.Duration {
	if t.baseIntervalFor != nil {
		if d := t.baseIntervalFor(sessionID, now); d > 0 {
			return d
		}
	}
	return mrp.SessionIdleIntervalDefault
}

// Track records a freshly-sent reliable datagram. counter is the
// MessageCounter we stamped into the message header; dest is the UDP
// peer; datagram is the wire bytes we shipped (defensive-copied so
// caller pools / reuses are safe). Subsequent inbound HasAck with
// AckCounter=counter clears the entry; otherwise the next ACK pump
// tick resends it after the Matter §4.12.6 backoff.
func (t *outboundReliableTracker) Track(counter uint32, sessionID, exchangeID uint16, datagram []byte, dest *net.UDPAddr, now time.Time) {
	cp := make([]byte, len(datagram))
	copy(cp, datagram)
	// The initial wait before the first retransmit uses the full spec
	// formula at transmission 0 — matter.js MRP.ts:129 evaluates the
	// peer-appropriate base interval for every transmission including
	// the first.
	delay := mrp.BackoffDuration(t.baseInterval(sessionID, now), 0, rand.Float64)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pending[outboundKey{sessionID, counter}] = &outboundEntry{
		counter:    counter,
		sessionID:  sessionID,
		exchangeID: exchangeID,
		datagram:   cp,
		dest:       dest,
		nextSendAt: now.Add(delay),
		sentWall:   time.Now(),
	}
}

// AbandonExchange drops every pending reliable entry whose
// exchangeID matches and wakes any [WaitForAck] callers blocked on
// those counters with [ErrAckWaitTimeout]. Returns the number of
// entries cleared.
//
// Used by the CASE Sigma3 receive path: once the commissioner has
// progressed past Sigma2, any still-pending Sigma2 retransmits on the
// same exchange are wasted bandwidth at best, and at worst confuse
// the commissioner's MRP layer (Apple iOS drops them with
// `Dropping message without piggyback ack when we are waiting for
// an ack`). Mirrors matter.js's per-exchange cleanup in
// `MessageExchange.ts::close` which discards every retx-pending
// message of the exchange when the receiver advances state.
func (t *outboundReliableTracker) AbandonExchange(exchangeID uint16) int {
	t.mu.Lock()
	cleared := 0
	wakers := make([]chan error, 0)
	for key, entry := range t.pending {
		if entry.exchangeID != exchangeID {
			continue
		}
		delete(t.pending, key)
		if ch, has := t.waiters[key]; has {
			delete(t.waiters, key)
			wakers = append(wakers, ch)
		}
		cleared++
	}
	t.mu.Unlock()
	// Wake outside the lock — channel sends may otherwise stall.
	for _, ch := range wakers {
		// Send abandon marker; ignore if no receiver (best-effort).
		select {
		case ch <- ErrAckWaitTimeout:
		default:
			close(ch)
		}
	}
	return cleared
}

// Ack removes the pending entry for (sessionID, acked). Returns true
// when an entry was found, false on stray / duplicate ACK. Wakes any
// [WaitForAck] caller blocked on the same key. sessionID comes from
// the received message header — an ACK is only valid within its own
// session (see the outboundKey rationale).
func (t *outboundReliableTracker) Ack(sessionID uint16, acked uint32) bool {
	key := outboundKey{sessionID, acked}
	t.mu.Lock()
	if _, ok := t.pending[key]; !ok {
		// Release the waiter even on stray ACKs — the chunked-send path
		// always installs a waiter before the datagram leaves, so a fast
		// Ack from the peer can land before Track() (or after a quirky
		// retransmit that re-acks an already-cleared counter).
		if ch, has := t.waiters[key]; has {
			delete(t.waiters, key)
			t.mu.Unlock()
			close(ch)
			return false
		}
		t.mu.Unlock()
		return false
	}
	entry := t.pending[key]
	delete(t.pending, key)
	// Only a first-try datagram yields a usable round-trip. After a
	// retransmit there is no way to tell whether the ACK answers the original
	// or the resend, so timing it from the first send overstates the peer's
	// latency by whole backoff intervals — Karn's algorithm, and the same
	// reason MRP itself never adapts its interval from a retransmitted
	// exchange.
	if entry != nil && entry.retries == 0 && !entry.sentWall.IsZero() {
		t.rtt.record(time.Since(entry.sentWall))
	}
	ch, has := t.waiters[key]
	if has {
		delete(t.waiters, key)
	}
	t.mu.Unlock()
	if has {
		close(ch)
	}
	return true
}

// RTTStats summarises the recent first-try round-trips to Matter controllers.
// Safe on a nil tracker (Matter disabled), which reports an empty summary.
func (t *outboundReliableTracker) RTTStats() MRPRTTStats {
	if t == nil {
		return MRPRTTStats{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.rtt.stats()
}

// WaitForAck blocks until the peer ACKs `counter`, the context is
// cancelled, or the entry is abandoned by [Tick] after exceeding the
// retransmit cap. Caller must invoke this AFTER [Track] (otherwise the
// channel is created but never resolved).
//
// Returns nil on ACK; ctx.Err() on context cancellation;
// [ErrAckWaitTimeout] if the deadline elapses without movement; the
// abandon-error (typically [mrp.ErrMaxRetransmissionsReached]) when
// Tick gives up.
func (t *outboundReliableTracker) WaitForAck(ctx context.Context, sessionID uint16, counter uint32) error {
	key := outboundKey{sessionID, counter}
	t.mu.Lock()
	if _, pending := t.pending[key]; !pending {
		t.mu.Unlock()
		return nil // already acked / never tracked
	}
	ch, exists := t.waiters[key]
	if !exists {
		ch = make(chan error, 1)
		t.waiters[key] = ch
	}
	t.mu.Unlock()

	select {
	case err, ok := <-ch:
		if !ok {
			return nil
		}
		return err
	case <-ctx.Done():
		t.mu.Lock()
		// Best-effort cleanup so a cancelled wait doesn't leak the
		// waiter channel forever — the next Ack/abandon for this
		// key would otherwise leave a stranded entry. Only delete
		// our own channel; if the wait raced with another caller
		// installing a fresh chan we don't touch theirs.
		if cur, has := t.waiters[key]; has && cur == ch {
			delete(t.waiters, key)
		}
		t.mu.Unlock()
		return ctx.Err()
	}
}

// Pending reports the count of in-flight entries. Test helper.
func (t *outboundReliableTracker) Pending() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.pending)
}

// outboundTickResult enumerates the outcome of a single Tick sweep.
type outboundTickResult struct {
	SessionID uint16
	Counter   uint32
	Err       error // nil = re-sent; mrp.ErrMaxRetransmissionsReached = abandoned
}

// Tick walks every entry whose nextSendAt has elapsed: re-sends via
// `send` (typically `listener.Send`), schedules the next attempt with
// exponential backoff, and gives up after [mrp.MaxRetransmissions].
// Abandoned entries are removed and reported with
// [mrp.ErrMaxRetransmissionsReached] so the caller can drop the
// associated subscription / state.
//
// `send` is called under the tracker's lock — production
// implementations enqueue (don't block) on the UDP socket. Send
// errors are surfaced via the result slice; the entry stays in the
// pending map for the next tick to retry.
func (t *outboundReliableTracker) Tick(now time.Time, send func(*net.UDPAddr, []byte) error) []outboundTickResult {
	t.mu.Lock()
	defer t.mu.Unlock()
	var out []outboundTickResult
	for key, p := range t.pending {
		if now.Before(p.nextSendAt) {
			continue
		}
		if p.retries >= mrp.MaxRetransmissions {
			delete(t.pending, key)
			if ch, has := t.waiters[key]; has {
				delete(t.waiters, key)
				ch <- mrp.ErrMaxRetransmissionsReached
				close(ch)
			}
			out = append(out, outboundTickResult{SessionID: key.sessionID, Counter: key.counter, Err: mrp.ErrMaxRetransmissionsReached})
			continue
		}
		p.retries++
		if err := send(p.dest, p.datagram); err != nil {
			out = append(out, outboundTickResult{SessionID: key.sessionID, Counter: key.counter, Err: err})
			continue
		}
		// Full spec backoff per Matter §4.12.2.1: peer-appropriate
		// base interval (re-evaluated per retransmission — the peer
		// may have gone idle since the last attempt), margin,
		// exponential growth past the threshold, and jitter. Mirrors
		// matter.js MRP.ts:125-146 retransmissionIntervalOf.
		delay := mrp.BackoffDuration(t.baseInterval(p.sessionID, now), p.retries, rand.Float64)
		p.nextSendAt = now.Add(delay)
		out = append(out, outboundTickResult{SessionID: key.sessionID, Counter: key.counter})
	}
	return out
}
