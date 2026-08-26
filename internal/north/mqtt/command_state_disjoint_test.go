// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// echoClient implements [Client] the way a real broker behaves toward
// the daemon's own connection: every Publish is routed straight back to
// each matching subscription with Retain=false. The retain bit is only
// set on the stored replay a broker delivers when a subscription is
// (re)established — live routing to an already-established subscription
// carries Retain=0 (MQTT 3.1.1 §3.3.1.3 / MQTT 5.0 §3.3.1.3). The
// daemon subscribes without the MQTT 5.0 No-Local option, so it
// receives its own publishes; this double reproduces that loop, which
// the record-only [NoopClient] cannot.
type echoClient struct {
	mu        sync.Mutex
	subs      map[string]MessageHandler
	published []Publication
}

func newEchoClient() *echoClient {
	return &echoClient{subs: make(map[string]MessageHandler)}
}

func (c *echoClient) Publish(_ context.Context, topic string, payload []byte, qos QoS, retain bool, _ ...PublishOption) error {
	c.mu.Lock()
	c.published = append(c.published, Publication{
		Topic: topic, Payload: append([]byte(nil), payload...), QoS: qos, Retain: retain,
	})
	type match struct{ handler MessageHandler }
	var matches []match
	for filter, h := range c.subs {
		if h != nil && mqttFilterMatches(filter, topic) {
			matches = append(matches, match{handler: h})
		}
	}
	c.mu.Unlock()
	for _, m := range matches {
		m.handler(&Message{Topic: topic, Payload: payload, Retain: false})
	}
	return nil
}

func (c *echoClient) Subscribe(_ context.Context, filter string, _ QoS, handler MessageHandler, _ ...SubscribeOption) (SubscribeResult, error) {
	c.mu.Lock()
	c.subs[filter] = handler
	c.mu.Unlock()
	return SubscribeResult{}, nil
}

func (c *echoClient) Unsubscribe(_ context.Context, filter string) error {
	c.mu.Lock()
	delete(c.subs, filter)
	c.mu.Unlock()
	return nil
}

// Published returns a copy of every recorded publication.
func (c *echoClient) Published() []Publication {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Publication, len(c.published))
	copy(out, c.published)
	return out
}

// Filters returns a snapshot of every active subscription filter.
func (c *echoClient) Filters() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, 0, len(c.subs))
	for f := range c.subs {
		out = append(out, f)
	}
	return out
}

// mqttFilterMatches implements MQTT topic-filter matching: `+` matches
// exactly one level, `#` matches the remainder (including zero levels).
func mqttFilterMatches(filter, topic string) bool {
	fp := strings.Split(filter, "/")
	tp := strings.Split(topic, "/")
	for i, f := range fp {
		if f == "#" {
			return true
		}
		if i >= len(tp) {
			return false
		}
		if f != "+" && f != tp[i] {
			return false
		}
	}
	return len(fp) == len(tp)
}

// A program's state publish must never come back through the daemon's
// own trigger-command subscription. The state mirror onto the
// `…/hub/programs/<id>/trigger` command topic did exactly that: the
// broker echoed the daemon's own publish into handleProgram with
// Retain=false (live routing), which ran `Program.execute` on the CCU —
// every program, including deactivated ones, on every boot, on every
// hub re-publish, and once for each freshly discovered program.
func TestProgramStatePublishMustNotEchoAsTriggerCommand(t *testing.T) {
	client := newEchoClient()
	sink := &fakeSink{}
	sub := NewCommandSubscriber(client, NewTopicBuilder("openccu-loom"), sink, nil)
	if err := sub.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	bridge := NewBridge(BridgeConfig{Base: "openccu-loom", CentralName: "ccu-01", RawEnabled: true}, client)

	prog := hub.NewProgram("ccu-01", "12459", "A_test_Alarm_SV", "", false, nil)
	if err := bridge.PublishProgram(context.Background(), "ccu-01", prog, true); err != nil {
		t.Fatalf("publish program: %v", err)
	}
	sub.WaitIdle()

	// Vacuity guard: the state topic must actually have been written —
	// otherwise a topology change could turn this test into a no-op.
	stateSeen := false
	for _, p := range client.Published() {
		if p.Topic == "openccu-loom/ccu-01/hub/programs/12459/state" {
			stateSeen = true
		}
	}
	if !stateSeen {
		t.Fatal("program state topic was never published — publish topology changed, test would be vacuous")
	}

	if n := sink.triggers.Load(); n != 0 {
		t.Fatalf("publishing program state executed the program %d time(s) via the daemon's own trigger subscription", n)
	}
	if n := sink.programEnables.Load(); n != 0 {
		t.Fatalf("publishing program state toggled the program's active flag %d time(s) via the daemon's own set subscription", n)
	}
}

// Every topic the hub state plane publishes must be disjoint from every
// command-topic filter the daemon subscribes: a broker delivers the
// daemon's own publishes back to it (no No-Local), so any overlap turns
// a state publish into a self-inflicted CCU write.
func TestHubStatePublishesAreDisjointFromCommandSubscriptions(t *testing.T) {
	client := newEchoClient()
	sink := &fakeSink{}
	sub := NewCommandSubscriber(client, NewTopicBuilder("openccu-loom"), sink, nil)
	if err := sub.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	filters := client.Filters()
	if len(filters) == 0 {
		t.Fatal("no command subscriptions registered — sweep would be vacuous")
	}
	bridge := NewBridge(BridgeConfig{Base: "openccu-loom", CentralName: "ccu-01", RawEnabled: true}, client)
	ctx := context.Background()

	prog := hub.NewProgram("ccu-01", "12459", "A_test_Alarm_SV", "", false, nil)
	if err := bridge.PublishProgram(ctx, "ccu-01", prog, true); err != nil {
		t.Fatalf("publish program: %v", err)
	}
	roles := bridge.ProgramRoles("ccu-01", prog)
	for i := range roles {
		if err := bridge.PublishRoleAvailability(ctx, &roles[i], true); err != nil {
			t.Fatalf("publish role availability: %v", err)
		}
	}
	sv := hub.NewSysvar("ccu-01", "PartyMode", "", hmenum.HubValueTypeLogic, nil)
	if err := bridge.PublishSysvar(ctx, "ccu-01", sv, true); err != nil {
		t.Fatalf("publish sysvar: %v", err)
	}
	if err := bridge.PublishInstallMode(ctx, "ccu-01", "HmIP-RF", 42); err != nil {
		t.Fatalf("publish install mode: %v", err)
	}
	sub.WaitIdle()

	published := client.Published()
	if len(published) == 0 {
		t.Fatal("nothing published — sweep would be vacuous")
	}
	for _, p := range published {
		for _, f := range filters {
			if mqttFilterMatches(f, p.Topic) {
				t.Errorf("state-plane publish %q matches the daemon's own command subscription %q", p.Topic, f)
			}
		}
	}
}
