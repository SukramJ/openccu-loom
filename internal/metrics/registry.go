// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package metrics

import (
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// Kind distinguishes the flavours the registry renders.
type Kind string

// Kind values.
const (
	KindCounter Kind = "counter"
	KindGauge   Kind = "gauge"
)

// Metric is one named measurement with optional labels.
type Metric struct {
	Name   string
	Help   string
	Kind   Kind
	Labels []string // ordered label names
	value  atomic.Uint64
	// For gauges we additionally track a float representation; we
	// store it as bit-pattern via atomic.Uint64.
	floatBits atomic.Uint64
}

// Counter is a monotonic integer.
type Counter struct{ m *Metric }

// Gauge is a read/write float.
type Gauge struct{ m *Metric }

// Inc adds 1. Panics on overflow only in absurd edge cases.
func (c *Counter) Inc() { c.m.value.Add(1) }

// Add adds delta (must be non-negative).
func (c *Counter) Add(delta uint64) { c.m.value.Add(delta) }

// Value returns the current count.
func (c *Counter) Value() uint64 { return c.m.value.Load() }

// Set updates the gauge.
func (g *Gauge) Set(v float64) {
	g.m.floatBits.Store(floatToBits(v))
}

// Value returns the current gauge value.
func (g *Gauge) Value() float64 {
	return bitsToFloat(g.m.floatBits.Load())
}

// Registry holds the collected metrics.
type Registry struct {
	mu      sync.RWMutex
	metrics map[string]*Metric
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{metrics: make(map[string]*Metric)}
}

// Counter registers (or returns the existing) counter with the
// given name + help text.
func (r *Registry) Counter(name, help string) *Counter {
	m := r.upsert(name, help, KindCounter)
	return &Counter{m: m}
}

// Gauge registers (or returns the existing) gauge.
func (r *Registry) Gauge(name, help string) *Gauge {
	m := r.upsert(name, help, KindGauge)
	return &Gauge{m: m}
}

func (r *Registry) upsert(name, help string, kind Kind) *Metric {
	r.mu.Lock()
	defer r.mu.Unlock()
	if m, ok := r.metrics[name]; ok {
		return m
	}
	m := &Metric{Name: name, Help: help, Kind: kind}
	r.metrics[name] = m
	return m
}

// Metrics returns every registered metric sorted by name.
func (r *Registry) Metrics() []*Metric {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Metric, 0, len(r.metrics))
	for _, m := range r.metrics {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Render writes the registry to w in Prometheus exposition format
// (v0.0.4 / OpenMetrics-compatible subset).
func (r *Registry) Render(w io.Writer) error {
	for _, m := range r.Metrics() {
		if m.Help != "" {
			if _, err := fmt.Fprintf(w, "# HELP %s %s\n", m.Name, strings.ReplaceAll(m.Help, "\n", " ")); err != nil {
				return err
			}
		}
		if _, err := fmt.Fprintf(w, "# TYPE %s %s\n", m.Name, m.Kind); err != nil {
			return err
		}
		var line string
		switch m.Kind {
		case KindCounter:
			line = fmt.Sprintf("%s %d\n", m.Name, m.value.Load())
		case KindGauge:
			line = fmt.Sprintf("%s %s\n", m.Name, strconv.FormatFloat(bitsToFloat(m.floatBits.Load()), 'f', -1, 64))
		}
		if _, err := io.WriteString(w, line); err != nil {
			return err
		}
	}
	return nil
}

// floatToBits / bitsToFloat round-trip a float64 through a uint64 so
// atomic.Uint64 can store it.
func floatToBits(f float64) uint64 { return bitsFromFloat(f) }
func bitsToFloat(b uint64) float64 { return floatFromBits(b) }
