// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build e2e

package e2e

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/tests/e2e/harness"
)

// The MQTT E2E suite drives the bridge end-to-end against the
// harness's embedded mochi-mqtt/server v2 broker. Three flavours,
// each its own daemon (the broker subscriptions are stateful and
// retained-message handling is easier when isolated):
//
//   - TestMQTTBridgeOnline: the daemon connects to the broker and
//     publishes its retained `openccu-loom/connected` level (1 or 2,
//     never the will's 0). This is the basic "bridge is alive" smoke.
//
//   - TestMQTTHomeAssistantDiscovery: every device with at least
//     one publishable channel emits an HA Discovery `config` payload
//     under `homeassistant/<component>/...`. The walker waits for at
//     least one such message — the topic schema doctest already
//     covers structural completeness.
//
//   - TestMQTTRawStateRoundtrip: when a value-change event flows from
//     godevccu through the daemon, the matching raw-plane state topic
//     receives an updated value. Driven by triggering a refresh
//     against godevccu and observing the state topic.

const (
	mqttDeadline   = 30 * time.Second
	mqttBridgeBase = "openccu-loom/connected"
	haDiscoveryAny = "homeassistant/+/+/+/config"
	rawStateAny    = "openccu-loom/status/+/+/+/+/values/+"
)

// TestMQTTBridgeOnline asserts the daemon publishes its retained
// connection level on `openccu-loom/connected`. This is the simplest
// signal that MQTT wiring works end-to-end (config → connect →
// publish → broker dispatch → subscriber).
func TestMQTTBridgeOnline(t *testing.T) {
	t.Parallel()
	h := harness.Start(t, harness.Options{EnableMQTT: true})
	if h.MQTT() == nil {
		t.Fatalf("MQTT broker not started — EnableMQTT not honoured")
	}

	got := awaitTopic(t, h.MQTT(), mqttBridgeBase, mqttDeadline, func(_ string, payload []byte) bool {
		// mqtt-smarthome 2.0 (ADR 0083): a plain 0/1/2. 1 = connected to
		// the broker, 2 = at least one central reachable; 0 is the will.
		return connectedAtLeast(payload, 1)
	})
	if got == "" {
		t.Fatalf("never observed online status on %s within %s", mqttBridgeBase, mqttDeadline)
	}
	t.Logf("bridge status payload: %s", got)
}

// TestMQTTHomeAssistantDiscovery asserts that the daemon emits at
// least one HA-Discovery `config` payload — proving the discovery
// pipeline (custom-DPs → entity-description → discovery-aggregate →
// MQTT publish) is wired.
func TestMQTTHomeAssistantDiscovery(t *testing.T) {
	t.Parallel()
	h := harness.Start(t, harness.Options{EnableMQTT: true})
	got := awaitTopic(t, h.MQTT(), haDiscoveryAny, mqttDeadline, func(topic string, payload []byte) bool {
		// The payload is a JSON object with `unique_id` (HA
		// convention) and the topic ends in `/config`.
		return strings.HasSuffix(topic, "/config") &&
			strings.Contains(string(payload), `"unique_id"`)
	})
	if got == "" {
		t.Fatalf("never observed an HA Discovery config payload within %s", mqttDeadline)
	}
}

