// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package bridge

import (
	"context"
	"errors"
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
// The pending map is keyed on MessageCounter — Matter §4.12.3 says
// that the ACK on a reliable message carries the counter of the
// acknowledged message in the protocol-header AckCounter field, so
// dispatch needs only the counter to find the entry.
type outboundReliableTracker struct {
	mu      sync.Mutex
	pending map[uint32]*outboundEntry
	// waiters lets callers block on Ack via [WaitForAck] — used by the
	// Subscribe-initial chunked send path so the bridge mirrors
	// matter.js's per-chunk handshake (one ReportData → wait for
	// StatusResponse(SUCCESS) → next ReportData). Closed by [Ack] /
	// [Tick] when the entry is resolved or abandoned.
	waiters map[uint32]chan error
}

// outboundEntry captures one in-flight reliable datagram and the
// retransmit schedule. The exchangeID is recorded alongside the
// counter so [outboundReliableTracker.AbandonExchange] can drop every
// pending entry of an exchange in one shot — used by the CASE Sigma3
// receive path to stop redundant Sigma2 retransmits once the
// commissioner has progressed past Sigma2.
type outboundEntry struct {
	counter    uint32
	exchangeID uint16
	datagram   []byte
	dest       *net.UDPAddr
	retries    int
	nextSendAt time.Time
}

// newOutboundReliableTracker returns a fresh, empty tracker.
func newOutboundReliableTracker() *outboundReliableTracker {
	return &outboundReliableTracker{
		pending: make(map[uint32]*outboundEntry),
		waiters: make(map[uint32]chan error),
	}
}

// Track records a freshly-sent reliable datagram. counter is the
// MessageCounter we stamped into the message header; dest is the UDP
// peer; datagram is the wire bytes we shipped (defensive-copied so
// caller pools / reuses are safe). Subsequent inbound HasAck with
// AckCounter=counter clears the entry; otherwise the next ACK pump
// tick resends it after the Matter §4.12.6 backoff.
func (t *outboundReliableTracker) Track(counter uint32, exchangeID uint16, datagram []byte, dest *net.UDPAddr, now time.Time) {
	cp := make([]byte, len(datagram))
	copy(cp, datagram)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pending[counter] = &outboundEntry{
		counter:    counter,
		exchangeID: exchangeID,
		datagram:   cp,
		dest:       dest,
		nextSendAt: now.Add(mrp.MRPBackoffBase),
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
	for counter, entry := range t.pending {
		if entry.exchangeID != exchangeID {
			continue
		}
		delete(t.pending, counter)
		if ch, has := t.waiters[counter]; has {
			delete(t.waiters, counter)
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

// Ack removes the pending entry with counter `acked`. Returns true
// when an entry was found, false on stray / duplicate ACK. Wakes any
// [WaitForAck] caller blocked on `acked`.
func (t *outboundReliableTracker) Ack(acked uint32) bool {
	t.mu.Lock()
	if _, ok := t.pending[acked]; !ok {
		// Release the waiter even on stray ACKs — the chunked-send path
		// always installs a waiter before the datagram leaves, so a fast
		// Ack from the peer can land before Track() (or after a quirky
		// retransmit that re-acks an already-cleared counter).
		if ch, has := t.waiters[acked]; has {
			delete(t.waiters, acked)
			t.mu.Unlock()
			close(ch)
			return false
		}
		t.mu.Unlock()
		return false
	}
	delete(t.pending, acked)
	ch, has := t.waiters[acked]
	if has {
		delete(t.waiters, acked)
	}
	t.mu.Unlock()
	if has {
		close(ch)
	}
	return true
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
func (t *outboundReliableTracker) WaitForAck(ctx context.Context, counter uint32) error {
	t.mu.Lock()
	if _, pending := t.pending[counter]; !pending {
		t.mu.Unlock()
		return nil // already acked / never tracked
	}
	ch, exists := t.waiters[counter]
	if !exists {
		ch = make(chan error, 1)
		t.waiters[counter] = ch
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
		// counter would otherwise leave a stranded entry. Only delete
		// our own channel; if the wait raced with another caller
		// installing a fresh chan we don't touch theirs.
		if cur, has := t.waiters[counter]; has && cur == ch {
			delete(t.waiters, counter)
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
	Counter uint32
	Err     error // nil = re-sent; mrp.ErrMaxRetransmissionsReached = abandoned
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
	for counter, p := range t.pending {
		if now.Before(p.nextSendAt) {
			continue
		}
		if p.retries >= mrp.MaxRetransmissions {
			delete(t.pending, counter)
			if ch, has := t.waiters[counter]; has {
				delete(t.waiters, counter)
				ch <- mrp.ErrMaxRetransmissionsReached
				close(ch)
			}
			out = append(out, outboundTickResult{Counter: counter, Err: mrp.ErrMaxRetransmissionsReached})
			continue
		}
		p.retries++
		if err := send(p.dest, p.datagram); err != nil {
			out = append(out, outboundTickResult{Counter: counter, Err: err})
			continue
		}
		// Linear-but-jittered backoff per Matter §4.12.6 — for the
		// ongoing-pump use case the fancy math/rand jitter from
		// mrp.Retransmitter is overkill; a fixed 1.6× growth keeps
		// the bookkeeping simple and matches MRPBackoffThreshold.
		delay := time.Duration(float64(mrp.MRPBackoffBase) * pow(mrp.MRPBackoffThreshold, p.retries))
		p.nextSendAt = now.Add(delay)
		out = append(out, outboundTickResult{Counter: counter})
	}
	return out
}

// pow is a tiny float-power helper. math.Pow would do, but pulling
// in math for a single call inflates the import surface.
func pow(base float64, exp int) float64 {
	if exp <= 0 {
		return 1
	}
	v := base
	for i := 1; i < exp; i++ {
		v *= base
	}
	return v
}
