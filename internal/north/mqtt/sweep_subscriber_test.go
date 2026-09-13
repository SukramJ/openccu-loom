// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

// sweepConnector is the connect/disconnect half a [SweepSubscriber] drives,
// counting both so a test can tell "opened once and closed" from "left
// standing".
type sweepConnector struct {
	mu         sync.Mutex
	connects   int
	disconnect int
	connectErr error
}

func (c *sweepConnector) Connect(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.connects++
	return c.connectErr
}

func (c *sweepConnector) Disconnect(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.disconnect++
	return nil
}

func (c *sweepConnector) counts() (connects, disconnects int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connects, c.disconnect
}

// replayingClient models the one go-mqtt behaviour finding 4 turns on:
// `TCPClient.Unsubscribe` returns early on `ErrNotConnected` or an ack
// timeout WITHOUT removing the local registration (publish.go:313-335), and
// `replaySubscriptions` (adapter_tcp.go:505,670) then re-installs every
// registration on every reconnect. A filter whose teardown failed once is
// therefore on the wire for the rest of the process.
type replayingClient struct {
	*fanoutClient
	unsubErr error
}

func newReplayingClient(unsubErr error) *replayingClient {
	return &replayingClient{fanoutClient: newFanoutClient(), unsubErr: unsubErr}
}

func (c *replayingClient) Unsubscribe(ctx context.Context, filter string) error {
	if c.unsubErr != nil {
		// Registration kept, exactly as go-mqtt keeps it.
		return c.unsubErr
	}
	return c.fanoutClient.Unsubscribe(ctx, filter)
}

// TestSweepsDoNotDoubleInboundCommands is the pin on finding 3, and the
// reason [SweepSubscriber] exists.
//
// The defect: the retain-cleanup sweep installed `<base>/#` on the SAME
// client the command plane subscribes on. A broker sends one copy of a
// PUBLISH per matching subscription and go-mqtt re-matches each copy against
// its whole local filter list, so for the length of the sweep window every
// inbound command invoked its handler twice — a doubled `PRESS_SHORT`, a
// doubled program trigger, a doubled alarm arm, with nothing in any log.
//
// The assertion is the handler call count, not the wiring, because the wiring
// is only interesting through its effect: [fanoutClient] reproduces BOTH
// multiplications, so pointing the sweep back at the command client makes
// this count 2.
func TestSweepsDoNotDoubleInboundCommands(t *testing.T) {
	t.Parallel()

	const (
		base  = "gh"
		topic = base + "/ccu-01/HmIP-RF/0001ABCD/1/values/PRESS_SHORT/set"
	)

	cmdClient := newFanoutClient()
	sweepClient := newFanoutClient()
	conn := &sweepConnector{}
	sweep := NewSweepSubscriber(func() (Client, Connector) { return sweepClient, conn }, nil)

	// The production wiring, and the reason it is spelled out here: the
	// bridge keeps the sweep connection in a field of its own, separate from
	// the long-lived subscribe client, and it is that field the sweeps
	// resolve. Handing `sweep` to WithSubscriber instead would put the sweep
	// on its own connection by a route production does not take — the
	// fallback for a wiring with no broker — and leave `sweepSub` nil, so
	// nothing below would exercise the field this whole separation lives in.
	bridge := NewBridge(BridgeConfig{
		Base: base, CentralName: "ccu-01", RawEnabled: true,
	}, cmdClient).WithSubscriber(cmdClient).WithSweepSubscriber(sweep)

	sink := &fakeSink{}
	cs := NewCommandSubscriber(cmdClient, NewTopicBuilder(base), sink, nil)
	if err := cs.Start(context.Background()); err != nil {
		t.Fatalf("command subscriber start: %v", err)
	}
	t.Cleanup(cs.Close)

	// Open a sweep window and hold it while the command arrives — the window
	// IS the exposure, so a command delivered outside it proves nothing.
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = bridge.RunRetainCleanupOnce(context.Background(), 400*time.Millisecond)
	}()
	// Polled across BOTH clients on purpose: which one the sweep lands on is
	// the thing under test, so a wait that looked only at the sweep client
	// would time out instead of reporting the doubling.
	waitFor(t, "the sweep window to open", func() bool {
		return hasWildcardFilter(sweepClient) || hasWildcardFilter(cmdClient)
	})

	if got := cmdClient.deliver(topic, []byte("true"), false); got != 1 {
		t.Fatalf("the broker matched %d subscriptions on the command client, want 1 — the sweep "+
			"filter is still riding the command plane's connection", got)
	}
	cs.WaitIdle()
	<-done

	if got := sink.setValues.Load(); got != 1 {
		t.Fatalf("one inbound command ran the handler %d times, want 1 — for a PRESS_SHORT, a "+
			"program trigger or an alarm arm that is a second physical action with nothing in "+
			"any log", got)
	}
	if connects, _ := conn.counts(); connects != 1 {
		t.Fatalf("the sweep connected %d times, want 1", connects)
	}
}

