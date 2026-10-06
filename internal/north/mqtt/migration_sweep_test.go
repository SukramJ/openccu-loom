// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mqtt

import (
	"context"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// retainedBroker is a broker's retained store with real subscribe semantics:
// a subscription receives the retained messages its filter matches, an
// empty retained publish deletes the topic and a non-empty one stores it.
// It is what makes "a second run clears nothing" a statement about the
// broker rather than about a mock that forgets nothing.
type retainedBroker struct {
	mu       sync.Mutex
	store    map[string][]byte
	filters  []string
	evicted  []string
	handlers map[string]MessageHandler
}

func newRetainedBroker(topics ...string) *retainedBroker {
	b := &retainedBroker{store: map[string][]byte{}, handlers: map[string]MessageHandler{}}
	for _, t := range topics {
		b.store[t] = []byte(`{"value":1}`)
	}
	return b
}

func (b *retainedBroker) Publish(_ context.Context, topic string, payload []byte, _ QoS, retain bool, _ ...PublishOption) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !retain {
		return nil
	}
	if len(payload) == 0 {
		delete(b.store, topic)
		b.evicted = append(b.evicted, topic)
		return nil
	}
	b.store[topic] = append([]byte(nil), payload...)
	return nil
}

func (b *retainedBroker) Subscribe(_ context.Context, filter string, _ QoS, handler MessageHandler, _ ...SubscribeOption) (SubscribeResult, error) {
	b.mu.Lock()
	b.filters = append(b.filters, filter)
	type msg struct {
		topic   string
		payload []byte
	}
	var deliver []msg
	for t, p := range b.store {
		if topicMatchesFilter(t, filter) {
			deliver = append(deliver, msg{t, p})
		}
	}
	b.mu.Unlock()
	for _, m := range deliver {
		handler(&Message{Topic: m.topic, Payload: m.payload, Retain: true})
	}
	return SubscribeResult{}, nil
}

func (b *retainedBroker) Unsubscribe(context.Context, string) error { return nil }

func (b *retainedBroker) retained() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]string, 0, len(b.store))
	for t := range b.store {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

func (b *retainedBroker) takeEvicted() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := b.evicted
	b.evicted = nil
	sort.Strings(out)
	return out
}

func (b *retainedBroker) subscribed() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.filters...)
}

// legacyLayoutOf returns one retained topic of every shape the
// pre-ADR-0083 layout published or subscribed, for central under base.
func legacyLayoutOf(base, central string) []string {
	dev := base + "/" + central + "/" + central + "-HmIP-RF/0001ABCD"
	ch := dev + "/1"
	hub := base + "/" + central + "/hub"
	return []string{
		ch + "/values/ACTUAL_TEMPERATURE", ch + "/values/ACTUAL_TEMPERATURE/config",
		ch + "/values/SET_POINT_TEMPERATURE/set",
		ch + "/master/TEMPERATURE_OFFSET", ch + "/master/TEMPERATURE_OFFSET/config", ch + "/master/TEMPERATURE_OFFSET/set",
		ch + "/calculated/DEW_POINT", ch + "/calculated/DEW_POINT/config",
		ch + "/custom/climate", ch + "/custom/climate/config", ch + "/custom/climate/set/set_mode",
		ch + "/event", ch + "/impulse", ch + "/device_error", ch + "/event/press_short",
		ch + "/combined/duration", ch + "/combined/duration/set",
		ch + "/week_profile/state", ch + "/week_profile/set",
		ch + "/schedule/state", ch + "/schedule/attrs", ch + "/schedule/1_1/state", ch + "/schedule/1_1/set",
		dev + "/availability", dev + "/info", dev + "/diagnostics", dev + "/update", dev + "/update/set",
		base + "/" + central + "/devices/0001ABCD/cdps/climate/set_mode/invoke",
		hub + "/status", hub + "/info", hub + "/diagnostics", hub + "/update",
		hub + "/alarm_messages", hub + "/service_messages", hub + "/inbox",
		hub + "/sysvars/Anwesenheit/state", hub + "/sysvars/Anwesenheit/set",
		hub + "/programs/1234/state", hub + "/programs/1234/set", hub + "/programs/1234/trigger",
		hub + "/programs/1234/execute_available",
		hub + "/connectivity/" + central + "-HmIP-RF",
		hub + "/install_mode/" + central + "-HmIP-RF", hub + "/install_mode/" + central + "-HmIP-RF/set",
		base + "/" + central + "/system/health_score", base + "/" + central + "/system/latency",
		base + "/" + central + "/system/last_event_age", base + "/" + central + "/system/status",
	}
}

