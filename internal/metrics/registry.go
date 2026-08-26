// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

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
	Name string
	Help string
	Kind Kind
	// Labels holds the ordered label names shared by every series under
	// Name (e.g. ["central"]); LabelValues holds this particular
	// series' values in the same order. Both are nil for an unlabeled
	// metric.
	Labels      []string
	LabelValues []string
	value       atomic.Uint64
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
	return r.upsertLabeled(name, help, kind, nil, nil)
}

// upsertLabeled is [Registry.upsert] extended with a label dimension.
// labelNames and labelValues are parallel and ordered; a metric is keyed
// by name PLUS labelValues, so distinct label values register distinct
// series while an identical (name, labelValues) pair shares storage —
// mirroring upsert's dedup-by-name for the unlabeled case.
func (r *Registry) upsertLabeled(name, help string, kind Kind, labelNames, labelValues []string) *Metric {
	key := seriesKey(name, labelValues)
	r.mu.Lock()
	defer r.mu.Unlock()
	if m, ok := r.metrics[key]; ok {
		return m
	}
	m := &Metric{Name: name, Help: help, Kind: kind, Labels: labelNames, LabelValues: labelValues}
	r.metrics[key] = m
	return m
}

// seriesKey composes the registry's internal storage key for one series:
// the metric name is not unique on its own once a label dimension is in
// play, so the label values join it to disambiguate.
func seriesKey(name string, labelValues []string) string {
	if len(labelValues) == 0 {
		return name
	}
	return name + "\x00" + strings.Join(labelValues, "\x00")
}

// LabeledCounter registers (or returns the existing) counter family
// sharing name, help and a single label named labelName. Use
// [LabeledCounter.WithLabelValue] to obtain the series for one label
// value; every series renders under the same Prometheus name with a
// distinguishing `{<labelName>="<value>"}` suffix.
func (r *Registry) LabeledCounter(name, help, labelName string) *LabeledCounter {
	return &LabeledCounter{reg: r, name: name, help: help, labelName: labelName}
}

// LabeledCounter is a Counter family keyed by one label's value —
// registered lazily, one series per distinct value seen.
//
// loom:reachable:reason="the per-central MQTT counters are LabeledCounter values reached through the collector's accessor funcs; the analyzer sees the accessor, not the type behind it"
type LabeledCounter struct {
	reg       *Registry
	name      string
	help      string
	labelName string
}

// WithLabelValue returns the Counter for value, registering it on first
// use. Repeated calls with the same value return the same series.
func (lc *LabeledCounter) WithLabelValue(value string) *Counter {
	m := lc.reg.upsertLabeled(lc.name, lc.help, KindCounter, []string{lc.labelName}, []string{value})
	return &Counter{m: m}
}

// Metrics returns every registered metric sorted by (name, label
// values) — every series of one labeled family therefore sorts
// consecutively, which is what [Registry.Render] relies on to emit one
// HELP/TYPE block per name.
func (r *Registry) Metrics() []*Metric {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Metric, 0, len(r.metrics))
	for _, m := range r.metrics {
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name != out[j].Name {
			return out[i].Name < out[j].Name
		}
		return strings.Join(out[i].LabelValues, "\x00") < strings.Join(out[j].LabelValues, "\x00")
	})
	return out
}

// Render writes the registry to w in Prometheus exposition format
// (v0.0.4 / OpenMetrics-compatible subset). A labeled family emits one
// HELP/TYPE block (keyed off the first series) followed by every series
// as its own sample line — the format Prometheus expects for one metric
// exposed with multiple label combinations.
func (r *Registry) Render(w io.Writer) error {
	metrics := r.Metrics()
	for i, m := range metrics {
		firstOfName := i == 0 || metrics[i-1].Name != m.Name
		if firstOfName {
			if m.Help != "" {
				if _, err := fmt.Fprintf(w, "# HELP %s %s\n", m.Name, strings.ReplaceAll(m.Help, "\n", " ")); err != nil {
					return err
				}
			}
			if _, err := fmt.Fprintf(w, "# TYPE %s %s\n", m.Name, m.Kind); err != nil {
				return err
			}
		}
		var line string
		switch m.Kind {
		case KindCounter:
			line = fmt.Sprintf("%s%s %d\n", m.Name, labelSuffix(m), m.value.Load())
		case KindGauge:
			line = fmt.Sprintf("%s%s %s\n", m.Name, labelSuffix(m), strconv.FormatFloat(bitsToFloat(m.floatBits.Load()), 'f', -1, 64))
		}
		if _, err := io.WriteString(w, line); err != nil {
			return err
		}
	}
	return nil
}

// labelSuffix renders a series' `{name="value",...}` label block, or the
// empty string for an unlabeled metric.
func labelSuffix(m *Metric) string {
	if len(m.Labels) == 0 {
		return ""
	}
	parts := make([]string, len(m.Labels))
	for i, name := range m.Labels {
		parts[i] = fmt.Sprintf("%s=%q", name, m.LabelValues[i])
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// floatToBits / bitsToFloat round-trip a float64 through a uint64 so
// atomic.Uint64 can store it.
func floatToBits(f float64) uint64 { return bitsFromFloat(f) }
func bitsToFloat(b uint64) float64 { return floatFromBits(b) }
