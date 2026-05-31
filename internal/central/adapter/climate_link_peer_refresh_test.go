// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/model/custom/climate"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// --- helpers ------------------------------------------------------------------

// newClimateFloatDP creates a VALUES-paramset *generic.Float on ch and
// returns it. Named distinctly to avoid conflicting with the existing
// newFloatDP helper (callback_combined_test.go) which returns
// *generic.DataPoint[float64].
func newClimateFloatDP(ch *device.Channel, param hmenum.Parameter) *generic.Float {
	dp := generic.NewFloat(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(param),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	})
	ch.Put(dp)
	return dp
}

// newClimateDev creates a thermostat device with a channel at "<addr>:1" that
// carries a *climate.Climate custom DP. Returns the device, the channel and
// the Climate.
func newClimateDev(iface, addr string) (*device.Device, *device.Channel, *climate.Climate) {
	d := device.New(device.Config{InterfaceID: iface, Address: addr, Model: "HmIP-eTRV"})
	ch := d.AddChannel(addr+":1", 1, "CLIMATE", hmenum.ParamsetKeyValues)
	newClimateFloatDP(ch, hmenum.ParameterSetPointTemperature) // gives Climate a valid key
	clim := climate.New(climate.Config{Channel: ch, Kind: climate.KindIP})
	ch.SetCustomDataPoint(clim)
	return d, ch, clim
}

// newValveDev creates a valve device with a channel at "<addr>:1" that has a
// LEVEL generic DP. Returns the device, channel and the LEVEL DP.
func newValveDev(iface, addr string) (*device.Device, *device.Channel, *generic.Float) {
	d := device.New(device.Config{InterfaceID: iface, Address: addr, Model: "HmIP-FALMOT-C12"})
	ch := d.AddChannel(addr+":1", 1, "DIMMER", hmenum.ParamsetKeyValues)
	lvl := newClimateFloatDP(ch, hmenum.ParameterLevel)
	return d, ch, lvl
}

// --- RecoveryCompletedEvent path ----------------------------------------------

// TestWireClimateLinkPeerRefreshLinkChangedWiresPeer is the primary happy-path
// test: a LinkPeerChangedEvent for a climate channel wires the peer channel so
// that a subsequent LEVEL push drives Climate.Activity.
func TestWireClimateLinkPeerRefreshLinkChangedWiresPeer(t *testing.T) {
	c := newCentralForHealthTest(t)

	const iface = "HmIP-RF"
	thermoDev, climCh, clim := newClimateDev(iface, "THERMO001")
	valveDev, _, lvlDP := newValveDev(iface, "VALVE001")

	c.ModelRegistry.Put(thermoDev)
	c.ModelRegistry.Put(valveDev)

	closer := WireClimateLinkPeerRefresh(c)
	defer closer()

	events.Publish(c.EventBus, hmevent.LinkPeerChangedEvent{
		Base:    hmevent.NewBase(),
		Address: climCh.Address,
		Peers:   []string{"VALVE001:1"},
	})

	lvlDP.OnEvent(float64(55)) // 55 % open → heating

	act, ok := clim.Activity()
	if !ok {
		t.Fatal("Activity not set after LinkPeerChanged + LEVEL push")
	}
	if act != climate.ActivityHeating {
		t.Errorf("Activity = %q, want %q", act, climate.ActivityHeating)
	}
}

// TestWireClimateLinkPeerRefreshLinkChangedIdlePeer verifies the idle branch:
// LEVEL = 0 → ActivityIdle.
func TestWireClimateLinkPeerRefreshLinkChangedIdlePeer(t *testing.T) {
	c := newCentralForHealthTest(t)

	const iface = "HmIP-RF"
	thermoDev, climCh, clim := newClimateDev(iface, "THERMO002")
	valveDev, _, lvlDP := newValveDev(iface, "VALVE002")

	c.ModelRegistry.Put(thermoDev)
	c.ModelRegistry.Put(valveDev)

	closer := WireClimateLinkPeerRefresh(c)
	defer closer()

	events.Publish(c.EventBus, hmevent.LinkPeerChangedEvent{
		Base:    hmevent.NewBase(),
		Address: climCh.Address,
		Peers:   []string{"VALVE002:1"},
	})

	lvlDP.OnEvent(float64(0)) // closed → idle

	act, ok := clim.Activity()
	if !ok {
		t.Fatal("Activity not set after LEVEL=0 push")
	}
	if act != climate.ActivityIdle {
		t.Errorf("Activity = %q, want idle", act)
	}
}

