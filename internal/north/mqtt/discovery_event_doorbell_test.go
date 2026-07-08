// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"encoding/json"
	"testing"
)

// ---------------------------------------------------------------------------
// BuildChannelEvent — doorbell device_class for HmIP-DBB / HmIP-DSD-PCB
// ---------------------------------------------------------------------------

// buildChannelEventDeviceClass drives BuildChannelEvent for a press channel
// on the given model and returns the `device_class` field from the
// unmarshalled discovery payload. Shared by the doorbell/button cases below.
func buildChannelEventDeviceClass(t *testing.T, model string) string {
	t.Helper()

	db := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu")
	ch := singlePressChannel()
	ev := Event{
		Interface:     "HmIP-RF",
		DeviceAddress: "0034WRC2",
		DeviceName:    "Haustür",
		ChannelNo:     1,
		Parameter:     "PRESS_SHORT",
		Channel:       ch,
		Model:         model,
	}

	comp, _, _, buf, ok := db.BuildChannelEvent(ev)
	if !ok {
		t.Fatalf("BuildChannelEvent(model=%q) returned ok=false", model)
	}
	if comp != string(HAComponentEvent) {
		t.Fatalf("BuildChannelEvent(model=%q): component=%q want %q", model, comp, HAComponentEvent)
	}

	var payload map[string]any
	if err := json.Unmarshal(buf, &payload); err != nil {
		t.Fatalf("BuildChannelEvent(model=%q): invalid JSON: %v", model, err)
	}

	dc, _ := payload["device_class"].(string)
	return dc
}

// TestBuildChannelEventDoorbellDSDPCB verifies that the aggregated
// channel-level event entity for a HmIP-DSD-PCB (doorbell sensor PCB)
// press channel carries device_class "doorbell" instead of the generic
// "button".
func TestBuildChannelEventDoorbellDSDPCB(t *testing.T) {
	t.Parallel()
	if got := buildChannelEventDeviceClass(t, "HmIP-DSD-PCB"); got != "doorbell" {
		t.Errorf("device_class=%q want \"doorbell\"", got)
	}
}

// TestBuildChannelEventDoorbellDBB verifies that the aggregated
// channel-level event entity for a HmIP-DBB (wireless doorbell button)
// press channel carries device_class "doorbell" instead of the generic
// "button".
func TestBuildChannelEventDoorbellDBB(t *testing.T) {
	t.Parallel()
	if got := buildChannelEventDeviceClass(t, "HmIP-DBB"); got != "doorbell" {
		t.Errorf("device_class=%q want \"doorbell\"", got)
	}
}

// TestBuildChannelEventButtonGenericModel verifies that a non-doorbell
// model (a plain wall-mounted remote) keeps the default "button"
// device_class on its aggregated channel-level event entity.
func TestBuildChannelEventButtonGenericModel(t *testing.T) {
	t.Parallel()
	if got := buildChannelEventDeviceClass(t, "HmIP-WRC2"); got != "button" {
		t.Errorf("device_class=%q want \"button\"", got)
	}
}
