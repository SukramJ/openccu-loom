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
	// receives. The retained flag carries the PUBLISH retain bit so a
	// side-effecting handler can drop the retained replay the broker
	// re-delivers on every (re)connect — without it a stale
	// `mosquitto_pub -r` command would be re-applied to the CCU on every
	// daemon start.
	MessageHandler = gomqtt.MessageHandler
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

// NewTCPClient constructs a broker-backed MQTT client.
func NewTCPClient(cfg TCPConfig) *TCPClient { return gomqtt.NewTCPClient(cfg) }

// NewLifecycle wraps a Connector in a reconnecting lifecycle.
func NewLifecycle(cfg LifecycleConfig, connector Connector) *Lifecycle {
	return gomqtt.NewLifecycle(cfg, connector)
}

// DefaultLifecycle returns the default reconnect-backoff configuration.
func DefaultLifecycle() LifecycleConfig { return gomqtt.DefaultLifecycle() }
