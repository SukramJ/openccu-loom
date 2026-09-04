// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package adapter

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/schedule"
	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
)

// simpleScheduleLoader hands a fixed non-climate schedule to a
// [weekprofile.Profile] so the publish path sees a loaded profile.
type simpleScheduleLoader struct{ sched *schedule.Simple }

func (l simpleScheduleLoader) Load(context.Context) (*schedule.Simple, error) { return l.sched, nil }

// TestPublishedScheduleEntityStateCountsActiveEntries pins the number MQTT
// publishes for a non-climate schedule to [weekprofile.CountSimpleEntries] —
// the same counter the hub tool reports as EntryCount. The expectation is
// read from that function, never restated as a literal, so the test measures
// agreement rather than a number someone typed twice.
//
// The fixture carries three entries of which one has no target channel, so
// "count the map" and "count the active entries" give different answers.
func TestPublishedScheduleEntityStateCountsActiveEntries(t *testing.T) {
	t.Parallel()

	reg, addr, chNo, wp := lockScheduleDevice(t, "HmIP-DLD", "DOOR_LOCK_STATE_TRANSMITTER")

	sched := schedule.NewSimple()
	sched.Entries[1] = schedule.SimpleEntry{TargetChannels: []string{"1_1"}}
	sched.Entries[2] = schedule.SimpleEntry{TargetChannels: []string{"1_2"}}
	sched.Entries[3] = schedule.SimpleEntry{TargetChannels: nil}
	if len(sched.Entries) == weekprofile.CountSimpleEntries(sched) {
		t.Fatalf("fixture is vacuous: %d entries, %d active", len(sched.Entries), weekprofile.CountSimpleEntries(sched))
	}

	prof := weekprofile.New[*schedule.Simple](simpleScheduleLoader{sched: sched}, nil)
	if _, err := prof.Load(context.Background()); err != nil {
		t.Fatalf("Load: %v", err)
	}
	wp.AttachSimpleProfile(prof)

	pub := mqtt.NewNoopClient()
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{
		Base:        "openccu-loom",
		CentralName: "ccu-01",
		RawEnabled:  true,
	}, pub)
	eb := NewEventBridge(reg, nil, mqtt.NewWiring(bridge, nil))
	eb.publishScheduleEntityPayload(context.Background(), "ccu-01", "HmIP-RF", addr, chNo, wp)

	want := strconv.Itoa(weekprofile.CountSimpleEntries(sched))
	var found bool
	for _, p := range pub.Published() {
		if !strings.HasSuffix(p.Topic, "/state") {
			continue
		}
		if !strings.Contains(p.Topic, "schedule") && !strings.Contains(p.Topic, "zeitplan") {
			continue
		}
		found = true
		if string(p.Payload) != want {
			t.Fatalf("published schedule state on %s = %q, want %q (CountSimpleEntries)", p.Topic, p.Payload, want)
		}
	}
	if !found {
		topics := make([]string, 0, len(pub.Published()))
		for _, p := range pub.Published() {
			topics = append(topics, p.Topic)
		}
		t.Fatalf("no schedule state topic published; saw %v", topics)
	}
}
