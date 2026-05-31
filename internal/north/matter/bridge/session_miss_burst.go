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
}

// sessionMissEntry caps each session-id's bookkeeping at a few words —
// the receive path needs ~µs lock-hold latency, so we deliberately
// avoid maintaining a slice of timestamps.
type sessionMissEntry struct {
	firstAt       time.Time // start of the current rolling window
	count         uint32    // misses observed inside the current window
	lastEmittedAt time.Time // last time a burst INFO fired for this session-id
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
	e, ok := t.entries[sessionID]
	if !ok {
		t.entries[sessionID] = &sessionMissEntry{firstAt: at, count: 1}
		return false
	}
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
