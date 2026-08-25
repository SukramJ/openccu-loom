// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/model/naming"
	"github.com/SukramJ/openccu-loom/internal/north/mqtt"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// A handful of address classes repeat verbatim across every CCU: the
// virtual-remote buses (BidCoS-RF, BidCoS-Wir, HmIP-RCV-1), the internal
// INT000* addresses, and the hub pseudo-addresses. Their routing key is
// therefore namespaced by the CCU's serial — without it, two CCUs
// produce the identical unique_id and Home Assistant keeps whichever
// entity it saw first, silently dropping the other CCU's.
//
// The serial reaches the discovery builder by a different route than the
// devices do, and later: the composition root's early stamp runs while
// SystemInformation is still empty, and the authoritative one happens on
// the hub publisher's ready-driven re-start. The device snapshot does not
// wait for it.

// TestDeviceDiscoveryNeverPublishesAnUnscopedUniqueID pins the invariant
// that survives whatever order those two paths run in: a device whose
// address class needs the serial must not reach the discovery plane
// without one.
//
// Publishing nothing is the correct outcome here. An entity that never
// appears is a visible gap an operator can act on; two CCUs sharing one
// unique_id is a silent, permanent loss of the second one — and it
// persists, because the payload is retained.
func TestDeviceDiscoveryNeverPublishesAnUnscopedUniqueID(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		address string
	}{
		{name: "virtual remote", address: "BidCoS-RF"},
		{name: "internal device", address: "INT0000001"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			payloads := publishVirtualRemoteDiscovery(t, "ccu-01", tc.address, "")
			for topic, uniqueID := range payloads {
				if strings.Contains(uniqueID, "loom__") {
					t.Errorf("%s carries unique_id %q — the empty serial leaves the CCU discriminator "+
						"blank, so every CCU in a multi-CCU daemon publishes this exact id and Home "+
						"Assistant keeps only the first. Skip the payload until the serial is known.",
						topic, uniqueID)
				}
			}
		})
	}
}

// TestTwoCentralsWithoutSerialsDoNotShareUniqueIDs is the same defect
// stated as the collision it causes, which is how it was found on a
// production broker: `loom__bidcos_rf_10_event` published under two
// different CCUs.
func TestTwoCentralsWithoutSerialsDoNotShareUniqueIDs(t *testing.T) {
	t.Parallel()

	first := publishVirtualRemoteDiscovery(t, "kearneygo", "BidCoS-RF", "")
	second := publishVirtualRemoteDiscovery(t, "kearney-loc", "BidCoS-RF", "")

	shared := make([]string, 0)
	seen := make(map[string]struct{}, len(first))
	for _, id := range first {
		seen[id] = struct{}{}
	}
	for _, id := range second {
		if _, dup := seen[id]; dup {
			shared = append(shared, id)
		}
	}
	if len(shared) > 0 {
		t.Errorf("two CCUs published the same unique_id(s): %v\n"+
			"  A virtual-remote address is identical on every CCU, so the serial is the only thing "+
			"separating them. Home Assistant keys its entity registry on unique_id and keeps the "+
			"first — the second CCU's entities are lost, and the retained payload keeps them lost.",
			shared)
	}
}

// TestDeviceDiscoveryScopesUniqueIDsOnceTheSerialIsKnown is the positive
// half: with the serial registered, the same two CCUs produce distinct
// ids and both sets of entities survive.
func TestDeviceDiscoveryScopesUniqueIDsOnceTheSerialIsKnown(t *testing.T) {
	t.Parallel()

	first := publishVirtualRemoteDiscovery(t, "kearneygo", "BidCoS-RF", "3014F711A0001F0123456789")
	second := publishVirtualRemoteDiscovery(t, "kearney-loc", "BidCoS-RF", "3014F711A0001F9876543210")

	if len(first) == 0 || len(second) == 0 {
		t.Fatal("no discovery payloads were published with a serial present — the fixture proves nothing")
	}
	for _, id := range first {
		if !strings.Contains(id, "23456789") {
			t.Errorf("unique_id %q does not carry the CCU's serial discriminator", id)
		}
	}
	seen := make(map[string]struct{}, len(first))
	for _, id := range first {
		seen[id] = struct{}{}
	}
	for _, id := range second {
		if _, dup := seen[id]; dup {
			t.Errorf("unique_id %q is shared by both CCUs even though both serials are known", id)
		}
	}
}

