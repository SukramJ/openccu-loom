// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ws

import (
	"testing"
	"time"
)

func TestPublishAssignsMonotonicSeq(t *testing.T) {
	t.Parallel()
	h := NewHub()
	h.Publish(Event{Topic: "a", Type: "t", When: time.Now()})
	h.Publish(Event{Topic: "b", Type: "t", When: time.Now()})
	h.Publish(Event{Topic: "c", Type: "t", When: time.Now()})
	if got := h.CurrentSeq(); got != 3 {
		t.Fatalf("CurrentSeq = %d, want 3", got)
	}
}

func TestPublishDefaultsKindToChange(t *testing.T) {
	t.Parallel()
	h := NewHub()
	h.Publish(Event{Topic: "a", Type: "t", When: time.Now()})
	res := h.Replay(0, nil)
	if len(res.Events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(res.Events))
	}
	if res.Events[0].Kind != KindChange {
		t.Fatalf("Kind default = %q, want %q", res.Events[0].Kind, KindChange)
	}
}

func TestPublishPreservesExplicitKind(t *testing.T) {
	t.Parallel()
	h := NewHub()
	h.Publish(Event{Kind: KindInitial, Topic: "a", Type: "t", When: time.Now()})
	res := h.Replay(0, nil)
	if res.Events[0].Kind != KindInitial {
		t.Fatalf("Kind = %q, want initial", res.Events[0].Kind)
	}
}

func TestReplay_FromZeroReturnsEverything(t *testing.T) {
	t.Parallel()
	h := NewHub()
	for range 5 {
		h.Publish(Event{Topic: "a", Type: "t", When: time.Now()})
	}
	res := h.Replay(0, nil)
	if len(res.Events) != 5 {
		t.Fatalf("len=%d, want 5", len(res.Events))
	}
	if res.Lost {
		t.Fatal("Lost must be false when buffer covers since=0")
	}
}

func TestReplay_FromCurrentReturnsNothing(t *testing.T) {
	t.Parallel()
	h := NewHub()
	h.Publish(Event{Topic: "a", Type: "t", When: time.Now()})
	h.Publish(Event{Topic: "b", Type: "t", When: time.Now()})
	res := h.Replay(2, nil)
	if len(res.Events) != 0 {
		t.Fatalf("len=%d, want 0", len(res.Events))
	}
	if res.Lost {
		t.Fatal("Lost must be false")
	}
}

func TestReplay_AppliesMatchFilter(t *testing.T) {
	t.Parallel()
	h := NewHub()
	h.Publish(Event{Topic: "device.x", Type: "t", When: time.Now()})
	h.Publish(Event{Topic: "central.y", Type: "t", When: time.Now()})
	res := h.Replay(0, func(topic string) bool { return topic == "device.x" })
	if len(res.Events) != 1 || res.Events[0].Topic != "device.x" {
		t.Fatalf("filter mismatch: %+v", res.Events)
	}
}

func TestReplay_LostWhenSinceIsBelowOldest(t *testing.T) {
	t.Parallel()
	h := NewHub()
	h.SetReplayCapacity(3)
	for range 5 {
		h.Publish(Event{Topic: "a", Type: "t", When: time.Now()})
	}
	// Buffer now holds seq 3, 4, 5 (oldest=3). since=0 must be Lost.
	res := h.Replay(0, nil)
	if !res.Lost {
		t.Fatal("expected Lost=true when since precedes oldest")
	}
	if res.OldestSeq != 3 {
		t.Fatalf("OldestSeq = %d, want 3", res.OldestSeq)
	}
}

func TestSetReplayCapacity_Zero_DisablesBuffer(t *testing.T) {
	t.Parallel()
	h := NewHub()
	h.SetReplayCapacity(0)
	h.Publish(Event{Topic: "a", Type: "t", When: time.Now()})
	res := h.Replay(0, nil)
	if len(res.Events) != 0 {
		t.Fatalf("disabled buffer must yield no events, got %d", len(res.Events))
	}
	// Seq is still assigned even when buffer is off.
	if h.CurrentSeq() != 1 {
		t.Fatalf("CurrentSeq = %d, want 1", h.CurrentSeq())
	}
}

func TestSetReplayCapacity_Shrink_TruncatesExisting(t *testing.T) {
	t.Parallel()
	h := NewHub()
	for range 10 {
		h.Publish(Event{Topic: "a", Type: "t", When: time.Now()})
	}
	h.SetReplayCapacity(3)
	res := h.Replay(0, nil)
	if !res.Lost {
		t.Fatal("after shrink, since=0 must yield Lost")
	}
}
