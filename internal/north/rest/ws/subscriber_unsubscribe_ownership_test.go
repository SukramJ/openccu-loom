// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package ws

// subscriber_unsubscribe_ownership_test.go pins who owns which unsubscribe in
// the registry-observing WebSocket subscribers, for every one of them at once.
//
// The question is not academic. Each of these types wires itself through
// central.Registry.OnRegister, so a CCU that joins the registry after Start —
// on the HTTP goroutine that adopts it — is subscribed without anybody calling
// a second registrar. That is the whole fix: the shape it replaced had a boot
// walk and a separate adopt hook, and the adopt hook is what kept getting
// forgotten.
//
// The invariant that follows is ownership: the subscriber owns every
// subscription it holds, so Stop drops the adopted central's along with the
// boot-time ones, and leaving the registry drops that central's alone. The
// same table then drives registration against Stop concurrently, which is what
// makes `-race` speak up if that ledger is ever touched unsafely.

import (
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// registryObservingSubscriber is the shape every subscriber in the table
// shares: an observer registration, a per-central attach, and a teardown.
type registryObservingSubscriber struct {
	start        func()
	startCentral func(*central.Unit) func()
	stop         func()
}

// wsSubscriberCase describes one subscriber plus the smallest event that
// proves its subscription is live, and the topic that event lands on.
type wsSubscriberCase struct {
	name string
	// build wires the subscriber to reg and hub.
	build func(reg *central.Registry, hub *Hub) registryObservingSubscriber
	// publish emits one event on the unit's bus, keyed to addr so two
	// centrals producing the same event still land on distinct topics.
	publish func(u *central.Unit, addr string)
	// topic is where that event surfaces.
	topic func(centralName, addr string) string
}

func wsSubscriberCases() []wsSubscriberCase {
	return []wsSubscriberCase{
		{
			name: "system status",
			build: func(reg *central.Registry, hub *Hub) registryObservingSubscriber {
				s := NewSystemStatusSubscriber(reg, hub)
				return registryObservingSubscriber{s.Start, s.StartCentral, s.Stop}
			},
			publish: func(u *central.Unit, _ string) {
				events.Publish(u.EventBus, hmevent.SystemStatusChangedEvent{
					CentralName: u.Name(), Component: "interface", Healthy: true,
				})
			},
			topic: func(centralName, _ string) string { return SystemStatusTopic(centralName) },
		},
		{
			name: "hub events",
			build: func(reg *central.Registry, hub *Hub) registryObservingSubscriber {
				s := NewHubEventsSubscriber(reg, hub)
				return registryObservingSubscriber{s.Start, s.StartCentral, s.Stop}
			},
			publish: func(u *central.Unit, addr string) {
				events.Publish(u.EventBus, hmevent.SysvarChangedEvent{
					CentralName: u.Name(), Name: addr, ValueType: hmenum.HubValueTypeLogic,
				})
			},
			topic: SysvarTopic,
		},
		{
			name: "device lifecycle",
			build: func(reg *central.Registry, hub *Hub) registryObservingSubscriber {
				s := NewDeviceLifecycleSubscriber(reg, hub)
				return registryObservingSubscriber{s.Start, s.StartCentral, s.Stop}
			},
			publish: func(u *central.Unit, addr string) {
				events.Publish(u.EventBus, hmevent.DeviceCreatedEvent{
					CentralName: u.Name(), Address: addr, Model: "HmIP-PS",
				})
			},
			topic: func(_, addr string) string { return DeviceLifecycleTopic(addr) },
		},
		{
			name: "device trigger",
			build: func(reg *central.Registry, hub *Hub) registryObservingSubscriber {
				s := NewDeviceTriggerSubscriber(reg, hub)
				return registryObservingSubscriber{s.Start, s.StartCentral, s.Stop}
			},
			publish: func(u *central.Unit, addr string) {
				events.Publish(u.EventBus, hmevent.DeviceTriggerEvent{
					CentralName: u.Name(), DeviceAddress: addr, ChannelNo: 1,
					EventType_: hmenum.DeviceTriggerEventTypeKeypress, Parameter: "PRESS_SHORT",
				})
			},
			topic: func(_, addr string) string { return DeviceTriggerTopic(addr, 1) },
		},
		{
			name: "optimistic rollback",
			build: func(reg *central.Registry, hub *Hub) registryObservingSubscriber {
				s := NewOptimisticRollbackSubscriber(reg, hub)
				return registryObservingSubscriber{s.Start, s.StartCentral, s.Stop}
			},
			publish: func(u *central.Unit, addr string) {
				events.Publish(u.EventBus, hmevent.DataPointOptimisticRolledBackEvent{
					Key: hmtypes.DataPointKey{
						InterfaceID:    u.Name() + "-HmIP-RF",
						ChannelAddress: addr + ":1",
						ParamsetKey:    hmenum.ParamsetKeyValues,
						Parameter:      "STATE",
					},
					Reason: hmenum.RollbackReasonTimeout,
				})
			},
			topic: func(_, addr string) string { return DataPointTopic(addr, 1, "STATE") },
		},
	}
}

// TestSubscriberSubscribesACentralRegisteredAfterStart asserts the fix itself,
// for every subscriber at once: a CCU that joins the registry after Start is
// subscribed with no further call, and stays subscribed until it leaves the
// registry.
func TestSubscriberSubscribesACentralRegisteredAfterStart(t *testing.T) {
	t.Parallel()

	for _, tc := range wsSubscriberCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			hub := NewHub()
			reg := central.NewRegistry()

			sub := tc.build(reg, hub)
			sub.start()
			t.Cleanup(sub.stop)

			adopted := registerHubEventsCentral(t, reg, "adopted")
			tc.publish(adopted, "ADOPT001")
			if got := hubEventsOnTopic(hub, tc.topic("adopted", "ADOPT001")); len(got) != 1 {
				t.Fatalf("%d event(s) on %q, want 1 — a central registered after Start reached nothing",
					len(got), tc.topic("adopted", "ADOPT001"))
			}

			// Leaving the registry detaches that central and only that central.
			if !reg.Unregister("adopted") {
				t.Fatal("Unregister reported the central was not present")
			}
			before := len(hubEventsOnTopic(hub, tc.topic("adopted", "ADOPT001")))
			tc.publish(adopted, "ADOPT001")
			if after := len(hubEventsOnTopic(hub, tc.topic("adopted", "ADOPT001"))); after != before {
				t.Errorf("events after Unregister = %d, want %d (the subscription leaked past removal)",
					after, before)
			}
		})
	}
}

