// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build loadtest

package loadtest

// metrics.go holds the no-dependency measurement primitives the load
// test reports against: a latency histogram (sorted-sample percentiles),
// a goroutine-leak detector, a dropped-request counter, and a memory
// snapshot pair. Everything stays in-stdlib so the harness adds no new
// module dependency.

import (
	"runtime"
	"sort"
	"sync"
	"time"
)

// latencyHist is a concurrency-safe sample collector. It keeps every
// observed duration and computes percentiles by sorting on demand —
// adequate for a soak run (a few million samples is a handful of MB)
// and exact, unlike a bucketed approximation. For the operator-scale
// 60-minute run the sample slice is the dominant allocation; that is an
// accepted trade for not pulling in an hdr-histogram dependency.
type latencyHist struct {
	mu      sync.Mutex
	samples []time.Duration
}

// newLatencyHist pre-sizes the sample slice to `hint` to avoid early
// re-grows under the initial request burst.
func newLatencyHist(hint int) *latencyHist {
	if hint < 0 {
		hint = 0
	}
	return &latencyHist{samples: make([]time.Duration, 0, hint)}
}

// observe records one latency sample.
func (h *latencyHist) observe(d time.Duration) {
	h.mu.Lock()
	h.samples = append(h.samples, d)
	h.mu.Unlock()
}

// count returns the number of recorded samples.
func (h *latencyHist) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.samples)
}

// percentiles returns p50 / p95 / p99 over the recorded samples. A
// nearest-rank percentile is used (no interpolation) — deterministic
// and sufficient for a pass/fail latency gate. Returns zeros for an
// empty histogram.
func (h *latencyHist) percentiles() (p50, p95, p99 time.Duration) {
	h.mu.Lock()
	snap := make([]time.Duration, len(h.samples))
	copy(snap, h.samples)
	h.mu.Unlock()
	if len(snap) == 0 {
		return 0, 0, 0
	}
	sort.Slice(snap, func(i, j int) bool { return snap[i] < snap[j] })
	return nearestRank(snap, 0.50), nearestRank(snap, 0.95), nearestRank(snap, 0.99)
}

// nearestRank returns the nearest-rank percentile of a pre-sorted
// slice. q is in [0,1]; the rank index is ceil(q*n)-1 clamped to the
// slice bounds.
func nearestRank(sorted []time.Duration, q float64) time.Duration {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	idx := int(q*float64(n) + 0.999999) // ceil
	if idx < 1 {
		idx = 1
	}
	if idx > n {
		idx = n
	}
	return sorted[idx-1]
}

// goroutineLeak captures a NumGoroutine baseline and reports the delta
// after the workload has wound down. A small positive delta is normal
// (the test runtime, the http transport's idle-conn reaper, GC
// scavenger) — callers compare against an allowed tolerance.
type goroutineLeak struct {
	baseline int
}

// captureGoroutineBaseline records the current goroutine count after a
// warmup window. settle gives in-flight goroutines time to retire and a
// GC a chance to run before the snapshot.
func captureGoroutineBaseline(settle time.Duration) goroutineLeak {
	settleRuntime(settle)
	return goroutineLeak{baseline: runtime.NumGoroutine()}
}

// delta returns (current - baseline) after letting the runtime settle.
// Negative deltas are clamped to 0 — a count that drops below baseline
// is never a leak.
func (g goroutineLeak) delta(settle time.Duration) int {
	settleRuntime(settle)
	d := runtime.NumGoroutine() - g.baseline
	if d < 0 {
		return 0
	}
	return d
}

// settleRuntime gives background goroutines time to retire and forces a
// GC so finalizer/idle goroutines are reaped before a snapshot.
func settleRuntime(d time.Duration) {
	if d > 0 {
		time.Sleep(d)
	}
	runtime.GC()
	if d > 0 {
		time.Sleep(d / 2)
	}
}

// memSnapshot is a coarse memory reading the soak uses to assert heap
// stability across the run.
type memSnapshot struct {
	HeapAlloc uint64
	Sys       uint64
}

// readMem returns a memory snapshot after a GC so HeapAlloc reflects
// live (not yet-collected) memory.
func readMem() memSnapshot {
	runtime.GC()
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	return memSnapshot{HeapAlloc: m.HeapAlloc, Sys: m.Sys}
}

// heapGrowthRatio reports the post/pre HeapAlloc ratio. A ratio near
// 1.0 means the heap returned to its pre-soak size; a runaway leak
// shows up as a large ratio. Guards against a zero pre-reading.
func heapGrowthRatio(pre, post memSnapshot) float64 {
	if pre.HeapAlloc == 0 {
		return 1.0
	}
	return float64(post.HeapAlloc) / float64(pre.HeapAlloc)
}
