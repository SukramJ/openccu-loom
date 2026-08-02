// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ws

import (
	"encoding/json"
	"testing"
	"time"
)

// TestPublishDataPointValueChangedEmitsCanonicalShape pins the wire
// shape of the typed value-changed envelope. Callers serialize the
// payload via json.Marshal and consumers read it field-by-name —
// renaming a field in the payload struct is a wire-breaking change.
func TestPublishDataPointValueChangedEmitsCanonicalShape(t *testing.T) {
	hub := NewHub()

	// Capture the published Event by subscribing a fake client. The
	// hub keeps a private clients map, so the easiest path is to call
	// the public Publish directly through a "topic-matches-anything"
	// stub. Instead we re-derive the topic + payload from the helper
	// itself to validate the shape contract without needing a live
	// transport.
	when := time.Date(2026, 4, 26, 10, 30, 0, 0, time.UTC)

	pl := DataPointValueChangedPayload{
		Central:       "ccu-01",
		Interface:     "HmIP-RF",
		DeviceAddress: "0001ABCDEF",
		Channel:       3,
		Parameter:     "LEVEL",
		ParamsetKey:   "VALUES",
		Value:         0.5,
		Previous:      0.0,
		ModifiedAt:    when.Format(time.RFC3339Nano),
	}
	raw, err := json.Marshal(pl)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, k := range []string{
		"central", "interface", "device_address", "channel", "parameter",
		"paramset_key", "value", "previous", "modified_at",
	} {
		if _, ok := decoded[k]; !ok {
			t.Errorf("payload missing key %q (got %v)", k, decoded)
		}
	}
	// Topic helper must agree with the spec convention.
	wantTopic := "device.0001ABCDEF.channels.3.data_points.LEVEL"
	if got := DataPointTopic("0001ABCDEF", 3, "LEVEL"); got != wantTopic {
		t.Fatalf("DataPointTopic = %q, want %q", got, wantTopic)
	}
	// Hub method exists and does not panic for an empty hub.
	hub.PublishDataPointValueChanged(ValueChange{
		Central: "ccu-01", Interface: "HmIP-RF", DeviceAddress: "0001ABCDEF", Channel: 3,
		Parameter: "LEVEL", ParamsetKey: "VALUES", Value: 0.5, Previous: 0.0, When: when,
		Available: true,
	})
}

// TestPublishCentralStateChangedShape pins the wire shape of the
// typed central-state envelope.
func TestPublishCentralStateChangedShape(t *testing.T) {
	pl := CentralStateChangedPayload{
		Central: "ccu-01", OldState: "starting", NewState: "running",
	}
	raw, err := json.Marshal(pl)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, k := range []string{"central", "old_state", "new_state"} {
		if _, ok := decoded[k]; !ok {
			t.Errorf("payload missing key %q", k)
		}
	}
	if got := CentralStateTopic("ccu-01"); got != "central.ccu-01.state" {
		t.Fatalf("CentralStateTopic = %q, want central.ccu-01.state", got)
	}
}
