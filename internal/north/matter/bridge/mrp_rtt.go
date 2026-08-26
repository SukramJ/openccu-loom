// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package bridge

import "time"

// The MRP round-trip is the distance to a paired Matter controller — an Apple
// TV or HomePod acting as a Home hub, a Google speaker, a chip-tool run. It is
// diagnostic only: nothing in the bridge steers on it, and it is deliberately
// not exposed as a cluster attribute. Matter has its own vocabulary for
// controller-visible diagnostics (the Diagnostic Logs and network-diagnostics
// clusters), and inventing an attribute outside it is exactly the kind of
// hand-rolled deviation from matter.js that produces silent pair-aborts.
//
// What it is for: telling a controller that has gone unreachable apart from
// one that is merely slow. A retransmit storm and a healthy fabric both show
// "subscriptions alive" in every other surface; the round-trip separates them.

// mrpRTTWindowSize caps the retained samples. Small on purpose — a controller
// that went slow ten minutes ago is not the fault being diagnosed, and a long
// window would keep reporting the healthy past through the sick present.
const mrpRTTWindowSize = 64

// mrpRTTWindow is a fixed-size ring of recent round-trip samples. Not safe for
// concurrent use on its own — every method is called with the owning tracker's
// mutex held.
type mrpRTTWindow struct {
	samples []time.Duration
	total   uint64
}

// record files one sample, evicting the oldest once the window is full.
func (w *mrpRTTWindow) record(d time.Duration) {
	if d <= 0 {
		return
	}
	w.total++
	if len(w.samples) < mrpRTTWindowSize {
		w.samples = append(w.samples, d)
		return
	}
	copy(w.samples, w.samples[1:])
	w.samples[len(w.samples)-1] = d
}

// MRPRTTStats summarises recent first-try round-trips to Matter controllers.
//
// Every field is published as a gauge; see [mqtt.LatencyStats] for why window
// occupancy and the single most recent sample are not among them.
// loom:reachable:reason="returned by Bridge.ControllerRTT and read field-by-field by the matter.controller_rtt_* diagnostics gauges; the analyzer sees the accessor, not the struct behind it"
type MRPRTTStats struct {
	// Total counts every measurement since start. Zero is the steady state of
	// a bridge nobody has paired; a total that stops advancing says the
	// median describes the last exchange rather than the present.
	Total uint64
	// MedianMs is the middle of the window.
	MedianMs float64
	// MaxMs is the worst in the window. A controller that is usually prompt
	// but occasionally takes seconds is the retransmit-storm signature, and
	// the median alone hides it.
	MaxMs float64
}

// stats summarises the window.
func (w *mrpRTTWindow) stats() MRPRTTStats {
	if len(w.samples) == 0 {
		return MRPRTTStats{Total: w.total}
	}
	sorted := make([]time.Duration, len(w.samples))
	copy(sorted, w.samples)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j] < sorted[j-1]; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	ms := func(d time.Duration) float64 { return float64(d.Nanoseconds()) / float64(time.Millisecond) }
	mid := len(sorted) / 2
	median := sorted[mid]
	if len(sorted)%2 == 0 {
		median = (sorted[mid-1] + sorted[mid]) / 2
	}
	return MRPRTTStats{
		Total:    w.total,
		MedianMs: ms(median),
		MaxMs:    ms(sorted[len(sorted)-1]),
	}
}
