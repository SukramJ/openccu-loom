// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mqtt

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
)

// ---------------------------------------------------------------------------
// BuildScheduleSwitchDiscovery — happy path
// ---------------------------------------------------------------------------

func TestBuildScheduleSwitchDiscoveryHappyPath(t *testing.T) {
	t.Parallel()

	builder := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu")
	ev := ScheduleSwitchEvent{
		Central:           "ccu1",
		Interface:         "HmIP-RF",
		DeviceAddress:     "0001ABCD",
		ScheduleChannelNo: 1,
		DeviceName:        "Test Device",
		Model:             "HmIP-MIO16-PCB",
		Key:               "1_1",
		TargetChannelNo:   18,
		Label:             "Zeitplan Kanal 18",
	}

	item := builder.BuildScheduleSwitchDiscovery("ccu1", ev)
	if !item.OK {
		t.Fatal("BuildScheduleSwitchDiscovery: expected OK=true")
	}
	if item.Component != string(HAComponentSwitch) {
		t.Errorf("Component = %q, want switch", item.Component)
	}
	if item.NodeID == "" {
		t.Error("NodeID must not be empty")
	}
	if !strings.Contains(item.ObjectID, "1_1") {
		t.Errorf("ObjectID = %q does not contain channel key", item.ObjectID)
	}

	var payload map[string]any
	if err := json.Unmarshal(item.Payload, &payload); err != nil {
		t.Fatalf("Payload is not valid JSON: %v", err)
	}
	if payload["name"] != "Zeitplan Kanal 18" {
		t.Errorf("name = %v, want %q", payload["name"], "Zeitplan Kanal 18")
	}
	if _, ok := payload["state_topic"]; !ok {
		t.Error("state_topic missing from discovery payload")
	}
	if _, ok := payload["command_topic"]; !ok {
		t.Error("command_topic missing from discovery payload")
	}
}

func TestBuildScheduleSwitchDiscoveryEmptyKey(t *testing.T) {
	t.Parallel()

	builder := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu")
	ev := ScheduleSwitchEvent{
		Central:       "ccu1",
		DeviceAddress: "0001ABCD",
		Key:           "", // empty key must return OK=false
	}
	item := builder.BuildScheduleSwitchDiscovery("ccu1", ev)
	if item.OK {
		t.Fatal("BuildScheduleSwitchDiscovery: expected OK=false when Key is empty")
	}
}

// ---------------------------------------------------------------------------
// Wire DP → Subscribe → Bus-Update integration
// ---------------------------------------------------------------------------

// TestChannelSwitchWiredDiscoveryAndBusUpdate verifies the end-to-end path:
// a ChannelSwitch backed by a ProfileDataPoint fires its Subscribe callback
// when the parent DP's schedule-enabled state changes, and the Discovery
// builder produces a valid payload for that switch entity.
func TestChannelSwitchWiredDiscoveryAndBusUpdate(t *testing.T) {
	t.Parallel()

	// Wire a ProfileDataPoint with one schedule channel.
	dp := weekprofile.NewProfileDataPoint(weekprofile.ProfileDataPointConfig{
		ScheduleType:   weekprofile.ScheduleTypeDefault,
		CentralName:    "ccu1",
		ChannelAddress: "0002BEEF:1",
	})
	dp.RegisterChannel("1_1", true)

	cs := weekprofile.NewChannelSwitch("ccu1", "0002BEEF", "1_1", dp)

	// Verify initial state.
	v := cs.Value()
	if v == nil || !*v {
		t.Fatalf("initial Value() = %v, want true", v)
	}

	// Subscribe: collect bus updates.
	var mu sync.Mutex
	var updates []bool
	unsubscribe := cs.Subscribe(func(enabled bool) {
		mu.Lock()
		updates = append(updates, enabled)
		mu.Unlock()
	})
	defer unsubscribe()

	// Drive a state change through ChannelSwitch.Set.
	if err := cs.Set(context.Background(), false); err != nil {
		t.Fatalf("Set(false): %v", err)
	}

	mu.Lock()
	n := len(updates)
	mu.Unlock()
	if n == 0 {
		t.Fatal("Subscribe callback was not invoked after Set(false)")
	}
	mu.Lock()
	lastVal := updates[n-1]
	mu.Unlock()
	if lastVal {
		t.Errorf("Subscribe callback received true, want false")
	}

	// Confirm the DP state also reflects the change.
	v = cs.Value()
	if v == nil || *v {
		t.Errorf("Value() after Set(false) = %v, want false", v)
	}

	// Verify the discovery builder produces a valid payload for this entity.
	builder := NewDefaultDiscoveryBuilder(NewTopicBuilder("gh"), "ccu1")
	discEv := ScheduleSwitchEvent{
		Central:           "ccu1",
		Interface:         "HmIP-RF",
		DeviceAddress:     "0002BEEF",
		ScheduleChannelNo: 1,
		DeviceName:        "Wired Device",
		Key:               cs.ChannelKey(),
		TargetChannelNo:   1,
		Label:             "Zeitplan 1_1",
	}
	item := builder.BuildScheduleSwitchDiscovery("ccu1", discEv)
	if !item.OK {
		t.Fatal("BuildScheduleSwitchDiscovery: expected OK=true after wiring")
	}
	if item.Component != string(HAComponentSwitch) {
		t.Errorf("Component = %q, want switch", item.Component)
	}
}
