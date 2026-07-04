// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import gomqtt "github.com/SukramJ/go-mqtt"

// The MQTT transport — wire codec, TCP/TLS adapter, and the reconnecting
// lifecycle — lives in the standalone github.com/SukramJ/go-mqtt module,
// shared with the go-*2mqtt bridges. These aliases re-export its surface
// under this package so the loom-specific side (bridge.go, the command
// subscriber, HA Discovery, entity descriptions, topic building, hub
// discovery, and the daemon wiring) keeps importing a single
// internal/north/mqtt package. Only the transport moved out; everything
// else in this package stays here.
type (
	// QoS mirrors the MQTT QoS enum.
	QoS = gomqtt.QoS
	// Publisher is the outbound contract the bridge publishes through.
	Publisher = gomqtt.Publisher
	// Subscriber is the inbound subscribe/unsubscribe contract.
	Subscriber = gomqtt.Subscriber
	// Client is the combined Publisher+Subscriber role the Bridge uses.
	Client = gomqtt.Client
	// MessageHandler is invoked for every message a subscription
	// receives. Message.Retain carries the PUBLISH retain bit so a
	// side-effecting handler can drop the retained replay the broker
	// re-delivers on every (re)connect — without it a stale
	// `mosquitto_pub -r` command would be re-applied to the CCU on every
	// daemon start.
	MessageHandler = gomqtt.MessageHandler
	// Message is the inbound PUBLISH surface handed to MessageHandler.
	Message = gomqtt.Message
	// SubscribeResult carries the SUBACK outcome (granted QoS, reason).
	SubscribeResult = gomqtt.SubscribeResult
	// Will is the CONNECT last-will configuration.
	Will = gomqtt.Will
	// ProtocolVersion selects the MQTT dialect (zero value = 5.0).
	ProtocolVersion = gomqtt.ProtocolVersion
	// Breaker is the circuit-breaking Publisher decorator.
	Breaker = gomqtt.Breaker
	// BreakerConfig tunes the circuit breaker.
	BreakerConfig = gomqtt.BreakerConfig
	// BreakerState is the circuit state (closed / open / half-open).
	BreakerState = gomqtt.BreakerState
	// PublishOption tunes a single Publish call.
	PublishOption = gomqtt.PublishOption
	// SubscribeOption tunes a single Subscribe call.
	SubscribeOption = gomqtt.SubscribeOption
	// Connector is the connect/disconnect contract the Lifecycle drives.
	Connector = gomqtt.Connector
	// TCPClient is the pure-Go MQTT 3.1.1 client.
	TCPClient = gomqtt.TCPClient
	// TCPConfig wires a TCPClient against a real broker.
	TCPConfig = gomqtt.TCPConfig
	// Lifecycle is the reconnect loop around a Connector.
	Lifecycle = gomqtt.Lifecycle
	// LifecycleConfig tunes the reconnect backoff.
	LifecycleConfig = gomqtt.LifecycleConfig
)

// QoS levels.
const (
	QoS0 = gomqtt.QoS0
	QoS1 = gomqtt.QoS1
	QoS2 = gomqtt.QoS2
)

// Protocol dialects.
const (
	ProtocolV50  = gomqtt.ProtocolV50
	ProtocolV311 = gomqtt.ProtocolV311
)

// Breaker states.
const (
	BreakerClosed   = gomqtt.BreakerClosed
	BreakerOpen     = gomqtt.BreakerOpen
	BreakerHalfOpen = gomqtt.BreakerHalfOpen
)

// ErrCircuitOpen is returned by a tripped [Breaker] instead of
// blocking on the broker's AckTimeout.
var ErrCircuitOpen = gomqtt.ErrCircuitOpen

// NewBreaker wraps a Publisher in a circuit breaker.
func NewBreaker(pub Publisher, cfg BreakerConfig) *Breaker { return gomqtt.NewBreaker(pub, cfg) }

// LegacyHandler adapts the pre-v1 (topic, payload, retained) handler
// shape to a [MessageHandler].
func LegacyHandler(fn func(topic string, payload []byte, retained bool)) MessageHandler {
	return gomqtt.LegacyHandler(fn)
}

// NewTCPClient constructs a broker-backed MQTT client.
func NewTCPClient(cfg TCPConfig) *TCPClient { return gomqtt.NewTCPClient(cfg) }

// NewLifecycle wraps a Connector in a reconnecting lifecycle.
func NewLifecycle(cfg LifecycleConfig, connector Connector) *Lifecycle {
	return gomqtt.NewLifecycle(cfg, connector)
}

// DefaultLifecycle returns the default reconnect-backoff configuration.
func DefaultLifecycle() LifecycleConfig { return gomqtt.DefaultLifecycle() }
