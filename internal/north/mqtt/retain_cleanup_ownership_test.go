// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// brokerClient models the two broker properties the discovery orphan sweep
// depends on, and nothing else:
//
//  1. a fresh subscription is served the retained store, with the retain
//     flag set;
//  2. a PUBLISH is fanned out to the matching subscriptions BEFORE the
//     publisher's own call returns.
//
// The second one is what the sweep gets wrong: the bridge records a config
// as declared only after Publish returns, so a config published while a
// sweep window is open reaches the sweep's handler as a topic nobody has
// declared, and the sweep retracts an entity the daemon is driving.
type brokerClient struct {
	mu       sync.Mutex
	retained map[string][]byte
	subs     map[string]MessageHandler

	// onSubscribe runs once, right after a subscription has been served
	// the retained store — i.e. inside the sweep's snapshot window. It is
	// how a test publishes "while the window is open" without a sleep.
	onSubscribe func()

	// holdTopic models a PUBLISH the broker has accepted and fanned out
	// but whose acknowledgement has not reached the publisher yet: its
	// Publish call blocks until hold is closed. That is the state every
	// in-flight discovery config is in while the sweep judges it.
	holdTopic string
	hold      chan struct{}
	// fannedOut is closed once holdTopic has been delivered to the
	// subscribers, so a test can wait for the delivery instead of sleeping.
	fannedOut chan struct{}

	published []publishedMsg
}

func newBrokerClient() *brokerClient {
	return &brokerClient{retained: map[string][]byte{}, subs: map[string]MessageHandler{}}
}

// seed puts a payload into the retained store as if a previous boot had
// published it.
func (c *brokerClient) seed(topic string, payload []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.retained[topic] = payload
}

func (c *brokerClient) Publish(_ context.Context, topic string, payload []byte, _ QoS, retain bool, _ ...PublishOption) error {
	c.mu.Lock()
	c.published = append(c.published, publishedMsg{topic: topic, payload: payload, retain: retain})
	if retain {
		if len(payload) == 0 {
			delete(c.retained, topic)
		} else {
			c.retained[topic] = payload
		}
	}
	handlers := make([]MessageHandler, 0, len(c.subs))
	for filter, h := range c.subs {
		if topicMatchesFilter(topic, filter) {
			handlers = append(handlers, h)
		}
	}
	c.mu.Unlock()
	// Fan out before returning — the broker has the message the moment it
	// accepts it, the publisher learns that only on the PUBACK.
	for _, h := range handlers {
		h(&Message{Topic: topic, Payload: payload})
	}
	if topic == c.holdTopic && len(payload) > 0 {
		close(c.fannedOut)
		<-c.hold
	}
	return nil
}

func (c *brokerClient) Subscribe(_ context.Context, filter string, _ QoS, handler MessageHandler, _ ...SubscribeOption) (SubscribeResult, error) {
	c.mu.Lock()
	replay := make(map[string][]byte, len(c.retained))
	for t, p := range c.retained {
		if topicMatchesFilter(t, filter) {
			replay[t] = p
		}
	}
	c.subs[filter] = handler
	hook := c.onSubscribe
	c.mu.Unlock()
	for topic, payload := range replay {
		handler(&Message{Topic: topic, Payload: payload, Retain: true})
	}
	if hook != nil {
		hook()
	}
	return SubscribeResult{}, nil
}

func (c *brokerClient) Unsubscribe(_ context.Context, filter string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.subs, filter)
	return nil
}

// retractions lists the topics cleared with an empty retained payload.
func (c *brokerClient) retractions() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for _, p := range c.published {
		if p.retain && len(p.payload) == 0 {
			out = append(out, p.topic)
		}
	}
	return out
}

func contains(list []string, want string) bool {
	for _, got := range list {
		if got == want {
			return true
		}
	}
	return false
}

// sysvarItem builds a real hub sysvar discovery item for centralName
// through the production builder, so the test publishes what the daemon
// publishes rather than a hand-written payload.
func sysvarItem(t *testing.T, b *Bridge, centralName, name string) DiscoveryItem {
	t.Helper()
	disco := b.DefaultBuilder()
	if disco == nil {
		t.Fatal("bridge has no default discovery builder")
	}
	disco.SetHubInfoFor(centralName, HubInfo{Name: centralName, Serial: "DEVCCU0001"})
	item := disco.BuildSysvarDiscovery(centralName, HubSysvarSpec{
		Name:      name,
		ValueType: hmenum.HubValueTypeLogic,
	})
	if !item.OK {
		t.Fatalf("BuildSysvarDiscovery(%q) produced no item", name)
	}
	return item
}

