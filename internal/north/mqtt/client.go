// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import "context"

// QoS mirrors the MQTT QoS enum.
type QoS byte

// QoS values.
const (
	QoS0 QoS = 0
	QoS1 QoS = 1
	QoS2 QoS = 2
)

// Publisher is the outbound contract the bridge publishes through.
// Adapters wrap any MQTT client (paho, nhave, etc.).
type Publisher interface {
	Publish(ctx context.Context, topic string, payload []byte, qos QoS, retain bool) error
}

// MessageHandler is invoked for every message a subscription receives.
// The retained flag carries the MQTT PUBLISH "retain" bit so handlers
// that perform side-effects on inbound write commands can drop
// retained replays from the broker — without that filter, an old
// `mosquitto_pub -r` left over from previous tests or automations
// would be re-applied to the CCU on every daemon start.
type MessageHandler func(topic string, payload []byte, retained bool)

// Subscriber is the inbound contract — subscribe to a topic filter
// and route matching messages to a handler. Wiring typically happens
// once at startup and stays active for the broker connection's
// lifetime.
type Subscriber interface {
	Subscribe(ctx context.Context, topicFilter string, qos QoS, handler MessageHandler) error
	Unsubscribe(ctx context.Context, topicFilter string) error
}

// Client is the combined role the Bridge uses. Most real adapters
// satisfy both; the split exists to make testing narrow facades
// easier.
type Client interface {
	Publisher
	Subscriber
}