// TestWireClimateLinkPeerRefreshSuccessRecoveryWalksInterface verifies that a
// RecoveryCompletedEvent with Result=Success causes the interface walk:
// channels with a Climate custom DP get their peer subscriptions reset.
// After the recovery event a subsequent LinkPeerChangedEvent wires the peer;
// a LEVEL push then drives the activity.
func TestWireClimateLinkPeerRefreshSuccessRecoveryWalksInterface(t *testing.T) {
	c := newCentralForHealthTest(t)

	const iface = "HmIP-RF"
	thermoDev, climCh, clim := newClimateDev(iface, "THERMO003")
	valveDev, _, lvlDP := newValveDev(iface, "VALVE003")

	c.ModelRegistry.Put(thermoDev)
	c.ModelRegistry.Put(valveDev)

	closer := WireClimateLinkPeerRefresh(c)
	defer closer()

	// Recovery fires first (resets activity subscriptions to empty peers)
	events.Publish(c.EventBus, hmevent.RecoveryCompletedEvent{
		Base:        hmevent.NewBase(),
		InterfaceID: iface,
		Result:      hmenum.RecoveryResultSuccess,
	})

	// Then the topology update re-wires the peer
	events.Publish(c.EventBus, hmevent.LinkPeerChangedEvent{
		Base:    hmevent.NewBase(),
		Address: climCh.Address,
		Peers:   []string{"VALVE003:1"},
	})

	lvlDP.OnEvent(float64(30))

	act, ok := clim.Activity()
	if !ok {
		t.Fatal("Activity not set after recovery + LinkPeerChanged + LEVEL push")
	}
	if act != climate.ActivityHeating {
		t.Errorf("Activity = %q, want heating", act)
	}
}

// TestWireClimateLinkPeerRefreshFailureRecoverySkipped verifies that a
// RecoveryCompletedEvent with Result=Failed does NOT invoke any refresh on
// climate channels — existing peer subscriptions survive.
func TestWireClimateLinkPeerRefreshFailureRecoverySkipped(t *testing.T) {
	c := newCentralForHealthTest(t)

	const iface = "HmIP-RF"
	thermoDev, climCh, clim := newClimateDev(iface, "THERMO004")
	valveDev, _, lvlDP := newValveDev(iface, "VALVE004")

	c.ModelRegistry.Put(thermoDev)
	c.ModelRegistry.Put(valveDev)

	closer := WireClimateLinkPeerRefresh(c)
	defer closer()

	// Wire peer first via LinkPeerChanged
	events.Publish(c.EventBus, hmevent.LinkPeerChangedEvent{
		Base:    hmevent.NewBase(),
		Address: climCh.Address,
		Peers:   []string{"VALVE004:1"},
	})
	lvlDP.OnEvent(float64(20)) // pre-condition: subscription is alive
	act1, ok1 := clim.Activity()
	if !ok1 || act1 != climate.ActivityHeating {
		t.Fatalf("pre-condition: Activity = %q ok=%v, want heating/true", act1, ok1)
	}

	// A *failed* recovery must not reset the subscriptions
	events.Publish(c.EventBus, hmevent.RecoveryCompletedEvent{
		Base:        hmevent.NewBase(),
		InterfaceID: iface,
		Result:      hmenum.RecoveryResultFailed,
	})

	// Subscription must still be live: drive LEVEL=0 → idle
	lvlDP.OnEvent(float64(0))
	act2, ok2 := clim.Activity()
	if !ok2 {
		t.Fatal("Activity lost after failed recovery — subscription was torn down unexpectedly")
	}
	if act2 != climate.ActivityIdle {
		t.Errorf("Activity = %q after LEVEL=0, want idle (subscription intact)", act2)
	}
}

