// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hub

import (
	"maps"
	"sync"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/datapoint"
	"github.com/SukramJ/openccu-loom/internal/payload"
)

// MetricKind identifies a hub metric.
type MetricKind string

// MetricKind values.
const (
	MetricSystemHealth     MetricKind = "system_health"
	MetricConnectionLatMs  MetricKind = "connection_latency_ms"
	MetricLastEventAgeSecs MetricKind = "last_event_age_seconds"
)

// MetricSystemHealthUnknown is the not-ready sentinel for
// [MetricSystemHealth]. A negative score means the producing central is not
// operational (a CCU outage), so the value is unknown rather than a live
// percentage. North-bound consumers render a negative system_health as
// "unknown" instead of a stale reading; the reconcile pass leaves a negative
// probe result untouched. Only system_health carries a sentinel — the other
// metrics are simply absent (unobserved) while unknown.
const MetricSystemHealthUnknown = -1.0

// MetricSample is one observation of a metric.
type MetricSample struct {
	Kind  MetricKind
	Value float64
	When  time.Time
}

// Metrics bundles the hub-level metric sensors. Each metric carries
// the last observed value, a ModifiedAt timestamp, and per-metric
// subscribers. Dedup is value-equality based: emitting the same
// value twice does not fire callbacks.
type Metrics struct {
	// ServiceRegistry implements the write-half of [payload.Source].
	// Metrics is read-only; the zero value gives correct no-service
	// behaviour.
	payload.ServiceRegistry

	mu      sync.RWMutex
	state   map[MetricKind]MetricSample
	subs    map[MetricKind][]func(MetricSample)
	anySubs []func(MetricSample)
}

// NewMetrics constructs an empty Metrics aggregator.
func NewMetrics() *Metrics {
	return &Metrics{
		state: make(map[MetricKind]MetricSample),
		subs:  make(map[MetricKind][]func(MetricSample)),
	}
}

// Observe records a metric observation. Returns true when the value
// actually changed.
func (m *Metrics) Observe(kind MetricKind, value float64) bool {
	now := time.Now()
	m.mu.Lock()
	prev, had := m.state[kind]
	changed := !had || prev.Value != value
	sample := MetricSample{Kind: kind, Value: value, When: now}
	m.state[kind] = sample
	perKind := make([]func(MetricSample), len(m.subs[kind]))
	copy(perKind, m.subs[kind])
	anyCopy := make([]func(MetricSample), len(m.anySubs))
	copy(anyCopy, m.anySubs)
	m.mu.Unlock()
	if !changed {
		return false
	}
	for _, cb := range perKind {
		if cb != nil {
			cb(sample)
		}
	}
	for _, cb := range anyCopy {
		if cb != nil {
			cb(sample)
		}
	}
	return true
}

// Value returns the last observation for kind, or (MetricSample{}, false)
// when the metric has never been observed.
func (m *Metrics) Value(kind MetricKind) (MetricSample, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.state[kind]
	return s, ok
}

// Snapshot returns a copy of every observed metric.
func (m *Metrics) Snapshot() map[MetricKind]MetricSample {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[MetricKind]MetricSample, len(m.state))
	maps.Copy(out, m.state)
	return out
}

// OnUpdate subscribes a handler for one metric kind. Returns an
// idempotent unsubscribe closure.
func (m *Metrics) OnUpdate(kind MetricKind, fn func(MetricSample)) func() {
	m.mu.Lock()
	m.subs[kind] = append(m.subs[kind], fn)
	idx := len(m.subs[kind]) - 1
	m.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			m.mu.Lock()
			defer m.mu.Unlock()
			if idx < len(m.subs[kind]) {
				m.subs[kind][idx] = nil
			}
		})
	}
}

// TranslationKeyForMetric returns the HA translation key for a given metric
// kind.
func TranslationKeyForMetric(kind MetricKind) string {
	switch kind {
	case MetricSystemHealth:
		return "system_health"
	case MetricConnectionLatMs:
		return "connection_latency_ms"
	case MetricLastEventAgeSecs:
		return "last_event_age_seconds"
	}
	return string(kind)
}

// MetricSensorName returns the stable, locale-independent identifier for a
// metric kind. It is read by [MetricHubSensor.Signature] and nothing else.
//
// It is not the operator-visible name of these entities: MQTT discovery
// resolves that through the i18n catalogues (keys discovery.system_health,
// discovery.connection_latency, discovery.last_event_age), so the same three
// metrics surface to a German operator as "Systemzustand",
// "Verbindungslatenz" and "Alter letztes Ereignis". Changing the strings
// below renames a debug signature, not an entity.
func MetricSensorName(kind MetricKind) string {
	switch kind {
	case MetricSystemHealth:
		return "HM-System-Health"
	case MetricConnectionLatMs:
		return "HM-Connection-Latency"
	case MetricLastEventAgeSecs:
		return "HM-Last-Event-Age"
	}
	return string(kind)
}

