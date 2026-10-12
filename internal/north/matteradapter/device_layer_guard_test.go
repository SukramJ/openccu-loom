// SPDX-License-Identifier: MIT
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
// assembler hands every bridged endpoint the go-fabric device layer's spec
// source, not a hand-built loom profile.
func TestEveryBridgedEndpointIsBuiltThroughTheDeviceLayer(t *testing.T) {
	top := assembleGolden(t)
	bridged := top.Bridged()
	if len(bridged) != goldenBridgedEndpointCount {
		t.Errorf("bridged endpoints = %d, want %d (an endpoint was lost or added)", len(bridged), goldenBridgedEndpointCount)
	}
	for _, ep := range bridged {
		if _, ok := ep.Source.(interface{ Spec() endpoint.Spec }); !ok {
			t.Errorf("endpoint %d (device type 0x%04X) source %T is not built through the device layer",
				ep.ID, ep.DeviceType, ep.Source)
		}
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
// (spec.Server, onoff.Server) is rebuilt on every reassembly and answers
// every command UNSUPPORTED_COMMAND, so it would hide a profile defect
// behind a conforming ServerList.
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
			switch s.(type) {
			case *spec.Server, *onoff.Server:
				t.Errorf("endpoint %d (device type 0x%04X) carries a generated default %T for cluster 0x%04X; the profile does not serve it",
					ep.ID, ep.DeviceType, s, s.MatterClusterID())
			}
		}
	}
}
