// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// unreachSensor attaches a channel-0 UNREACH data point to d so
// [device.Device.Available] answers from it.
func unreachSensor(t *testing.T, d *device.Device) *generic.BinarySensor {
	t.Helper()
	ch := d.Channel(d.Address + ":0")
	if ch == nil {
		ch = d.AddChannel(d.Address+":0", 0, "MAINTENANCE", hmenum.ParamsetKeyValues)
	}
	dp := generic.NewBinarySensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: d.Address + ":0",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterUnreach),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
	ch.Put(dp)
	return dp
}

// collectAvailabilityEvents subscribes to the availability sub-type of
// [hmevent.DeviceLifecycleEvent] on u's bus and returns a reader of what
// arrived so far.
func collectAvailabilityEvents(t *testing.T, u *central.Unit) func() []hmevent.DeviceLifecycleEvent {
	t.Helper()
	var (
		mu  sync.Mutex
		got []hmevent.DeviceLifecycleEvent
	)
	unsub := events.Subscribe(u.EventBus, func(e hmevent.DeviceLifecycleEvent) {
		if e.Subtype != hmenum.DeviceLifecycleSubtypeAvailabilityChanged {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		got = append(got, e)
	})
	t.Cleanup(unsub)
	return func() []hmevent.DeviceLifecycleEvent {
		mu.Lock()
		defer mu.Unlock()
		return append([]hmevent.DeviceLifecycleEvent(nil), got...)
	}
}

// TestEventBridgeAnnouncesAvailabilityTransition drives an UNREACH push
// through the bridge and asserts the effective-availability flip is
// announced once on the central's bus — with and without MQTT wiring.
//
// Only the interface-level forced-availability path used to publish that
// event, so a single device the CCU stopped reaching stayed `available`
// on every consumer of the bus (the WebSocket device-lifecycle plane and
// the Matter reachability forward) until the next full resync. The MQTT
// availability topic already flipped from the same transition, which is
// what made the gap invisible.
func TestEventBridgeAnnouncesAvailabilityTransition(t *testing.T) {
	t.Parallel()

	for _, withMQTT := range []bool{true, false} {
		name := "without_mqtt"
		if withMQTT {
			name = "with_mqtt"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			_, u, d, stop := startedBridgeWS(t, withMQTT)
			t.Cleanup(stop)

			dp := unreachSensor(t, d)
			read := collectAvailabilityEvents(t, u)

			publishUnreach := func(v bool) {
				dp.OnEvent(v)
				events.Publish(u.EventBus, hmevent.DataPointValueChangedEvent{
					Base: hmevent.NewBase(),
					Key: hmtypes.DataPointKey{
						InterfaceID:    "HmIP-RF",
						ChannelAddress: d.Address + ":0",
						ParamsetKey:    hmenum.ParamsetKeyValues,
						Parameter:      string(hmenum.ParameterUnreach),
					},
					NewValue: hmtypes.BoolValue(v),
				})
			}

			publishUnreach(true)
			waitFor(t, func() bool { return len(read()) == 1 })
			if got := read()[0]; got.Available || got.Address != d.Address ||
				got.CentralName != u.Name() || got.InterfaceID != "HmIP-RF" {
				t.Fatalf("first announcement = %+v", got)
			}

			// The same value again is not a transition — announcing it
			// would put one frame per wire event on every consumer.
			publishUnreach(true)
			time.Sleep(50 * time.Millisecond)
			if n := len(read()); n != 1 {
				t.Fatalf("announcements after a repeated value = %d, want 1", n)
			}

			publishUnreach(false)
			waitFor(t, func() bool { return len(read()) == 2 })
			if got := read()[1]; !got.Available {
				t.Fatalf("recovery announcement = %+v, want available", got)
			}
		})
	}
}

// waitFor polls cond until it holds or the deadline expires. The live
// dispatch hands the MQTT arm to the fan-out worker, so the announcement
// can land on a different goroutine than the publish.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("condition not reached within deadline")
}

// availabilityPublishCount counts how often the device-availability topic of
// addr appears in what the fake client received.
func availabilityPublishCount(pub *mqtt.NoopClient, addr string) int {
	n := 0
	for _, p := range pub.Published() {
		if strings.Contains(p.Topic, addr) && strings.HasSuffix(p.Topic, "/availability") {
			n++
		}
	}
	return n
}

// TestPublishInitialSnapshotResetsAvailabilityGate pins that the transition
// gate does not outlive the retained topics it describes. The snapshot runs
// on every broker (re)connect, and a broker that came back without its
// retained store needs the availability topic re-published — the gate would
// otherwise report "no transition" forever and leave every HA entity of the
// device unavailable until the daemon restarts.
func TestPublishInitialSnapshotResetsAvailabilityGate(t *testing.T) {
	t.Parallel()
	eb, pub := buildBridgeEnv(t)
	ctx := context.Background()

	if !eb.markAvailability(ctx, "ccu-01", "HmIP-RF", "AVAILGATE01", true) {
		t.Fatal("first markAvailability must report a transition")
	}
	if eb.markAvailability(ctx, "ccu-01", "HmIP-RF", "AVAILGATE01", true) {
		t.Fatal("repeated markAvailability must stay deduped")
	}
	before := availabilityPublishCount(pub, "AVAILGATE01")

	eb.PublishInitialSnapshot(ctx)

	if !eb.markAvailability(ctx, "ccu-01", "HmIP-RF", "AVAILGATE01", true) {
		t.Fatal("markAvailability after a snapshot pass must publish again")
	}
	if got := availabilityPublishCount(pub, "AVAILGATE01"); got <= before {
		t.Fatalf("availability publishes = %d, want more than %d", got, before)
	}
}

// TestOnDeviceRemovedForgetsAvailability pins the same invariant for a single
// device: the removal retracts the availability topic, so a device readopted
// under the same address must be able to publish `online` again.
func TestOnDeviceRemovedForgetsAvailability(t *testing.T) {
	t.Parallel()
	eb, _ := buildBridgeEnv(t)
	ctx := context.Background()

	eb.markAvailability(ctx, "ccu-01", "HmIP-RF", "AVAILGATE02", true)

	eb.onDeviceRemoved(ctx, hmevent.DeviceRemovedEvent{
		CentralName: "ccu-01",
		InterfaceID: "HmIP-RF",
		Address:     "AVAILGATE02",
	})

	if !eb.markAvailability(ctx, "ccu-01", "HmIP-RF", "AVAILGATE02", true) {
		t.Fatal("availability must be publishable again after the topic was retracted")
	}
}
