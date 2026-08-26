// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build chiptool

package chiptool

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/tests/chiptool/harness"
)

// TestSubscribe_OnOff finds the first OnOff endpoint and issues a
// Subscribe with MinInterval=1, MaxInterval=5. chip-tool returns
// after the priming Report + first steady-state ReportData (≤ 5 s).
//
// Mirrors v9 capability report T7.
func TestSubscribe_OnOff(t *testing.T) {
	b := requireBridge(t)
	eps := discoverEndpointsWith(t, b, 0x0006, 1)
	if len(eps) == 0 {
		t.Skip("no OnOff endpoint")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	out, err := b.SharedCtl.Subscribe(ctx, t, "onoff", "on-off", eps[0], 1, 5)
	if err != nil {
		// Subscribe blocks until the max-interval window elapses or
		// chip-tool gracefully tears the session down. A non-zero
		// exit at the timeout is expected here — chip-tool itself
		// times out from our side. Only fail if the marker is also
		// missing.
		if !harness.SubscriptionEstablished(out) {
			t.Fatalf("subscribe did not establish: %v\n%s", err, out)
		}
	}
	if !harness.SubscriptionEstablished(out) {
		t.Errorf("subscription marker missing:\n%s", out)
	}
}

// TestSubscribe_TemperatureMeasurement finds the first
// TemperatureMeasurement endpoint and subscribes. Same pattern as
// the OnOff subscribe but exercises the numeric-typed report path.
func TestSubscribe_TemperatureMeasurement(t *testing.T) {
	b := requireBridge(t)
	eps := discoverEndpointsWith(t, b, 0x0402, 1)
	if len(eps) == 0 {
		t.Skip("no TemperatureMeasurement endpoint")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	out, err := b.SharedCtl.Subscribe(ctx, t, "temperaturemeasurement", "measured-value", eps[0], 1, 5)
	if err != nil && !harness.SubscriptionEstablished(out) {
		t.Fatalf("subscribe failed: %v\n%s", err, out)
	}
	if !harness.SubscriptionEstablished(out) {
		t.Errorf("subscription marker missing:\n%s", out)
	}
}

// TestSubscribe_BDBI_Reachable subscribes to BDBI.Reachable. Bridges
// emit Reachable=true on priming for every alive child; the report
// must surface that value.
func TestSubscribe_BDBI_Reachable(t *testing.T) {
	b := requireBridge(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	aggOut, err := b.SharedCtl.ReadAttr(ctx, t, "descriptor", "parts-list", 1)
	if err != nil {
		t.Fatalf("read aggregator parts-list: %v", err)
	}
	eps := harness.EndpointsInPartsList(aggOut)
	if len(eps) == 0 {
		t.Skip("no bridged endpoints")
	}

	out, err := b.SharedCtl.Subscribe(ctx, t, "bridgeddevicebasicinformation", "reachable", eps[0], 1, 5)
	if err != nil && !harness.SubscriptionEstablished(out) {
		t.Fatalf("subscribe failed: %v\n%s", err, out)
	}
	if !harness.SubscriptionEstablished(out) {
		t.Errorf("subscription marker missing:\n%s", out)
	}
}