// --- Interface scoping --------------------------------------------------------

// TestWireClimateLinkPeerRefreshScopedToInterface verifies that a
// RecoveryCompletedEvent for interface A does not reset climate channels on
// interface B.
func TestWireClimateLinkPeerRefreshScopedToInterface(t *testing.T) {
	c := newCentralForHealthTest(t)

	const ifaceA = "HmIP-RF"
	const ifaceB = "BidCos-RF"

	thermoDevB, climChB, climB := newClimateDev(ifaceB, "THERMO010")
	c.ModelRegistry.Put(thermoDevB)

	closer := WireClimateLinkPeerRefresh(c)
	defer closer()

	// Pre-set activity on B's climate
	climB.OnActivity(climate.ActivityHeating)

	// Fire recovery for A — B must be untouched
	events.Publish(c.EventBus, hmevent.RecoveryCompletedEvent{
		Base:        hmevent.NewBase(),
		InterfaceID: ifaceA,
		Result:      hmenum.RecoveryResultSuccess,
	})

	act, ok := climB.Activity()
	if !ok {
		t.Fatal("Activity cleared on interface B after recovery for A")
	}
	if act != climate.ActivityHeating {
		t.Errorf("Activity = %q on B after recovery for A, want heating (unchanged)", act)
	}

	_ = climChB // referenced so compiler doesn't complain
}

// --- Closer -------------------------------------------------------------------

// TestWireClimateLinkPeerRefreshCloserUnsubscribes verifies that after calling
// the returned closer, further events have no effect (no panic).
func TestWireClimateLinkPeerRefreshCloserUnsubscribes(t *testing.T) {
	c := newCentralForHealthTest(t)

	const iface = "HmIP-RF"
	thermoDev, climCh, _ := newClimateDev(iface, "THERMO020")
	c.ModelRegistry.Put(thermoDev)

	closer := WireClimateLinkPeerRefresh(c)
	closer() // unsubscribe immediately

	// Events after closer must not panic
	events.Publish(c.EventBus, hmevent.RecoveryCompletedEvent{
		Base:        hmevent.NewBase(),
		InterfaceID: iface,
		Result:      hmenum.RecoveryResultSuccess,
	})
	events.Publish(c.EventBus, hmevent.LinkPeerChangedEvent{
		Base:    hmevent.NewBase(),
		Address: climCh.Address,
		Peers:   []string{"VALVE020:1"},
	})
}

// TestWireClimateLinkPeerRefreshNilUnitReturnsNoop verifies the nil-guard.
func TestWireClimateLinkPeerRefreshNilUnitReturnsNoop(t *testing.T) {
	closer := WireClimateLinkPeerRefresh(nil)
	if closer == nil {
		t.Fatal("expected non-nil closer from nil unit")
	}
	closer() // must not panic
}

// TestWireClimateLinkPeerRefreshNonClimateChannelIgnored verifies that a
// channel without a *climate.Climate custom DP is silently skipped.
func TestWireClimateLinkPeerRefreshNonClimateChannelIgnored(t *testing.T) {
	c := newCentralForHealthTest(t)

	const iface = "HmIP-RF"
	d := device.New(device.Config{InterfaceID: iface, Address: "SWITCH001", Model: "HmIP-PSM"})
	ch := d.AddChannel("SWITCH001:1", 1, "SWITCH", hmenum.ParamsetKeyValues)
	// No custom DP on channel — should be silently skipped
	c.ModelRegistry.Put(d)

	closer := WireClimateLinkPeerRefresh(c)
	defer closer()

	// Must not panic
	events.Publish(c.EventBus, hmevent.LinkPeerChangedEvent{
		Base:    hmevent.NewBase(),
		Address: ch.Address,
		Peers:   []string{"X:1"},
	})
	events.Publish(c.EventBus, hmevent.RecoveryCompletedEvent{
		Base:        hmevent.NewBase(),
		InterfaceID: iface,
		Result:      hmenum.RecoveryResultSuccess,
	})
}

// --- P3 Climate peer cache tests ------------------------------------------

