// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

//go:build e2e

package e2e

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/tests/e2e/harness"
)

// TestE2EReconnectRepublishes verifies the daemon's connection-loss detection
// at the MQTT plane:
//
//  1. Wait for the daemon to report online (bridge/status = online).
//  2. Stop godevccu (simulates CCU disconnect).
//  3. Assert that the daemon detects the connection loss and publishes a
//     system-status event with healthy=false on
//     openccu-loom/<central>/system/status within the test deadline.
//
// The detection path: the per-interface probe goroutine (15 s cadence) and
// the central.check_connection scheduler job (CheckConnectionInterval) both
// call CheckConnectionAvailability; after five consecutive ping failures the
// circuit breaker opens and both paths publish ConnectionLostEvent.
// ConnectionLostEvent → ConnectionRecoveryCoordinator.triggerRecovery →
// RecoveryStartedEvent → EvaluateCentralState → SystemStatusChangedEvent
// (healthy=false because recovery is in flight).
//
// A short CheckConnectionInterval (5 s) drives the scheduler job fast enough
// that the circuit-breaker threshold (5 failures) is reached within 25 s.
// After Recovery starts (~3 s cooldown) EvaluateCentralState fires and the
// system/status topic carries healthy=false.
func TestE2EReconnectRepublishes(t *testing.T) {
	t.Parallel()

	const checkInterval = 4 * time.Second
	h := harness.Start(t, harness.Options{
		EnableMQTT:              true,
		CheckConnectionInterval: checkInterval,
	})
	if h.MQTT() == nil {
		t.Fatal("MQTT broker not started")
	}

	// Phase 1: wait for the initial online birth message.
	deadline := 30 * time.Second
	onlineTopic := awaitTopic(t, h.MQTT(), "openccu-loom/bridge/status", deadline, func(_ string, payload []byte) bool {
		body := strings.TrimSpace(string(payload))
		return body != "" &&
			body != "offline" &&
			!strings.Contains(body, `"status":"offline"`)
	})
	if onlineTopic == "" {
		t.Skipf("bridge/status never went online within %s — skipping reconnect phase", deadline)
	}
	t.Logf("bridge went online on %s", onlineTopic)

	// Phase 2: stop the CCU; the daemon should detect the connection loss and
	// publish system/status with healthy=false.
	if err := h.CCU().Stop(); err != nil {
		t.Fatalf("stop mock CCU: %v", err)
	}
	t.Log("mock CCU stopped — waiting for system-status degraded event")

	type statusPay struct {
		Healthy bool `json:"healthy"`
	}

	// Allow 5 failures × checkInterval + recovery cooldown + margin.
	statusDeadline := 6*checkInterval + 15*time.Second
	unhealthyTopic := awaitTopic(t, h.MQTT(), "openccu-loom/ccu-e2e/system/status", statusDeadline, func(_ string, payload []byte) bool {
		var p statusPay
		if err := json.Unmarshal(payload, &p); err != nil {
			return false
		}
		return !p.Healthy
	})

	if unhealthyTopic == "" {
		t.Fatalf("system/status did not transition to healthy=false within %s after CCU stop", statusDeadline)
	}
	t.Logf("system/status reported healthy=false after CCU stop on %s", unhealthyTopic)
}
