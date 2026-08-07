// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/north/filter"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/internal/store/visibility"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// putMasterIntDP plants an observed integer MASTER-paramset entry on ch —
// the shape seedMasterValues produces during hydration.
func putMasterIntDP(ch *device.Channel, param string, value int32) *generic.Sensor[int32] {
	dp := generic.NewIntegerSensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyMaster,
			Parameter:      param,
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeInteger,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite,
		},
	})
	dp.OnEvent(value)
	ch.PutMaster(dp)
	return dp
}

func snapshotTestBridge(reg *central.Registry, visReg *visibility.Registry) (*EventBridge, *mqtt.NoopClient) {
	pub := mqtt.NewNoopClient()
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{
		Base: "openccu-loom", CentralName: "ccu-01", RawEnabled: true,
		HADiscoveryEnabled: true,
		DiscoveryBuilder:   mqtt.NewDefaultDiscoveryBuilder(mqtt.NewTopicBuilder("openccu-loom"), "ccu-01"),
		Visibility:         filter.NewAdapter(visReg),
	}, pub)
	eb := NewEventBridge(reg, nil, mqtt.NewWiring(bridge, nil)).
		WithVisibility(filter.NewAdapter(visReg))
	return eb, pub
}

// TestPublishInitialSnapshotSkipsCentralMidBringUp is the regression tripwire
// for the boot race that flooded brokers with MASTER-paramset topics: the
// boot-time PublishInitialSnapshot runs on the daemon's main path while the
// readiness-gated bring-up hydrates devices in the background. A device that
// is already hydrated (MASTER values seeded) but whose visibility passes have
// not run yet published its ENTIRE MASTER paramset — retained, forever.
//
// Contract: a central whose southbound bring-up has not latched ready is
// skipped by the snapshot entirely. The CentralSouthboundReadyEvent path
// publishes it after finishIngest applied the suppression marks.
func TestPublishInitialSnapshotSkipsCentralMidBringUp(t *testing.T) {
	t.Parallel()

	reg, dev := registryWithDeviceNotReady(t)
	ch1 := dev.AddChannel("0001ABCD:1", 1, "TEST", hmenum.ParamsetKeyValues)
	stateDP := generic.NewDataPoint[bool](generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: "0001ABCD:1",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "STATE",
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
	ch1.Put(stateDP)
	if !stateDP.OnWireValue(true) {
		t.Fatalf("OnWireValue refused to seed")
	}
	ch8 := dev.AddChannel("0001ABCD:8", 8, "SWITCH_WEEK_PROFILE", hmenum.ParamsetKeyValues)
	wpDP := putMasterIntDP(ch8, "01_WP_WEEKDAY", 0)

	visReg := visibility.NewRegistry()
	eb, pub := snapshotTestBridge(reg, visReg)
	eb.Start(context.Background())
	defer eb.Stop()

	// Mid-bring-up: hydrated, unmarked, NOT ready. The snapshot must not
	// touch this central at all.
	eb.PublishInitialSnapshot(context.Background())
	if got := len(pub.Published()); got != 0 {
		t.Fatalf("snapshot published %d topics for a not-ready central, want 0 (first: %s)",
			got, pub.Published()[0].Topic)
	}

	// Bring-up completes: suppression marks land, ready latches. Now the
	// snapshot publishes the visible surface — and only that.
	visibility.ApplyIgnoredParameterMarks(dev, visReg.Parameter())
	unit, _ := reg.Get("ccu-01")
	unit.MarkSouthboundReady()
	eb.PublishInitialSnapshot(context.Background())

	var stateSeen, masterLeaks int
	for _, p := range pub.Published() {
		if strings.HasSuffix(p.Topic, "/0001ABCD/1/values/STATE") {
			stateSeen++
		}
		if strings.Contains(p.Topic, "01_WP_WEEKDAY") || strings.Contains(p.Topic, "01_wp_weekday") {
			masterLeaks++
		}
	}
	if stateSeen == 0 {
		t.Fatalf("ready central's STATE snapshot missing after MarkSouthboundReady")
	}
	if masterLeaks != 0 {
		t.Fatalf("suppressed MASTER DP leaked %d topics after marks were applied", masterLeaks)
	}
	if wpDP.Visible() {
		t.Fatalf("precondition drift: 01_WP_WEEKDAY should be suppressed by the marks pass")
	}
}

// TestPublishInitialSnapshotSkipsSuppressedUnobservedDP pins the unobserved
// branch of registerAndLoadDP to the same visibility rule as the observed
// path: a suppressed DP on a channel without a custom DP publishes NOTHING —
// no slot state, no /config companion, no discovery. Before the fix the raw
// slot + /config published unconditionally, so every ignored parameter
// (BOOTED, INSTALL_TEST, *_STATUS) still landed retained on the broker.
func TestPublishInitialSnapshotSkipsSuppressedUnobservedDP(t *testing.T) {
	t.Parallel()

	reg, dev := registryWithDevice(t)
	ch := dev.AddChannel("0001ABCD:0", 0, "MAINTENANCE", hmenum.ParamsetKeyValues)
	booted := generic.NewDataPoint[bool](generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "HmIP-RF",
			ChannelAddress: "0001ABCD:0",
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      "BOOTED",
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	})
	ch.Put(booted) // intentionally unobserved

	visReg := visibility.NewRegistry()
	visibility.ApplyIgnoredParameterMarks(dev, visReg.Parameter())
	if booted.Visible() {
		t.Fatalf("precondition: BOOTED must be suppressed by the marks pass")
	}

	eb, pub := snapshotTestBridge(reg, visReg)
	eb.Start(context.Background())
	defer eb.Stop()

	eb.PublishInitialSnapshot(context.Background())

	for _, p := range pub.Published() {
		if strings.Contains(p.Topic, "BOOTED") || strings.Contains(p.Topic, "booted") {
			t.Fatalf("suppressed unobserved DP leaked topic %s", p.Topic)
		}
	}
}
