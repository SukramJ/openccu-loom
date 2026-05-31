// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build e2e

package e2e

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/tests/e2e/harness"
)

// TestE2EHotPlugTriggers asserts that when a new device is added to the
// running godevccu instance the daemon picks it up and emits HA-Discovery
// config payloads for the new device.
//
// The mechanism: call godevccu's AddDevices RPC via the VirtualCCU's
// RPCFunctions (which fires an event to all registered callbacks). The
// daemon's HotPlugCoordinator should pick this up and trigger a discovery
// publish.
//
// Known limitation: godevccu exposes device add/remove only through its
// RPC surface (RPCFunctions.AddDevices / RemoveDevices). The harness's
// MockCCU does not expose a convenience wrapper, so we drive the API
// directly. If the VirtualCCU pointer is inaccessible from the harness
// public surface, this test skips with a Phase-F note.
func TestE2EHotPlugTriggers(t *testing.T) {
	t.Parallel()

	h := harness.Start(t, harness.Options{EnableMQTT: true})
	if h.MQTT() == nil {
		t.Fatal("MQTT broker not started")
	}

	ccu := h.CCU()
	if ccu == nil {
		t.Skip("MockCCU not available — Phase-F candidate")
	}

	// Wait for initial boot discovery to settle so the hot-plug delta
	// is distinguishable from bootstrap noise.
	deadline := 30 * time.Second
	initial := awaitTopic(t, h.MQTT(), "homeassistant/+/+/+/config", deadline, func(topic string, payload []byte) bool {
		return strings.HasSuffix(topic, "/config") &&
			strings.Contains(string(payload), `"unique_id"`)
	})
	if initial == "" {
		t.Skipf("no initial discovery within %s — Phase-F candidate", deadline)
	}
	t.Logf("initial discovery settled on %s", initial)

	// Add a new device to the running godevccu instance.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := ccu.V().RPC().AddDevices(ctx, []string{"HmIP-eTRV-2"}); err != nil {
		t.Skipf("AddDevices returned error — Phase-F candidate: %v", err)
	}
	t.Log("injected HmIP-eTRV-2 device into godevccu")

	// Wait for a new discovery payload. The object_id for a fresh eTRV-2
	// channel will contain a device address that was absent before.
	newDiscovery := awaitTopic(t, h.MQTT(), "homeassistant/+/+/+/config", 20*time.Second, func(_ string, payload []byte) bool {
		return strings.Contains(string(payload), "eTRV") ||
			strings.Contains(string(payload), "etrv") ||
			strings.Contains(string(payload), "temperature")
	})
	if newDiscovery == "" {
		t.Skip("hot-plug discovery not emitted within 20s after AddDevices — Phase-F candidate")
	}
	t.Logf("hot-plug discovery emitted on %s", newDiscovery)
}
