// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package bridge

import (
	"sync"
	"time"
)

// sessionMissBurst tracks repeated session-id lookup misses so the
// receive path can promote one diagnostic INFO row per burst out of
// the per-datagram DEBUG firehose. The trigger is the Apple iPhone
// stale-session cache: after RemoveFabric the controller's
// MTRDeviceController retains its CHIP SecureSession and keeps
// retransmitting MRP frames on the now-closed session-id until the
// phone reboots. The wire behaviour (silent drop) matches matter.js
// and chip; the burst log just makes the failure mode self-diagnosing
// for an operator watching the daemon.
//
// Contract: [record] returns true the first time a given session-id
// crosses the burst threshold within the rolling window, and once
// per cooldown thereafter. The receive path emits a single
// `matter.rx.session_miss.burst` INFO at that point.
type sessionMissBurst struct {
	mu      sync.Mutex
	entries map[uint16]*sessionMissEntry
	// lastSweepAt gates [sessionMissBurst.maybeSweepLocked] to at most
	// one full map walk per [sessionMissBurstSweepInterval], the same
	// amortisation [exchangeRouting.maybeSweepExpiredTimedDeadlines]
	// uses for its own unbounded-map guard.
	lastSweepAt time.Time
}

// sessionMissEntry caps each session-id's bookkeeping at a few words —
// the receive path needs ~µs lock-hold latency, so we deliberately
// avoid maintaining a slice of timestamps.
type sessionMissEntry struct {
	firstAt       time.Time // start of the current rolling window
	count         uint32    // misses observed inside the current window
	lastEmittedAt time.Time // last time a burst INFO fired for this session-id
	// lastSeenAt is updated on every record() call for this entry,
	// independent of whether the rolling window reset — it is the
	// staleness clock [maybeSweepLocked] reclaims against. firstAt
	// alone cannot serve this role: a session-id seen exactly once
	// never revisits the window-reset branch that would otherwise
	// touch firstAt, so its entry would sit forever without a
	// separate recency field.
	lastSeenAt time.Time
}

const (
	// sessionMissBurstThreshold is the miss count within a single
	// rolling window that triggers the INFO promotion. 8 is high
	// enough to ignore a single MRP retransmit clutch (which caps at
	// 4 retries per Matter §4.11.2.1) and low enough to surface a
	// genuinely wedged controller within seconds.
	sessionMissBurstThreshold uint32 = 8
	// sessionMissBurstWindow is the rolling window over which the
	// threshold accumulates. A clutch of MRP retransmits clears in
	// well under 15 s.
	sessionMissBurstWindow = 15 * time.Second
	// sessionMissBurstCooldown caps the INFO log frequency for the
	// same session-id so a permanently-wedged iPhone produces one row
	// per cooldown, not one per arriving datagram.
	sessionMissBurstCooldown = 5 * time.Minute
	// sessionMissBurstSweepInterval bounds how often record() pays the
	// full-map reclamation cost.
	sessionMissBurstSweepInterval = time.Minute
	// sessionMissBurstMaxEntries hard-caps the table the way
	// maxUnsecuredWindows caps unsecuredWindows (securechannel.go): a
	// session-id miss arrives on the pre-decrypt path (any LAN host
	// that can reach the operational UDP port can spray them), so
	// without a cap a spoofed/varying-session-id flood grows the table
	// without bound faster than the periodic sweep can reclaim it.
	sessionMissBurstMaxEntries = 4096
)

// record registers one session-id miss at `at` and reports whether
// this miss crossed the burst threshold and should fire an INFO. The
// caller (decryptIfNeeded) emits the burst log when this returns true;
// every other call stays silent.
func (t *sessionMissBurst) record(sessionID uint16, at time.Time) (emit bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.entries == nil {
		t.entries = make(map[uint16]*sessionMissEntry)
	}
	t.maybeSweepLocked(at)

	e, ok := t.entries[sessionID]
	if !ok {
		if len(t.entries) >= sessionMissBurstMaxEntries {
			// At the hard cap: refuse to grow further rather than
			// evict an existing (possibly still-relevant) entry. A
			// flood that outpaces the sweep interval still cannot
			// grow the table past this bound.
			return false
		}
		t.entries[sessionID] = &sessionMissEntry{firstAt: at, count: 1, lastSeenAt: at}
		return false
	}
	e.lastSeenAt = at
	// Reset the rolling window when it has aged out — a single late
	// retransmit after a quiet period must not chain onto the previous
	// burst.
	if at.Sub(e.firstAt) > sessionMissBurstWindow {
		e.firstAt = at
		e.count = 1
		return false
	}
	e.count++
	if e.count < sessionMissBurstThreshold {
		return false
	}
	// Threshold reached. Suppress repeated INFO rows for the same
	// session-id until the cooldown elapses; the operator only needs
	// one row per remediation cycle.
	if !e.lastEmittedAt.IsZero() && at.Sub(e.lastEmittedAt) < sessionMissBurstCooldown {
		return false
	}
	e.lastEmittedAt = at
	return true
}

// maybeSweepLocked reclaims entries that have gone quiet for longer
// than the window+cooldown horizon — an entry past that horizon can no
// longer influence any future decision record() makes for it, so it is
// unambiguous garbage. Must be called with t.mu held. Amortises to at
// most one full map walk per [sessionMissBurstSweepInterval].
func (t *sessionMissBurst) maybeSweepLocked(now time.Time) {
	if !t.lastSweepAt.IsZero() && now.Sub(t.lastSweepAt) < sessionMissBurstSweepInterval {
		return
	}
	t.lastSweepAt = now
	horizon := sessionMissBurstWindow + sessionMissBurstCooldown
	for id, e := range t.entries {
		if now.Sub(e.lastSeenAt) > horizon {
			delete(t.entries, id)
		}
	}
}
