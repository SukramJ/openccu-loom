// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package metrics

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
// "mqtt.<central>.messages_sent") so multi-CCU daemons keep counters
// separated per Unit without a label-vec dependency.
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
	prefix := "mqtt_" + centralName + "_"
	return &MqttCollector{
		MessagesSent:         reg.Counter(prefix+"messages_sent", "Total MQTT state messages published to the broker for central "+centralName+"."),
		DiscoverySent:        reg.Counter(prefix+"discovery_sent", "Total HA discovery config payloads published for central "+centralName+"."),
		PublishErrors:        reg.Counter(prefix+"publish_errors", "Total broker-level publish failures for central "+centralName+"."),
		ReceivedCommands:     reg.Counter(prefix+"received_commands", "Total inbound MQTT command messages dispatched by CommandSubscriber for central "+centralName+"."),
		SubscribeFailures:    reg.Counter(prefix+"subscribe_failures", "Total broker-rejected Subscribe calls for central "+centralName+"."),
		CircuitBreakerOpened: reg.Counter(prefix+"circuit_breaker_opened", "Number of times the MQTT CircuitBreaker transitioned to Open for central "+centralName+"."),
	}
}