// TestSweepConnectionClosesWhenTheWindowDoes pins the other half of the
// separate connection: it exists only while a sweep does, so the daemon pays
// for a second broker session for the length of a window rather than for the
// life of the process.
func TestSweepConnectionClosesWhenTheWindowDoes(t *testing.T) {
	t.Parallel()

	sweepClient := newFanoutClient()
	conn := &sweepConnector{}
	sweep := NewSweepSubscriber(func() (Client, Connector) { return sweepClient, conn }, nil)
	bridge := NewBridge(BridgeConfig{
		Base: "gh", CentralName: "ccu-01", RawEnabled: true,
	}, newFanoutClient()).WithSubscriber(sweep)

	if _, err := bridge.RunRetainCleanupOnce(context.Background(), 20*time.Millisecond); err != nil {
		t.Fatalf("retain cleanup: %v", err)
	}
	if sweep.connected() {
		t.Fatal("the sweep connection is still open after the window closed — a second broker " +
			"session standing for the life of the process")
	}
	connects, disconnects := conn.counts()
	if connects != 1 || disconnects != 1 {
		t.Fatalf("connects=%d disconnects=%d, want 1/1", connects, disconnects)
	}
	if got := len(sweepClient.Filters()); got != 0 {
		t.Fatalf("the sweep left %d filters installed, want 0", got)
	}
}

