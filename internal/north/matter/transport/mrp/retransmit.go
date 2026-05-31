// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mrp

import (
	"errors"
	"math"
	"math/rand/v2"
	"sync"
	"time"
)

// MRP backoff parameters (Matter Core Spec §4.12.6 Table 9).
const (
	// MRPBackoffBase is the initial retransmission interval.
	MRPBackoffBase = 300 * time.Millisecond
	// MRPBackoffJitterFactor is the upper bound on the random jitter
	// fraction added to each retransmit window.
	MRPBackoffJitterFactor = 0.25
	// MRPBackoffMargin scales the computed backoff up to compensate
	// for clock skew and processing delay on the peer.
	MRPBackoffMargin = 1.1
	// MRPBackoffThreshold is the multiplicative growth factor between
	// successive retransmissions.
	MRPBackoffThreshold = 1.6
	// MaxRetransmissions is the default cap on retries before the
	// caller surfaces a delivery failure.
	MaxRetransmissions = 4
)

// ErrMaxRetransmissionsReached is returned by [Retransmitter.Tick] for
// every entry whose retry count has exceeded [MaxRetransmissions].
var ErrMaxRetransmissionsReached = errors.New("mrp: max retransmissions reached")

// pending captures one in-flight reliable message awaiting an ACK.
type pending struct {
	counter    uint32
	exchange   uint16
	payload    []byte
	sentAt     time.Time
	retries    int
	nextSendAt time.Time
}

// SendFunc is the callback the [Retransmitter] invokes when a pending
// message is due for re-send. Implementations push the bytes onto the
// underlying UDP transport. Errors are surfaced via the next [Tick]
// call's results.
type SendFunc func(payload []byte) error

// Retransmitter tracks reliable messages and re-emits them after the
// configured backoff schedule. The unit-test surface drives time
// progress explicitly via [Tick(now)], so the package itself does not
// own a goroutine — the UDP loop wakes the retransmitter when its
// next-due deadline elapses.
type Retransmitter struct {
	mu      sync.Mutex
	entries map[uint32]*pending // keyed on message counter
	send    SendFunc
	rng     *rand.Rand
}

// NewRetransmitter returns a retransmitter configured to invoke send
// when an entry is due. The optional rng makes jitter deterministic
// in tests; pass nil for production (the package picks a default).
//
// The rng is only consumed for retransmission jitter — it is not
// security-sensitive (the duplicate-detection window prevents replay
// regardless of jitter predictability), so a math/rand/v2 PCG is
// appropriate here. crypto/rand-strength sources are reserved for
// session-key material under [..]/secure.
func NewRetransmitter(send SendFunc, rng *rand.Rand) *Retransmitter {
	if rng == nil {
		rng = rand.New(rand.NewPCG(0xCAFEBABE, 0xDEADBEEF)) //nolint:gosec // G404: jitter only — see doc comment
	}
	return &Retransmitter{
		entries: map[uint32]*pending{},
		send:    send,
		rng:     rng,
	}
}

// Track records a freshly-sent reliable message. now is the wall
// clock at send-time — stored on the entry so [Tick] can compute
// elapsed time without re-reading time.Now() (test seam).
func (r *Retransmitter) Track(counter uint32, exchangeID uint16, payload []byte, now time.Time) {
	cp := make([]byte, len(payload))
	copy(cp, payload)
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries[counter] = &pending{
		counter:    counter,
		exchange:   exchangeID,
		payload:    cp,
		sentAt:     now,
		nextSendAt: now.Add(r.backoff(0)),
	}
}

// Ack removes the pending entry whose counter matches `acked`. Returns
// true when an entry was found and removed, false when the ACK
// references a counter that is not in flight (typical on duplicate
// ACKs after a re-send race).
func (r *Retransmitter) Ack(acked uint32) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.entries[acked]; !ok {
		return false
	}
	delete(r.entries, acked)
	return true
}

// Pending reports the count of in-flight entries.
func (r *Retransmitter) Pending() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.entries)
}

// TickResult enumerates the outcome of a [Tick] sweep for a single
// pending entry.
type TickResult struct {
	Counter uint32
	Err     error // nil = re-sent; ErrMaxRetransmissionsReached = abandoned
}

// Tick re-sends every entry whose nextSendAt deadline has elapsed,
// schedules the next attempt with exponential backoff, and gives up
// after [MaxRetransmissions]. Returns one [TickResult] per entry
// touched. Entries that are abandoned are removed from the tracker.
func (r *Retransmitter) Tick(now time.Time) []TickResult {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []TickResult
	for counter, p := range r.entries {
		if now.Before(p.nextSendAt) {
			continue
		}
		if p.retries >= MaxRetransmissions {
			delete(r.entries, counter)
			out = append(out, TickResult{Counter: counter, Err: ErrMaxRetransmissionsReached})
			continue
		}
		p.retries++
		// Send under the lock is acceptable because the SendFunc is
		// expected to enqueue (not block on) the underlying UDP
		// socket. If a future SendFunc blocks on I/O we will move the
		// dispatch out of the critical section.
		if err := r.send(p.payload); err != nil {
			out = append(out, TickResult{Counter: counter, Err: err})
			continue
		}
		p.nextSendAt = now.Add(r.backoff(p.retries))
		out = append(out, TickResult{Counter: counter})
	}
	return out
}

// backoff computes the next-attempt interval per Matter §4.12.6:
//
//	interval = MRPBackoffBase × MRPBackoffMargin × (MRPBackoffThreshold)^max(0, retries-1) × (1 + uniform[0, jitter))
//
// chip's ReliableMessageMgr.cpp:295 uses `max(0, n-1)` as the exponent
// so the first retransmit still runs at the un-grown base interval —
// the threshold only kicks in from the second retransmit onward. The
// earlier `Pow(1.6, retries)` form grew on the first retransmit
// already, which is ~1.6× too aggressive and shows up in Apple's
// "Dropping message" rate under CASE handshake load.
func (r *Retransmitter) backoff(retries int) time.Duration {
	exp := retries - 1
	if exp < 0 {
		exp = 0
	}
	factor := math.Pow(MRPBackoffThreshold, float64(exp))
	jitter := 1 + r.rng.Float64()*MRPBackoffJitterFactor
	d := float64(MRPBackoffBase) * MRPBackoffMargin * factor * jitter
	return time.Duration(d)
}
