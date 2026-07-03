// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package metrics

import (
	"fmt"
	"hash/fnv"
	"strings"
)

// MqttCollector holds per-bridge Prometheus counters.
//
// Counters:
//   - MessagesSent          — raw or aggregated state publishes that reached the broker.
//   - DiscoverySent         — HA discovery config payloads that reached the broker.
//   - PublishErrors         — broker-level publish failures (non-nil error from Publisher).
//   - ReceivedCommands      — inbound /set and /invoke messages dispatched by CommandSubscriber.
//   - SubscribeFailures     — broker-rejected Subscribe calls (non-nil error from Subscriber.Subscribe).
//   - CircuitBreakerOpened  — number of times the MQTT CircuitBreaker transitioned to Open.
//
// The central_name label is embedded in the metric name (pattern:
// "mqtt_<central>_messages_sent") so multi-CCU daemons keep counters
// separated per Unit without a label-vec dependency. The central segment
// is sanitized to the Prometheus name charset by [metricSegment] so an
// unusual CCU name never produces an invalid exposition line that would
// make Prometheus drop the whole scrape.
type MqttCollector struct {
	MessagesSent         *Counter
	DiscoverySent        *Counter
	PublishErrors        *Counter
	ReceivedCommands     *Counter
	SubscribeFailures    *Counter
	CircuitBreakerOpened *Counter
}

// NewMqttCollector registers the six counters in reg and returns an
// initialised collector. The centralName is embedded in the metric
// names so callers can fan-in all centrals into a single Registry.
// Calling this twice with the same centralName is safe — the Registry
// deduplicates.
func NewMqttCollector(reg *Registry, centralName string) *MqttCollector {
	prefix := "mqtt_" + metricSegment(centralName) + "_"
	return &MqttCollector{
		MessagesSent:         reg.Counter(prefix+"messages_sent", "Total MQTT state messages published to the broker for central "+centralName+"."),
		DiscoverySent:        reg.Counter(prefix+"discovery_sent", "Total HA discovery config payloads published for central "+centralName+"."),
		PublishErrors:        reg.Counter(prefix+"publish_errors", "Total broker-level publish failures for central "+centralName+"."),
		ReceivedCommands:     reg.Counter(prefix+"received_commands", "Total inbound MQTT command messages dispatched by CommandSubscriber for central "+centralName+"."),
		SubscribeFailures:    reg.Counter(prefix+"subscribe_failures", "Total broker-rejected Subscribe calls for central "+centralName+"."),
		CircuitBreakerOpened: reg.Counter(prefix+"circuit_breaker_opened", "Number of times the MQTT CircuitBreaker transitioned to Open for central "+centralName+"."),
	}
}

// metricSegment sanitizes name into a token safe for a Prometheus
// metric-name segment: every rune outside [A-Za-z0-9_] is replaced with
// '_'. To keep two distinct names from silently merging after
// sanitization (e.g. "a b" and "a-b" both normalise to "a_b"), a short
// deterministic hash of the ORIGINAL name is appended whenever the
// sanitization actually changed the string. Already-safe names pass
// through unchanged, so the common case keeps clean, stable metric names.
func metricSegment(name string) string {
	var b strings.Builder
	b.Grow(len(name))
	changed := false
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
			changed = true
		}
	}
	safe := b.String()
	if !changed {
		return safe
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(name))
	return fmt.Sprintf("%s_%08x", safe, h.Sum32())
}
