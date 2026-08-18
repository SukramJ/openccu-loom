// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/eligibility"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/internal/north/rest/ws"
)

// matterEventPublisher implements
// [handlers.MatterEventPublisher] by forwarding events to the
// daemon's WebSocket hub. Subscribers on the matching topic
// receive the event payload as a JSON object on the wire.
type matterEventPublisher struct {
	hub *ws.Hub
	// fabrics resolves the identity of a freshly commissioned fabric for the
	// `matter.fabric_added` payload. Nil in minimal wirings, which degrades
	// the payload to the fabric index alone.
	fabrics handlers.MatterFabricStore
}

// fabricLookupTimeout bounds the store read publishFabricAdded does. It runs
// inside the commissioning callback, so a slow or wedged store must degrade
// the payload rather than hold up the AddNOC path.
const fabricLookupTimeout = 3 * time.Second

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
		Topic: handlers.MatterTopicEndpointAssembled,
		// The wire `type` carries the full dotted name, identical to the
		// topic — both WS consumers key on `matter.<event>`.
		Type:    handlers.MatterTopicEndpointAssembled,
		When:    time.Now().UTC(),
		Payload: map[string]any{"endpoint_count": count},
	})
}

// publishFabricAdded emits `matter.fabric_added` for the freshly commissioned
// fabric. Wired through a wrapping StoreFacade in the daemon so the
// cluster-side AddNOC + REST-side flows both surface the event.
//
// The declared payload is the full MatterFabric identity (assets/wsapi.json →
// the MatterFabric schema in assets/openapi.yaml), so the index alone leaves
// every generated client decoding a frame that is missing five required
// members. The record is already persisted when the hook fires — AddNOC writes
// it before invoking OnFabricInstalled — so it is read back here; a lookup
// miss degrades to the index rather than dropping the event.
func (p *matterEventPublisher) publishFabricAdded(fabricIndex uint8) {
	p.PublishMatterEvent(context.Background(), handlers.MatterEvent{
		Topic:   handlers.MatterTopicFabricAdded,
		Type:    handlers.MatterTopicFabricAdded,
		When:    time.Now().UTC(),
		Payload: p.fabricPayload(fabricIndex),
	})
}

// zeroFabricPayload is the degraded fabricPayload fallback: a
// handlers.MatterFabricResponse carrying only fabricIndex, with every other
// field at its schema-valid zero form. assets/openapi.yaml's MatterFabric
// schema (which assets/wsapi.json's matter.fabric_added payload schema
// references) declares fabric_id, fabric_id_hex, node_id, node_id_hex,
// vendor_id, compressed_id and root_public_key as required — a generated
// strict client rejects a frame that omits any of them, so a lookup miss or
// a slow/erroring store read must still ship every required key rather than
// the bare index alone. The _hex fields are zero-padded to their real
// 16-digit width rather than left as Go's empty-string zero value so they
// still parse as the hex shape the schema documents.
func zeroFabricPayload(fabricIndex uint8) handlers.MatterFabricResponse {
	return handlers.MatterFabricResponse{
		FabricIndex: fabricIndex,
		FabricIDHex: fmt.Sprintf("%016X", uint64(0)),
		NodeIDHex:   fmt.Sprintf("%016X", uint64(0)),
	}
}

// fabricPayload builds the MatterFabric body for fabricIndex, falling back to
// [zeroFabricPayload] when no store is wired or the fabric cannot be
// resolved — never to a partial object, which every required field in the
// declared schema would then be missing from.
func (p *matterEventPublisher) fabricPayload(fabricIndex uint8) any {
	if p == nil || p.fabrics == nil {
		return zeroFabricPayload(fabricIndex)
	}
	ctx, cancel := context.WithTimeout(context.Background(), fabricLookupTimeout)
	defer cancel()
	recs, err := p.fabrics.ListFabrics(ctx)
	if err != nil {
		return zeroFabricPayload(fabricIndex)
	}
	for i := range recs {
		r := &recs[i]
		if r.FabricIndex != fabricIndex {
			continue
		}
		// Same projection GET /api/v1/matter/fabrics serves, so a WS
		// consumer and a REST consumer see one shape for one fabric.
		return handlers.MatterFabricResponse{
			FabricIndex:   r.FabricIndex,
			FabricID:      r.FabricID,
			FabricIDHex:   fmt.Sprintf("%016X", r.FabricID),
			NodeID:        r.NodeID,
			NodeIDHex:     fmt.Sprintf("%016X", r.NodeID),
			VendorID:      r.VendorID,
			VendorName:    eligibility.VendorName(r.VendorID),
			Label:         r.Label,
			CompressedID:  hex.EncodeToString(r.CompressedID[:]),
			RootPublicKey: hex.EncodeToString(r.RootPublicKey),
		}
	}
	return zeroFabricPayload(fabricIndex)
}

// publishFabricRemoved emits `matter.fabric_removed` with the
// revoked fabric_index. Wired via [Bridge.SetOnFabricRemoved] so
// the WS layer receives the event whenever RemoveFabric completes.
func (p *matterEventPublisher) publishFabricRemoved(fabricIndex uint8) {
	p.PublishMatterEvent(context.Background(), handlers.MatterEvent{
		Topic:   handlers.MatterTopicFabricRemoved,
		Type:    handlers.MatterTopicFabricRemoved,
		When:    time.Now().UTC(),
		Payload: map[string]any{"fabric_index": fabricIndex},
	})
}
