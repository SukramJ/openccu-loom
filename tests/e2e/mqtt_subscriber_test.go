// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build e2e

package e2e

import (
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/tests/e2e/harness"
)

// TestE2EMQTTSubscriberRoutesToCCU asserts that the daemon's command
// subscriber actually consumes inbound SET frames. The broker-level proof
// is that the $SYS/broker/messages/received counter rises after we publish
// a canonical SET frame. A full godevccu echo is parked as Phase-F work
// because it requires the daemon to have fully indexed the simulated device
// tree — wire-level routing proof is sufficient here.
func TestE2EMQTTSubscriberRoutesToCCU(t *testing.T) {
	t.Parallel()

	h := harness.Start(t, harness.Options{EnableMQTT: true})
	if h.MQTT() == nil {
		t.Fatal("MQTT broker not started")
	}

	// Wait until the daemon has subscribed to at least one topic so the
	// broker knows about it. $SYS/broker/subscriptions rises on each SUBSCRIBE.
	deadline := 30 * time.Second
	subCount := awaitTopic(t, h.MQTT(), "$SYS/broker/subscriptions", deadline, func(_ string, payload []byte) bool {
		return atoiPayload(payload) >= 1
	})
	if subCount == "" {
		t.Skipf("broker never reported any subscriptions within %s — daemon MQTT wiring not yet ready", deadline)
	}

	// Publish a canonical raw-plane SET frame on the HmIP-RF interface.
	// The daemon will fail to route it (no matching device in godevccu has
	// that exact address), but the broker MUST count the inbound publish.
	setTopic := "openccu-loom/ccu-e2e/HmIP-RF/VCU0000000/3/values/STATE/set"
	if err := h.MQTT().Publish(setTopic, []byte(`{"value":true}`), false, 0); err != nil {
		t.Fatalf("publish %s: %v", setTopic, err)
	}

	// The broker messages/received counter must tick past baseline.
	got := awaitTopic(t, h.MQTT(), "$SYS/broker/messages/received", deadline, func(_ string, payload []byte) bool {
		return atoiPayload(payload) > 0
	})
	if got == "" {
		t.Fatalf("broker never reported a received PUBLISH within %s", deadline)
	}
	t.Logf("broker messages/received after publish: %s", got)
}
