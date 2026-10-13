// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package main

import (
	"bytes"
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/SukramJ/go-fabric/bridge/bridgetest"
	"github.com/SukramJ/go-fabric/im"
	"github.com/SukramJ/go-fabric/tlv"

	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/model/custom"
	switchdev "github.com/SukramJ/openccu-loom/internal/model/custom/switch"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// Matter cluster and command identifiers read by the assertions below
// (core spec §1.3 Groups, §11.2 GroupKeyManagement), mirrored from
// matter.js packages/types/src/clusters/groups.ts and
// group-key-management.ts.
const (
	groupsClusterID      uint32 = 0x0004
	groupsCmdAddGroup    uint32 = 0x00
	groupsCmdViewGroup   uint32 = 0x01
	gkmClusterID         uint32 = 0x003F
	gkmAttrGroupKeyMap   uint32 = 0x0000
	gkmCmdKeySetWrite    uint32 = 0x00
	groupsTestGroupID    uint16 = 0x0101
	groupsTestKeySetID   uint16 = 0x01A1
	groupsTestPasscode   uint32 = 20202021
	groupsTestDiscrim    uint16 = 3840
	groupsTestFabricID   uint64 = 0x0FAB
	groupsTestDeviceNode uint64 = 0x0D0D
)

// bootCommissionedBridge boots the Matter bridge through the daemon's own
// startMatterBridge with one switch device, commissions it over the wire with
// go-fabric's test commissioner and returns the CASE session plus the
// bridged OnOff endpoint.
func bootCommissionedBridge(t *testing.T) (context.Context, *matterBridgeBundle, *bridgetest.Session, uint16) {
	t.Helper()

	cfg := config.Default()
	cfg.North.Matter.Enabled = true
	cfg.North.Matter.MDNSAdvertise = "noop"
	cfg.North.Matter.Listen = "127.0.0.1:0"
	cfg.North.Matter.Commissioning.Passcode = groupsTestPasscode
	cfg.North.Matter.Commissioning.Iterations = 1000
	cfg.North.Matter.CASE.NodeID = groupsTestDeviceNode
	cfg.North.Matter.CASE.FabricID = groupsTestFabricID
	cfg.North.Matter.Discriminator = groupsTestDiscrim
	cfg.DataDir = t.TempDir()
	cfg.Centrals = []config.CentralConfig{{Name: "ccu-01", Host: "127.0.0.1", Interfaces: []config.InterfaceSpec{{Name: "HmIP-RF"}}}}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	// A switch channel with a real custom data point: the assembler
	// projects it as an OnOff Plug-in Unit endpoint, a device type whose
	// mandatory server list includes Groups.
	reg := buildTestRegistry(t, "ccu-01")
	unit, ok := reg.Get("ccu-01")
	if !ok {
		t.Fatal("central ccu-01 not registered")
	}
	dev := device.New(device.Config{Address: "SWT0001", Name: "Steckdose"})
	chAddr := "SWT0001:4"
	ch := dev.AddChannel(chAddr, 4, "SWITCH", hmenum.ParamsetKeyValues)
	ch.Put(generic.NewSwitch(generic.Spec{
		Key: hmtypes.DataPointKey{
			ChannelAddress: chAddr,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterState),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	}))
	ch.SetCustomDataPoint(switchdev.New(ch, custom.RebasedChannelGroupConfig{}))
	unit.ModelRegistry.Put(dev)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	db := openTestLoomDB(t)
	bundle := startMatterBridge(ctx, cfg, reg, db, health.NewTracker(), nil, slog.New(slog.DiscardHandler))
	if bundle == nil {
		t.Fatal("startMatterBridge returned nil")
	}
	t.Cleanup(bundle.stop)
	exposeEveryCandidate(ctx, t, bundle, reg)

	topo := bundle.bridge.Topology()
	if topo == nil {
		t.Fatal("bridge has no topology after Start")
	}
	var onOffEP uint16
	for _, ep := range topo.Bridged() {
		if ep.DeviceType == 0x010A { // OnOffPlugInUnit
			onOffEP = ep.ID
			break
		}
	}
	if onOffEP == 0 {
		t.Fatalf("no OnOffPlugInUnit endpoint among %d bridged endpoints", len(topo.Bridged()))
	}

	cmr := bridgetest.Dial(t, bundle.bridge.LocalAddr(), bridgetest.Options{
		Passcode:      groupsTestPasscode,
		Discriminator: groupsTestDiscrim,
		FabricID:      groupsTestFabricID,
		NodeID:        groupsTestDeviceNode,
		Seed:          1,
	})
	sess, err := cmr.Commission(ctx)
	if err != nil {
		t.Fatalf("Commission: %v", err)
	}
	return ctx, bundle, sess, onOffEP
}

// TestStartMatterBridgeMountsGroupsOnBridgedEndpoints pins, through the
// daemon's own Matter start path, that group membership is stack state
// the bridged endpoints can use. go-fabric mounts the Groups server on an
// endpoint only when endpoint.Config.Groups is set, and the old read-only
// stub is gone, so without the manager startMatterBridge hands the
// assembler every OnOff endpoint serves no Groups cluster and a
// controller's AddGroup is refused with UNSUPPORTED_CLUSTER.
//
// The effect is observed from outside: a commissioner joins the running
// bridge over the wire, writes the group key map and key set, then adds a
// group on the bridged OnOff endpoint and views it back.
func TestStartMatterBridgeMountsGroupsOnBridgedEndpoints(t *testing.T) {
	t.Parallel()
	ctx, _, sess, onOffEP := bootCommissionedBridge(t)

	// AddGroup is refused unless the group already has a key mapping
	// (core spec §1.3.7.1). The key set and the map are written on the
	// root endpoint, as a controller does.
	rootInvoke := im.ConcreteCommandPath{Endpoint: 0, Cluster: gkmClusterID, Command: gkmCmdKeySetWrite}
	res, err := sess.Invoke(ctx, rootInvoke, func(e *tlv.Encoder, tag tlv.Tag) {
		e.StartStruct(tag)
		e.StartStruct(tlv.ContextTag(0))
		e.PutUint(tlv.ContextTag(0), uint64(groupsTestKeySetID))
		e.PutUint(tlv.ContextTag(1), 0) // TrustFirst
		e.PutOctets(tlv.ContextTag(2), bytes.Repeat([]byte{0xA5}, 16))
		e.PutUint(tlv.ContextTag(3), 1)
		e.PutNull(tlv.ContextTag(4))
		e.PutNull(tlv.ContextTag(5))
		e.PutNull(tlv.ContextTag(6))
		e.PutNull(tlv.ContextTag(7))
		e.PutUint(tlv.ContextTag(8), 0)
		_ = e.EndContainer()
		_ = e.EndContainer()
	})
	if err != nil || res.Status != im.StatusSuccess {
		t.Fatalf("KeySetWrite: status=%v err=%v", statusOf(res), err)
	}
	status, err := sess.Write(ctx, im.ConcreteAttributePath{
		Endpoint: 0, Cluster: gkmClusterID, Attribute: gkmAttrGroupKeyMap,
		HasEndpoint: true, HasCluster: true, HasAttribute: true,
	}, func(e *tlv.Encoder, tag tlv.Tag) {
		e.StartArray(tag)
		e.StartStruct(tlv.AnonymousTag())
		e.PutUint(tlv.ContextTag(1), uint64(groupsTestGroupID))
		e.PutUint(tlv.ContextTag(2), uint64(groupsTestKeySetID))
		_ = e.EndContainer()
		_ = e.EndContainer()
	})
	if err != nil || status != im.StatusSuccess {
		t.Fatalf("GroupKeyMap write: status=%v err=%v", status, err)
	}

	add, err := sess.Invoke(ctx, im.ConcreteCommandPath{Endpoint: onOffEP, Cluster: groupsClusterID, Command: groupsCmdAddGroup},
		func(e *tlv.Encoder, tag tlv.Tag) {
			e.StartStruct(tag)
			e.PutUint(tlv.ContextTag(0), uint64(groupsTestGroupID))
			e.PutUTF8(tlv.ContextTag(1), "pin")
			_ = e.EndContainer()
		})
	if err != nil {
		t.Fatalf("AddGroup on endpoint %d: %v", onOffEP, err)
	}
	if add.Status != im.StatusSuccess {
		t.Fatalf("AddGroup on OnOff endpoint %d answered status %v, want a response — "+
			"startMatterBridge did not hand the assembler a group manager, so the Groups cluster is not mounted", onOffEP, add.Status)
	}
	if st, _ := add.Fields.Child(0); st.Uint != 0 {
		t.Fatalf("AddGroupResponse status = %d, want 0 (Success)", st.Uint)
	}

	view, err := sess.Invoke(ctx, im.ConcreteCommandPath{Endpoint: onOffEP, Cluster: groupsClusterID, Command: groupsCmdViewGroup},
		func(e *tlv.Encoder, tag tlv.Tag) {
			e.StartStruct(tag)
			e.PutUint(tlv.ContextTag(0), uint64(groupsTestGroupID))
			_ = e.EndContainer()
		})
	if err != nil {
		t.Fatalf("ViewGroup on endpoint %d: %v", onOffEP, err)
	}
	if st, _ := view.Fields.Child(0); st.Uint != 0 {
		t.Fatalf("ViewGroupResponse status = %d, want 0 (Success)", st.Uint)
	}
	if name, _ := view.Fields.Child(2); name.String != "pin" {
		t.Fatalf("ViewGroupResponse name = %q, want %q", name.String, "pin")
	}
}

func statusOf(r *bridgetest.InvokeResult) im.StatusCode {
	if r == nil {
		return 0xFF
	}
	return r.Status
}

// TestStartMatterBridgePersistsServerSubscriptions pins the subscription
// store attach by its effect: a controller's subscription on the running
// bridge leaves a row in matter_server_subscriptions, which is what lets
// the bridge resume it after a restart. Without the attach the bridge
// keeps subscriptions in memory only and the table stays empty.
func TestStartMatterBridgePersistsServerSubscriptions(t *testing.T) {
	t.Parallel()
	ctx, bundle, sess, onOffEP := bootCommissionedBridge(t)

	sub, err := sess.Subscribe(ctx, 0, 60, im.ConcreteAttributePath{
		Endpoint: onOffEP, Cluster: 0x0006, Attribute: 0x0000,
		HasEndpoint: true, HasCluster: true, HasAttribute: true,
	})
	if err != nil {
		t.Fatalf("Subscribe on endpoint %d: %v", onOffEP, err)
	}
	// The bridge records a subscription once it is active, after the
	// SubscribeResponse the controller just received, so the row lands a
	// moment later than the response; wait for it rather than racing it.
	deadline := time.Now().Add(5 * time.Second)
	var payloads [][]byte
	for {
		payloads, err = bundle.store.LoadServerSubscriptions(ctx)
		if err != nil {
			t.Fatalf("LoadServerSubscriptions: %v", err)
		}
		if len(payloads) == 1 || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(payloads) != 1 {
		t.Fatalf("persisted server subscriptions = %d after subscription %d, want 1 — "+
			"the bridge was not given a subscription store, so a restart drops every controller subscription", len(payloads), sub.ID)
	}
}
