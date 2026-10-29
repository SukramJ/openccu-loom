// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package matteradapter_test

import (
	"context"
	"fmt"
	"log/slog"
	"testing"

	"github.com/SukramJ/go-fabric/cluster/onoff"
	"github.com/SukramJ/go-fabric/cluster/spec"
	"github.com/SukramJ/go-fabric/contract"
	"github.com/SukramJ/go-fabric/endpoint"

	"github.com/SukramJ/openccu-loom/internal/north/matteradapter"
)

// goldenBridgedEndpointCount is the number of bridged endpoints the golden
// fleet assembles into (see testdata/topology.golden.json: nine endpoints
// minus the root and the aggregator). An endpoint lost to a refused device
// composition changes this number instead of silently degrading.
const goldenBridgedEndpointCount = 7

// acceptedDeviceTypeViolations declares device-type conformance violations
// on bridged endpoints that are known and deliberately tolerated. The key is
// "<device type>|<requirement>|<kind>", the value the reason. Empty means
// every bridged endpoint of the golden fleet conforms to its device types.
var acceptedDeviceTypeViolations = map[string]string{}

func assembleGolden(t *testing.T) *endpoint.Topology {
	t.Helper()
	a := matteradapter.New(goldenConfig(t), slog.New(slog.DiscardHandler))
	top, err := assembleDevicesWith(context.Background(), a, newFakeStore(), goldenEndpointConfig(t), []matteradapter.DeviceSnapshot{{
		CentralName: goldenCentralName,
		Devices:     goldenFleet(t),
	}})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	return top
}

// TestEveryBridgedEndpointIsBuiltThroughTheDeviceLayer pins that the
// assembler hands every bridged endpoint the go-fabric device layer's spec
// source, not a hand-built loom profile: the cluster-serving endpoints
// (their servers declared with device.HostServer) and the sensor endpoints
// (their measurement declared with device.Measurement) alike. A hand-built
// endpoint is a composition the device layer refused, which the assembler
// only logs.
func TestEveryBridgedEndpointIsBuiltThroughTheDeviceLayer(t *testing.T) {
	top := assembleGolden(t)
	bridged := top.Bridged()
	if len(bridged) != goldenBridgedEndpointCount {
		t.Errorf("bridged endpoints = %d, want %d (an endpoint was lost or added)", len(bridged), goldenBridgedEndpointCount)
	}
	var servers, sensors int
	for _, ep := range bridged {
		if _, ok := ep.Source.(interface{ Spec() endpoint.Spec }); !ok {
			t.Errorf("endpoint %d (device type 0x%04X) source %T is not built through the device layer",
				ep.ID, ep.DeviceType, ep.Source)
			continue
		}
		if ep.Measurement != nil {
			sensors++
		} else {
			servers++
		}
	}
	// The golden fleet has both shapes; a fleet where one of them vanished
	// would let this guard pass without judging that shape at all.
	if servers == 0 || sensors == 0 {
		t.Errorf("cluster-serving endpoints = %d, sensor endpoints = %d; the golden fleet must exercise both", servers, sensors)
	}
}

// TestAssembledEndpointsSatisfyTheirDeviceTypes runs the device-type
// conformance validator over the assembled golden topology and fails on any
// violation of a bridged endpoint that is not declared in
// acceptedDeviceTypeViolations. Root and aggregator violations are the
// daemon's to satisfy, not the assembler's, and are ignored here.
func TestAssembledEndpointsSatisfyTheirDeviceTypes(t *testing.T) {
	top := assembleGolden(t)
	bridged := map[uint16]bool{}
	for _, ep := range top.Bridged() {
		bridged[ep.ID] = true
	}
	for _, v := range endpoint.ValidateDeviceTypes(top) {
		if !bridged[v.Endpoint] {
			continue
		}
		key := fmt.Sprintf("%s|%s|%s", v.DeviceType, v.Requirement, v.Kind)
		if _, ok := acceptedDeviceTypeViolations[key]; ok {
			continue
		}
		t.Errorf("endpoint %d violates its device type: %s\n  full: %+v\n  fix the profile, or declare %q in acceptedDeviceTypeViolations with a reason",
			v.Endpoint, v, v, key)
	}
}

// TestNoBridgedEndpointCarriesAGeneratedDefaultServer pins that loom supplies
// every cluster it advertises itself. A device-layer default server
// (spec.Server, or an onoff.Server built without a Sink) is rebuilt on
// every reassembly and answers a command without the CCU ever hearing of
// it, so it would hide a profile defect behind a conforming ServerList.
//
// The OnOff cluster is go-fabric's onoff.Server on every profile that
// serves it (ADR 0084 phase 2), so its type cannot tell loom's server from
// the device layer's default. Its effect can: loom's server routes On to
// the profile's CCU write, and the fixture's data points carry no writer,
// so the Sink refuses and the command answers an error while the
// attribute keeps its value. A default (Sink-less) server stores On and
// answers Success — the result this test turns red on.
func TestNoBridgedEndpointCarriesAGeneratedDefaultServer(t *testing.T) {
	top := assembleGolden(t)
	for _, ep := range top.Bridged() {
		src, ok := ep.Source.(interface {
			Servers() []contract.ClusterServer
		})
		if !ok {
			continue
		}
		for _, s := range src.Servers() {
			switch s := s.(type) {
			case *spec.Server:
				t.Errorf("endpoint %d (device type 0x%04X) carries a generated default %T for cluster 0x%04X; the profile does not serve it",
					ep.ID, ep.DeviceType, s, s.MatterClusterID())
			case *onoff.Server:
				if _, err := s.MatterInvoke(context.Background(), onoff.CmdOn, nil); err == nil {
					t.Errorf("endpoint %d (device type 0x%04X): OnOff On answered Success without reaching a CCU writer — the onoff.Server carries no Sink (a device-layer default, or a profile that built it without one)",
						ep.ID, ep.DeviceType)
				}
				if on, _ := s.MatterRead(onoff.AttrOnOff); on != false {
					t.Errorf("endpoint %d (device type 0x%04X): OnOff became %v after a refused On; the attribute must keep the CCU's value",
						ep.ID, ep.DeviceType, on)
				}
			}
		}
	}
}