// TestDiscoveryOrphanSweepKeepsConfigsPublishedInsideItsWindow pins that a
// discovery config the daemon publishes while the sweep's snapshot window is
// open survives that sweep.
//
// The bridge records a topic as declared only once Publish returns, but the
// broker has already delivered it to the sweep's own `homeassistant/#`
// subscription by then. Judging the delivery against `declared` alone made
// the sweep retract entities it had announced seconds earlier — and because
// the hub pass runs once per boot, nothing re-announced them for the life of
// the daemon.
func TestDiscoveryOrphanSweepKeepsConfigsPublishedInsideItsWindow(t *testing.T) {
	t.Parallel()

	const centralName = "ccu"
	cl := newBrokerClient()
	b := NewBridge(BridgeConfig{
		Base:               "loom",
		HADiscoveryEnabled: true,
		CentralName:        centralName,
	}, cl)
	// The hub plane has finished its pass for this central; the deferral of
	// an undeclared plane is a separate property with its own test.
	b.MarkHubPlaneDeclared(centralName)

	// A real leftover from a previous build: retained, never re-published.
	orphan := "homeassistant/sensor/ccu_sysvars/retired/config"
	cl.seed(orphan, []byte(`{"unique_id":"loom_devccu0001_sysvar_retired"}`))

	live := sysvarItem(t, b, centralName, "living_room_light")
	liveTopic := b.Topics().DiscoveryConfig(live.Component, live.NodeID, live.ObjectID)
	// Publish it from inside the snapshot window — where the hub plane's
	// burst lands on a real boot — and keep the publish in flight for the
	// whole sweep. That is the state the defect lives in: the broker has
	// the config and has already handed it to the sweep's subscription,
	// while the publisher has not returned and so has recorded nothing.
	cl.holdTopic = liveTopic
	cl.hold = make(chan struct{})
	cl.fannedOut = make(chan struct{})
	publishDone := make(chan struct{})
	cl.onSubscribe = func() {
		go func() {
			defer close(publishDone)
			if err := b.PublishHubDiscovery(context.Background(), live); err != nil {
				t.Errorf("PublishHubDiscovery: %v", err)
			}
		}()
		<-cl.fannedOut
	}

	n, err := b.RunDiscoveryOrphanCleanupOnce(context.Background(), centralName, 50*time.Millisecond)
	close(cl.hold)
	<-publishDone
	if err != nil {
		t.Fatalf("RunDiscoveryOrphanCleanupOnce: %v", err)
	}

	retracted := cl.retractions()
	if contains(retracted, liveTopic) {
		t.Errorf("the sweep retracted %s, a config the daemon published inside the sweep window", liveTopic)
	}
	if !contains(retracted, orphan) {
		t.Errorf("the sweep left the orphan %s in place; retracted=%v", orphan, retracted)
	}
	if n != 1 {
		t.Errorf("evicted %d topics, want exactly the one orphan (retracted=%v)", n, retracted)
	}
}

// TestDiscoveryOrphanSweepDefersHubPlaneUntilItDeclared pins that the sweep
// leaves the hub plane's retained configs alone until the hub publisher
// reports its pass done, and sweeps them once it has.
//
// The hub plane publishes last: its entities are gated on the CCU serial the
// readiness-gated bring-up resolves, while the sweep is triggered by the
// device snapshot. A sweep that judges hub node ids before that pass deletes
// the previous boot's sysvars, programs and system sensors from Home
// Assistant moments before this boot re-announces them, taking the operator's
// entity registry entries with them.
func TestDiscoveryOrphanSweepDefersHubPlaneUntilItDeclared(t *testing.T) {
	t.Parallel()

	const centralName = "ccu"
	hubTopic := "homeassistant/sensor/ccu_sysvars/from_last_boot/config"
	deviceTopic := "homeassistant/sensor/ccu_vcu0000001/1_temperature/config"

	cl := newBrokerClient()
	cl.seed(hubTopic, []byte(`{"unique_id":"loom_devccu0001_sysvar_from_last_boot"}`))
	cl.seed(deviceTopic, []byte(`{"unique_id":"loom_devccu0001_vcu0000001_1_temperature"}`))
	b := NewBridge(BridgeConfig{
		Base:               "loom",
		HADiscoveryEnabled: true,
		CentralName:        centralName,
	}, cl)

	if _, err := b.RunDiscoveryOrphanCleanupOnce(context.Background(), centralName, 50*time.Millisecond); err != nil {
		t.Fatalf("RunDiscoveryOrphanCleanupOnce: %v", err)
	}
	retracted := cl.retractions()
	if contains(retracted, hubTopic) {
		t.Errorf("the sweep retracted %s before the hub plane declared", hubTopic)
	}
	if !contains(retracted, deviceTopic) {
		t.Errorf("deferring the hub plane also stopped the device plane from being swept; retracted=%v", retracted)
	}

	// Once the hub publisher reports its pass complete the same leftover is
	// a genuine orphan again — the deferral must not disable the sweep.
	b.MarkHubPlaneDeclared(centralName)
	n, err := b.RunDiscoveryOrphanCleanupOnce(context.Background(), centralName, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("RunDiscoveryOrphanCleanupOnce (declared): %v", err)
	}
	if n != 1 || !contains(cl.retractions(), hubTopic) {
		t.Errorf("after MarkHubPlaneDeclared the sweep evicted %d topics and retracted %v, want the hub leftover %s",
			n, cl.retractions(), hubTopic)
	}
}
