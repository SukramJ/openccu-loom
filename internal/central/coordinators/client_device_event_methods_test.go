// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package coordinators

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/central/registry"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// ---- L-A5-10: WaitForTCPReady ----

func TestWaitForTCPReadyPortZeroIsNoOp(t *testing.T) {
	c := NewClientCoordinator()
	if err := c.WaitForTCPReady(context.Background(), "192.0.2.1", 0); err != nil {
		t.Fatalf("WaitForTCPReady port=0 should be no-op, got: %v", err)
	}
}

func TestWaitForTCPReadyUnreachable(t *testing.T) {
	c := NewClientCoordinator()
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	if err := c.WaitForTCPReady(ctx, "192.0.2.1", 9999); err == nil {
		t.Fatal("WaitForTCPReady to unreachable host should return error")
	}
}

// ---- L-A5-12: RecordLastFailure / LastFailureReason / LastFailureInterfaceID ----

func TestLastFailureTracking(t *testing.T) {
	c := NewClientCoordinator()
	if c.LastFailureReason() != "" {
		t.Fatal("LastFailureReason should be empty initially")
	}
	if c.LastFailureInterfaceID() != "" {
		t.Fatal("LastFailureInterfaceID should be empty initially")
	}
	c.RecordLastFailure("auth_timeout", "HmIP-RF.local")
	if c.LastFailureReason() != "auth_timeout" {
		t.Fatalf("LastFailureReason = %q, want auth_timeout", c.LastFailureReason())
	}
	if c.LastFailureInterfaceID() != "HmIP-RF.local" {
		t.Fatalf("LastFailureInterfaceID = %q, want HmIP-RF.local", c.LastFailureInterfaceID())
	}
	// Overwrite with new failure.
	c.RecordLastFailure("connection_refused", "BidCos-RF.local")
	if c.LastFailureReason() != "connection_refused" {
		t.Fatalf("LastFailureReason = %q, want connection_refused", c.LastFailureReason())
	}
}

// ---- L-A5-14: Available() semantics — all, not any ----

func TestAvailableAllSemantic(t *testing.T) {
	c := NewClientCoordinator()
	// Empty coordinator.
	if c.Available() {
		t.Fatal("Available() on empty coordinator should be false")
	}
	// One nil-client entry (disconnected).
	_ = c.Register(&ClientEntry{InterfaceID: "iface1", Interface: hmenum.InterfaceHmIPRF, Client: nil})
	if c.Available() {
		t.Fatal("Available() should be false when any client is disconnected")
	}
}

// ---- L-A5-23: InvalidateFirmwareCache ----

func TestInvalidateFirmwareCacheEvictsEntries(t *testing.T) {
	dc, _, _, descs, _ := newDCFull(t)
	iface := wireKey(hmenum.InterfaceHmIPRF)

	descs.Put(iface, device("ADDR001", "MODEL", "1.0", "ADDR001:0"))
	descs.Put(iface, channel("ADDR001:0", "ADDR001", "MODEL"))

	if _, ok := descs.Get(iface, "ADDR001"); !ok {
		t.Fatal("desc should exist before invalidation")
	}
	dc.InvalidateFirmwareCache(iface, "ADDR001")
	if _, ok := descs.Get(iface, "ADDR001"); ok {
		t.Fatal("top-level desc should be removed after firmware cache invalidation")
	}
	if _, ok := descs.Get(iface, "ADDR001:0"); ok {
		t.Fatal("channel desc should be removed after firmware cache invalidation")
	}
}

// ---- L-A5-24: RefreshDeviceDescriptions refreshOnlyExisting ----

func TestRefreshDeviceDescriptionsOnlyExisting(t *testing.T) {
	dc, _, _, descs, _ := newDCFull(t)
	iface := wireKey(hmenum.InterfaceHmIPRF)

	descs.Put(iface, device("ADDR001", "OLD_MODEL", "1.0"))

	lister := listerOf(
		device("ADDR001", "NEW_MODEL", "2.0"),
		device("ADDR002", "MODEL_B", "1.0"),
	)

	if err := dc.RefreshDeviceDescriptions(context.Background(), lister, iface, true); err != nil {
		t.Fatalf("RefreshDeviceDescriptions: %v", err)
	}

	// ADDR001 should be updated.
	d, ok := descs.Get(iface, "ADDR001")
	if !ok || d.Type != "NEW_MODEL" {
		t.Fatalf("ADDR001 not updated to NEW_MODEL, got type=%q ok=%v", d.Type, ok)
	}
	// ADDR002 should not appear (refreshOnlyExisting=true).
	if _, ok := descs.Get(iface, "ADDR002"); ok {
		t.Fatal("ADDR002 should not be added when refreshOnlyExisting=true")
	}
}

