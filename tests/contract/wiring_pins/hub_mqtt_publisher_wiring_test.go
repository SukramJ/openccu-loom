// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package wiring_pins

import (
	"testing"

	"github.com/SukramJ/openccu-loom/tests/contract"
)

// TestPin_BuildHubUpdateDiscovery_CalledInPublisher pins that
// hub_mqtt_publisher.go calls BuildHubUpdateDiscovery when publishing hub
// firmware-update discovery. This is a method call on the discovery
// builder (disco.BuildHubUpdateDiscovery), not a package function, so it
// is pinned by receiver + method name.
func TestPin_BuildHubUpdateDiscovery_CalledInPublisher(t *testing.T) {
	contract.MustFindMethodCall(
		t,
		"internal/central/adapter/hub_mqtt_publisher.go",
		"disco", "BuildHubUpdateDiscovery",
	)
}

// TestPin_BuildInboxDiscovery_CalledInPublisher pins that
// hub_mqtt_publisher.go calls BuildInboxDiscovery when publishing hub
// discovery messages.  Without this call the inbox-count sensor would
// disappear from Home Assistant discovery. This is a method call on the
// discovery builder (disco.BuildInboxDiscovery), not a package function,
// so it is pinned by receiver + method name.
func TestPin_BuildInboxDiscovery_CalledInPublisher(t *testing.T) {
	contract.MustFindMethodCall(
		t,
		"internal/central/adapter/hub_mqtt_publisher.go",
		"disco", "BuildInboxDiscovery",
	)
}

// TestPin_PublishHubUpdate_CalledInPublisher pins that hub_mqtt_publisher.go
// calls PublishHubUpdate to push the CCU firmware-update state to MQTT.
//
// A method call on the Bridge, so it pins the receiver rather than a package:
// `b` is the Bridge throughout this file, and the receiver matcher compares
// whole expression segments, so the pin cannot be satisfied by some other
// variable whose name merely ends in b.
func TestPin_PublishHubUpdate_CalledInPublisher(t *testing.T) {
	contract.MustFindMethodCall(
		t,
		"internal/central/adapter/hub_mqtt_publisher.go",
		"b", "PublishHubUpdate",
	)
}

// TestPin_PublishHubConnectionLatency_CalledInPublisher pins that
// hub_mqtt_publisher.go calls PublishHubConnectionLatency on latency events.
// Removing the call would silently drop per-interface latency metrics from
// the MQTT plane. Method call on the Bridge — see the note above on why the
// short receiver still discriminates.
func TestPin_PublishHubConnectionLatency_CalledInPublisher(t *testing.T) {
	contract.MustFindMethodCall(
		t,
		"internal/central/adapter/hub_mqtt_publisher.go",
		"b", "PublishHubConnectionLatency",
	)
}
