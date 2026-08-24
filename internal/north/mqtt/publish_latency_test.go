// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"context"
	"errors"
	"testing"
	"time"
)

// slowPublisher answers after a fixed delay, so a test can assert on a
// duration it chose rather than on however long the machine took.
type slowPublisher struct {
	delay time.Duration
	err   error
	calls int
}

func (p *slowPublisher) Publish(_ context.Context, _ string, _ []byte, _ QoS, _ bool, _ ...PublishOption) error {
	p.calls++
	time.Sleep(p.delay)
	return p.err
}

// TestLatencyProbeTimesOnlyAcknowledgedPublishes is the negative control for
// the MQTT round-trip: a QoS 1 publish is timed, a QoS 0 publish is not.
//
// Without the QoS gate the probe would report near-zero on a QoS 0 deployment
// however sick the broker is, because a QoS 0 publish returns as soon as the
// packet is written to the socket — the broker never answers it. That reading
// is the same whether the broker is healthy or gone, which makes it worse than
// no reading at all.
func TestLatencyProbeTimesOnlyAcknowledgedPublishes(t *testing.T) {
	t.Parallel()

	t.Run("QoS 0 is forwarded but never timed", func(t *testing.T) {
		t.Parallel()
		inner := &slowPublisher{delay: 20 * time.Millisecond}
		p := NewLatencyProbe(inner)

		if err := p.Publish(context.Background(), "t", nil, QoS0, false); err != nil {
			t.Fatalf("Publish: %v", err)
		}

		if inner.calls != 1 {
			t.Errorf("inner publisher called %d times, want 1 — the probe must forward every publish", inner.calls)
		}
		if got := p.Stats(); got.Samples != 0 || got.Total != 0 {
			t.Errorf("Stats() = %+v after a QoS 0 publish, want no samples: the broker never acknowledges one, "+
				"so the duration measures this process's own socket buffer", got)
		}
	})

	t.Run("QoS 1 is timed", func(t *testing.T) {
		t.Parallel()
		inner := &slowPublisher{delay: 20 * time.Millisecond}
		p := NewLatencyProbe(inner)

		if err := p.Publish(context.Background(), "t", nil, QoS1, false); err != nil {
			t.Fatalf("Publish: %v", err)
		}

		got := p.Stats()
		if got.Samples != 1 || got.Total != 1 {
			t.Fatalf("Stats() = %+v after a QoS 1 publish, want one sample", got)
		}
		if got.LastMs < 15 {
			t.Errorf("LastMs = %v, want at least the publisher's 20ms delay (allowing for timer slack)", got.LastMs)
		}
	})
}

// TestLatencyProbeIgnoresFailedPublishes pins the second gate. A publish that
// failed took as long as it took to hit a refused connection or a tripped
// circuit breaker — that duration describes the failure, not the distance to a
// working broker, and mixing it in makes an outage look like a latency spike.
func TestLatencyProbeIgnoresFailedPublishes(t *testing.T) {
	t.Parallel()

	inner := &slowPublisher{delay: 5 * time.Millisecond, err: errors.New("broker gone")}
	p := NewLatencyProbe(inner)

	if err := p.Publish(context.Background(), "t", nil, QoS1, false); err == nil {
		t.Fatal("Publish returned nil, want the inner publisher's error passed through")
	}
	if got := p.Stats(); got.Samples != 0 {
		t.Errorf("Stats() = %+v after a failed publish, want no samples", got)
	}
}

// TestLatencyProbeWindowRollsAndSummarises pins the summary: the window is
// bounded, the median describes the retained samples, and Total keeps counting
// past the window so "quiet" stays distinguishable from "rolled many times".
func TestLatencyProbeWindowRollsAndSummarises(t *testing.T) {
	t.Parallel()

	p := NewLatencyProbe(&slowPublisher{})
	// Feed the ring directly: this asserts the summary maths, and driving it
	// through Publish would make the numbers whatever the scheduler produced.
	for i := 1; i <= latencyWindow+10; i++ {
		p.record(time.Duration(i) * time.Millisecond)
	}

	got := p.Stats()
	if got.Samples != latencyWindow {
		t.Errorf("Samples = %d, want the window cap %d", got.Samples, latencyWindow)
	}
	if got.Total != uint64(latencyWindow+10) {
		t.Errorf("Total = %d, want %d — Total must count past the window", got.Total, latencyWindow+10)
	}
	// The retained window is the last latencyWindow samples: 11ms … 138ms.
	if got.MaxMs != float64(latencyWindow+10) {
		t.Errorf("MaxMs = %v, want %v", got.MaxMs, float64(latencyWindow+10))
	}
	if got.LastMs != float64(latencyWindow+10) {
		t.Errorf("LastMs = %v, want %v", got.LastMs, float64(latencyWindow+10))
	}
	// Even count → mean of the two middle samples of 11..138.
	if want := 74.5; got.MedianMs != want {
		t.Errorf("MedianMs = %v, want %v", got.MedianMs, want)
	}
}

// TestLatencyProbeNilIsInert pins the MQTT-less deployment: a nil probe reports
// an empty summary instead of panicking the diagnostics gauge that reads it.
func TestLatencyProbeNilIsInert(t *testing.T) {
	t.Parallel()

	if p := NewLatencyProbe(nil); p != nil {
		t.Fatal("NewLatencyProbe(nil) returned a decorator around nothing")
	}
	var p *LatencyProbe
	if got := p.Stats(); got.Samples != 0 {
		t.Errorf("nil probe Stats() = %+v, want the zero value", got)
	}
}
