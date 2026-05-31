// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package health_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// TestUpdateFromClient verifies that UpdateFromClient overwrites the
// client state.
func TestUpdateFromClient(t *testing.T) {
	c := health.NewConnection("iface-1", hmenum.InterfaceHmIPRF)
	if c.IsConnected() {
		t.Fatal("expected not connected at creation")
	}
	c.UpdateFromClient(hmenum.ClientStateConnected)
	if !c.IsConnected() {
		t.Fatal("expected connected after UpdateFromClient(CONNECTED)")
	}
	c.UpdateFromClient(hmenum.ClientStateDisconnected)
	if c.IsConnected() {
		t.Fatal("expected not connected after UpdateFromClient(DISCONNECTED)")
	}
}

// TestUpdateAllFromClients verifies UpdateAllFromClients fans out
// states to all registered connections.
func TestUpdateAllFromClients(t *testing.T) {
	reg := health.NewConnectionRegistry()
	c1 := health.NewConnection("if-1", hmenum.InterfaceHmIPRF)
	c2 := health.NewConnection("if-2", hmenum.InterfaceBidCosRF)
	reg.Register(c1)
	reg.Register(c2)

	states := map[string]hmenum.ClientState{
		"if-1": hmenum.ClientStateConnected,
		"if-2": hmenum.ClientStateConnected,
	}
	reg.UpdateAllFromClients(states)

	if !c1.IsConnected() {
		t.Error("c1 should be connected after UpdateAllFromClients")
	}
	if !c2.IsConnected() {
		t.Error("c2 should be connected after UpdateAllFromClients")
	}
}

// TestUpdateClientHealth_ReconnectAttempt verifies that transitioning
// to RECONNECTING (from another state) records a reconnect attempt.
func TestUpdateClientHealth_ReconnectAttempt(t *testing.T) {
	reg := health.NewConnectionRegistry()
	c := health.NewConnection("if-1", hmenum.InterfaceHmIPRF)
	reg.Register(c)

	snap0 := c.Snapshot()
	if snap0.ReconnectAttempts != 0 {
		t.Fatalf("expected 0 reconnect attempts, got %d", snap0.ReconnectAttempts)
	}

	reg.UpdateClientHealth("if-1", hmenum.ClientStateDisconnected, hmenum.ClientStateReconnecting)
	snap1 := c.Snapshot()
	if snap1.ReconnectAttempts != 1 {
		t.Errorf("expected 1 reconnect attempt, got %d", snap1.ReconnectAttempts)
	}
}

// TestUpdateClientHealth_ConnectedResetsCounter verifies that
// transitioning to CONNECTED resets the reconnect counter.
func TestUpdateClientHealth_ConnectedResetsCounter(t *testing.T) {
	reg := health.NewConnectionRegistry()
	c := health.NewConnection("if-1", hmenum.InterfaceHmIPRF)
	reg.Register(c)

	reg.UpdateClientHealth("if-1", hmenum.ClientStateDisconnected, hmenum.ClientStateReconnecting)
	reg.UpdateClientHealth("if-1", hmenum.ClientStateReconnecting, hmenum.ClientStateConnected)

	snap := c.Snapshot()
	if snap.ReconnectAttempts != 0 {
		t.Errorf("expected 0 reconnect attempts after CONNECTED, got %d", snap.ReconnectAttempts)
	}
}

// TestShouldBeDegraded verifies ShouldBeDegraded returns true when
// some (not all) connections are healthy.
func TestShouldBeDegraded(t *testing.T) {
	reg := health.NewConnectionRegistry()
	c1 := health.NewConnection("if-1", hmenum.InterfaceHmIPRF)
	c2 := health.NewConnection("if-2", hmenum.InterfaceBidCosRF)
	reg.Register(c1)
	reg.Register(c2)

	// Neither connected — not degraded (none healthy).
	if reg.ShouldBeDegraded() {
		t.Error("ShouldBeDegraded should be false when no connections are healthy")
	}

	// Connect only c1.
	c1.UpdateFromClient(hmenum.ClientStateConnected)
	if !reg.ShouldBeDegraded() {
		t.Error("ShouldBeDegraded should be true when only one of two connections is healthy")
	}

	// Connect both.
	c2.UpdateFromClient(hmenum.ClientStateConnected)
	if reg.ShouldBeDegraded() {
		t.Error("ShouldBeDegraded should be false when all connections are healthy")
	}
}

// TestShouldBeRunning verifies ShouldBeRunning returns true only when
// all connections are healthy.
func TestShouldBeRunning(t *testing.T) {
	reg := health.NewConnectionRegistry()
	c1 := health.NewConnection("if-1", hmenum.InterfaceHmIPRF)
	reg.Register(c1)

	if reg.ShouldBeRunning() {
		t.Error("ShouldBeRunning should be false before any connection is healthy")
	}
	c1.UpdateFromClient(hmenum.ClientStateConnected)
	if !reg.ShouldBeRunning() {
		t.Error("ShouldBeRunning should be true when the single connection is healthy")
	}
}

// TestPrimaryClientHealthy_PreferHmIPRF verifies the primary client
// prefers HmIP-RF over other interfaces.
func TestPrimaryClientHealthy_PreferHmIPRF(t *testing.T) {
	reg := health.NewConnectionRegistry()
	cBidcos := health.NewConnection("if-bidcos", hmenum.InterfaceBidCosRF)
	cHmIP := health.NewConnection("if-hmip", hmenum.InterfaceHmIPRF)
	reg.Register(cBidcos)
	reg.Register(cHmIP)

	// Neither connected.
	if reg.PrimaryClientHealthy() {
		t.Error("PrimaryClientHealthy should be false when HmIP-RF not connected")
	}

	// Connect BidCos only — HmIP-RF still not healthy.
	cBidcos.UpdateFromClient(hmenum.ClientStateConnected)
	if reg.PrimaryClientHealthy() {
		t.Error("PrimaryClientHealthy should be false: HmIP-RF still disconnected")
	}

	// Connect HmIP-RF.
	cHmIP.UpdateFromClient(hmenum.ClientStateConnected)
	if !reg.PrimaryClientHealthy() {
		t.Error("PrimaryClientHealthy should be true when HmIP-RF is connected")
	}
}

// TestPrimaryClientHealthy_FallbackNoHmIPRF verifies that when there
// is no HmIP-RF interface, the first registered interface is used as
// primary.
func TestPrimaryClientHealthy_FallbackNoHmIPRF(t *testing.T) {
	reg := health.NewConnectionRegistry()
	c := health.NewConnection("if-cuxd", hmenum.InterfaceCUxD)
	reg.Register(c)

	if reg.PrimaryClientHealthy() {
		t.Error("expected false before connection is healthy")
	}
	c.UpdateFromClient(hmenum.ClientStateConnected)
	if !reg.PrimaryClientHealthy() {
		t.Error("expected true after connection becomes healthy")
	}
}
