// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"sync"
	"time"
)

// LatencyProbe is a [Publisher] decorator that times how long the broker takes
// to acknowledge a publish, so an operator can tell a slow broker apart from a
// slow CCU. Wrap the breaker with it at the composition root; it forwards every
// call unchanged.
//
// Only QoS 1 and 2 publishes are timed. A QoS 0 publish returns as soon as the
// packet is written to the socket — the broker never answers it — so timing one
// would measure this process's own buffer and report near-zero however sick the
// broker is. That would be a reading with no negative control: it looks the same
// whether the broker is healthy or gone. The daemon publishes state at the
// configured QoS, so on a QoS 0 deployment this probe has nothing to report and
// says so ([LatencyProbe.Stats] returns Samples 0) rather than inventing a number.
//
// A failed publish is not timed either. Its duration is the time to a refused
// connection or a tripped circuit breaker, which describes the failure, not the
// distance to a working broker.
//
// What it measures is the full acknowledged publish: network there and back,
// plus the broker's own processing, plus any time the client's in-flight window
// held the packet back. That last part means a saturated publish queue shows up
// here as latency — which is what an operator wants to see, but it is why this
// is reported as acknowledgement time rather than as a network round-trip.
// loom:reachable:reason="wrapped around the breaker in buildMQTT and held by mqttSwap; the composition root passes it as a Publisher, so the analyzer sees the interface and never the concrete type"
type LatencyProbe struct {
	pub Publisher

	mu      sync.Mutex
	samples []time.Duration
	total   uint64
}

// latencyWindow caps how many recent samples the probe keeps. The window
// bounds memory on a bridge that publishes continuously, and it keeps the
// summary describing the recent past: a median over the whole uptime of a
// daemon that has been running for weeks would take days to react to a broker
// that went slow an hour ago.
const latencyWindow = 128

// NewLatencyProbe wraps pub. A nil pub yields a nil probe so a deployment
// without MQTT wires nothing rather than a decorator around nothing.
func NewLatencyProbe(pub Publisher) *LatencyProbe {
	if pub == nil {
		return nil
	}
	return &LatencyProbe{pub: pub, samples: make([]time.Duration, 0, latencyWindow)}
}

// Publish implements [Publisher].
func (p *LatencyProbe) Publish(ctx context.Context, topic string, payload []byte, qos QoS, retain bool, opts ...PublishOption) error {
	if p == nil {
		return nil
	}
	if qos == QoS0 {
		return p.pub.Publish(ctx, topic, payload, qos, retain, opts...)
	}
	started := time.Now()
	err := p.pub.Publish(ctx, topic, payload, qos, retain, opts...)
	if err == nil {
		p.record(time.Since(started))
	}
	return err
}

// record files one acknowledgement duration, evicting the oldest sample once
// the window is full.
func (p *LatencyProbe) record(d time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.total++
	if len(p.samples) < latencyWindow {
		p.samples = append(p.samples, d)
		return
	}
	copy(p.samples, p.samples[1:])
	p.samples[len(p.samples)-1] = d
}

// LatencyStats summarises the recent acknowledged publishes.
//
// Every field here is published as a gauge. Window occupancy and the single
// most recent sample were dropped rather than carried: occupancy saturates
// within seconds of bring-up and then cannot distinguish a live median from a
// stale one (which is what Total is for), and one sample is noise next to the
// median beside it.
// loom:reachable:reason="returned by LatencyProbe.Stats through mqttSupervisor.PublishLatency and read field-by-field by the mqtt.publish_ack_* diagnostics gauges; the analyzer sees the accessor, not the struct behind it"
type LatencyStats struct {
	// Total counts every timed publish since start. A total that advances
	// says the median is current, one that stops says it is stale, and zero
	// says nothing has ever been measured — which on a QoS 0 deployment is
	// the permanent and correct answer.
	Total uint64
	// MedianMs is the middle of the window: the latency a typical publish
	// sees, unmoved by a single stalled one.
	MedianMs float64
	// MaxMs is the worst in the window. It is reported beside the median
	// because the two disagreeing is the signal — a broker that is fine on
	// average but occasionally stalls looks healthy on the median alone.
	MaxMs float64
}

// Stats summarises the current window. Safe on a nil probe (an MQTT-less
// deployment), which reports an empty summary.
func (p *LatencyProbe) Stats() LatencyStats {
	if p == nil {
		return LatencyStats{}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.samples) == 0 {
		return LatencyStats{Total: p.total}
	}
	sorted := make([]time.Duration, len(p.samples))
	copy(sorted, p.samples)
	// Insertion sort: the window is small and nearly always already
	// near-sorted only by accident, but 128 elements make the constant
	// factor irrelevant next to pulling in a sort for one call site.
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
	return LatencyStats{
		Total:    p.total,
		MedianMs: ms(median),
		MaxMs:    ms(sorted[len(sorted)-1]),
	}
}