// legacyDaemonTrees returns one retained topic of every shape the
// pre-ADR-0083 daemon-level trees carried below base.
func legacyDaemonTrees(base string) []string {
	return []string{
		base + "/bridge/status", base + "/bridge/health",
		base + "/alarm/eg/state", base + "/alarm/eg/availability", base + "/alarm/eg/event",
		base + "/alarm/eg/set", base + "/alarm/master/triggered-motion",
		base + "/security/state", base + "/security/alarm", base + "/security/problem",
		base + "/security/health", base + "/security/last_alarm", base + "/security/last_fault",
		base + "/security/event", base + "/security/fault", base + "/security/availability",
		base + "/security/class/smoke", base + "/security/zone/keller",
		base + "/system/addon_update/state", base + "/system/addon_update/set",
	}
}

// TestMigrationSweepClearsExactlyTheOldLayoutOfThisDaemon is the ADR 0083
// retained sweep against a broker that also holds what it must not touch:
//
//   - a sibling daemon on another base, with the identical old shapes;
//   - a sibling daemon on the SAME base, serving a central this one does not
//     own — the case a prefix rule over `<base>/` would destroy (ADR 0070
//     measured a prefix rule deleting 510 live components of a sibling);
//   - this daemon's own new-layout topics, whose first level below the base
//     is a function name;
//   - a topic below an owned central that matches no exact old shape;
//   - Home Assistant's discovery tree.
//
// It clears every old shape of this daemon's configured centrals and of its
// own daemon-level trees, nothing else, and a second run clears nothing. Both
// bases run: the default and a multi-level one, which stays accepted.
//
// Falsifiability: make [LegacyLayoutMatcher] a prefix match on the base and
// the same-base sibling disappears; drop the function-name guard in
// [RetainCleanup.collect] together with the central scoping and the new
// layout's status items disappear; subscribe `<base>/#` and the overlap arm
// fails.
func TestMigrationSweepClearsExactlyTheOldLayoutOfThisDaemon(t *testing.T) {
	t.Parallel()
	for _, base := range []string{"openccu-loom", "home/loom"} {
		t.Run(strings.ReplaceAll(base, "/", "_"), func(t *testing.T) {
			t.Parallel()
			owned := append(append(legacyLayoutOf(base, "GoOtto"), legacyLayoutOf(base, "Zweite")...),
				legacyDaemonTrees(base)...)
			survivors := []string{
				// A sibling daemon on another base.
				"other-loom/GoOtto/GoOtto-HmIP-RF/0001ABCD/1/values/STATE",
				"other-loom/bridge/status",
				"other-loom/alarm/eg/state",
				// A sibling daemon on the same base, serving another central.
				base + "/Fremd/Fremd-HmIP-RF/0001ABCD/1/values/STATE",
				base + "/Fremd/Fremd-HmIP-RF/0001ABCD/availability",
				base + "/Fremd/hub/sysvars/Anwesenheit/state",
				// This daemon's new layout.
				base + "/connected", base + "/info", base + "/maintenance/stats",
				base + "/status/GoOtto/online",
				base + "/status/GoOtto/GoOtto-HmIP-RF/0001ABCD/1/values/ACTUAL_TEMPERATURE",
				base + "/meta/GoOtto/GoOtto-HmIP-RF/0001ABCD/1/values/ACTUAL_TEMPERATURE",
				base + "/status/alarm/eg/panel",
				base + "/status/security/severity",
				base + "/status/system/addon_update",
				// Below an owned central, but no exact old shape.
				base + "/GoOtto/notes/whatever",
				base + "/GoOtto/GoOtto-HmIP-RF/0001ABCD/x/values/STATE",
				// Below an owned literal tree, but no exact old shape.
				base + "/security/somethingelse/deep",
				// Home Assistant's tree.
				"homeassistant/sensor/ccu_goOtto/1_state/config",
			}
			broker := newRetainedBroker(append(append([]string(nil), owned...), survivors...)...)
			b := NewBridge(BridgeConfig{
				Base: base, CentralName: "GoOtto", CentralNames: []string{"GoOtto", "Zweite"},
				RawEnabled: true,
			}, broker)

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			n, err := b.RunRetainCleanupOnce(ctx, 50*time.Millisecond)
			if err != nil {
				t.Fatalf("first sweep: %v", err)
			}
			evicted := broker.takeEvicted()
			wantEvicted := append([]string(nil), owned...)
			sort.Strings(wantEvicted)
			if !slices.Equal(evicted, wantEvicted) {
				for _, w := range wantEvicted {
					if !slices.Contains(evicted, w) {
						t.Errorf("old topic %q was not cleared", w)
					}
				}
				for _, e := range evicted {
					if !slices.Contains(wantEvicted, e) {
						t.Errorf("%q was cleared, but is not an old topic of this daemon", e)
					}
				}
			}
			if n != len(owned) {
				t.Errorf("first sweep reported %d evictions, want %d", n, len(owned))
			}
			wantLeft := append([]string(nil), survivors...)
			sort.Strings(wantLeft)
			if got := broker.retained(); !slices.Equal(got, wantLeft) {
				t.Errorf("retained after the sweep:\n got  %q\n want %q", got, wantLeft)
			}

			for _, sf := range broker.subscribed() {
				if strings.HasPrefix(sf, base+"/#") {
					t.Errorf("the sweep subscribed the whole base (%q)", sf)
				}
				for _, cf := range commandFilters(base) {
					if filtersOverlap(strings.Split(sf, "/"), strings.Split(cf, "/")) {
						t.Errorf("sweep filter %q overlaps command route %q", sf, cf)
					}
				}
			}

			n, err = b.RunRetainCleanupOnce(ctx, 50*time.Millisecond)
			if err != nil {
				t.Fatalf("second sweep: %v", err)
			}
			if n != 0 || len(broker.takeEvicted()) != 0 {
				t.Errorf("a second sweep cleared %d topics, want 0 — the sweep must be idempotent", n)
			}
		})
	}
}