// TestWireClimateLinkPeerRefreshCacheFilledByLinkPeerChanged verifies that a
// LinkPeerChangedEvent populates ch.LinkPeers() so the cache is available for
// a subsequent recovery cycle.
func TestWireClimateLinkPeerRefreshCacheFilledByLinkPeerChanged(t *testing.T) {
	c := newCentralForHealthTest(t)

	const iface = "HmIP-RF"
	thermoDev, climCh, _ := newClimateDev(iface, "THERMO030")
	_, _, _ = newValveDev(iface, "VALVE030")

	c.ModelRegistry.Put(thermoDev)

	closer := WireClimateLinkPeerRefresh(c)
	defer closer()

	events.Publish(c.EventBus, hmevent.LinkPeerChangedEvent{
		Base:    hmevent.NewBase(),
		Address: climCh.Address,
		Peers:   []string{"VALVE030:1"},
	})

	cached := climCh.LinkPeers()
	if len(cached) != 1 || cached[0] != "VALVE030:1" {
		t.Errorf("LinkPeers() after LinkPeerChangedEvent = %v, want [VALVE030:1]", cached)
	}
}

// TestWireClimateLinkPeerRefreshCacheEmptiedByEmptyPeers verifies that a
// LinkPeerChangedEvent with an empty peer list clears the cache.
func TestWireClimateLinkPeerRefreshCacheEmptiedByEmptyPeers(t *testing.T) {
	c := newCentralForHealthTest(t)

	const iface = "HmIP-RF"
	thermoDev, climCh, _ := newClimateDev(iface, "THERMO031")
	c.ModelRegistry.Put(thermoDev)

	closer := WireClimateLinkPeerRefresh(c)
	defer closer()

	// Populate cache first.
	events.Publish(c.EventBus, hmevent.LinkPeerChangedEvent{
		Base:    hmevent.NewBase(),
		Address: climCh.Address,
		Peers:   []string{"VALVE031:1"},
	})

	// Now clear it.
	events.Publish(c.EventBus, hmevent.LinkPeerChangedEvent{
		Base:    hmevent.NewBase(),
		Address: climCh.Address,
		Peers:   []string{},
	})

	if got := climCh.LinkPeers(); got != nil {
		t.Errorf("LinkPeers() after empty LinkPeerChangedEvent = %v, want nil", got)
	}
}

// TestWireClimateLinkPeerRefreshRecoveryUsesCachedPeers verifies the P3
// optimisation: a RecoveryCompletedEvent uses ch.LinkPeers() so that Climate
// activity subscriptions are re-wired immediately — without waiting for a
// subsequent LinkPeerChangedEvent topology push.
func TestWireClimateLinkPeerRefreshRecoveryUsesCachedPeers(t *testing.T) {
	c := newCentralForHealthTest(t)

	const iface = "HmIP-RF"
	thermoDev, climCh, clim := newClimateDev(iface, "THERMO032")
	valveDev, _, lvlDP := newValveDev(iface, "VALVE032")

	c.ModelRegistry.Put(thermoDev)
	c.ModelRegistry.Put(valveDev)

	closer := WireClimateLinkPeerRefresh(c)
	defer closer()

	// Step 1: topology push seeds the cache.
	events.Publish(c.EventBus, hmevent.LinkPeerChangedEvent{
		Base:    hmevent.NewBase(),
		Address: climCh.Address,
		Peers:   []string{"VALVE032:1"},
	})

	// Step 2: recovery fires — must reuse the cached peer, NOT nil.
	events.Publish(c.EventBus, hmevent.RecoveryCompletedEvent{
		Base:        hmevent.NewBase(),
		InterfaceID: iface,
		Result:      hmenum.RecoveryResultSuccess,
	})

	// Step 3: a LEVEL push should now drive Activity without an
	// intermediate LinkPeerChangedEvent.
	lvlDP.OnEvent(float64(40)) // 40 % → heating

	act, ok := clim.Activity()
	if !ok {
		t.Fatal("Activity not set after recovery using cached peers + LEVEL push")
	}
	if act != climate.ActivityHeating {
		t.Errorf("Activity = %q after cached-peer recovery, want heating", act)
	}

	_ = climCh // referenced
}
