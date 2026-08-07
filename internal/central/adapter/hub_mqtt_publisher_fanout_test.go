// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// gatedPublisher is an mqtt.Publisher that blocks in Publish for every topic
// containing blockOn until it is released (or the context is cancelled), and
// records everything it accepts. It stands in for a broker that has gone
// half-open on one topic family while the rest of the daemon keeps running.
type gatedPublisher struct {
	blockOn string
	release chan struct{}

	mu      sync.Mutex
	topics  []string
	entered chan struct{}
	once    sync.Once
}

func newGatedPublisher(blockOn string) *gatedPublisher {
	return &gatedPublisher{
		blockOn: blockOn,
		release: make(chan struct{}),
		entered: make(chan struct{}),
	}
}

func (g *gatedPublisher) Publish(ctx context.Context, topic string, _ []byte, _ mqtt.QoS, _ bool, _ ...mqtt.PublishOption) error {
	if g.blockOn != "" && strings.Contains(topic, g.blockOn) {
		g.once.Do(func() { close(g.entered) })
		select {
		case <-g.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	g.mu.Lock()
	g.topics = append(g.topics, topic)
	g.mu.Unlock()
	return nil
}

// accepted returns every topic the publisher let through, oldest first.
func (g *gatedPublisher) accepted() []string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]string(nil), g.topics...)
}

// hubFanoutFixture builds a single central wired to pub, plus a ready
// HubMQTTPublisher. The CCU serial is stamped so the serial-gated hub
// discovery plane is actually exercised.
func hubFanoutFixture(t *testing.T, pub mqtt.Publisher) (*central.Unit, *HubMQTTPublisher) {
	t.Helper()
	c, err := central.New(central.Config{Name: "ccu-01"})
	if err != nil {
		t.Fatalf("central.New: %v", err)
	}
	c.SetSystemInformation(central.SystemInfo{Serial: "3014F711A0001F5A4993D962"})
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("reg.Register: %v", err)
	}
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{
		Base:               "openccu-loom",
		CentralName:        "ccu-01",
		RawEnabled:         true,
		HADiscoveryEnabled: true,
	}, pub)
	return c, NewHubMQTTPublisher(reg, mqtt.NewWiring(bridge, nil), nil)
}

func connectivityEvent(iface string, reachable bool) hmevent.ConnectivityChangedEvent {
	return hmevent.ConnectivityChangedEvent{
		Base:        hmevent.NewBase(),
		CentralName: "ccu-01",
		InterfaceID: iface,
		Reachable:   reachable,
	}
}

// TestHubMQTTPublisherSlowBrokerDoesNotStallBusDispatch is the core decoupling
// test for the hub plane: while the publisher's worker hangs in a connectivity
// publish the broker never answers, the goroutine that dispatched that event
// must already have returned. In production that goroutine is the event bus's
// single dispatch goroutine, shared by every central, so a handler that blocks
// in broker I/O freezes event delivery daemon-wide.
//
// Run the publish handler inline again and this test fails: the dispatching
// goroutine sits in the broker until the release, and the assertion below trips
// on its deadline. It is deliberately NOT written as "a second Publish still
// returns" — a concurrent Publish is queued on the bus's deferred list and
// returns either way, so that phrasing would stay green with the fix removed.
func TestHubMQTTPublisherSlowBrokerDoesNotStallBusDispatch(t *testing.T) {
	t.Parallel()
	// The connectivity STATE topic; the discovery config rides the
	// `homeassistant/` prefix and stays unblocked.
	gate := newGatedPublisher("/hub/connectivity/")
	c, publisher := hubFanoutFixture(t, gate)

	publisher.Start(context.Background())
	defer publisher.Stop()
	// Registered second, so it runs FIRST on teardown: releasing the broker
	// before Stop keeps the teardown independent of which goroutine is blocked
	// where, so a regression surfaces as the assertion below rather than as a
	// hung test.
	defer close(gate.release)

	dispatched := make(chan struct{})
	go func() {
		events.Publish(c.EventBus, connectivityEvent("HmIP-RF", true))
		close(dispatched)
	}()

	// The publish really does reach the broker and really does hang there.
	select {
	case <-gate.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("fan-out worker never reached the connectivity publish")
	}

	// And the dispatching goroutine is not the one hanging.
	select {
	case <-dispatched:
	case <-time.After(2 * time.Second):
		t.Fatal("bus dispatch stalled inside the hub publisher's blocked broker publish")
	}

	// A follow-up event on the same bus keeps flowing too.
	done := make(chan struct{})
	go func() {
		events.Publish(c.EventBus, connectivityEvent("BidCos-RF", true))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("follow-up bus dispatch stalled behind the blocked broker publish")
	}
}