// TestLegacyLayoutMatcherNeverMatchesAFunctionLevel pins the soundness rule
// the reserved central-name guard exists for: a topic whose first level
// below the base is a function name is new, whatever it looks like below
// that — even when the configured central set would claim the segment.
func TestLegacyLayoutMatcherNeverMatchesAFunctionLevel(t *testing.T) {
	t.Parallel()
	centrals := map[string]bool{"status": true, "set": true, "meta": true, "GoOtto": true}
	for _, topic := range []string{
		"openccu-loom/status/GoOtto-HmIP-RF/0001ABCD/1/values/STATE",
		"openccu-loom/set/GoOtto-HmIP-RF/0001ABCD/1/values/STATE",
		"openccu-loom/meta/GoOtto-HmIP-RF/0001ABCD/availability",
		"openccu-loom/connected",
		"openccu-loom/info",
		"openccu-loom/maintenance/stats",
	} {
		if LegacyLayoutMatcher("openccu-loom", centrals, topic) {
			t.Errorf("%q matched as an old topic", topic)
		}
	}
	if !LegacyLayoutMatcher("openccu-loom", centrals, "openccu-loom/GoOtto/GoOtto-HmIP-RF/0001ABCD/1/values/STATE") {
		t.Error("the control topic of an owned central did not match — the assertions above prove nothing")
	}
}
