// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/payload"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// stubTextDisplay drives the text-display discovery + notify-companion
// path: a Source + HADiscoveryPayloadBuilder that classifies as a
// text-display custom-DP and exposes a custom-DP slot for the write
// service-method command topic.
type stubTextDisplay struct {
	stubSource
	slot payload.TopicSlot
}

func (s *stubTextDisplay) HADiscoveryPayload(ctx payload.HADiscoveryContext) (component string, body map[string]any) {
	return "text", map[string]any{
		"command_topic": ctx.ServiceMethodCommandTopic("write"),
		"mode":          "text",
	}
}

func (s *stubTextDisplay) Category() hmenum.DataPointCategory {
	return hmenum.DataPointCategoryTextDisplay
}

func (s *stubTextDisplay) TopicSlot() payload.TopicSlot { return s.slot }

func textDisplayEvent() Event {
	src := &stubTextDisplay{
		slot: payload.TopicSlot{
			Address:   "002A5D8989D5C3",
			Channel:   3,
			Bucket:    payload.BucketCustom,
			Parameter: "text_display",
		},
	}
	return Event{
		Source:        src,
		Central:       "ccu",
		Interface:     "HmIP-RF",
		DeviceAddress: "002A5D8989D5C3",
		DeviceName:    "Displayschalter",
		Model:         "HmIP-WRCD",
		ChannelNo:     3,
		ChannelType:   "TEXT_DISPLAY_TRANSMITTER",
	}
}

// TestTextDisplayProducesNotifyOnly is the parity tripwire for the
// HmIP-WRCD display: the reference stack maps a TEXT_DISPLAY custom-DP
// onto a `notify` entity ONLY (no `text` entity). The aggregate path
// must suppress the text entity; BuildTextDisplayNotify produces the
// notify entity that is the display's sole HA surface.
func TestTextDisplayProducesNotifyOnly(t *testing.T) {
	t.Parallel()
	db := NewDefaultDiscoveryBuilder(NewTopicBuilder("openccu-loom"), "ccu")
	db.SetHubInfoFor("ccu", HubInfo{Serial: "3014F711A0001234"})

	ev := textDisplayEvent()

	// The aggregate path must NOT yield a text entity for a text-display
	// custom-DP — that surplus entity collides with the notify under a
	// `_2` suffix in HA.
	if _, _, _, _, ok := db.Build(ev); ok {
		t.Fatal("aggregate path must suppress the text entity for a text-display custom-DP")
	}

	// The notify entity is the sole HA surface.
	item := db.BuildTextDisplayNotify(ev)
	if !item.OK {
		t.Fatal("BuildTextDisplayNotify must produce a notify entity for a text-display custom-DP")
	}
	if item.Component != string(HAComponentNotify) {
		t.Fatalf("notify component=%q want notify", item.Component)
	}
	var m map[string]any
	if err := json.Unmarshal(item.Payload, &m); err != nil {
		t.Fatalf("payload: %v", err)
	}
	cmd, _ := m["command_topic"].(string)
	if !strings.Contains(cmd, "/write") {
		t.Fatalf("notify command_topic=%q must target the write service method", cmd)
	}
	tmpl, _ := m["command_template"].(string)
	if !strings.Contains(tmpl, "tojson") || !strings.Contains(tmpl, `"text"`) {
		t.Fatalf("notify command_template=%q must wrap the message into the write payload", tmpl)
	}
	// The notify and text entities must carry distinct unique_ids so HA
	// does not collapse them.
	notifyUID, _ := m["unique_id"].(string)
	if notifyUID == "" || !strings.Contains(notifyUID, "notify") {
		t.Fatalf("notify unique_id=%q must be notify-scoped", notifyUID)
	}
}

// TestBuildTextDisplayNotifySkipsNonTextDisplay pins that the notify
// companion is only produced for text-display custom-DPs.
func TestBuildTextDisplayNotifySkipsNonTextDisplay(t *testing.T) {
	t.Parallel()
	db := NewDefaultDiscoveryBuilder(NewTopicBuilder("openccu-loom"), "ccu")
	if item := db.BuildTextDisplayNotify(Event{}); item.OK {
		t.Fatal("event without a text-display source must not produce a notify entity")
	}
	src := &stubBuilder{component: "switch", body: map[string]any{}}
	if item := db.BuildTextDisplayNotify(Event{Source: src, ChannelNo: 1}); item.OK {
		t.Fatal("non-text-display source must not produce a notify entity")
	}
}