// TestHubMQTTPublisherPreservesPublishOrder pins the ordering guarantee the
// single worker exists to provide: a sysvar's discovery config precedes its
// first state, and a burst of value changes reaches the broker in the order the
// model produced them. A per-event goroutine would satisfy neither.
func TestHubMQTTPublisherPreservesPublishOrder(t *testing.T) {
	t.Parallel()
	gate := newGatedPublisher("")
	c, publisher := hubFanoutFixture(t, gate)

	sv := &hub.Sysvar{
		HubDataPoint: hub.HubDataPoint{Name: "Counter"},
		ValueType:    hmenum.HubValueTypeInteger,
	}
	sv.OnValue(hmtypes.IntValue(0))
	c.HubModel.PutSysvar(sv)

	publisher.Start(context.Background())
	defer publisher.Stop()

	const updates = 100
	for i := 1; i <= updates; i++ {
		sv.OnValue(hmtypes.IntValue(i))
	}
	publisher.Flush()

	var (
		discoveryAt = -1
		stateSeq    []string
	)
	for i, topic := range gate.accepted() {
		switch {
		case strings.HasPrefix(topic, "homeassistant/") && strings.Contains(topic, "sysvars"):
			if discoveryAt < 0 {
				discoveryAt = i
			}
		case strings.Contains(topic, "/hub/sysvars/Counter/state"):
			stateSeq = append(stateSeq, topic)
		}
	}
	if discoveryAt < 0 {
		t.Fatalf("no sysvar discovery published; topics=%v", gate.accepted())
	}
	if len(stateSeq) == 0 {
		t.Fatalf("no sysvar state published; topics=%v", gate.accepted())
	}
	// Discovery must precede every state publish of the same entity.
	firstStateAt := -1
	for i, topic := range gate.accepted() {
		if strings.Contains(topic, "/hub/sysvars/Counter/state") {
			firstStateAt = i
			break
		}
	}
	if discoveryAt > firstStateAt {
		t.Fatalf("sysvar state published before its discovery (discovery at %d, state at %d)", discoveryAt, firstStateAt)
	}
}

// TestHubMQTTPublisherPreservesPayloadOrder asserts the payload sequence, not
// just the topic sequence: the retained value that survives is the last one the
// model produced, and every intermediate value arrives in order.
func TestHubMQTTPublisherPreservesPayloadOrder(t *testing.T) {
	t.Parallel()
	pub := mqtt.NewNoopClient()
	c, publisher := hubFanoutFixture(t, pub)

	sv := &hub.Sysvar{
		HubDataPoint: hub.HubDataPoint{Name: "Counter"},
		ValueType:    hmenum.HubValueTypeInteger,
	}
	sv.OnValue(hmtypes.IntValue(0))
	c.HubModel.PutSysvar(sv)

	publisher.Start(context.Background())
	defer publisher.Stop()
	publisher.Flush()

	const updates = 100
	for i := 1; i <= updates; i++ {
		sv.OnValue(hmtypes.IntValue(i))
	}
	publisher.Flush()

	var got []int
	for _, p := range pub.Published() {
		if !strings.Contains(p.Topic, "/hub/sysvars/Counter/state") {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(string(p.Payload)))
		if err != nil {
			continue
		}
		got = append(got, n)
	}
	if len(got) < updates {
		t.Fatalf("published %d sysvar states, want at least %d", len(got), updates)
	}
	for i := 1; i < len(got); i++ {
		if got[i] < got[i-1] {
			t.Fatalf("out-of-order sysvar state at index %d: %d after %d (full=%v)", i, got[i], got[i-1], got)
		}
	}
	if last := got[len(got)-1]; last != updates {
		t.Fatalf("retained sysvar state is %d, want the newest value %d", last, updates)
	}
}

