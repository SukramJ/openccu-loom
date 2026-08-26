// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package middleware

import (
	"net/http"
	"sync/atomic"
)

// StatusMetrics collects HTTP-status-code counters for the daemon's
// REST surface. The struct is safe for concurrent use; all counters
// are atomics so the middleware never contends on a mutex on the hot
// path.
//
// Counters are cumulative since daemon start. Diagnostics endpoints
// surface them as gauges via [StatusMetrics.ServerErrors] and
// [StatusMetrics.ClientErrors] — values are monotonically
// increasing, so a non-zero number always represents a real
// observation rather than transient state.
type StatusMetrics struct {
	serverErrors  atomic.Uint64
	clientErrors  atomic.Uint64
	totalRequests atomic.Uint64
}

// NewStatusMetrics returns a freshly-zeroed counter set.
func NewStatusMetrics() *StatusMetrics {
	return &StatusMetrics{}
}

// ServerErrors returns the cumulative count of `5xx` responses.
func (m *StatusMetrics) ServerErrors() uint64 { return m.serverErrors.Load() }

// ClientErrors returns the cumulative count of `4xx` responses.
// Exposed alongside the 5xx counter so the SPA can distinguish
// "configured wrong" (4xx burst) from "daemon broken" (5xx burst).
func (m *StatusMetrics) ClientErrors() uint64 { return m.clientErrors.Load() }

// TotalRequests returns the cumulative count of every served
// request, regardless of status. Useful for rate-style readouts in
// future revisions; the diagnostics endpoint currently only emits
// the error counters.
func (m *StatusMetrics) TotalRequests() uint64 { return m.totalRequests.Load() }

// StatusCounter is a Chi-compatible middleware that classifies every
// response by its HTTP status code and increments the matching
// counter on `metrics`. Passing nil metrics yields a pass-through
// (no allocation, no work) so callers can wire the middleware
// unconditionally and toggle observability via the deps wiring.
func StatusCounter(metrics *StatusMetrics) func(http.Handler) http.Handler {
	if metrics == nil {
		return func(next http.Handler) http.Handler { return next }
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(sw, r)
			metrics.totalRequests.Add(1)
			switch {
			case sw.status >= 500:
				metrics.serverErrors.Add(1)
			case sw.status >= 400:
				metrics.clientErrors.Add(1)
			}
		})
	}
}