// TestSubscriberStopDropsEverySubscriptionItHolds pins the other half of the
// ownership rule: the subscriber owns every subscription the observer made, so
// Stop drops the adopted central's along with the boot-time ones. A shape that
// left the adopted one behind would keep publishing after shutdown.
func TestSubscriberStopDropsEverySubscriptionItHolds(t *testing.T) {
	t.Parallel()

	for _, tc := range wsSubscriberCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			hub := NewHub()
			reg := central.NewRegistry()
			boot := registerHubEventsCentral(t, reg, "boot")

			sub := tc.build(reg, hub)
			sub.start()

			adopted := registerHubEventsCentral(t, reg, "adopted")

			sub.stop()
			sub.stop() // idempotent

			tc.publish(boot, "BOOT0001")
			if got := hubEventsOnTopic(hub, tc.topic("boot", "BOOT0001")); len(got) != 0 {
				t.Errorf("the boot-time subscription survived Stop: %d event(s) on %q",
					len(got), tc.topic("boot", "BOOT0001"))
			}

			tc.publish(adopted, "ADOPT001")
			if got := hubEventsOnTopic(hub, tc.topic("adopted", "ADOPT001")); len(got) != 0 {
				t.Errorf("the adopted central's subscription survived Stop: %d event(s) on %q",
					len(got), tc.topic("adopted", "ADOPT001"))
			}
		})
	}
}

// TestSubscriberRegistrationRunsConcurrentlyWithStop drives the overlap the
// daemon can actually produce — an adopt on an HTTP goroutine while shutdown
// tears the subscribers down — for every registry-observing subscriber.
//
// Under `-race` this fails the moment the subscription ledger is touched
// without the registry's wiring lock. Without it the run is still worth
// having: it proves the two entry points do not deadlock or panic against
// each other.
func TestSubscriberRegistrationRunsConcurrentlyWithStop(t *testing.T) {
	t.Parallel()

	for _, tc := range wsSubscriberCases() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			hub := NewHub()
			reg := central.NewRegistry()
			registerHubEventsCentral(t, reg, "boot")

			sub := tc.build(reg, hub)
			sub.start()

			const adopts = 8
			var wg sync.WaitGroup
			for i := range adopts {
				wg.Add(1)
				go func() {
					defer wg.Done()
					unit, err := central.New(central.Config{Name: "adopted-" + string(rune('a'+i))})
					if err != nil {
						return
					}
					_ = reg.Register(unit)
				}()
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				sub.stop()
			}()
			wg.Wait()
		})
	}
}
