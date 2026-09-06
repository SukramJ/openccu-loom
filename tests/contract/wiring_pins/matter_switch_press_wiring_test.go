// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package wiring_pins

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/north/matteradapter"

	matterbridge "github.com/SukramJ/go-fabric/bridge"
	matterendpoint "github.com/SukramJ/go-fabric/endpoint"
	"github.com/SukramJ/go-fabric/mdns"

	loomendpoint "github.com/SukramJ/openccu-loom/internal/store/matterendpoint"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// Matter §1.13 GenericSwitch, the identifiers the assertion below reads
// out of the event log. Cluster and event IDs are fixed by the spec and
// mirrored from matter.js packages/types/src/clusters/switch.ts.
const (
	genericSwitchCluster uint32 = 0x003B
	evInitialPress       uint32 = 0x01
	evShortRelease       uint32 = 0x03
)

// TestAPhysicalPressReachesTheMatterEventLog pins the one seam that
// carries a button from the CCU to a commissioner: the reassemble
// wiring that subscribes a bridged GenericSwitch cluster to its
// endpoint's press source.
//
// The seam is invisible when it breaks. It is a pair of type
// assertions in go-fabric bridge/bridge.go — the cluster
// server to [contract.SwitchEventEmitter], the endpoint's
// measurement source to the bridge's matterSwitchSubscribable. Both
// are optional capability checks, so a failed assertion is not an
// error, not a log line and not a compile failure: the topology
// assembles, the endpoint advertises Switch (0x003B), a commissioner
// subscribes happily, and no press ever arrives. It broke exactly that
// way once, when the two sides each declared their own emitter
// interface — identical method sets, distinct types, so the assertion
// could never match.
//
// A compile-time `var _` assertion in the bridge proves the types line
// up. It cannot prove a press reaches the event log, because it never
// runs a press. This does: real model DPs, the real persistent store,
// the real constructor, the real Start path, one real value update.
func TestAPhysicalPressReachesTheMatterEventLog(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	// One KEY channel with the two press parameters the resolver
	// materialises for a HM button. The assembler consolidates both
	// into a single ButtonGroup behind one GenericSwitch endpoint.
	dev := device.New(device.Config{Address: "BTN0001", Name: "Taster"})
	ch := dev.AddChannel("BTN0001:1", 1, "KEY", hmenum.ParamsetKeyValues)
	pressShort := pressEventDP("BTN0001:1", hmenum.ParameterPressShort)
	ch.Put(pressShort)
	ch.Put(pressEventDP("BTN0001:1", hmenum.ParameterPressLong))

	db, err := sqlite.Open(ctx, ":memory:")
	if err != nil {
		t.Fatalf("sqlite.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	// Same composition the root performs in
	// cmd/openccu-loom/daemon_matter.go: the daemon owns the assembler,
	// walks its own model, and hands the bridge one closure that returns
	// the finished topology. The bridge never sees a device.
	identity := loomendpoint.New(db)
	walk := func(_ context.Context) []matteradapter.DeviceSnapshot {
		return []matteradapter.DeviceSnapshot{{
			CentralName:   "ccu1",
			Devices:       []*device.Device{dev},
			ModelComplete: true,
		}}
	}
	assembler, err := matteradapter.New(identity, matteradapter.Config{
		VendorID:  0x1234,
		ProductID: 0x5678,
		NodeLabel: "switch-press-wiring-pin",
	}, nil)
	if err != nil {
		t.Fatalf("matteradapter.New: %v", err)
	}
	snapshotter := func(ctx context.Context) (*matterendpoint.Topology, error) {
		return assembler.AssembleDevices(ctx, walk(ctx))
	}
	bridge, err := matterbridge.New(
		snapshotter,
		mdns.NewNoop(),
		matterbridge.Config{
			Listen:    ":0",
			VendorID:  0x1234,
			ProductID: 0x5678,
			NodeLabel: "switch-press-wiring-pin",
		},
		nil,
	)
	if err != nil {
		t.Fatalf("matterbridge.New: %v", err)
	}
	t.Cleanup(func() {
		stopCtx, stopCancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer stopCancel()
		_ = bridge.Stop(stopCtx)
	})
	if err := bridge.Start(ctx); err != nil {
		t.Fatalf("bridge.Start: %v", err)
	}

	// Locate the consolidated button endpoint the assembler produced.
	topo := bridge.Topology()
	if topo == nil {
		t.Fatal("bridge has no topology after Start")
	}
	var (
		switchEP uint16
		found    int
	)
	for _, ep := range topo.Bridged() {
		key, ok := ep.SourceKey.(loomendpoint.SourceKey)
		if ok && key.DPKey == matteradapter.ButtonGroupDPKey {
			switchEP = ep.ID
			found++
		}
	}
	if found != 1 {
		t.Fatalf("expected exactly one consolidated GenericSwitch endpoint for the KEY channel, got %d", found)
	}

	// The physical press. Everything after this point is production
	// code: the DP's update stream, the button group's press-cycle
	// state machine, the emitter the reassemble wiring handed it, the
	// cluster's Fire* methods, the bridge's event emitter, the log a
	// subscribed commissioner reads from.
	pressShort.OnEvent(true)

	countEvents := func(eventID uint32) int {
		return len(bridge.EventLog().Query(switchEP, genericSwitchCluster, eventID, 0))
	}
	if got := countEvents(evInitialPress); got != 1 {
		t.Fatalf("InitialPress events on endpoint %d after one press = %d, want 1 — "+
			"the reassemble wiring did not subscribe the GenericSwitch cluster to its press source, "+
			"so no button press would reach a commissioner", switchEP, got)
	}
	if got := countEvents(evShortRelease); got != 1 {
		t.Fatalf("ShortRelease events on endpoint %d after one press = %d, want 1 — "+
			"the press-cycle gesture reached the event log only half-narrated", switchEP, got)
	}
}

// pressEventDP builds an event-only press DP the way the data-point
// resolver does for a KEY / KEY_TRANSCEIVER channel.
func pressEventDP(channelAddr string, p hmenum.Parameter) *generic.Button {
	return generic.NewButton(generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "iface",
			ChannelAddress: channelAddr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(p),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeAction,
			Operations: hmenum.OperationsEvent,
		},
	})
}
