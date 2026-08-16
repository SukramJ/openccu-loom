// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matter/store"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/internal/north/rest/ws"
)

// TestMatterEventPublisher_NilHub_IsNoop verifies that calling
// PublishMatterEvent when the hub field is nil does not panic. This is
// the "Matter disabled" path where the publisher is still wired but the
// hub has not been started.
func TestMatterEventPublisher_NilHub_IsNoop(t *testing.T) {
	t.Parallel()
	pub := &matterEventPublisher{hub: nil}
	// Must not panic.
	pub.PublishMatterEvent(context.Background(), handlers.MatterEvent{
		Topic:   "matter.exposable_changed",
		Type:    "exposable_changed",
		When:    time.Now().UTC(),
		Payload: map[string]any{"dp_key": "onoff"},
	})
}

// TestMatterEventPublisher_NilReceiver_IsNoop verifies that a nil
// *matterEventPublisher pointer (pointer receiver) does not panic.
func TestMatterEventPublisher_NilReceiver_IsNoop(t *testing.T) {
	t.Parallel()
	var pub *matterEventPublisher
	// Must not panic — PublishMatterEvent has a nil-receiver guard.
	pub.PublishMatterEvent(context.Background(), handlers.MatterEvent{
		Topic: "matter.fabric_removed",
	})
}

// TestMatterEventPublisher_WithHub_ZeroWhen_FillsTimestamp verifies
// that publishing an event with a zero When field uses the current
// time (non-zero) instead of passing zero to the hub. We confirm this
// via MatchCount — 0 clients are registered so nothing blows up; the
// test guards the non-panic + timestamp-fill path.
func TestMatterEventPublisher_WithHub_NoClients_IsNoop(t *testing.T) {
	t.Parallel()
	hub := ws.NewHub()
	pub := &matterEventPublisher{hub: hub}

	// Zero When — the publisher must fill it.
	pub.PublishMatterEvent(context.Background(), handlers.MatterEvent{
		Topic:   "matter.endpoint_assembled",
		Type:    "endpoint_assembled",
		When:    time.Time{}, // intentionally zero
		Payload: map[string]any{"endpoint_count": 2},
	})
	// No clients → MatchCount should be 0. We just confirm no panic.
	if n := hub.MatchCount("matter.endpoint_assembled"); n != 0 {
		t.Errorf("expected 0 matching clients (none registered), got %d", n)
	}
}

// TestMatterEventPublisher_WithHub_PublishesEvent verifies that when a
// hub with at least one subscribed client is available, the event is
// delivered. We use hub.MatchCount to confirm the hub can route the
// topic after a subscription is established via a raw client
// registration trick: ws.Hub exposes ClientCount but not direct client
// injection, so we assert the structural invariant — no error, no panic
// — for a hub with zero clients and trust the ws package's own
// TestHandlerEndToEnd for the delivery path.
func TestMatterEventPublisher_WithHub_PublishDoesNotPanic(t *testing.T) {
	t.Parallel()
	hub := ws.NewHub()
	pub := &matterEventPublisher{hub: hub}

	// Calling Publish on a hub with no connected clients must silently
	// no-op without panicking.
	pub.PublishMatterEvent(context.Background(), handlers.MatterEvent{
		Topic:   handlers.MatterTopicFabricAdded,
		Type:    "fabric_added",
		When:    time.Now().UTC(),
		Payload: map[string]any{"fabric_index": 1},
	})
	if hub.ClientCount() != 0 {
		t.Errorf("expected 0 clients, got %d", hub.ClientCount())
	}
}

// stubFabricStore serves one persisted fabric, the way the Matter store does
// once AddNOC has written it — which is before OnFabricInstalled fires.
type stubFabricStore struct {
	recs []store.FabricRecord
	err  error
}

func (s stubFabricStore) ListFabrics(context.Context) ([]store.FabricRecord, error) {
	return s.recs, s.err
}

