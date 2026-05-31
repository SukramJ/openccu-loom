// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package central_test

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/coordinators"
	"github.com/SukramJ/openccu-loom/internal/client"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestCentralUnitAvailable_DegradedHealthIsAvailable verifies that
// Available returns true when health is DEGRADED.
func TestCentralUnitAvailable_DegradedHealthIsAvailable(t *testing.T) {
	c, err := central.New(central.Config{Name: "test"})
	if err != nil {
		t.Fatal(err)
	}
	// Record one unhealthy sample to get to DEGRADED (first fail → DEGRADED).
	c.Health.Record("client-1", health.Sample{Healthy: true})
	c.Health.Record("client-1", health.Sample{Healthy: false})

	if !c.Available() {
		t.Error("Available() should return true when health is DEGRADED")
	}
}

// TestCentralUnitAvailable_UnhealthyIsNotAvailable verifies that
// Available returns false when health is UNHEALTHY.
func TestCentralUnitAvailable_UnhealthyIsNotAvailable(t *testing.T) {
	c, err := central.New(central.Config{Name: "test"})
	if err != nil {
		t.Fatal(err)
	}
	// Record two consecutive unhealthy samples → UNHEALTHY.
	c.Health.Record("client-1", health.Sample{Healthy: false})
	c.Health.Record("client-1", health.Sample{Healthy: false})

	if c.Available() {
		t.Error("Available() should return false when health is UNHEALTHY")
	}
}

// TestCentralUnitAvailable_NilHealthIsFalse verifies that Available
// returns false when the health tracker is nil.
func TestCentralUnitAvailable_NilHealthIsFalse(t *testing.T) {
	c, err := central.New(central.Config{Name: "test"})
	if err != nil {
		t.Fatal(err)
	}
	c.Health = nil
	if c.Available() {
		t.Error("Available() should return false when health is nil")
	}
}

// TestCentralUnitHasPingPong_NoPingPong verifies HasPingPong returns
// false when no clients are registered.
func TestCentralUnitHasPingPong_NoPingPong(t *testing.T) {
	c, err := central.New(central.Config{Name: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if c.HasPingPong() {
		t.Error("HasPingPong() should return false when no clients are registered")
	}
}

// TestCentralUnitHasPingPong_WithPingPongCapable verifies HasPingPong
// returns true when a client with PingPong=true is registered.
func TestCentralUnitHasPingPong_WithPingPongCapable(t *testing.T) {
	c, err := central.New(central.Config{Name: "test"})
	if err != nil {
		t.Fatal(err)
	}
	// Build a minimal InterfaceClient with PingPong capability.
	// Provide a no-op Caller so the constructor does not reject the config.
	nopCaller := client.CallerFunc(func(_ context.Context, _ string, _ []any) (any, error) { //nolint:unparam // error return is always nil by design in test stub
		return nil, nil
	})
	ic, err2 := client.New(client.Config{
		CentralName:  "test",
		Interface:    hmenum.InterfaceHmIPRF,
		Caller:       nopCaller,
		Capabilities: backends.Capabilities{PingPong: true},
	})
	if err2 != nil {
		t.Fatalf("client.New: %v", err2)
	}
	entry := &coordinators.ClientEntry{
		InterfaceID: "ccu-main-HmIP-RF",
		Interface:   hmenum.InterfaceHmIPRF,
		Client:      ic,
	}
	_ = c.Clients.Register(entry)

	if !c.HasPingPong() {
		t.Error("HasPingPong() should return true when a client with PingPong=true is registered")
	}
}

// TestCentralUnitGetChannel_NotFound verifies GetChannel returns nil
// for an unknown address.
func TestCentralUnitGetChannel_NotFound(t *testing.T) {
	c, err := central.New(central.Config{Name: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if ch := c.GetChannel("UNKNOWN:1"); ch != nil {
		t.Errorf("expected nil for unknown channel, got %v", ch)
	}
}

// TestCentralUnitGetChannel_Found verifies GetChannel returns the
// channel for a registered device's channel address.
func TestCentralUnitGetChannel_Found(t *testing.T) {
	c, err := central.New(central.Config{Name: "test"})
	if err != nil {
		t.Fatal(err)
	}
	d := device.New(device.Config{
		Interface:   hmenum.InterfaceHmIPRF,
		Address:     "ABC001",
		InterfaceID: "ccu-main-HmIP-RF",
	})
	d.AddChannel("ABC001:1", 1, "SWITCH", hmenum.ParamsetKeyValues)
	c.ModelRegistry.Put(d)

	ch := c.GetChannel("ABC001:1")
	if ch == nil {
		t.Fatal("expected channel, got nil")
	}
	if ch.Address != "ABC001:1" {
		t.Errorf("Address = %q, want ABC001:1", ch.Address)
	}
}
