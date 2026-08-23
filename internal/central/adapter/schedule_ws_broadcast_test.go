// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/model/weekprofile"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/internal/north/rest/ws"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// climateBridgeFixtureWS is [climateBridgeFixture] plus a WS hub, so a test
// can observe what the WebSocket plane received alongside the MQTT publish.
// It also returns the device address, the channel number and the
// week-profile DP itself so the test can drive a profile-pointer change.
func climateBridgeFixtureWS(t *testing.T, loader *countingClimateLoader) (
	eb *EventBridge, hub *ws.Hub, deviceAddr string, channelNo int, wp *weekprofile.ProfileDataPoint,
) {
	t.Helper()
	reg, dev := registryWithDevice(t)
	ch := dev.AddChannel(dev.Address+":1", 1, "HEATING_CLIMATECONTROL_TRANSCEIVER", hmenum.ParamsetKeyValues)
	wp = weekprofile.NewProfileDataPoint(weekprofile.ProfileDataPointConfig{
		CentralName:    "ccu-01",
		ChannelAddress: ch.Address,
		ScheduleType:   weekprofile.ScheduleTypeClimate,
		ProfileCount:   3,
	})
	wp.AttachClimateProfile(weekprofile.NewClimate(loader, nil))
	ch.AttachWeekProfile(wp)

	bridge := mqtt.NewBridge(mqtt.BridgeConfig{
		Base:        "openccu-loom",
		CentralName: "ccu-01",
		RawEnabled:  true,
	}, mqtt.NewNoopClient())
	hub = ws.NewHub()
	eb = NewEventBridge(reg, hub, mqtt.NewWiring(bridge, nil))
	return eb, hub, dev.Address, 1, wp
}

// waitForWSType polls the hub's replay buffer for a frame of the given type
// on the given topic. Async fan-out (SafeGo goroutines, the fanout worker)
// means the frame is not guaranteed to be there the statement after the
// triggering call, so this polls instead of reading the sink once — see
// notes/contributor/engineering-rules.md on async fan-out flakes.
func waitForWSType(t *testing.T, hub *ws.Hub, topic, typ string) bool {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, e := range hub.Replay(0, nil).Events {
			if e.Topic == topic && e.Type == typ {
				return true
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	return false
}

// TestClimateScheduleChangeBroadcastsSchedulesChangedWS pins the WS half of
// A week-profile / climate-schedule change published a
// state topic to MQTT and reached no other plane, so an SPA schedule view
// already open never learned a CCU-side or second-operator change happened.
// The reproducer drives the real OnChange seam (SetCurrentProfile, the same
// path SyncProfilePointer takes on a CCU push) and asserts the WS plane
// carries a `schedules.changed` frame on the device's lifecycle topic.
func TestClimateScheduleChangeBroadcastsSchedulesChangedWS(t *testing.T) {
	t.Parallel()

	loader := &countingClimateLoader{}
	eb, wsHub, addr, _, wp := climateBridgeFixtureWS(t, loader)
	eb.Start(context.Background())
	defer eb.Stop()

	// Wires the OnChange subscriptions this test exercises — the pass
	// [publishWeekProfileSnapshot] runs over every attached week profile.
	eb.PublishInitialSnapshot(context.Background())

	topic := ws.DeviceLifecycleTopic(addr)

	if err := wp.SetCurrentProfile("P2"); err != nil {
		t.Fatalf("SetCurrentProfile: %v", err)
	}

	if !waitForWSType(t, wsHub, topic, "schedules.changed") {
		t.Fatalf("no WS schedules.changed push on topic %q after a profile-pointer change", topic)
	}
}
