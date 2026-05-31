// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package wiring_pins

import (
	"testing"

	"github.com/SukramJ/openccu-loom/tests/contract"
)

// TestPin_BuildHubUpdateDiscovery_CalledInPublisher pins that
// hub_mqtt_publisher.go calls BuildHubUpdateDiscovery when publishing hub
// firmware-update discovery.
func TestPin_BuildHubUpdateDiscovery_CalledInPublisher(t *testing.T) {
	contract.MustFindCallerInFile(
		t,
		"internal/central/adapter/hub_mqtt_publisher.go",
		"internal/north/mqtt", "BuildHubUpdateDiscovery",
	)
}

// TestPin_PublishHubUpdate_CalledInPublisher pins that hub_mqtt_publisher.go
// calls PublishHubUpdate to push the CCU firmware-update state to MQTT.
func TestPin_PublishHubUpdate_CalledInPublisher(t *testing.T) {
	contract.MustFindCallerInFile(
		t,
		"internal/central/adapter/hub_mqtt_publisher.go",
		"internal/north/mqtt", "PublishHubUpdate",
	)
}

// TestPin_BuildInboxDiscovery_CalledInPublisher pins that
// hub_mqtt_publisher.go calls BuildInboxDiscovery when publishing hub
// discovery messages.  Without this call the inbox-count sensor would
// disappear from Home Assistant discovery.
func TestPin_BuildInboxDiscovery_CalledInPublisher(t *testing.T) {
	contract.MustFindCallerInFile(
		t,
		"internal/central/adapter/hub_mqtt_publisher.go",
		"internal/north/mqtt", "BuildInboxDiscovery",
	)
}

// TestPin_PublishHubConnectionLatency_CalledInPublisher pins that
// hub_mqtt_publisher.go calls PublishHubConnectionLatency on latency events.
// Removing the call would silently drop per-interface latency metrics from
// the MQTT plane.
func TestPin_PublishHubConnectionLatency_CalledInPublisher(t *testing.T) {
	contract.MustFindCallerInFile(
		t,
		"internal/central/adapter/hub_mqtt_publisher.go",
		"internal/north/mqtt", "PublishHubConnectionLatency",
	)
}
