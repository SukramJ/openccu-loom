// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/internal/north/rest/ws"
)

// matterEventPublisher implements
// [handlers.MatterEventPublisher] by forwarding events to the
// daemon's WebSocket hub. Subscribers on the matching topic
// receive the event payload as a JSON object on the wire.
type matterEventPublisher struct {
	hub *ws.Hub
}

// PublishMatterEvent implements [handlers.MatterEventPublisher].
func (p *matterEventPublisher) PublishMatterEvent(_ context.Context, ev handlers.MatterEvent) {
	if p == nil || p.hub == nil {
		return
	}
	when := ev.When
	if when.IsZero() {
		when = time.Now().UTC()
	}
	p.hub.Publish(ws.Event{
		Topic:   ev.Topic,
		Type:    ev.Type,
		When:    when,
		Payload: ev.Payload,
	})
}

// publishEndpointAssembled emits `matter.endpoint_assembled` with the
// current bridged-endpoint count. Wired by the daemon as the
// `Bridge.SetOnReassembled` hook.
func (p *matterEventPublisher) publishEndpointAssembled(count int) {
	p.PublishMatterEvent(context.Background(), handlers.MatterEvent{
		Topic:   handlers.MatterTopicEndpointAssembled,
		Type:    "endpoint_assembled",
		When:    time.Now().UTC(),
		Payload: map[string]any{"endpoint_count": count},
	})
}

// publishFabricAdded emits `matter.fabric_added` with the freshly
// assigned fabric_index. Wired through a wrapping StoreFacade in
// the daemon so the cluster-side AddNOC + REST-side flows both
// surface the event.
func (p *matterEventPublisher) publishFabricAdded(fabricIndex uint8) {
	p.PublishMatterEvent(context.Background(), handlers.MatterEvent{
		Topic:   handlers.MatterTopicFabricAdded,
		Type:    "fabric_added",
		When:    time.Now().UTC(),
		Payload: map[string]any{"fabric_index": fabricIndex},
	})
}

// publishFabricRemoved emits `matter.fabric_removed` with the
// revoked fabric_index. Wired via [Bridge.SetOnFabricRemoved] so
// the WS layer receives the event whenever RemoveFabric completes.
func (p *matterEventPublisher) publishFabricRemoved(fabricIndex uint8) {
	p.PublishMatterEvent(context.Background(), handlers.MatterEvent{
		Topic:   handlers.MatterTopicFabricRemoved,
		Type:    "fabric_removed",
		When:    time.Now().UTC(),
		Payload: map[string]any{"fabric_index": fabricIndex},
	})
}
