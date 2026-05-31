// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mrp

import "sync"

// windowSize is the Matter-mandated count of recently-received
// counters tracked for duplicate detection. Per Core Spec §4.6.6 a
// 32-entry sliding window is sufficient because the receiver
// implements Matter §4.5.4.1's "max counter delta" limit at the
// session layer.
const windowSize = 32

// Window is the sliding-window duplicate detector for inbound
// message counters. It accepts counters that are larger than the
// current "highest seen" counter or that fall inside the last
// [windowSize] valid slots.
//
// Concurrency: Window is safe for concurrent use; lookups and
// updates are serialised through an internal mutex. The protocol's
// hot path is single-threaded per session in practice, but the
// listener loop and any session-management goroutine may both
// touch the window.
type Window struct {
	mu     sync.Mutex
	max    uint32 // highest counter seen
	bitmap uint32 // bit i set ⇒ counter (max-i) was received
	primed bool   // false until the first counter is recorded
}

// NewWindow returns a fresh empty duplicate-detection window.
func NewWindow() *Window { return &Window{} }

// Accept records a received counter and reports whether it is fresh
// (returns true) or a duplicate (returns false). Out-of-window stale
// counters return false too — the caller drops the message.
//
// The 32-bit counter wraps; Window assumes the session-layer ensures
// consecutive Accept calls stay within ±2^31 of each other (the
// Matter "valid window" definition). Outside that envelope Accept
// treats the counter as fresh and resets the window — this is the
// post-rekey re-establishment path.
func (w *Window) Accept(c uint32) bool {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.primed {
		w.max = c
		w.bitmap = 1 // bit 0 records "max" itself.
		w.primed = true
		return true
	}

	switch {
	case c == w.max:
		return false
	case wraps(c, w.max):
		// Counter is "before" max in the modular ordering. Could be
		// either in-window or stale-out-of-window.
		delta := w.max - c
		if delta >= windowSize {
			return false
		}
		mask := uint32(1) << delta
		if w.bitmap&mask != 0 {
			return false
		}
		w.bitmap |= mask
		return true
	default:
		// Counter is "after" max — advance the window.
		delta := c - w.max
		if delta >= windowSize {
			// Big jump: reset bitmap; only `c` is recorded.
			w.bitmap = 1
		} else {
			w.bitmap = (w.bitmap << delta) | 1
		}
		w.max = c
		return true
	}
}

// wraps reports whether c is "before" hi under modular arithmetic
// (i.e., a smaller-or-equal counter modulo 2^32). The argument is
// named hi (not max) to avoid shadowing the builtin.
func wraps(c, hi uint32) bool {
	return int32(hi-c) > 0 //nolint:gosec // G115: signed delta is the modular-distance test
}
