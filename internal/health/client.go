// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package health

import (
	"strings"
	"time"
)

// ClientHealth carries the per-interface detail metrics tracked
// per CCU connection: request timestamps, failure counters, and
// recovery state.
//
// The fields are populated by [Tracker.RecordRequest],
// [Tracker.SetRecoveryFlag], [Tracker.RecordReconnectAttempt],
// [Tracker.ResetReconnects], plus the existing [Tracker.Record] flow.
//
// Time fields use the Go-native [time.Time] returned by the injected
// clock; Go's runtime carries a monotonic reading on every `Time` value
// it produces from `Now()`, so [time.Since] / [time.Time.Sub] are
// DST-safe out of the box (no separate `*_monotonic` shadow field
// needed).
type ClientHealth struct {
	// LastSuccessfulRequest is the timestamp of the most recent RPC
	// that completed without error. Zero when no successful request
	// has been observed yet.
	LastSuccessfulRequest time.Time
	// LastFailedRequest is the timestamp of the most recent RPC that
	// returned a transport-level error. Semantic faults (unknown
	// parameter, validation rejections) are reported separately by
	// the reliability layer and do NOT advance this field — only
	// errors the circuit breaker would consider count here.
	LastFailedRequest time.Time
	// LastEventReceived is the timestamp of the most recent push
	// event the callback server received from this interface.
	LastEventReceived time.Time
	// ConsecutiveFailures counts RPC failures since the last success.
	// Reset to zero on the next successful call.
	ConsecutiveFailures int
	// ReconnectAttempts counts how often a recovery was triggered for
	// this interface since the daemon started (or since the last
	// [Tracker.ResetReconnects]).
	ReconnectAttempts int
	// InRecovery reports whether a recovery is currently in flight.
	// Mirrors the `RecoveryStarted` / `RecoveryCompleted` /
	// `RecoveryFailed` event pair on the bus.
	InRecovery bool
}

// PrimaryInterfaceHmIP is the fallback interface name used by
// [Tracker.PrimaryClientHealthy] when no explicit primary has been
// pinned via [Tracker.SetPrimaryInterface]. HmIP-RF is the
// preferred primary interface per the reference design.
const PrimaryInterfaceHmIP = "HmIP-RF"

// clientScoreState returns the State-Machine component of the
// per-client score (40 % weight in the health-score formula). Maps
// the current [Status] to a normalised value in [0, 1].
func clientScoreState(s Status) float64 {
	switch s {
	case StatusHealthy:
		return 1.0
	case StatusDegraded:
		return 0.5
	case StatusUnhealthy, StatusUnknown:
		return 0.0
	}
	return 0.0
}

// clientScoreCircuit returns the Circuit-Breaker component of the
// per-client score (30 % weight). Reads the most recent sample Note
// to infer the breaker state — `"breaker closed"` ⇒ 1.0,
// `"breaker half-open"` ⇒ 0.5, `"breaker open"` ⇒ 0.0. Missing
// information is treated as closed so a freshly-registered client
// does not get penalised for the breaker layer alone.
func clientScoreCircuit(note string) float64 {
	switch {
	case strings.Contains(note, "breaker open"):
		return 0.0
	case strings.Contains(note, "breaker half-open"):
		// A recovering (half-open) breaker contributes a third of the
		// circuit weight, not half.
		return 0.33
	default:
		return 1.0
	}
}

// clientScoreActivity returns the Recent-Activity component of the
// per-client score (30 % weight). Maps the age of the most recent
// event-received sample to a normalised value: < 60 s ⇒ 1.0,
// < 300 s ⇒ 0.66, < 600 s ⇒ 0.33, else 0. Staged-decay heuristic.
func clientScoreActivity(age time.Duration) float64 {
	switch {
	case age < 60*time.Second:
		return 1.0
	case age < 300*time.Second:
		return 0.66
	case age < 600*time.Second:
		return 0.33
	default:
		return 0.0
	}
}

// composeClientScore weights the three pillars to the same 40/30/30
// mix the health-score formula uses, then clamps the result to
// [0, 1] so a misconfigured Sample.Note cannot push the score outside
// the legal range.
func composeClientScore(state, circuit, activity float64) float64 {
	score := 0.4*state + 0.3*circuit + 0.3*activity
	if score < 0 {
		return 0
	}
	if score > 1 {
		return 1
	}
	return score
}