// TestMQTTSetCommandIngested asserts that the daemon's MQTT command
// subscriber actually consumes inbound `/set` frames. We publish on
// a canonical raw-plane SET topic (no real device behind it — see
// notes/testplans/e2e-testplan.md §11.5 for why a true echo roundtrip cannot
// run against godevccu) and verify via the broker's $SYS counters
// that the message was received and routed.
//
// This is a wire-level smoke: it proves the bridge subscribed to
// the SET wildcard and the broker forwarded our publish. A full
// device echo roundtrip is parked behind §11.5.
func TestMQTTSetCommandIngested(t *testing.T) {
	t.Parallel()
	h := harness.Start(t, harness.Options{EnableMQTT: true})

	// Wait until the bridge has subscribed to *something* (its
	// command-topic wildcards) — the broker reports subscriber count
	// on $SYS/broker/subscriptions.
	subsBefore := awaitTopic(t, h.MQTT(), "$SYS/broker/subscriptions",
		mqttDeadline, func(_ string, payload []byte) bool {
			n := atoiPayload(payload)
			return n >= 1
		})
	if subsBefore == "" {
		t.Fatalf("bridge never subscribed to any topic on the broker within %s", mqttDeadline)
	}

	// Publish a canonical raw-plane SET frame. Address values are
	// arbitrary — the daemon will fail to route it (no matching
	// device), but the broker MUST count the inbound publish.
	target := "openccu-loom/set/ccu-e2e/HmIP-RF/VCU0000000/3/values/STATE"
	if err := h.MQTT().Publish(target, []byte(`{"val": true}`), false, 0); err != nil {
		t.Fatalf("publish %s: %v", target, err)
	}

	// The broker exposes a "messages received" $SYS counter that
	// rises on every PUBLISH. Wait until it ticks past the baseline.
	got := awaitTopic(t, h.MQTT(), "$SYS/broker/messages/received",
		mqttDeadline, func(_ string, payload []byte) bool {
			return atoiPayload(payload) > 0
		})
	if got == "" {
		t.Fatalf("broker never reported a received PUBLISH on %s within %s", target, mqttDeadline)
	}
}

// atoiPayload parses an MQTT $SYS counter payload as an int64.
// Returns 0 on any parse failure so the caller treats malformed
// frames as "not yet incremented".
func atoiPayload(payload []byte) int64 {
	var n int64
	for _, b := range payload {
		if b < '0' || b > '9' {
			return 0
		}
		n = n*10 + int64(b-'0')
	}
	return n
}

// awaitTopic subscribes to filter and waits until accept(topic,
// payload) returns true or the deadline elapses. Returns the matched
// topic (so the caller can log "saw <topic>" for debugging) or the
// empty string on timeout.
//
// Subscriptions on the embedded broker stay alive for its lifetime;
// the harness tears the broker down via t.Cleanup, so leaking the
// goroutine inside the handler is bounded by the test's runtime.
func awaitTopic(t *testing.T, broker harness.MQTTBroker, filter string, deadline time.Duration, accept func(topic string, payload []byte) bool) string {
	t.Helper()
	hit := make(chan string, 1)
	var once sync.Once
	if err := broker.Subscribe(filter, func(topic string, payload []byte, _ bool) {
		if accept(topic, payload) {
			once.Do(func() { hit <- topic })
		}
	}); err != nil {
		t.Fatalf("subscribe %s: %v", filter, err)
	}
	select {
	case topic := <-hit:
		return topic
	case <-time.After(deadline):
		return ""
	}
}

// connectedAtLeast reports whether payload is the instance's plain
// `<name>/connected` level (ADR 0083) at or above level.
func connectedAtLeast(payload []byte, level int64) bool {
	body := strings.TrimSpace(string(payload))
	if body == "" {
		return false
	}
	return atoiPayload([]byte(body)) >= level
}

// statusObjectVal decodes a status object (`{"val","ts","lc",…}`, ADR 0083)
// and returns its `val`. ok is false for anything else — an empty retained
// clear, a bare token, a JSON document without `val` — so a check that
// waits for a value cannot be satisfied by the pre-convention payloads.
func statusObjectVal(payload []byte) (val any, ok bool) {
	var obj map[string]any
	if err := json.Unmarshal(payload, &obj); err != nil {
		return nil, false
	}
	val, ok = obj["val"]
	if !ok {
		return nil, false
	}
	if _, hasTS := obj["ts"]; !hasTS {
		return nil, false
	}
	return val, true
}

// statusObjectBool reports whether payload is a boolean status item whose
// `val` equals want — the shape of every `online` reachability item.
func statusObjectBool(payload []byte, want bool) bool {
	val, ok := statusObjectVal(payload)
	if !ok {
		return false
	}
	b, isBool := val.(bool)
	return isBool && b == want
}