func TestRefreshDeviceDescriptionsAllWhenFalse(t *testing.T) {
	dc, _, _, descs, _ := newDCFull(t)
	iface := wireKey(hmenum.InterfaceHmIPRF)

	descs.Put(iface, device("ADDR001", "OLD_MODEL", "1.0"))

	lister := listerOf(
		device("ADDR001", "NEW_MODEL", "2.0"),
		device("ADDR002", "MODEL_B", "1.0"),
	)

	if err := dc.RefreshDeviceDescriptions(context.Background(), lister, iface, false); err != nil {
		t.Fatalf("RefreshDeviceDescriptions (all): %v", err)
	}

	// Both addresses should be present.
	if _, ok := descs.Get(iface, "ADDR002"); !ok {
		t.Fatal("ADDR002 should be added when refreshOnlyExisting=false")
	}
}

// ---- L-A5-25: IdentifyDevicesMissingParamsets ----

func TestIdentifyDevicesMissingParamsets(t *testing.T) {
	dc, _, _, descs, psets := newDCFull(t)
	iface := wireKey(hmenum.InterfaceHmIPRF)

	descs.Put(iface, channel("ADDR001:0", "ADDR001", "MODEL"))
	descs.Put(iface, channel("ADDR001:1", "ADDR001", "MODEL"))
	// Put a VALUES paramset for :0 only — :1 has neither.
	psets.Put(iface, "ADDR001:0", hmenum.ParamsetKeyValues, hmproto.Paramset{})

	missing := dc.IdentifyDevicesMissingParamsets(iface)
	if len(missing) != 1 || missing[0] != "ADDR001:1" {
		t.Fatalf("IdentifyDevicesMissingParamsets = %v, want [ADDR001:1]", missing)
	}
}

// ---- L-A5-26: RenameNewDeviceFromOverride ----

func TestRenameNewDeviceFromOverrideAppliesName(t *testing.T) {
	dc, _, _, _, _ := newDCFull(t)

	overrider := &testNameOverrider{names: map[string]string{"ADDR001": "Bücherregal"}}
	dc.SetDeviceNameOverrideChecker(overrider)

	var renamed string
	dc.RenameNewDeviceFromOverride(wireKey(hmenum.InterfaceHmIPRF), "ADDR001", func(addr, name string) {
		renamed = name
	})
	if renamed != "Bücherregal" {
		t.Fatalf("override name = %q, want Bücherregal", renamed)
	}
}

func TestRenameNewDeviceFromOverrideNoMatchIsNoOp(t *testing.T) {
	dc, _, _, _, _ := newDCFull(t)

	overrider := &testNameOverrider{names: map[string]string{}}
	dc.SetDeviceNameOverrideChecker(overrider)

	var called bool
	dc.RenameNewDeviceFromOverride(wireKey(hmenum.InterfaceHmIPRF), "ADDR999", func(_, _ string) {
		called = true
	})
	if called {
		t.Fatal("rename callback must not fire when no override exists")
	}
}

// ---- L-A5-28: _STATUS events keep their own parameter name ----

// TestHandleRawEventNormalizedStatusKeepsOwnName pins the fix for the
// double-zero ingestion: "<X>_STATUS" carries the measurement status of
// "<X>" (0 = NORMAL), not a value echo. The former suffix-stripping
// published the status index as a value_changed for the BASE parameter,
// so north-bound consumers oscillated between the real measurement and 0.
// The event must dispatch under its own name.
func TestHandleRawEventNormalizedStatusKeepsOwnName(t *testing.T) {
	bus := events.NewBus()
	cache := NewCacheCoordinator()
	ec := NewEventCoordinator(bus, cache, nil)
	ec.SetCentralName("ccu1")

	var receivedParam string
	ec.AddDataPointSubscription(func(e hmevent.DataPointValueChangedEvent) {
		receivedParam = e.Key.Parameter
	})

	ec.HandleRawEventNormalized(context.Background(), "iface1", "ADDR001:0", "LEVEL_STATUS",
		hmtypes.IntValue(0))

	time.Sleep(50 * time.Millisecond)
	if receivedParam != "LEVEL_STATUS" {
		t.Fatalf("parameter = %q, want LEVEL_STATUS (no base-name rewrite)", receivedParam)
	}
}

