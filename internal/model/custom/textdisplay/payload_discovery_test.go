// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package textdisplay

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestTextDisplayWriteAcceptsTemplatedPayload feeds the object the notify
// entity's command template renders through the invoke path the MQTT bridge
// uses and asserts it reaches the wire.
//
// The template itself lives on the bridge's notify plane
// (mqtt.textDisplayCommandTemplate) because the notify entity is the only
// Home Assistant surface a text display has. What this pins is the other
// half of that contract: the shape that template produces is the shape
// `write` accepts.
func TestTextDisplayWriteAcceptsTemplatedPayload(t *testing.T) {
	t.Parallel()
	w := &stubWriter{}
	td := New("VCU3756007:3", w)

	// What HA publishes after rendering the command template.
	var params map[string]any
	if err := json.Unmarshal([]byte(`{"id": 1, "text": "Hello"}`), &params); err != nil {
		t.Fatal(err)
	}
	if err := td.Invoke(context.Background(), "write", params, hmenum.CommandPriorityHigh); err != nil {
		t.Fatalf("write: %v", err)
	}
	if len(w.calls) == 0 {
		t.Fatal("write produced no wire call")
	}
}