// TestHubMQTTPublisherStopCancelsInflightPublish verifies the lifecycle
// contract: Stop returns even while the worker is blocked in a broker publish,
// because it cancels the context that publish runs under. Without it a daemon
// shutdown would hang behind a half-open broker.
func TestHubMQTTPublisherStopCancelsInflightPublish(t *testing.T) {
	t.Parallel()
	gate := newGatedPublisher("/hub/connectivity/")
	c, publisher := hubFanoutFixture(t, gate)

	publisher.Start(context.Background())
	go events.Publish(c.EventBus, connectivityEvent("HmIP-RF", true))
	select {
	case <-gate.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("fan-out worker never reached the connectivity publish")
	}

	stopped := make(chan struct{})
	go func() {
		publisher.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop hung behind an in-flight broker publish")
	}
}

// TestHubMQTTPublisherStopLeavesNoGoroutine pins that no worker outlives the
// teardown, across repeated Start/Stop cycles (the on-connect hook re-Starts
// the publisher on every broker reconnect, so a worker leaked per cycle would
// accumulate for the daemon's lifetime).
//
// It joins each cycle's own worker rather than sampling the process-wide
// goroutine count, which the package's parallel tests would make meaningless.
func TestHubMQTTPublisherStopLeavesNoGoroutine(t *testing.T) {
	t.Parallel()
	pub := mqtt.NewNoopClient()
	c, publisher := hubFanoutFixture(t, pub)

	sv := &hub.Sysvar{HubDataPoint: hub.HubDataPoint{Name: "Anwesenheit"}, ValueType: hmenum.HubValueTypeLogic}
	sv.OnValue(hmtypes.BoolValue(true))
	c.HubModel.PutSysvar(sv)

	for cycle := range 5 {
		publisher.Start(context.Background())
		worker := publisher.fanout.Load()
		if worker == nil {
			t.Fatalf("cycle %d: Start did not bring up a fan-out worker", cycle)
		}
		sv.OnValue(hmtypes.BoolValue(false))
		publisher.Flush()
		publisher.Stop()

		joined := make(chan struct{})
		go func() {
			worker.wg.Wait()
			close(joined)
		}()
		select {
		case <-joined:
		case <-time.After(2 * time.Second):
			t.Fatalf("cycle %d: fan-out worker still running after Stop", cycle)
		}
		if worker.ctx.Err() == nil {
			t.Fatalf("cycle %d: Stop left the worker context live, so an in-flight publish would not abort", cycle)
		}
	}
	if publisher.fanout.Load() != nil {
		t.Fatal("Stop left a fan-out worker installed")
	}
}

// TestHubMQTTPublisherConnectivityDedupIsWorkerOwned drives connectivity events
// for several interfaces from several goroutines at once. The discovery-dedup
// map behind that path has no lock of its own — it is safe only because every
// access happens inside a queued job on the single worker. Run under -race this
// fails the moment that ownership is broken; it also asserts the dedup still
// announces each interface exactly once.
func TestHubMQTTPublisherConnectivityDedupIsWorkerOwned(t *testing.T) {
	t.Parallel()
	pub := mqtt.NewNoopClient()
	c, publisher := hubFanoutFixture(t, pub)

	publisher.Start(context.Background())
	defer publisher.Stop()

	ifaces := []string{"HmIP-RF", "BidCos-RF", "BidCos-Wired", "CUxD"}
	var wg sync.WaitGroup
	for _, iface := range ifaces {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range 25 {
				events.Publish(c.EventBus, connectivityEvent(iface, i%2 == 0))
			}
		}()
	}
	wg.Wait()
	publisher.Flush()

	discoveryPerIface := map[string]int{}
	for _, p := range pub.Published() {
		if !strings.HasSuffix(p.Topic, "/config") || !strings.Contains(p.Topic, "connectivity") {
			continue
		}
		discoveryPerIface[p.Topic]++
	}
	if len(discoveryPerIface) != len(ifaces) {
		t.Fatalf("connectivity discovery topics: got %d distinct, want %d (%v)",
			len(discoveryPerIface), len(ifaces), discoveryPerIface)
	}
	for topic, n := range discoveryPerIface {
		if n != 1 {
			t.Fatalf("connectivity discovery %q published %d times, want 1 (dedup map lost)", topic, n)
		}
	}
}
