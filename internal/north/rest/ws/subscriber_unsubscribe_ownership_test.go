// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package ws

// subscriber_unsubscribe_ownership_test.go pins who owns which
// unsubscribe in the registry-walking WebSocket subscribers, for every one
// of them at once.
//
// The question is not academic. Each of these types keeps an `unsubs` slice
// that Start fills and Stop drains, and Start runs on the composition root's
// goroutine while StartCentral runs on the HTTP goroutine that adopts a CCU.
// The slice carries no lock, and it is only safe to leave it that way as
// long as StartCentral stays off it — handing its unwire back to the adopt
// path instead of recording it. A future StartCentral that "helpfully"
// appended to the slice would compile, pass every existing test, and turn
// the field into shared mutable state written from two goroutines.
//
// So the invariant gets a check rather than a comment: the subscription
// StartCentral returns must survive Stop (it belongs to the caller), while
// the ones the boot walk made must not. The same table then drives
// StartCentral against Stop concurrently, which is what makes `-race` speak
// up if the ownership rule is ever broken.

import (
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// registryWalkingSubscriber is the shape every subscriber in the table
// shares: a boot walk, a per-central attach, and a teardown.
type registryWalkingSubscriber struct {
	start        func()
	startCentral func(*central.Unit) func()
	stop         func()
}

// wsSubscriberCase describes one subscriber plus the smallest event that
// proves its subscription is live, and the topic that event lands on.
type wsSubscriberCase struct {
	name string
	// build wires the subscriber to reg and hub.
	build func(reg *central.Registry, hub *Hub) registryWalkingSubscriber
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
			build: func(reg *central.Registry, hub *Hub) registryWalkingSubscriber {
				s := NewSystemStatusSubscriber(reg, hub)
				return registryWalkingSubscriber{s.Start, s.StartCentral, s.Stop}
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
			build: func(reg *central.Registry, hub *Hub) registryWalkingSubscriber {
				s := NewHubEventsSubscriber(reg, hub)
				return registryWalkingSubscriber{s.Start, s.StartCentral, s.Stop}
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
			build: func(reg *central.Registry, hub *Hub) registryWalkingSubscriber {
				s := NewDeviceLifecycleSubscriber(reg, hub)
				return registryWalkingSubscriber{s.Start, s.StartCentral, s.Stop}
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
			build: func(reg *central.Registry, hub *Hub) registryWalkingSubscriber {
				s := NewDeviceTriggerSubscriber(reg, hub)
				return registryWalkingSubscriber{s.Start, s.StartCentral, s.Stop}
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
			build: func(reg *central.Registry, hub *Hub) registryWalkingSubscriber {
				s := NewOptimisticRollbackSubscriber(reg, hub)
				return registryWalkingSubscriber{s.Start, s.StartCentral, s.Stop}
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

// TestSubscriberStopDropsOnlyTheBootWalksSubscriptions asserts the
// ownership rule the unguarded `unsubs` slice rests on: Stop drops what the
// boot walk attached and leaves what StartCentral handed to the adopt path.
//
// Break it — record StartCentral's unwire in the slice — and a runtime
// adopted central goes silent the moment the daemon tears the boot-time
// subscribers down, while the adopt path's own unwire runs a second time.
func TestSubscriberStopDropsOnlyTheBootWalksSubscriptions(t *testing.T) {
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
			unwire := sub.startCentral(adopted)
			if unwire == nil {
				t.Fatal("StartCentral returned a nil unwire for a central with an event bus")
			}
			t.Cleanup(unwire)

			sub.stop()

			tc.publish(boot, "BOOT0001")
			if got := hubEventsOnTopic(hub, tc.topic("boot", "BOOT0001")); len(got) != 0 {
				t.Errorf("the boot walk's subscription survived Stop: %d event(s) on %q",
					len(got), tc.topic("boot", "BOOT0001"))
			}

			tc.publish(adopted, "ADOPT001")
			if got := hubEventsOnTopic(hub, tc.topic("adopted", "ADOPT001")); len(got) != 1 {
				t.Errorf("Stop also dropped the subscription StartCentral handed to the adopt path: "+
					"%d event(s) on %q, want 1 — the adopt path owns that unwire, and recording it in "+
					"the boot slice both silences the adopted central and makes the slice shared state",
					len(got), tc.topic("adopted", "ADOPT001"))
			}
		})
	}
}

// TestSubscriberStartCentralRunsConcurrentlyWithStop drives the overlap the
// daemon can actually produce — an adopt on an HTTP goroutine while shutdown
// tears the subscribers down — for every registry-walking subscriber.
//
// Under `-race` this fails the moment StartCentral touches the boot slice
// Stop is draining. Without it the run is still worth having: it proves the
// two entry points do not deadlock or panic against each other.
func TestSubscriberStartCentralRunsConcurrentlyWithStop(t *testing.T) {
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
			units := make([]*central.Unit, adopts)
			for i := range units {
				units[i] = registerHubEventsCentral(t, reg, "adopted-"+string(rune('a'+i)))
			}

			var wg sync.WaitGroup
			unwires := make([]func(), adopts)
			for i := range units {
				wg.Add(1)
				go func() {
					defer wg.Done()
					unwires[i] = sub.startCentral(units[i])
				}()
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				sub.stop()
			}()
			wg.Wait()

			for _, unwire := range unwires {
				if unwire != nil {
					unwire()
				}
			}
		})
	}
}