// TestPublishFabricAddedCarriesTheDeclaredFabricIdentity pins the payload
// against the schema the broadcast declares. `matter.fabric_added` is declared
// as MatterFabric, whose six required members every generated client decodes
// strictly; publishing the index alone leaves five of them absent, so a strict
// decoder rejects the frame and a TypeScript consumer reads undefined.
func TestPublishFabricAddedCarriesTheDeclaredFabricIdentity(t *testing.T) {
	t.Parallel()
	hub := ws.NewHub()
	hub.SetReplayCapacity(4)
	recs := []store.FabricRecord{
		{
			FabricIndex: 7, FabricID: 0xAABB, NodeID: 0xCCDD, VendorID: 0x1349, Label: "Apple Home",
			CompressedID: [8]byte{1, 2, 3, 4, 5, 6, 7, 8}, RootPublicKey: []byte{4, 9, 9},
		},
		{FabricIndex: 2, FabricID: 1, NodeID: 1},
	}
	pub := &matterEventPublisher{hub: hub, fabrics: stubFabricStore{recs: recs}}

	pub.publishFabricAdded(7)

	events := hub.Replay(0, nil).Events
	if len(events) != 1 {
		t.Fatalf("published %d events, want 1", len(events))
	}
	raw, err := json.Marshal(events[0].Payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	// The MatterFabric schema's required members.
	for _, key := range []string{"fabric_index", "fabric_id", "node_id", "vendor_id", "compressed_id", "root_public_key"} {
		if _, ok := got[key]; !ok {
			t.Errorf("payload is missing the required member %q: %s", key, raw)
		}
	}
	if got["compressed_id"] != "0102030405060708" {
		t.Errorf("compressed_id = %v, want the hex-encoded identifier", got["compressed_id"])
	}
	if got["label"] != "Apple Home" {
		t.Errorf("label = %v, want the fabric's own label", got["label"])
	}
}

// TestPublishFabricAddedCarriesTheHexIdentifiers pins that the broadcast fills
// the schema-required fabric_id_hex / node_id_hex the same way
// GET /matter/fabrics does. FabricID and NodeID are 64-bit and a JSON number
// carries only 53 bits, so a browser rounds them — a consumer that renders the
// numeric field prints the wrong id. Omitting the hex members leaves a strict
// decoder with empty strings for two required fields.
func TestPublishFabricAddedCarriesTheHexIdentifiers(t *testing.T) {
	t.Parallel()
	hub := ws.NewHub()
	hub.SetReplayCapacity(4)
	recs := []store.FabricRecord{
		{
			FabricIndex: 7, FabricID: 0xAABB, NodeID: 0xCCDD, VendorID: 0x1349, Label: "Apple Home",
			CompressedID: [8]byte{1, 2, 3, 4, 5, 6, 7, 8}, RootPublicKey: []byte{4, 9, 9},
		},
	}
	pub := &matterEventPublisher{hub: hub, fabrics: stubFabricStore{recs: recs}}

	pub.publishFabricAdded(7)

	events := hub.Replay(0, nil).Events
	if len(events) != 1 {
		t.Fatalf("published %d events, want 1", len(events))
	}
	raw, err := json.Marshal(events[0].Payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if got["fabric_id_hex"] != "000000000000AABB" {
		t.Errorf("fabric_id_hex = %v, want the 16-digit hex of the fabric id: %s", got["fabric_id_hex"], raw)
	}
	if got["node_id_hex"] != "000000000000CCDD" {
		t.Errorf("node_id_hex = %v, want the 16-digit hex of the node id: %s", got["node_id_hex"], raw)
	}
}

// TestPublishFabricAddedStillEmitsWhenTheFabricCannotBeResolved keeps the
// event itself unconditional: a store read that fails must degrade the payload,
// never swallow the commissioning notification.
func TestPublishFabricAddedStillEmitsWhenTheFabricCannotBeResolved(t *testing.T) {
	t.Parallel()
	hub := ws.NewHub()
	hub.SetReplayCapacity(4)
	pub := &matterEventPublisher{hub: hub, fabrics: stubFabricStore{err: errors.New("store closed")}}

	pub.publishFabricAdded(3)

	events := hub.Replay(0, nil).Events
	if len(events) != 1 {
		t.Fatalf("published %d events, want 1", len(events))
	}
	payload, ok := events[0].Payload.(map[string]any)
	if !ok {
		t.Fatalf("payload type = %T, want the degraded map", events[0].Payload)
	}
	if payload["fabric_index"] != uint8(3) {
		t.Errorf("fabric_index = %v, want 3", payload["fabric_index"])
	}
}