func TestHandleRawEventNormalizedNoSuffixPassthrough(t *testing.T) {
	bus := events.NewBus()
	cache := NewCacheCoordinator()
	ec := NewEventCoordinator(bus, cache, nil)
	ec.SetCentralName("ccu1")

	var receivedParam string
	ec.AddDataPointSubscription(func(e hmevent.DataPointValueChangedEvent) {
		receivedParam = e.Key.Parameter
	})

	ec.HandleRawEventNormalized(context.Background(), "iface1", "ADDR001:0", "LEVEL",
		hmtypes.FloatValue(0.5))

	time.Sleep(50 * time.Millisecond)
	if receivedParam != "LEVEL" {
		t.Fatalf("parameter = %q, want LEVEL (no-suffix passthrough)", receivedParam)
	}
}

// ---- L-A5-30: PONG routing ----

func TestHandleRawEventNormalizedPONGRoutesToTracker(t *testing.T) {
	bus := events.NewBus()
	cache := NewCacheCoordinator()
	ec := NewEventCoordinator(bus, cache, nil)

	pongCalled := make(chan string, 1)
	ec.SetPingPongTracker(func(ifaceID, _ string) { pongCalled <- ifaceID })

	dpCalled := make(chan struct{}, 1)
	ec.AddDataPointSubscription(func(_ hmevent.DataPointValueChangedEvent) {
		dpCalled <- struct{}{}
	})

	// A tracking PONG carries the echoed caller_id "<interfaceID>#<token>".
	ec.HandleRawEventNormalized(context.Background(), "iface1", "ADDR001:0", "PONG",
		hmtypes.StringValue("iface1#7"))

	select {
	case id := <-pongCalled:
		if id != "iface1" {
			t.Fatalf("ping-pong tracker received %q, want iface1", id)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("ping-pong tracker not called")
	}
	// DataPoint subscriber must NOT fire for PONG.
	time.Sleep(30 * time.Millisecond)
	if len(dpCalled) != 0 {
		t.Fatal("DataPoint subscriber must not fire for PONG")
	}
}

// ---- EmitDevicesCreatedEvents / EmitDeviceRemovedEvent ----

func TestEmitDevicesCreatedEvents(t *testing.T) {
	bus := events.NewBus()
	cache := NewCacheCoordinator()
	ec := NewEventCoordinator(bus, cache, nil)
	ec.SetCentralName("ccu1")

	received := make(chan hmevent.DeviceCreatedEvent, 3)
	_ = events.Subscribe(bus, func(e hmevent.DeviceCreatedEvent) { received <- e })

	ec.EmitDevicesCreatedEvents("iface1", []string{"A1", "A2"}, "MODEL_X", hmenum.SourceOfDeviceCreationNew)

	time.Sleep(50 * time.Millisecond)
	if len(received) != 2 {
		t.Fatalf("got %d DeviceCreatedEvents, want 2", len(received))
	}
}

func TestEmitDeviceRemovedEvent(t *testing.T) {
	bus := events.NewBus()
	cache := NewCacheCoordinator()
	ec := NewEventCoordinator(bus, cache, nil)
	ec.SetCentralName("ccu1")

	received := make(chan hmevent.DeviceRemovedEvent, 1)
	_ = events.Subscribe(bus, func(e hmevent.DeviceRemovedEvent) { received <- e })

	ec.EmitDeviceRemovedEvent("iface1", "ADDR001")

	select {
	case e := <-received:
		if e.Address != "ADDR001" {
			t.Fatalf("address = %q, want ADDR001", e.Address)
		}
	case <-time.After(200 * time.Millisecond):
		t.Fatal("DeviceRemovedEvent not received")
	}
}

// ---- L-A5-31: InitHub ----

func TestInitHubClearsSysvars(t *testing.T) {
	bus := events.NewBus()
	hc := NewHubCoordinator("ccu1", bus)
	hc.UpdateSysvar(context.Background(), SysvarSnapshot{Name: "sv1", Value: hmtypes.BoolValue(true)})
	if sysvars := hc.Sysvars(); len(sysvars) == 0 {
		t.Fatal("expected sysvar before InitHub")
	}
	hc.InitHub()
	if sysvars := hc.Sysvars(); len(sysvars) != 0 {
		t.Fatalf("InitHub did not clear sysvars, got %d", len(sysvars))
	}
}

// ---- L-A5-33: ClearOnStop ----

func TestClearOnStopReturnsTrueWhenInitComplete(t *testing.T) {
	c := NewCacheCoordinator()
	c.Set(hmtypes.DataPointKey{InterfaceID: "i", ChannelAddress: "A", Parameter: "P"}, hmtypes.BoolValue(true), "test")
	c.SetDataCacheInitializationComplete()

	complete := c.ClearOnStop()
	if !complete {
		t.Fatal("ClearOnStop should return true when initialization was complete")
	}
	if c.Len() != 0 {
		t.Fatalf("ClearOnStop should empty cache, got %d entries", c.Len())
	}
}

func TestClearOnStopReturnsFalseBeforeInit(t *testing.T) {
	c := NewCacheCoordinator()
	complete := c.ClearOnStop()
	if complete {
		t.Fatal("ClearOnStop should return false when initialization was not complete")
	}
}

// ---- L-A5-34: SaveAllWithDescription / SaveIfChangedWithDescription ----

func TestSaveAllWithDescriptionNoOp(t *testing.T) {
	c := NewCacheCoordinator()
	if err := c.SaveAllWithDescription(context.Background(), "test save"); err != nil {
		t.Fatalf("SaveAllWithDescription: %v", err)
	}
	if err := c.SaveIfChangedWithDescription(context.Background(), "test save"); err != nil {
		t.Fatalf("SaveIfChangedWithDescription: %v", err)
	}
}

// ---- L-A5-35: GetLinksForLocale / GetLinkableChannelsForLocale ----

func TestGetLinksForLocaleRoleFilter(t *testing.T) {
	lc := NewLinkCoordinator(func(_ string) (LinkClient, bool) {
		return &fakeLinkClient{
			links: []DeviceLink{
				{SenderAddress: "A:1", ReceiverAddress: "B:1", Direction: "outgoing"},
				{SenderAddress: "B:1", ReceiverAddress: "A:1", Direction: "incoming"},
			},
		}, true
	})

	outgoing, err := lc.GetLinksForLocale(context.Background(), "A", "de", "outgoing")
	if err != nil {
		t.Fatalf("GetLinksForLocale: %v", err)
	}
	if len(outgoing) != 1 || outgoing[0].Direction != "outgoing" {
		t.Fatalf("GetLinksForLocale = %v, want 1 outgoing entry", outgoing)
	}

	all, err := lc.GetLinksForLocale(context.Background(), "A", "", "")
	if err != nil {
		t.Fatalf("GetLinksForLocale (all): %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("GetLinksForLocale (all) = %d, want 2", len(all))
	}
}

func TestGetLinkableChannelsForLocaleRoleFilter(t *testing.T) {
	lc := NewLinkCoordinator(func(_ string) (LinkClient, bool) {
		return &fakeLinkClient{
			linkable: []LinkableChannel{
				{Address: "A:1", ChannelType: "SWITCH_VIRTUAL_RECEIVER"},
				{Address: "A:2", ChannelType: "DIMMER_VIRTUAL_RECEIVER"},
			},
		}, true
	})

	filtered, err := lc.GetLinkableChannelsForLocale(context.Background(), "A", "de", "SWITCH_VIRTUAL_RECEIVER")
	if err != nil {
		t.Fatalf("GetLinkableChannelsForLocale: %v", err)
	}
	if len(filtered) != 1 || filtered[0].ChannelType != "SWITCH_VIRTUAL_RECEIVER" {
		t.Fatalf("expected 1 SWITCH_VIRTUAL_RECEIVER entry, got %v", filtered)
	}
}

// ---- L-A5-36: DeviceLink extended fields ----

func TestDeviceLinkExtendedFieldsPresent(t *testing.T) {
	lk := DeviceLink{
		SenderAddress:   "A:1",
		ReceiverAddress: "B:1",
		LinkType:        "DIRECT",
		PeerName:        "Switch Wohnzimmer",
		PeerInterface:   "HmIP-RF",
		PeerSerial:      "000E4B",
		PeerType:        "HmIP-PSM",
		PeerSubtype:     "",
		Direction:       "outgoing",
	}
	if lk.PeerType != "HmIP-PSM" {
		t.Fatalf("PeerType = %q, want HmIP-PSM", lk.PeerType)
	}
	if lk.LinkType != "DIRECT" {
		t.Fatalf("LinkType = %q, want DIRECT", lk.LinkType)
	}
	if lk.PeerInterface != "HmIP-RF" {
		t.Fatalf("PeerInterface = %q, want HmIP-RF", lk.PeerInterface)
	}
}

// ---- Helpers ----

// testNameOverrider implements DeviceNameOverrideChecker for tests.
type testNameOverrider struct {
	names map[string]string
}

func (f *testNameOverrider) GetNameOverride(addr string) (string, bool) {
	n, ok := f.names[addr]
	return n, ok
}

// newDCFullWithRegistries reuses the newDCFull helper from device_deep_test.go.
// The registries are exposed so tests can verify state directly.
var _ = registry.NewDeviceRegistry // ensure registry import is used
