// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package hmreliability is the single source of truth for the timing
// and threshold defaults used by the southbound reliability layer
// (`internal/client/reliability`). The constants mirror the Python
// reference implementation's `client/_send_request.py` defaults so
// the two stacks stay behaviourally aligned.
//
// Why a public package: the per-component defaults were historically
// scattered across `circuit.go`, `retry.go`, `throttle.go`, and
// `pingpong.go`. That made silent drift against the Python reference
// implementation possible and invisible to CI. Centralising them here
// lets the [TestRecordedReliabilityDefaults] snapshot lock the
// values; any drift triggers a CI failure that demands an explicit
// by-design entry (notes/parity/by_design.md) before the change lands.
//
// Drift policy: when the Python reference implementation advances and
// a default genuinely changes upstream, update the constant *and* the
// snapshot in the same commit, with a reference in the commit
// message.
package hmreliability

import "time"

// Circuit-breaker defaults — mirror the Python reference
// implementation's `Connection` constructor (`client/connection.py`).
const (
	// CircuitFailureThreshold is the number of consecutive errors after which
	// the breaker trips OPEN.
	CircuitFailureThreshold = 5
	// CircuitResetTimeout is the dwell time in OPEN before the breaker advances
	// to HALF-OPEN.
	CircuitResetTimeout = 30 * time.Second
	// CircuitHalfOpenSuccess is the number of consecutive successes in HALF-OPEN
	// required to close the breaker.
	CircuitHalfOpenSuccess = 2
)

// Retrier defaults — mirror
// (`client/_send_request.py` + `command_retry.py`).
const (
	// RetryInitialBackoff is the first sleep before the first retry.
	// The 2 s value is production-hardened on real CCUs; smaller
	// initial backoff produces measurably more retry storms under
	// duty-cycle saturation.
	RetryInitialBackoff = 2 * time.Second
	// RetryMaxBackoff bounds exponential growth.
	RetryMaxBackoff = 30 * time.Second
	// RetryDutyCycleDelay is the wait inserted when a CCU rejects a command with
	// a duty-cycle / RF-throttle error.
	RetryDutyCycleDelay = 40 * time.Second
	// RetryTransmissionPendingDelay is the wait inserted when a CCU reports a
	// transmission-pending stall.
	RetryTransmissionPendingDelay = 5 * time.Second
	// RetryRecoveryWait caps how long the retrier blocks for a
	// connection-recovery event before giving up. 120 s mirrors the
	// reference Python stack's recovery-wait ceiling.
	RetryRecoveryWait = 120 * time.Second
)

// Throttle defaults — mirror
// (`command_throttle.py`).
const (
	// ThrottleInterCommandDelay is the minimum gap between two consecutive
	// non-critical commands on the same interface. Default 0 — burst
	// protection alone is usually sufficient for HmIP-RF.
	ThrottleInterCommandDelay = 0 * time.Millisecond
	// ThrottleBurstThreshold is the soft cap on non-critical commands per
	// [ThrottleBurstWindow].
	ThrottleBurstThreshold = 5
	// ThrottleBurstWindow is the rolling window across which the burst threshold
	// is measured.
	ThrottleBurstWindow = 500 * time.Millisecond
	// ThrottleMaxQueueDepthFactor scales [ThrottleMaxInFlight] into
	// the recommended [ThrottleConfig.MaxQueueDepth]. The audit
	// recommendation in §4.2.O9 is "4× MaxInFlight"; we centralise
	// it here so the multiplier stays one value across callers.
	ThrottleMaxQueueDepthFactor = 4
)

// Ping/pong defaults — mirror
// (`client/connection.py`).
const (
	// PingPongTTL is how long a sent ping is kept in the pending map before it
	// is considered orphaned.
	PingPongTTL = 300 * time.Second
)
