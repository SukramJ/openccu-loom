// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"encoding/json"
	"reflect"
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

// TestBuildChannelEventDoorbellHmSenDBPCB verifies that the classic
// wired doorbell PCB HM-Sen-DB-PCB — newly added to the curated
// doorbell-models set — now also classifies as "doorbell" rather than
// the generic "button".
func TestBuildChannelEventDoorbellHmSenDBPCB(t *testing.T) {
	t.Parallel()
	if got := buildChannelEventDeviceClass(t, "HM-Sen-DB-PCB"); got != "doorbell" {
		t.Errorf("device_class=%q want \"doorbell\"", got)
	}
}

// ---------------------------------------------------------------------------
// BuildChannelEvent — announced event_types for doorbell vs. button models
// ---------------------------------------------------------------------------

// buildChannelEventTypes drives BuildChannelEvent for a single-PRESS_SHORT
// press channel on the given model and returns the `event_types` field
// from the unmarshalled discovery payload as a string slice.
func buildChannelEventTypes(t *testing.T, model string) []string {
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

	raw, _ := payload["event_types"].([]any)
	out := make([]string, len(raw))
	for i, v := range raw {
		out[i], _ = v.(string)
	}
	return out
}

// TestBuildChannelEventTypesRingForDoorbellDBB verifies that the
// discovery-announced `event_types` for a HmIP-DBB press channel carries
// the HA-standard "ring" type instead of the raw "press_short" parameter.
func TestBuildChannelEventTypesRingForDoorbellDBB(t *testing.T) {
	t.Parallel()
	got := buildChannelEventTypes(t, "HmIP-DBB")
	want := []string{"ring"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("event_types=%v want %v", got, want)
	}
}

// TestBuildChannelEventTypesPressShortForGenericModel verifies that a
// non-doorbell model keeps announcing the raw "press_short" event type
// (no ring rewrite).
func TestBuildChannelEventTypesPressShortForGenericModel(t *testing.T) {
	t.Parallel()
	got := buildChannelEventTypes(t, "HmIP-WRC2")
	want := []string{"press_short"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("event_types=%v want %v", got, want)
	}
}
