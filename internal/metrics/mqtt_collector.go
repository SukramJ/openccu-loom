// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package metrics

// MqttCollector holds the daemon-wide MQTT Prometheus counters.
//
// Counters:
//   - MessagesSent          — raw or aggregated state publishes that reached the broker.
//   - DiscoverySent         — HA discovery config payloads that reached the broker.
//   - PublishErrors         — broker-level publish failures (non-nil error from Publisher).
//   - ReceivedCommands      — inbound /set and /invoke messages dispatched by CommandSubscriber.
//   - SubscribeFailures     — broker-rejected Subscribe calls (non-nil error from Subscriber.Subscribe).
//   - CircuitBreakerOpened  — number of times the MQTT CircuitBreaker transitioned to Open.
//
// The counters are a SINGLE daemon-wide series (pattern
// "mqtt_messages_sent"), NOT per-central. A single shared MQTT bridge
// carries the traffic of every configured CCU and increments the same
// collector for all of them, so a per-central name would have keyed the
// series to one arbitrary CCU while silently folding the rest of the
// fleet's traffic into it. The traffic is genuinely daemon-wide, so the
// metric name is too.
type MqttCollector struct {
	MessagesSent         *Counter
	DiscoverySent        *Counter
	PublishErrors        *Counter
	ReceivedCommands     *Counter
	SubscribeFailures    *Counter
	CircuitBreakerOpened *Counter
}

// NewMqttCollector registers the six daemon-wide counters in reg and
// returns an initialised collector. Calling this twice is safe — the
// Registry deduplicates by name.
func NewMqttCollector(reg *Registry) *MqttCollector {
	const prefix = "mqtt_"
	return &MqttCollector{
		MessagesSent:         reg.Counter(prefix+"messages_sent", "Total MQTT state messages published to the broker across all centrals."),
		DiscoverySent:        reg.Counter(prefix+"discovery_sent", "Total HA discovery config payloads published across all centrals."),
		PublishErrors:        reg.Counter(prefix+"publish_errors", "Total broker-level publish failures across all centrals."),
		ReceivedCommands:     reg.Counter(prefix+"received_commands", "Total inbound MQTT command messages dispatched by CommandSubscriber across all centrals."),
		SubscribeFailures:    reg.Counter(prefix+"subscribe_failures", "Total broker-rejected Subscribe calls across all centrals."),
		CircuitBreakerOpened: reg.Counter(prefix+"circuit_breaker_opened", "Number of times the MQTT CircuitBreaker transitioned to Open."),
	}
}
