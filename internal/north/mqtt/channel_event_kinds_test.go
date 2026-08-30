// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"encoding/json"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/event"
)

// listingChannel is a fakeChannelInspector that also reports its parameter
// names, so [ChannelKindTypes] takes the list branch rather than the
// known-roots fallback. A real channel does the same via
// device.Channel.ParameterNames.
type listingChannel struct {
	names []string
}

func (l *listingChannel) HasParameter(name string) bool {
	for _, n := range l.names {
		if n == name {
			return true
		}
	}
	return false
}

func (l *listingChannel) ParameterNames() []string { return l.names }

func decodeBody(t *testing.T, buf []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(buf, &m); err != nil {
		t.Fatalf("unmarshal discovery payload: %v", err)
	}
	return m
}

func stringsOf(t *testing.T, v any) []string {
	t.Helper()
	raw, ok := v.([]any)
	if !ok {
		t.Fatalf("event_types is %T, want a list", v)
	}
	out := make([]string, 0, len(raw))
	for _, e := range raw {
		s, sok := e.(string)
		if !sok {
			t.Fatalf("event_types entry is %T, want string", e)
		}
		out = append(out, s)
	}
	return out
}

// TestEachEventKindGetsItsOwnEntity pins the gap this closed: impulse and
// device-error events reached the REST and WebSocket planes but were never
// published on MQTT at all, because the publish path returned early for any
// parameter that was not a keypress.
//
// Each kind must land on its own entity, its own topic and its own
// event_types list. Sharing a topic would make every entity fire on every
// other kind's pulse, and an event_type missing from the announced list is
// dropped by Home Assistant without a trace.
func TestEachEventKindGetsItsOwnEntity(t *testing.T) {
	t.Parallel()

	db := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu")
	// One channel carrying all three kinds, including a device-error
	// parameter that only prefix matching finds.
	ch := &listingChannel{names: []string{"PRESS_SHORT", "SEQUENCE_OK", "ERROR_OVERHEAT", "SENSOR_ERROR"}}

	seen := map[string]map[string]any{}
	for _, param := range []string{"PRESS_SHORT", "SEQUENCE_OK", "ERROR_OVERHEAT"} {
		ev := Event{
			Interface:     "HmIP-RF",
			DeviceAddress: "0034WRC2",
			DeviceName:    "Flur Taster",
			ChannelNo:     1,
			Parameter:     param,
			Channel:       ch,
		}
		comp, _, objectID, buf, ok := db.Build(ev)
		if !ok {
			t.Fatalf("Build(%q) returned ok=false", param)
		}
		if comp != string(HAComponentEvent) {
			t.Fatalf("Build(%q): component=%q want %q", param, comp, HAComponentEvent)
		}
		seen[param] = decodeBody(t, buf)
		seen[param]["__object_id"] = objectID
	}

	// Three distinct entities: distinct unique_id, object_id and state_topic.
	for _, field := range []string{"unique_id", "__object_id", "state_topic"} {
		values := map[string]string{}
		for param, body := range seen {
			v, _ := body[field].(string)
			if v == "" {
				t.Fatalf("%s: %s is empty", param, field)
			}
			if other, dup := values[v]; dup {
				t.Errorf("%s collides on %s=%q with %s — the kinds would share one entity", param, field, v, other)
			}
			values[v] = param
		}
	}

	// The device-error entity must announce the prefix-matched parameter, or
	// Home Assistant discards its pulses.
	errTypes := stringsOf(t, seen["ERROR_OVERHEAT"]["event_types"])
	want := map[string]bool{"error_overheat": true, "sensor_error": true}
	for _, got := range errTypes {
		if !want[got] {
			t.Errorf("device-error event_types has unexpected %q (got %v)", got, errTypes)
		}
		delete(want, got)
	}
	for missing := range want {
		t.Errorf("device-error event_types is missing %q — its pulses would be dropped (got %v)", missing, errTypes)
	}

	// The impulse entity carries only its own kind.
	if got := stringsOf(t, seen["SEQUENCE_OK"]["event_types"]); len(got) != 1 || got[0] != "sequence_ok" {
		t.Errorf("impulse event_types = %v, want [sequence_ok]", got)
	}

	// The keypress entity keeps its established identity: it must still be
	// the `_event` object, not a renamed one.
	if oid, _ := seen["PRESS_SHORT"]["__object_id"].(string); len(oid) < 6 || oid[len(oid)-6:] != "_event" {
		t.Errorf("keypress object_id = %q, want it to keep the established _event suffix", oid)
	}
}

// TestKnownRootsFallbackWhenTheChannelCannotList covers a channel that
// implements only HasParameter: device-error matching then reaches the known
// roots and nothing else, which is a documented limit rather than a silent
// one.
func TestKnownRootsFallbackWhenTheChannelCannotList(t *testing.T) {
	t.Parallel()

	ch := &fakeChannelInspector{params: map[string]struct{}{"ERROR": {}, "ERROR_OVERHEAT": {}}}
	got := ChannelKindTypes(ch, event.KindDeviceError)
	if len(got) != 1 || got[0] != "error" {
		t.Fatalf("fallback types = %v, want [error] — the roots only", got)
	}
}
