// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

// Retained-flag drop tests for the CommandSubscriber. The MQTT broker
// replays every retained message on a topic at subscribe time, so a
// stale `mosquitto_pub -r` against any `*/set` topic would otherwise
// re-apply itself on every daemon restart. These tests assert each
// inbound write handler verbatim ignores retained messages.

import (
	"context"
	"testing"
)

func TestHandleDataPoint_RetainedDropped(t *testing.T) {
	t.Parallel()
	noop := NewNoopClient()
	topics := NewTopicBuilder("openccu-loom")
	sink := &fakeSink{}
	sub := NewCommandSubscriber(noop, topics, sink, nil)
	if err := sub.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	if !noop.DeliverInboundRetained("openccu-loom/+/+/+/+/+/set",
		"openccu-loom/ccu-01/HmIP-RF/0001ABCD/1/STATE/set", []byte("true")) {
		t.Fatal("retained delivery rejected by subscriber lookup")
	}
	if got := sink.setValues.Load(); got != 0 {
		t.Fatalf("retained set produced sink call: setValues=%d", got)
	}
	// Sanity: a non-retained replay of the same shape DOES propagate.
	noop.DeliverInbound("openccu-loom/+/+/+/+/+/set",
		"openccu-loom/ccu-01/HmIP-RF/0001ABCD/1/STATE/set", []byte("true"))
	if got := sink.setValues.Load(); got != 1 {
		t.Fatalf("non-retained set blocked too: setValues=%d, want 1", got)
	}
}

func TestHandleSysvar_RetainedDropped(t *testing.T) {
	t.Parallel()
	noop := NewNoopClient()
	topics := NewTopicBuilder("openccu-loom")
	sink := &fakeSink{}
	sub := NewCommandSubscriber(noop, topics, sink, nil)
	if err := sub.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	noop.DeliverInboundRetained("openccu-loom/+/hub/sysvars/+/set",
		"openccu-loom/ccu-01/hub/sysvars/Anwesenheit/set", []byte("true"))
	if got := sink.setSysvars.Load(); got != 0 {
		t.Fatalf("retained sysvar set produced sink call: setSysvars=%d", got)
	}
	noop.DeliverInbound("openccu-loom/+/hub/sysvars/+/set",
		"openccu-loom/ccu-01/hub/sysvars/Anwesenheit/set", []byte("true"))
	if got := sink.setSysvars.Load(); got != 1 {
		t.Fatalf("non-retained sysvar blocked: setSysvars=%d, want 1", got)
	}
}

func TestHandleProgram_RetainedDropped(t *testing.T) {
	t.Parallel()
	noop := NewNoopClient()
	topics := NewTopicBuilder("openccu-loom")
	sink := &fakeSink{}
	sub := NewCommandSubscriber(noop, topics, sink, nil)
	if err := sub.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	noop.DeliverInboundRetained("openccu-loom/+/hub/programs/+/trigger",
		"openccu-loom/ccu-01/hub/programs/123/trigger", nil)
	if got := sink.triggers.Load(); got != 0 {
		t.Fatalf("retained program trigger fired sink: triggers=%d", got)
	}
	noop.DeliverInbound("openccu-loom/+/hub/programs/+/trigger",
		"openccu-loom/ccu-01/hub/programs/123/trigger", nil)
	if got := sink.triggers.Load(); got != 1 {
		t.Fatalf("non-retained trigger blocked: triggers=%d, want 1", got)
	}
}

func TestHandleServiceMethod_RetainedDropped(t *testing.T) {
	t.Parallel()
	noop := NewNoopClient()
	topics := NewTopicBuilder("openccu-loom")
	sink := &fakeSink{}
	cdp := &fakeCDPSink{}
	sub := NewCommandSubscriber(noop, topics, sink, nil).WithCDPSink(cdp)
	if err := sub.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	noop.DeliverInboundRetained("openccu-loom/+/+/+/+/custom/+/set/+",
		"openccu-loom/ccu-01/HmIP-RF/0001ABCD/1/custom/climate/set/boost", []byte("true"))
	if got := cdp.calls.Load(); got != 0 {
		t.Fatalf("retained service method dispatched: calls=%d", got)
	}
}

func TestHandleCDPInvoke_RetainedDropped(t *testing.T) {
	t.Parallel()
	noop := NewNoopClient()
	topics := NewTopicBuilder("openccu-loom")
	sink := &fakeSink{}
	cdp := &fakeCDPSink{}
	sub := NewCommandSubscriber(noop, topics, sink, nil).WithCDPSink(cdp)
	if err := sub.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	noop.DeliverInboundRetained("openccu-loom/+/devices/+/cdps/+/+/invoke",
		"openccu-loom/ccu-01/devices/0001ABCD/cdps/climate/boost/invoke", []byte(`{}`))
	if got := cdp.calls.Load(); got != 0 {
		t.Fatalf("retained CDP invoke dispatched: calls=%d", got)
	}
}

func TestHandleWeekProfile_RetainedDropped(t *testing.T) {
	t.Parallel()
	noop := NewNoopClient()
	topics := NewTopicBuilder("openccu-loom")
	sink := &fakeSink{}
	wp := &fakeWPSink{}
	sub := NewCommandSubscriber(noop, topics, sink, nil).WithWeekProfileSink(wp)
	if err := sub.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	noop.DeliverInboundRetained("openccu-loom/+/+/+/+/week_profile/set",
		"openccu-loom/ccu-01/HmIP-RF/0001ABCD/1/week_profile/set", []byte("P2"))
	if got := wp.calls.Load(); got != 0 {
		t.Fatalf("retained week-profile set dispatched: calls=%d", got)
	}
}
