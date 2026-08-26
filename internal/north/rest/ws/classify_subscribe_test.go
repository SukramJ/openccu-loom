// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package ws

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"
)

// TestClassifyOptIn_WithClassify_ReceivesClassificationFields verifies that
// a client subscribing with classify:true receives category and
// data_point_type fields populated on value-changed payloads.
func TestClassifyOptIn_WithClassify_ReceivesClassificationFields(t *testing.T) {
	t.Parallel()
	hub, _, _, _, _, _ := newTestHub(t)
	server := httptest.NewServer(Handler(hub, nil, nil))
	t.Cleanup(server.Close)

	c := dialWS(t, server)

	// Subscribe with classify:true.
	c.send(map[string]any{
		"op":       "subscribe",
		"topics":   []string{"device.*"},
		"classify": true,
	})
	waitForMatch(t, hub, "device.DEV001.channels.4.data_points.LEVEL")

	hub.PublishDataPointValueChanged(ValueChange{
		EnvelopeKind: KindChange, Central: "home", Interface: "HmIP-RF",
		DeviceAddress: "DEV001", Channel: 4, Parameter: "LEVEL", ParamsetKey: "VALUES",
		Value: 0.75, Previous: 0.0, When: time.Now(),
		Category: "sensor", DataPointType: "float", Available: true,
	})

	var ev outboundEvent
	c.recv(&ev)

	raw, err := json.Marshal(ev.Payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if got, ok := decoded["category"]; !ok || got != "sensor" {
		t.Errorf("category = %v (ok=%v), want \"sensor\"", got, ok)
	}
	if got, ok := decoded["data_point_type"]; !ok || got != "float" {
		t.Errorf("data_point_type = %v (ok=%v), want \"float\"", got, ok)
	}
}

// TestClassifyOptOut_WithoutClassify_StripsClassificationFields verifies that
// a client subscribing without classify receives value-changed payloads
// with category and data_point_type absent, even when the publisher
// provided non-empty values.
func TestClassifyOptOut_WithoutClassify_StripsClassificationFields(t *testing.T) {
	t.Parallel()
	hub, _, _, _, _, _ := newTestHub(t)
	server := httptest.NewServer(Handler(hub, nil, nil))
	t.Cleanup(server.Close)

	c := dialWS(t, server)

	// Subscribe without classify — default off.
	c.send(map[string]any{
		"op":     "subscribe",
		"topics": []string{"device.*"},
	})
	waitForMatch(t, hub, "device.DEV001.channels.4.data_points.LEVEL")

	hub.PublishDataPointValueChanged(ValueChange{
		EnvelopeKind: KindChange, Central: "home", Interface: "HmIP-RF",
		DeviceAddress: "DEV001", Channel: 4, Parameter: "LEVEL", ParamsetKey: "VALUES",
		Value: 0.5, Previous: 0.0, When: time.Now(),
		Category: "actuator", DataPointType: "float", Available: true,
	})

	var ev outboundEvent
	c.recv(&ev)

	raw, err := json.Marshal(ev.Payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if v, ok := decoded["category"]; ok && v != "" {
		t.Errorf("category must be absent/empty, got %v", v)
	}
	if v, ok := decoded["data_point_type"]; ok && v != "" {
		t.Errorf("data_point_type must be absent/empty, got %v", v)
	}
}

// TestClassifyNoCrossTalk_TwoClients verifies that one classified client and
// one unclassified client subscribed to the same event receive the correct
// payload variant each. The strip in writePump must copy the payload struct
// and never mutate the buffered event that the other client reads.
func TestClassifyNoCrossTalk_TwoClients(t *testing.T) {
	t.Parallel()
	hub, _, _, _, _, _ := newTestHub(t)
	server := httptest.NewServer(Handler(hub, nil, nil))
	t.Cleanup(server.Close)

	classified := dialWS(t, server)
	unclassified := dialWS(t, server)

	classified.send(map[string]any{
		"op":       "subscribe",
		"topics":   []string{"device.*"},
		"classify": true,
	})
	unclassified.send(map[string]any{
		"op":     "subscribe",
		"topics": []string{"device.*"},
	})

	// Wait for both clients to register their subscriptions.
	waitForMatch(t, hub, "device.DEV002.channels.1.data_points.STATE")
	// After waitForMatch fires (at least 1 subscriber), give the second
	// subscribe frame time to be processed so MatchCount reaches 2.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if hub.MatchCount("device.DEV002.channels.1.data_points.STATE") >= 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	hub.PublishDataPointValueChanged(ValueChange{
		EnvelopeKind: KindChange, Central: "home", Interface: "HmIP-RF",
		DeviceAddress: "DEV002", Channel: 1, Parameter: "STATE", ParamsetKey: "VALUES",
		Value: true, Previous: false, When: time.Now(),
		Category: "switch", DataPointType: "bool", Available: true,
	})

	// Both clients receive the event; order between the two is not specified,
	// so we read from each independently.
	decodePayload := func(c *wsConn) map[string]any {
		t.Helper()
		var ev outboundEvent
		c.recv(&ev)
		raw, err := json.Marshal(ev.Payload)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		var m map[string]any
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		return m
	}

	classifiedPL := decodePayload(classified)
	unclassifiedPL := decodePayload(unclassified)

	// Classified client must see the classification fields.
	if got := classifiedPL["category"]; got != "switch" {
		t.Errorf("classified: category = %v, want switch", got)
	}
	if got := classifiedPL["data_point_type"]; got != "bool" {
		t.Errorf("classified: data_point_type = %v, want bool", got)
	}

	// Unclassified client must NOT see them.
	if v, ok := unclassifiedPL["category"]; ok && v != "" {
		t.Errorf("unclassified: category must be absent/empty, got %v", v)
	}
	if v, ok := unclassifiedPL["data_point_type"]; ok && v != "" {
		t.Errorf("unclassified: data_point_type must be absent/empty, got %v", v)
	}
}