// MetricSensorUnit returns the unit string for a metric kind. It is the one
// live member of this group: the REST hub-data-points handler and the
// WebSocket hub-events stream both stamp it onto the sample they emit.
func MetricSensorUnit(kind MetricKind) string {
	switch kind {
	case MetricSystemHealth:
		return "%"
	case MetricConnectionLatMs:
		return "ms"
	case MetricLastEventAgeSecs:
		return "s"
	}
	return ""
}

// MetricSensorDescription returns the English description for a metric kind,
// stamped onto [MetricHubSensor.Description] at construction. No north-bound
// adapter reads it: entity descriptions on the published surfaces come from
// the i18n catalogues, and a string returned here would reach an operator
// untranslated. Give it a catalogue key before wiring it to any plane.
func MetricSensorDescription(kind MetricKind) string {
	switch kind {
	case MetricSystemHealth:
		return "HomeMatic system health percentage"
	case MetricConnectionLatMs:
		return "Round-trip latency to the CCU in milliseconds"
	case MetricLastEventAgeSecs:
		return "Seconds since the last event was received from the CCU"
	}
	return ""
}

// MetricHubSensor exposes one [MetricKind] from a [*Metrics] aggregate
// as a [HubDataPointer]-compatible entity. Each [MetricKind]
// (system_health, connection_latency_ms, last_event_age_seconds) is a thin
// view over the shared [Metrics] aggregate rather than a separate data-point
// instance, so the three views cost no extra state.
//
// No production code constructs one. The planes that surface hub metrics
// today read the [Metrics] aggregate directly — MQTT discovery builds its own
// entity per metric with a catalogue-resolved name, and REST/WS read
// [Metrics.Value] — so this type is a [HubDataPointer]-shaped adapter that is
// available but unused. Anything asserted here about a north-bound consumer
// would be an assertion about no caller; wiring it is what would make its
// Name and Description operator-visible, and both are English-only.
type MetricHubSensor struct {
	datapoint.BaseDataPointFields
	// Kind identifies which metric this view exposes.
	Kind MetricKind
	// Unit is the unit string (%, ms, s).
	Unit string
	// Name is [MetricSensorName] for Kind — the locale-independent debug
	// identifier [MetricHubSensor.Signature] is built from, not a label
	// fit for an operator.
	Name string
	// Description is [MetricSensorDescription] for Kind: English only,
	// with no catalogue key behind it.
	Description string

	metrics *Metrics
}

// NewMetricHubSensor constructs a MetricHubSensor for the given kind,
// backed by the provided Metrics aggregate.
func NewMetricHubSensor(centralName string, kind MetricKind, m *Metrics) *MetricHubSensor {
	name := MetricSensorName(kind)
	return &MetricHubSensor{
		BaseDataPointFields: datapoint.NewBaseDataPointFields(centralName, "HUB", string(kind)),
		Kind:                kind,
		Unit:                MetricSensorUnit(kind),
		Name:                name,
		Description:         MetricSensorDescription(kind),
		metrics:             m,
	}
}

// Signature returns the debug identifier. Satisfies [HubDataPointer].
func (s *MetricHubSensor) Signature() string {
	return "HUB_SENSOR/" + s.Name
}

// StateUncertain returns false when at least one observation has been
// recorded for this metric kind; true otherwise. Satisfies [HubDataPointer].
func (s *MetricHubSensor) StateUncertain() bool {
	_, ok := s.metrics.Value(s.Kind)
	return !ok
}

// Available returns true when the metric has been observed at least once.
func (s *MetricHubSensor) Available() bool {
	_, ok := s.metrics.Value(s.Kind)
	return ok
}

// Value returns the last observed float64 value and whether it has been
// observed.
func (s *MetricHubSensor) Value() (float64, bool) {
	sample, ok := s.metrics.Value(s.Kind)
	return sample.Value, ok
}

// TranslationKey returns the HA translation key for this metric sensor.
func (s *MetricHubSensor) TranslationKey() string {
	return TranslationKeyForMetric(s.Kind)
}

// MetricHubSensors creates the three standard metric hub sensors backed
// by the given Metrics aggregate. Returns (systemHealth, latency, eventAge).
func MetricHubSensors(centralName string, m *Metrics) (systemHealth, latency, eventAge *MetricHubSensor) {
	return NewMetricHubSensor(centralName, MetricSystemHealth, m),
		NewMetricHubSensor(centralName, MetricConnectionLatMs, m),
		NewMetricHubSensor(centralName, MetricLastEventAgeSecs, m)
}

// Compile-time assertion: MetricHubSensor satisfies HubDataPointer.
var _ HubDataPointer = (*MetricHubSensor)(nil)

// OnAny subscribes a handler fired on every metric update.
func (m *Metrics) OnAny(fn func(MetricSample)) func() {
	m.mu.Lock()
	m.anySubs = append(m.anySubs, fn)
	idx := len(m.anySubs) - 1
	m.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			m.mu.Lock()
			defer m.mu.Unlock()
			if idx < len(m.anySubs) {
				m.anySubs[idx] = nil
			}
		})
	}
}

// EnabledByDefault reports that the metrics aggregate is always included in
// the default north-bound surface without requiring explicit operator opt-in.
func (*Metrics) EnabledByDefault() bool { return true }
