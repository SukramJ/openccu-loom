// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	pload "github.com/SukramJ/openccu-loom/internal/payload"
)

// The JSON-schema light's state lives twice (ADR 0083, amendment item 7):
// as a status object under `status`, like every other status item, and as
// Home Assistant's bare document under this family's `ha` function, which
// the light entity reads. These pin that both come from one publish, leave
// together and are swept together.

const (
	lightTwinStatus = "gh/status/ccu-01/HmIP-RF/0001BDT00001/4/custom/light"
	lightTwinHA     = "gh/ha/ccu-01/HmIP-RF/0001BDT00001/4/custom/light"
)

var lightTwinSlot = pload.TopicSlot{Address: "0001BDT00001", Channel: 4, Bucket: pload.BucketCustom, Parameter: "light"}

func TestLightStatePublishesTheStatusObjectAndItsHATwin(t *testing.T) {
	t.Parallel()
	obs := newObservedPlane()
	b := NewBridge(BridgeConfig{Base: "gh", CentralName: "ccu-01", RawEnabled: true}, obs)
	doc := map[string]any{"state": "ON", "brightness": 128.0, "color_mode": "brightness"}
	if err := b.PublishCustomDPState(context.Background(), "ccu-01", "HmIP-RF", lightTwinSlot, doc); err != nil {
		t.Fatal(err)
	}
	got := map[string]publishRecord{}
	for _, rec := range obs.records() {
		got[rec.topic] = rec
	}
	if len(got) != 2 {
		t.Fatalf("published %v, want exactly the status item and its `ha` twin", got)
	}
	status, ha := got[lightTwinStatus], got[lightTwinHA]
	if !status.retain || !ha.retain {
		t.Errorf("retain: status %v, ha %v — both are state", status.retain, ha.retain)
	}
	var bare, obj map[string]any
	if err := json.Unmarshal([]byte(ha.payload), &bare); err != nil || !reflect.DeepEqual(bare, doc) {
		t.Errorf("`ha` twin = %s, want the light document bare", ha.payload)
	}
	if err := json.Unmarshal([]byte(status.payload), &obj); err != nil || !reflect.DeepEqual(obj["val"], doc) {
		t.Errorf("`status` item = %s, want a status object whose val is the light document", status.payload)
	}
	if _, ok := obj["ts"]; !ok {
		t.Errorf("`status` item = %s, has no ts", status.payload)
	}

	// Another aggregate has no twin.
	obs2 := newObservedPlane()
	b2 := NewBridge(BridgeConfig{Base: "gh", CentralName: "ccu-01", RawEnabled: true}, obs2)
	cover := lightTwinSlot
	cover.Parameter = "cover"
	if err := b2.PublishCustomDPState(context.Background(), "ccu-01", "HmIP-RF", cover, map[string]any{"position": 50}); err != nil {
		t.Fatal(err)
	}
	if recs := obs2.records(); len(recs) != 1 {
		t.Errorf("a cover aggregate published %d topics, want its status item alone", len(recs))
	}
}

func TestLightTwinLeavesWithTheDevice(t *testing.T) {
	t.Parallel()
	obs := newObservedPlane()
	b := NewBridge(BridgeConfig{Base: "gh", CentralName: "ccu-01", RawEnabled: true}, obs)
	ctx := context.Background()
	if err := b.PublishCustomDPState(ctx, "ccu-01", "HmIP-RF", lightTwinSlot, map[string]any{"state": "OFF"}); err != nil {
		t.Fatal(err)
	}
	b.RetractRawStateForDevice(ctx, "ccu-01", "HmIP-RF", "0001BDT00001")
	cleared := map[string]bool{}
	for _, rec := range obs.records() {
		if rec.payload == "" && rec.retain {
			cleared[rec.topic] = true
		}
	}
	for _, topic := range []string{lightTwinStatus, lightTwinHA} {
		if !cleared[topic] {
			t.Errorf("device removal left %s retained", topic)
		}
	}
}

func TestLightTwinIsARawOrphanCandidate(t *testing.T) {
	t.Parallel()
	if !RawOrphanCandidateMatcher("gh", "ccu-01", lightTwinHA) {
		t.Errorf("the orphan sweep cannot see %s, so a light that is gone keeps its twin retained", lightTwinHA)
	}
	if got := NewTopicBuilder("gh").SlotHAState("ccu-01", "HmIP-RF", lightTwinSlot); got != lightTwinHA {
		t.Errorf("SlotHAState = %q, want %q", got, lightTwinHA)
	}
}