// TestAFailedUnsubscribeDropsTheSweepConnection is the pin on finding 4.
//
// go-mqtt returns early from `Unsubscribe` on `ErrNotConnected` or an ack
// timeout without removing the local registration, and replays every
// registration on every reconnect — so a sweep whose teardown failed once
// leaves `<base>/#` on the wire forever, with a single warn line nothing
// reacts to. Boot, when the 2 s window runs, is exactly when links are flaky.
//
// The fix is structural rather than a retry: the sweep connection is dropped
// and the NEXT sweep builds a fresh client, so the stranded filter dies with
// the session it was stranded on.
func TestAFailedUnsubscribeDropsTheSweepConnection(t *testing.T) {
	t.Parallel()

	var (
		mu      sync.Mutex
		opened  []*replayingClient
		unsubOK bool
	)
	conn := &sweepConnector{}
	sweep := NewSweepSubscriber(func() (Client, Connector) {
		mu.Lock()
		defer mu.Unlock()
		var c *replayingClient
		if unsubOK {
			c = newReplayingClient(nil)
		} else {
			c = newReplayingClient(errors.New("mqtt: not connected"))
		}
		opened = append(opened, c)
		return c, conn
	}, nil)
	bridge := NewBridge(BridgeConfig{
		Base: "gh", CentralName: "ccu-01", RawEnabled: true,
	}, newFanoutClient()).WithSubscriber(sweep)

	// First sweep: the broker refuses the UNSUBSCRIBE.
	if _, err := bridge.RunRetainCleanupOnce(context.Background(), 20*time.Millisecond); err != nil {
		t.Fatalf("first retain cleanup: %v", err)
	}
	if sweep.connected() {
		t.Fatal("the sweep connection survived a refused UNSUBSCRIBE — go-mqtt replays the " +
			"registration on every reconnect, so `<base>/#` would stay on the wire for the " +
			"rest of the process with one warn line and nothing reacting to it")
	}

	// Second sweep: a brand-new client, not the poisoned one.
	mu.Lock()
	unsubOK = true
	mu.Unlock()
	if _, err := bridge.RunRetainCleanupOnce(context.Background(), 20*time.Millisecond); err != nil {
		t.Fatalf("second retain cleanup: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(opened) != 2 {
		t.Fatalf("the sweep opened %d clients, want 2 — the second sweep reused the client whose "+
			"teardown failed", len(opened))
	}
	if got := len(opened[0].Filters()); got == 0 {
		t.Fatal("the fake dropped the filter on a failed UNSUBSCRIBE, so it does not model " +
			"go-mqtt and this test cannot see the defect")
	}
	if got := len(opened[1].Filters()); got != 0 {
		t.Fatalf("the second sweep's client holds %d filters after its window, want 0", got)
	}
}

// hasWildcardFilter reports whether c holds a `#` sweep filter.
func hasWildcardFilter(c *fanoutClient) bool {
	for _, f := range c.Filters() {
		if strings.HasSuffix(f, "/#") {
			return true
		}
	}
	return false
}

// TestAFailedUnsubscribeDropsAConnectionOtherSweepsStillHold covers the case
// the reference count alone does not: a teardown that fails while a second
// sweep still holds a subscription on the same connection.
//
// Dropping at zero is what normally retires a stranded filter, but a filter
// whose UNSUBSCRIBE was refused is live NOW, and waiting for an unrelated
// sweep to finish leaves it delivering into an inert collector in the
// meantime. The connection goes either way.
func TestAFailedUnsubscribeDropsAConnectionOtherSweepsStillHold(t *testing.T) {
	t.Parallel()

	client := newReplayingClient(errors.New("mqtt: not connected"))
	conn := &sweepConnector{}
	sweep := NewSweepSubscriber(func() (Client, Connector) { return client, conn }, nil)

	ctx := context.Background()
	for _, filter := range []string{"gh/#", "homeassistant/#"} {
		if _, err := sweep.Subscribe(ctx, filter, QoS0, func(*Message) {}); err != nil {
			t.Fatalf("subscribe %s: %v", filter, err)
		}
	}
	if err := sweep.Unsubscribe(ctx, "gh/#"); err == nil {
		t.Fatal("the fake accepted the UNSUBSCRIBE, so this test cannot see the defect")
	}
	if sweep.connected() {
		t.Fatal("a refused UNSUBSCRIBE left the sweep connection standing while a second sweep " +
			"held it — the filter go-mqtt could not remove keeps delivering, and every reconnect " +
			"replays it")
	}
}

// TestTheBirthWatchDoesNotRideTheSweepConnection pins the boundary between
// the two subscribe clients.
//
// Everything that subscribes through the bridge used to resolve one client,
// so moving the sweeps onto their own connection would have taken the Home
// Assistant birth watch with them — and that connection is torn down when a
// window closes and dropped outright when an UNSUBSCRIBE fails. The birth
// watch would have ended silently, and Home Assistant restarts would stop
// replaying discovery: the exact class of defect that is invisible until
// somebody restarts HA and half their entities do not come back.
func TestTheBirthWatchDoesNotRideTheSweepConnection(t *testing.T) {
	t.Parallel()

	longLived := newFanoutClient()
	sweepClient := newFanoutClient()
	conn := &sweepConnector{}
	sweep := NewSweepSubscriber(func() (Client, Connector) { return sweepClient, conn }, nil)
	bridge := NewBridge(BridgeConfig{
		Base: "gh", CentralName: "ccu-01", RawEnabled: true, HADiscoveryEnabled: true,
	}, newFanoutClient()).WithSubscriber(longLived).WithSweepSubscriber(sweep)

	if err := NewBirthSync(longLived, bridge, nil).Start(context.Background()); err != nil {
		t.Fatalf("birth sync start: %v", err)
	}
	if got := sweepClient.Filters(); len(got) != 0 {
		t.Fatalf("the birth watch landed on the sweep connection (%v) — that connection closes "+
			"with every sweep window, so the watch would end silently and Home Assistant "+
			"restarts would stop replaying discovery", got)
	}
	birth := longLived.Filters()
	if len(birth) != 1 || birth[0] != HABirthTopic {
		t.Fatalf("the long-lived client holds %v, want just %q", birth, HABirthTopic)
	}

	// And the sweeps still go the other way.
	if _, err := bridge.RunRetainCleanupOnce(context.Background(), 20*time.Millisecond); err != nil {
		t.Fatalf("retain cleanup: %v", err)
	}
	if connects, _ := conn.counts(); connects != 1 {
		t.Fatalf("the sweep opened %d connections of its own, want 1", connects)
	}
	if got := longLived.Filters(); len(got) != 1 {
		t.Fatalf("the sweep filter landed on the long-lived client too: %v", got)
	}
}

// waitFor polls cond until it holds, failing the test if it never does.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
