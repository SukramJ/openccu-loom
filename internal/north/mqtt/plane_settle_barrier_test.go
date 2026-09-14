// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/metrics"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// TestSettleOutlastsAWorkerStalledPastTheBrokerCall is the regression
// guard for finding **F12**, and it is the reason
// [SecurityMQTTPublisher.quiesce] exists in the production file rather
// than as a poll in this one.
//
// Finding **F5** made [observedPlane.settle] refuse to return before the
// plane had written something of its own, which closed the vacuous
// direction: a plane that had not started could no longer be mistaken for
// one that had finished. The trailing direction stayed open. A publish
// does not end when the broker call returns — [Bridge.publishRuntimeState]
// records the retained topic in the bridge's index and counts the message
// after it, on the same worker goroutine — and the worker publishes a
// whole reconcile one message at a time. So a worker descheduled past the
// broker call, or between two messages of one burst, is silent in exactly
// the way a finished worker is, and `settle`'s sixty milliseconds of quiet
// ends on the wrong side of it. That is what
// TestSecurityPlaneIsVisibleToTheBridge was seen failing as, once, under
// heavy load: a retained topic the recorder had already seen and the index
// had not reached yet.
//
// The stall is injected here rather than waited for. `afterPublish` holds
// the worker past the recorder for longer than the quiet window on EVERY
// message, which turns a one-in-many scheduling accident into the only
// possible interleaving: quiescence alone would end the wait after the
// plane's first write, every time. Dropping `p` from the `settle` call
// below is the mutation this test is pinned against — it then fails on
// every run.
func TestSettleOutlastsAWorkerStalledPastTheBrokerCall(t *testing.T) {
	t.Parallel()
	const base = "openccu-loom"

	reg := metrics.NewRegistry()
	col := metrics.NewMqttCollector(reg)
	obs := newObservedPlane()
	// Longer than `settle`'s quiet window, so the recorder looks finished
	// after every single write while the plane is still working.
	obs.afterPublish = func() { time.Sleep(80 * time.Millisecond) }

	bridge := NewBridge(BridgeConfig{
		Base: base, CentralName: "ccu-01",
		RawEnabled: true, HADiscoveryEnabled: true,
		Collector: col,
	}, obs)

	p := NewSecurityMQTTPublisher(staticSecuritySnapshot{roundTripSnapshot()},
		NewWiring(bridge, slog.Default()), "en", "", slog.Default())
	bus := events.NewBus()
	p.Start(bus)
	t.Cleanup(p.Stop)

	events.Publish(bus, hmevent.SecurityStateChangedEvent{Base: hmevent.NewBaseAt(time.Now())})
	obs.settle(t, p)

	// The whole reconcile has to be there, not its first message. The
	// per-class and per-zone states are enqueued last, so they are what a
	// wait that ends mid-burst loses.
	published := obs.publishedTopics()
	for _, want := range []string{
		base + "/security/state",
		base + "/security/alarm",
		base + "/security/problem",
		base + "/security/health",
	} {
		if !published[want] {
			t.Errorf("settle returned before %q was written; the plane was still publishing", want)
		}
	}
	tail := 0
	for topic := range published {
		if strings.HasPrefix(topic, base+"/security/class/") || strings.HasPrefix(topic, base+"/security/zone/") {
			tail++
		}
	}
	if tail == 0 {
		t.Error("settle returned before the reconcile reached its per-class and per-zone states, " +
			"which it enqueues last — the observed topic set is the head of a burst, not the plane")
	}

	// And every write the recorder saw has to have completed its
	// bridge-side bookkeeping, which happens after the broker call.
	var retained []string
	for _, rec := range obs.records() {
		if strings.HasPrefix(rec.topic, base+"/security/") && rec.retain && rec.payload != "" {
			retained = append(retained, rec.topic)
		}
	}
	if len(retained) == 0 {
		t.Fatal("the security plane wrote no retained topic — the fixture cannot show the bookkeeping")
	}
	bridge.mu.Lock()
	var missing []string
	for _, topic := range retained {
		if _, ok := bridge.rawTopics[topic]; !ok {
			missing = append(missing, topic)
		}
	}
	bridge.mu.Unlock()
	if len(missing) > 0 {
		t.Errorf("settle returned with %v recorded on the broker but absent from the bridge's "+
			"retained-topic index — the wait ended inside the window between the two", missing)
	}
	if got := col.MessagesSent("").Value(); got < uint64(len(retained)) {
		t.Errorf("messages_sent = %d, want at least %d — the counter is incremented after the "+
			"broker call, so the wait ended before the publish did", got, len(retained))
	}
}
