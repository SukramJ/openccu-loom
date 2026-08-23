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
// A single shared MQTT bridge carries the traffic of every configured
// CCU, so MessagesSent, DiscoverySent, PublishErrors and
// ReceivedCommands each carry a `central` Prometheus label — a real
// label, not a name prefix, per the multi-CCU dashboard/alert use case
// (a label lets "sum by ()" and "sum by (central)" both work against
// the same series; a name prefix forces every consumer to enumerate
// central names up front). Call [MqttCollector.MessagesSent] etc. with
// the central the increment belongs to; a call site with no central to
// report (a genuinely daemon-level dispatch) says so in its own
// comment rather than passing an empty label silently.
//
// SubscribeFailures and CircuitBreakerOpened stay plain, unlabeled
// counters: a Subscribe call registers one wildcard filter that answers
// for every configured central at once, and the breaker sits between
// the bridge and the single shared broker connection — neither event
// belongs to one CCU, so a `central` label would either be empty on
// every sample or arbitrarily pick one CCU to blame for a fleet-wide
// condition.
type MqttCollector struct {
	messagesSent     *LabeledCounter
	discoverySent    *LabeledCounter
	publishErrors    *LabeledCounter
	receivedCommands *LabeledCounter

	SubscribeFailures    *Counter
	CircuitBreakerOpened *Counter
}

// NewMqttCollector registers the six MQTT counters in reg and returns an
// initialised collector. Calling this twice is safe — the Registry
// deduplicates by name (and, for the labeled counters, by name + label
// value).
func NewMqttCollector(reg *Registry) *MqttCollector {
	const prefix = "mqtt_"
	return &MqttCollector{
		messagesSent: reg.LabeledCounter(prefix+"messages_sent",
			"Total MQTT state messages published to the broker, by central.", "central"),
		discoverySent: reg.LabeledCounter(prefix+"discovery_sent",
			"Total HA discovery config payloads published, by central.", "central"),
		publishErrors: reg.LabeledCounter(prefix+"publish_errors",
			"Total broker-level publish failures, by central.", "central"),
		receivedCommands: reg.LabeledCounter(prefix+"received_commands",
			"Total inbound MQTT command messages dispatched by CommandSubscriber, by central.", "central"),
		SubscribeFailures: reg.Counter(prefix+"subscribe_failures",
			"Total broker-rejected Subscribe calls (daemon-wide — one Subscribe filter answers for every central)."),
		CircuitBreakerOpened: reg.Counter(prefix+"circuit_breaker_opened",
			"Number of times the MQTT CircuitBreaker transitioned to Open (daemon-wide — one breaker guards the shared broker connection)."),
	}
}

// MessagesSent returns the messages_sent series for central.
func (c *MqttCollector) MessagesSent(central string) *Counter {
	return c.messagesSent.WithLabelValue(central)
}

// DiscoverySent returns the discovery_sent series for central.
func (c *MqttCollector) DiscoverySent(central string) *Counter {
	return c.discoverySent.WithLabelValue(central)
}

// PublishErrors returns the publish_errors series for central.
func (c *MqttCollector) PublishErrors(central string) *Counter {
	return c.publishErrors.WithLabelValue(central)
}

// ReceivedCommands returns the received_commands series for central.
func (c *MqttCollector) ReceivedCommands(central string) *Counter {
	return c.receivedCommands.WithLabelValue(central)
}
