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
	a, err := matteradapter.New(newFakeStore(), goldenConfig(t), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	top, err := a.AssembleDevices(context.Background(), []matteradapter.DeviceSnapshot{{
		CentralName:   goldenCentralName,
		Devices:       goldenFleet(t),
		ModelComplete: true,
	}})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	return top
}

// TestEveryBridgedEndpointIsBuiltThroughTheDeviceLayer pins that the
// assembler hands every cluster-serving bridged endpoint the go-fabric
// device layer's spec source, not a hand-built loom profile, and every
// sensor endpoint its measurement source.
//
// A sensor endpoint is the one hand-built shape left: the device layer has
// no measurement-source form since go-fabric v0.10.0, and the endpoint
// assembler a host keeps beside the device kit materialises
// endpoint.Spec.Measurement itself (go-fabric guides "Advanced seams",
// section endpoint, and "Host ports", Measurements and classification).
// An endpoint with neither a device-layer source nor a measurement is a
// composition the device layer refused, which the assembler only logs.
func TestEveryBridgedEndpointIsBuiltThroughTheDeviceLayer(t *testing.T) {
	top := assembleGolden(t)
	bridged := top.Bridged()
	if len(bridged) != goldenBridgedEndpointCount {
		t.Errorf("bridged endpoints = %d, want %d (an endpoint was lost or added)", len(bridged), goldenBridgedEndpointCount)
	}
	var deviceLayer, sensors int
	for _, ep := range bridged {
		_, built := ep.Source.(interface{ Spec() endpoint.Spec })
		switch {
		case built && ep.Measurement == nil:
			deviceLayer++
		case ep.Source == nil && ep.Measurement != nil:
			sensors++
		default:
			t.Errorf("endpoint %d (device type 0x%04X) source %T, measurement %T: neither built through the device layer nor a sensor endpoint",
				ep.ID, ep.DeviceType, ep.Source, ep.Measurement)
		}
	}
	// The golden fleet has both shapes; a fleet where one of them vanished
	// would let this guard pass without judging that shape at all.
	if deviceLayer == 0 || sensors == 0 {
		t.Errorf("device-layer endpoints = %d, sensor endpoints = %d; the golden fleet must exercise both", deviceLayer, sensors)
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
