// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package events

import (
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// ---------- local test-only event types ----------

// keyedEvent is a minimal hmevent.Event whose EventKey() returns the
// Key field. We define it here rather than adding production code so
// the key-filter tests are self-contained.
type keyedEvent struct {
	hmevent.Base
	Key string
}

func (keyedEvent) Type() hmevent.EventType { return "test.keyed_event" }
func (e keyedEvent) EventKey() string      { return e.Key }

// ---------- 1. Priority ordering (Low / Normal / High + custom) ----------

// TestPriorityHandlersFireInOrder verifies that handlers registered with
// different priorities always fire highest-first, regardless of
// registration order. We also check that a custom numeric priority
// (between Normal and High) slots in the right place.
func TestPriorityHandlersFireInOrder(t *testing.T) {
	t.Parallel()

	b := NewBus()
	var order []string

	// Register in "wrong" order to prove sorting is not registration-order.
	Subscribe(b, func(hmevent.DeviceCreatedEvent) { order = append(order, "low") }, WithPriority(PriorityLow))
	Subscribe(b, func(hmevent.DeviceCreatedEvent) { order = append(order, "high") }, WithPriority(PriorityHigh))
	Subscribe(b, func(hmevent.DeviceCreatedEvent) { order = append(order, "normal") }, WithPriority(PriorityNormal))
	// Custom priority between Normal(0) and High(10).
	Subscribe(b, func(hmevent.DeviceCreatedEvent) { order = append(order, "custom5") }, WithPriority(5))

	Publish(b, hmevent.DeviceCreatedEvent{Base: hmevent.NewBase(), CentralName: "c"})

	want := []string{"high", "custom5", "normal", "low"}
	if !slices.Equal(order, want) {
		t.Fatalf("fire order=%v, want %v", order, want)
	}
}

// TestPriorityEqualRegistrationFIFO confirms that within the same
// priority level, handlers fire in registration order (FIFO).
func TestPriorityEqualRegistrationFIFO(t *testing.T) {
	t.Parallel()

	b := NewBus()
	var order []string

	for _, name := range []string{"a", "b", "c"} {
		n := name
		Subscribe(b, func(hmevent.DeviceCreatedEvent) { order = append(order, n) }, WithPriority(PriorityHigh))
	}

	Publish(b, hmevent.DeviceCreatedEvent{Base: hmevent.NewBase(), CentralName: "c"})

	if !slices.Equal(order, []string{"a", "b", "c"}) {
		t.Fatalf("FIFO broken: %v", order)
	}
}

// ---------- 2. Key filtering ----------

// TestKeyFilterMatchesOnly checks that WithKey("k1") makes a handler fire
// only when the event's EventKey() returns "k1", while a keyless handler
// always fires.
func TestKeyFilterMatchesOnly(t *testing.T) {
	t.Parallel()

	b := NewBus()

	var keylessCount, filteredCount int

	Subscribe(b, func(keyedEvent) { keylessCount++ })
	Subscribe(b, func(keyedEvent) { filteredCount++ }, WithKey("k1"))

	// Publish event whose key matches the filter.
	Publish(b, keyedEvent{Base: hmevent.NewBase(), Key: "k1"})
	if keylessCount != 1 || filteredCount != 1 {
		t.Fatalf("after k1 publish: keyless=%d (want 1), filtered=%d (want 1)", keylessCount, filteredCount)
	}

	// Publish event whose key does NOT match the filter.
	Publish(b, keyedEvent{Base: hmevent.NewBase(), Key: "k2"})
	if keylessCount != 2 || filteredCount != 1 {
		t.Fatalf("after k2 publish: keyless=%d (want 2), filtered=%d (want 1)", keylessCount, filteredCount)
	}
}

// TestKeyFilterEmptyKeyMatchesAll verifies that an event whose
// EventKey() returns "" is delivered to every subscriber, including
// those registered with WithKey("something") — because empty string
// on the *event* side disables key-filtering for that specific publish.
//
// Observation: the bus compares h.key != "" && h.key != key. When key
// == "", the second condition is h.key != "" which is true for any non-
// empty filter key, so the handler is SKIPPED. This is the production
// behaviour: events with an empty EventKey() do not match key-filtered
// subscribers. The test documents this intentionally.
func TestKeyFilterEmptyEventKeySkipsFiltered(t *testing.T) {
	t.Parallel()

	b := NewBus()

	var keylessCount, filteredCount int
	Subscribe(b, func(keyedEvent) { keylessCount++ })
	Subscribe(b, func(keyedEvent) { filteredCount++ }, WithKey("k1"))

	// Event with empty key — keyless handler fires, filtered handler does NOT.
	Publish(b, keyedEvent{Base: hmevent.NewBase(), Key: ""})
	if keylessCount != 1 {
		t.Fatalf("keyless count=%d, want 1", keylessCount)
	}
	if filteredCount != 0 {
		t.Fatalf("filtered count=%d, want 0 (empty event key should not satisfy a non-empty filter)", filteredCount)
	}
}

// ---------- 3. Re-entrant publish across different event types ----------

// TestReentrantPublishDeferredCrossType verifies the deferred-dispatch
// guarantee when a handler publishes a *different* event type. Handler A
// (subscribed to DeviceCreatedEvent) publishes a ClientStateChangedEvent
// (type B). Handler B must not execute until handler A's frame has
// returned.
func TestReentrantPublishDeferredCrossType(t *testing.T) {
	t.Parallel()

	b := NewBus()

	var aFinished atomic.Bool
	var bFiredAfterA atomic.Bool
	var bFiredAt int64 // nanoseconds; set by B's handler
	var aReturnAt int64

	Subscribe(b, func(hmevent.DeviceCreatedEvent) {
		// A's body — re-entrant publish of type B.
		Publish(b, hmevent.ClientStateChangedEvent{Base: hmevent.NewBase(), CentralName: "c"})
		// At this point B's handler must NOT have run yet.
		if aFinished.Load() {
			// Already marked finished — impossible in this frame.
			t.Error("aFinished was set before A returned (impossible)")
		}
		aReturnAt = time.Now().UnixNano()
		aFinished.Store(true)
	})

	Subscribe(b, func(hmevent.ClientStateChangedEvent) {
		bFiredAt = time.Now().UnixNano()
		bFiredAfterA.Store(aFinished.Load())
	})

	Publish(b, hmevent.DeviceCreatedEvent{Base: hmevent.NewBase(), CentralName: "c"})

	if !aFinished.Load() {
		t.Fatal("A's handler never ran")
	}
	if !bFiredAfterA.Load() {
		t.Fatal("B's handler fired before A's frame completed (re-entrancy not deferred)")
	}
	// On platforms with coarse time.Now() resolution (e.g. macOS) two
	// adjacent reads can yield identical nanosecond timestamps. The
	// causal ordering is already pinned by bFiredAfterA above; this
	// secondary check only enforces "not earlier".
	if bFiredAt < aReturnAt {
		t.Fatalf("B timestamp (%d) earlier than A return (%d)", bFiredAt, aReturnAt)
	}
}

// ---------- 4. Unsubscribe is idempotent ----------

// TestUnsubscribeIsIdempotent calls the returned closure twice and
// verifies no panic and that HandlerCount stays at the post-first-unsub
// value.
func TestUnsubscribeIsIdempotent(t *testing.T) {
	t.Parallel()

	b := NewBus()
	typ := hmevent.EventTypeDeviceCreated

	unsub1 := Subscribe(b, func(hmevent.DeviceCreatedEvent) {})
	_ = Subscribe(b, func(hmevent.DeviceCreatedEvent) {})

	if got := b.HandlerCount(typ); got != 2 {
		t.Fatalf("before unsub: HandlerCount=%d, want 2", got)
	}

	// First call removes the handler.
	unsub1()
	if got := b.HandlerCount(typ); got != 1 {
		t.Fatalf("after first unsub: HandlerCount=%d, want 1", got)
	}

	// Second call must be a no-op — no panic, count unchanged.
	unsub1()
	if got := b.HandlerCount(typ); got != 1 {
		t.Fatalf("after second unsub: HandlerCount=%d, want 1 (idempotency broken)", got)
	}
}

// ---------- 5. HandlerCount lifecycle ----------

// TestHandlerCountReflectsRegistration tracks HandlerCount through the
// full Subscribe → Subscribe → Unsubscribe → Unsubscribe lifecycle.
func TestHandlerCountReflectsRegistration(t *testing.T) {
	t.Parallel()

	b := NewBus()
	typ := hmevent.EventTypeDeviceRemoved

	if got := b.HandlerCount(typ); got != 0 {
		t.Fatalf("initial HandlerCount=%d, want 0", got)
	}

	u1 := Subscribe(b, func(hmevent.DeviceRemovedEvent) {})
	if got := b.HandlerCount(typ); got != 1 {
		t.Fatalf("after 1st Subscribe: HandlerCount=%d, want 1", got)
	}

	u2 := Subscribe(b, func(hmevent.DeviceRemovedEvent) {})
	if got := b.HandlerCount(typ); got != 2 {
		t.Fatalf("after 2nd Subscribe: HandlerCount=%d, want 2", got)
	}

	u1()
	if got := b.HandlerCount(typ); got != 1 {
		t.Fatalf("after 1st Unsubscribe: HandlerCount=%d, want 1", got)
	}

	u2()
	if got := b.HandlerCount(typ); got != 0 {
		t.Fatalf("after 2nd Unsubscribe: HandlerCount=%d, want 0", got)
	}
}

// ---------- 6. Concurrent stress under -race ----------

// TestConcurrentSubscribeUnsubscribePublish stress-tests the bus with 32
// goroutines: half doing Subscribe+Unsubscribe cycles, half publishing.
// The test does not assert on event counts — the goal is clean execution
// under the race detector.
//
// Note: t.Parallel() is deliberately omitted because this test stresses
// the bus itself and should own the scheduler's full attention.
func TestConcurrentSubscribeUnsubscribePublish(t *testing.T) {
	const (
		goroutines = 32
		iterations = 200
	)

	b := NewBus()
	var wg sync.WaitGroup

	half := goroutines / 2

	// Goroutines that repeatedly subscribe, publish once, then unsubscribe.
	for i := range half {
		_ = i
		wg.Go(func() {
			for range iterations {
				unsub := Subscribe(b, func(hmevent.RecoveryStartedEvent) {})
				Publish(b, hmevent.RecoveryStartedEvent{Base: hmevent.NewBase(), CentralName: "c"})
				unsub()
			}
		})
	}

	// Goroutines that only publish.
	for i := range half {
		_ = i
		wg.Go(func() {
			for range iterations {
				Publish(b, hmevent.RecoveryStartedEvent{Base: hmevent.NewBase(), CentralName: "c"})
			}
		})
	}

	wg.Wait()
	// No assertion — reaching here without a race or panic is the goal.
}

// ---------- 7. HandlerStats: calls vs matches ----------

// TestHandlerStatsTracksCallsAndMatches registers a key-filtered handler
// and a keyless handler, publishes 3 events (2 matching key, 1 not), then
// verifies that the filtered handler's Calls==3 and Matches==2.
func TestHandlerStatsTracksCallsAndMatches(t *testing.T) {
	t.Parallel()

	b := NewBus()

	Subscribe(b, func(keyedEvent) {}, WithKey("k1"), WithName("filtered"))
	Subscribe(b, func(keyedEvent) {}, WithName("keyless"))

	// Two events with key "k1" → filtered handler matches both.
	Publish(b, keyedEvent{Base: hmevent.NewBase(), Key: "k1"})
	Publish(b, keyedEvent{Base: hmevent.NewBase(), Key: "k1"})
	// One event with key "k2" → filtered handler is called but does NOT match.
	Publish(b, keyedEvent{Base: hmevent.NewBase(), Key: "k2"})

	stats := b.HandlerStats()

	var filteredStat, keylessStat *HandlerStat
	for i := range stats {
		s := &stats[i]
		switch s.Name {
		case "filtered":
			filteredStat = s
		case "keyless":
			keylessStat = s
		}
	}

	if filteredStat == nil {
		t.Fatal("HandlerStats missing 'filtered' entry")
	}
	if filteredStat.Calls != 3 {
		t.Errorf("filtered.Calls=%d, want 3", filteredStat.Calls)
	}
	if filteredStat.Matches != 2 {
		t.Errorf("filtered.Matches=%d, want 2", filteredStat.Matches)
	}

	if keylessStat == nil {
		t.Fatal("HandlerStats missing 'keyless' entry")
	}
	// Keyless handler matches every event (empty filter key ≡ match-all).
	if keylessStat.Calls != 3 {
		t.Errorf("keyless.Calls=%d, want 3", keylessStat.Calls)
	}
	if keylessStat.Matches != 3 {
		t.Errorf("keyless.Matches=%d, want 3", keylessStat.Matches)
	}
}

// TestHandlerStatsEventTypeIsSet verifies the EventType field of each
// returned HandlerStat correctly identifies the subscribed event type.
func TestHandlerStatsEventTypeIsSet(t *testing.T) {
	t.Parallel()

	b := NewBus()
	Subscribe(b, func(hmevent.DeviceCreatedEvent) {}, WithName("devCreated"))
	Subscribe(b, func(hmevent.DeviceRemovedEvent) {}, WithName("devRemoved"))

	stats := b.HandlerStats()
	found := make(map[string]hmevent.EventType)
	for _, s := range stats {
		found[s.Name] = s.EventType
	}

	if got := found["devCreated"]; got != hmevent.EventTypeDeviceCreated {
		t.Errorf("devCreated EventType=%q, want %q", got, hmevent.EventTypeDeviceCreated)
	}
	if got := found["devRemoved"]; got != hmevent.EventTypeDeviceRemoved {
		t.Errorf("devRemoved EventType=%q, want %q", got, hmevent.EventTypeDeviceRemoved)
	}
}

// ---------- 8. LeakedSubscriptions ----------

// TestLeakedSubscriptionsReportsNamedNonReleased subscribes with a name,
// never calls unsubscribe, and checks that LeakedSubscriptions includes
// the name.
func TestLeakedSubscriptionsReportsNamedNonReleased(t *testing.T) {
	t.Parallel()

	b := NewBus()
	Subscribe(b, func(hmevent.DeviceCreatedEvent) {}, WithName("leaky"))
	// Intentionally NOT unsubscribing.

	leaked := b.LeakedSubscriptions()
	if len(leaked) == 0 {
		t.Fatal("LeakedSubscriptions returned empty, want at least one entry")
	}

	found := false
	for _, s := range leaked {
		if strings.Contains(s, "leaky") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("'leaky' not found in LeakedSubscriptions: %v", leaked)
	}
}

// TestLeakedSubscriptionsEmptyAfterUnsubscribe confirms that
// LeakedSubscriptions returns nil when every subscription has been
// released.
func TestLeakedSubscriptionsEmptyAfterUnsubscribe(t *testing.T) {
	t.Parallel()

	b := NewBus()
	u := Subscribe(b, func(hmevent.DeviceCreatedEvent) {}, WithName("released"))
	u()

	if got := b.LeakedSubscriptions(); got != nil {
		t.Fatalf("LeakedSubscriptions=%v after full unsub, want nil", got)
	}
}

// TestLeakedSubscriptionsFormatIncludesEventType verifies the format
// "<EventType>:<Name>" used by LeakedSubscriptions.
func TestLeakedSubscriptionsFormatIncludesEventType(t *testing.T) {
	t.Parallel()

	b := NewBus()
	Subscribe(b, func(hmevent.DeviceCreatedEvent) {}, WithName("myHandler"))

	leaked := b.LeakedSubscriptions()
	wantPrefix := string(hmevent.EventTypeDeviceCreated) + ":"
	found := false
	for _, s := range leaked {
		if strings.HasPrefix(s, wantPrefix) && strings.HasSuffix(s, "myHandler") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected entry with prefix %q and suffix %q in %v", wantPrefix, "myHandler", leaked)
	}
}

// ---------- 9. Publish without subscribers is a no-op ----------

// TestPublishWithoutSubscribersIsNoop verifies that publishing to a bus
// with no registered handlers neither panics nor returns an error.
func TestPublishWithoutSubscribersIsNoop(t *testing.T) {
	t.Parallel()

	b := NewBus()
	// No subscribers registered.
	Publish(b, hmevent.DeviceCreatedEvent{Base: hmevent.NewBase(), CentralName: "c"})
	Publish(b, hmevent.CentralStateChangedEvent{CentralName: "c"})
	// Reaching here without panic is the assertion.
}

// TestPublishAfterAllUnsubscribedIsNoop publishes after every subscriber
// has unsubscribed, confirming the bus handles the empty-slice case.
func TestPublishAfterAllUnsubscribedIsNoop(t *testing.T) {
	t.Parallel()

	b := NewBus()
	var count int
	u := Subscribe(b, func(hmevent.DeviceCreatedEvent) { count++ })

	Publish(b, hmevent.DeviceCreatedEvent{Base: hmevent.NewBase()})
	if count != 1 {
		t.Fatalf("before unsub: count=%d, want 1", count)
	}

	u()
	// Should silently do nothing.
	Publish(b, hmevent.DeviceCreatedEvent{Base: hmevent.NewBase()})
	if count != 1 {
		t.Fatalf("after unsub: count=%d, want 1 (second publish must be no-op)", count)
	}
}

// ---------- Additional edge cases ----------

// TestHandlerStatsFallbackToGeneratedID ensures that handlers registered
// without WithName appear in HandlerStats with a generated "h<N>" name
// (and are therefore not silently dropped).
func TestHandlerStatsFallbackToGeneratedID(t *testing.T) {
	t.Parallel()

	b := NewBus()
	Subscribe(b, func(hmevent.DeviceCreatedEvent) {}) // no WithName

	stats := b.HandlerStats()
	if len(stats) != 1 {
		t.Fatalf("HandlerStats len=%d, want 1", len(stats))
	}
	name := stats[0].Name
	if !strings.HasPrefix(name, "h") {
		t.Errorf("generated name=%q, expected 'h<N>' format", name)
	}
}

// TestReentrantPublishChain verifies a three-level chain A→B→C is
// flushed in FIFO order (A, then B, then C) with no re-entrancy
// violations at any level.
func TestReentrantPublishChain(t *testing.T) {
	t.Parallel()

	b := NewBus()
	var order []string
	var mu sync.Mutex

	record := func(label string) {
		mu.Lock()
		order = append(order, label)
		mu.Unlock()
	}

	Subscribe(b, func(hmevent.DeviceCreatedEvent) {
		record("A")
		// Publishes B from inside A.
		Publish(b, hmevent.DeviceRemovedEvent{Base: hmevent.NewBase()})
	})

	Subscribe(b, func(hmevent.DeviceRemovedEvent) {
		record("B")
		// Publishes C from inside B's deferred frame.
		Publish(b, hmevent.RecoveryStartedEvent{Base: hmevent.NewBase()})
	})

	Subscribe(b, func(hmevent.RecoveryStartedEvent) {
		record("C")
	})

	Publish(b, hmevent.DeviceCreatedEvent{Base: hmevent.NewBase(), CentralName: "c"})

	mu.Lock()
	got := order
	mu.Unlock()

	want := []string{"A", "B", "C"}
	if !slices.Equal(got, want) {
		t.Fatalf("chain order=%v, want %v", got, want)
	}
}
