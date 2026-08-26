// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"context"

	"github.com/SukramJ/openccu-loom/internal/central/adapter"
)

// mqttService adapts the MQTT event-fan-out surface (the EventBridge and,
// when a broker is configured, the HubMQTTPublisher) onto the north-bound
// bridge.Service contract so the registry owns its ordered teardown.
//
// Like the Matter surface, MQTT is teardown-managed with a no-op Start: the
// EventBridge and HubMQTTPublisher are started early in the composition root
// — BEFORE southbound hydration — because the boot-time initial snapshot of
// retained CCU state must publish onto a live bridge (the PhaseEarly reason,
// ADR 0047 §2). Their start also carries the mqttSup.OnConnect reconnect
// re-publish hooks. The broker supervisor (mqttSup) is genuinely shared
// infrastructure with its own lifecycle and the config-watcher hot-reload
// (Swap) path, so it stays in wireSharedInfrastructure; this Service owns the
// teardown of the two fan-out components only. Start is therefore a no-op;
// see ADR 0047.
type mqttService struct {
	bridge  *adapter.EventBridge
	hubMQTT *adapter.HubMQTTPublisher // nil when no broker is configured
}

// newMQTTService wraps the EventBridge (always present) and the optional
// HubMQTTPublisher.
func newMQTTService(bridge *adapter.EventBridge, hubMQTT *adapter.HubMQTTPublisher) *mqttService {
	return &mqttService{bridge: bridge, hubMQTT: hubMQTT}
}

// Name implements bridge.Service.
func (s *mqttService) Name() string { return "mqtt" }

// Start is a no-op: the components are started early (pre-hydration) in the
// composition root (see the type doc).
func (s *mqttService) Start(context.Context) error { return nil }

// Stop tears the fan-out components down in the same order the previous LIFO
// defers used — HubMQTTPublisher first, then the EventBridge — so a slow hub
// publish never blocks the bridge stop. Idempotent via the registry.
func (s *mqttService) Stop(context.Context) error {
	if s.hubMQTT != nil {
		s.hubMQTT.Stop()
	}
	if s.bridge != nil {
		s.bridge.Stop()
	}
	return nil
}