// publishVirtualRemoteDiscovery runs a device of the given address class
// through the real EventBridge and returns every discovery payload's
// unique_id, keyed by topic. An empty serial models the window before the
// hub bring-up has resolved it.
func publishVirtualRemoteDiscovery(t *testing.T, centralName, address, serial string) map[string]string {
	t.Helper()

	dev := device.New(device.Config{
		InterfaceID: "BidCos-RF",
		Interface:   hmenum.InterfaceBidCosRF,
		Address:     address,
		Model:       "HM-RCV-50",
		Name:        "Virtuelle Fernbedienung",
	})
	ch := dev.AddChannel(address+":10", 10, "KEY", hmenum.ParamsetKeyValues)
	// A virtual remote's channels carry click events, which is what the
	// production fleet publishes ~900 of. The resolver decides the shape;
	// hand-building one would let the fixture pick the outcome.
	for _, param := range []hmenum.Parameter{hmenum.ParameterPressShort, hmenum.ParameterPressLong} {
		dp := resolveDataPoint(generic.Spec{
			Key: hmtypes.DataPointKey{
				InterfaceID:    "BidCos-RF",
				ChannelAddress: ch.Address,
				ParamsetKey:    hmenum.ParamsetKeyValues,
				Parameter:      string(param),
			},
			Descriptor: hmproto.ParameterData{
				Type:       hmenum.ParameterTypeAction,
				Operations: hmenum.OperationsEvent,
			},
		})
		if dp == nil {
			t.Fatalf("the resolver produced no data point for %s", param)
		}
		ch.Put(dp)
	}
	for _, dp := range ch.DataPoints() {
		if init, ok := dp.(namingInitializer); ok {
			init.SetNameData(device.BuildDataPointName(ch, string(dp.Parameter()), ""))
			init.SetPathData(naming.NewDataPointPathData(
				"", hmtypes.NewWireInterfaceID("", hmenum.InterfaceBidCosRF), ch.Address, ch.Number, naming.BucketValues, string(dp.Parameter()),
			))
			init.SetIsInMultipleChannels(false)
		}
	}

	c, err := central.New(central.Config{Name: centralName})
	if err != nil {
		t.Fatalf("central: %v", err)
	}
	reg := central.NewRegistry()
	if err := reg.Register(c); err != nil {
		t.Fatalf("register: %v", err)
	}
	c.ModelRegistry.Put(dev)
	c.MarkSouthboundReady()

	pub := mqtt.NewNoopClient()
	bridge := mqtt.NewBridge(mqtt.BridgeConfig{
		Base:               "openccu-loom",
		CentralName:        centralName,
		RawEnabled:         true,
		HADiscoveryEnabled: true,
	}, pub)
	// The serial reaches the builder through HubInfo. An empty one is the
	// state the daemon is in until the hub bring-up resolves it.
	bridge.SetHubInfoFor(centralName, mqtt.HubInfo{Name: centralName, Serial: serial})

	eb := NewEventBridge(reg, nil, mqtt.NewWiring(bridge, nil))
	eb.Start(context.Background())
	defer eb.Stop()
	eb.PublishInitialSnapshot(context.Background())

	out := make(map[string]string)
	for _, p := range pub.Published() {
		if !strings.HasPrefix(p.Topic, "homeassistant/") || !strings.HasSuffix(p.Topic, "/config") {
			continue
		}
		var body map[string]any
		if err := json.Unmarshal(p.Payload, &body); err != nil {
			continue
		}
		if id, ok := body["unique_id"].(string); ok && id != "" {
			out[p.Topic] = id
		}
	}
	return out
}
