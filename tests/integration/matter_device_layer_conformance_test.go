// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build integration

// matter_device_layer_conformance_test.go — the device-layer guards over the
// category smoke fleet: every bridged endpoint of one topology holding a
// device of each category must be built through go-fabric's device layer and
// must satisfy the device types it lists.
package integration

import (
	"context"
	"fmt"
	"testing"

	"github.com/SukramJ/go-fabric/cluster/onoff"
	"github.com/SukramJ/go-fabric/cluster/spec"
	"github.com/SukramJ/go-fabric/contract"
	"github.com/SukramJ/go-fabric/endpoint"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/north/matteradapter"
)

// acceptedCategoryDeviceTypeViolations declares known device-type conformance
// departures of the category fleet. Key: "<device type>|<requirement>|<kind>".
var acceptedCategoryDeviceTypeViolations = map[string]string{
	"OnOffLight|OnOff.LT|missing":                   "finding: the OnOff smoke fixture is a single-cluster stub without the LT feature, see report",
	"ExtendedColorLight|ColorControl.XY|missing":    "finding: loom's ColorControl server is HS-only, see report",
	"ExtendedColorLight|ColorControl.CT|missing":    "finding: loom's ColorControl server carries no colour-temperature feature, see report",
	"SmokeCoAlarm|device:PowerSource|instanceCount": "finding: the smoke siren endpoint has no PowerSource composition in the smoke fleet, see report",
}

func buildCategoryFleetTopology(t *testing.T) *endpoint.Topology {
	t.Helper()
	devs := []*device.Device{
		makeSmokeOnOffDevice("DLAYER_ONOFF", "OnOff"),
		makeSmokeTempDevice("DLAYER_TEMP", "Temp"),
		makeSmokeClimateDevice("DLAYER_CLIM", "Climate"),
		makeSmokeLockDevice("DLAYER_LOCK", "Lock"),
		makeSmokeCoverDevice("DLAYER_COVR", "Cover"),
		makeSmokeColorLightDevice("DLAYER_COLR", "ColorLight"),
		makeSmokeSmokeSirenDevice("DLAYER_SMOK", "SmokeSiren"),
	}
	return buildSmokeTopology(t, []matteradapter.DeviceSnapshot{
		{CentralName: "ccu1", Devices: devs, ModelComplete: true},
	})
}

func TestMatterDeviceLayerBuildsEveryCategoryEndpoint(t *testing.T) {
	top := buildCategoryFleetTopology(t)
	bridged := top.Bridged()
	if len(bridged) == 0 {
		t.Fatal("no bridged endpoints assembled")
	}
	for _, ep := range bridged {
		if _, ok := ep.Source.(interface{ Spec() endpoint.Spec }); !ok {
			t.Errorf("endpoint %d (device type 0x%04X) source %T is not built through the device layer",
				ep.ID, ep.DeviceType, ep.Source)
		}
	}
}

func TestMatterDeviceLayerCategoryEndpointsSatisfyTheirDeviceTypes(t *testing.T) {
	top := buildCategoryFleetTopology(t)
	bridged := map[uint16]bool{}
	for _, ep := range top.Bridged() {
		bridged[ep.ID] = true
	}
	for _, v := range endpoint.ValidateDeviceTypes(top) {
		if !bridged[v.Endpoint] {
			continue
		}
		key := fmt.Sprintf("%s|%s|%s", v.DeviceType, v.Requirement, v.Kind)
		if _, ok := acceptedCategoryDeviceTypeViolations[key]; ok {
			continue
		}
		t.Errorf("endpoint %d violates its device type: %s\n  full: %+v\n  fix the profile, or declare %q in acceptedCategoryDeviceTypeViolations with a reason",
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
	top := buildCategoryFleetTopology(t)
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
				if _, ok := acceptedGeneratedDefaultServers[fmt.Sprintf("0x%04X|0x%04X", ep.DeviceType, s.MatterClusterID())]; ok {
					continue
				}
				t.Errorf("endpoint %d (device type 0x%04X) carries a generated default %T for cluster 0x%04X; the profile does not serve it",
					ep.ID, ep.DeviceType, s, s.MatterClusterID())
			case *onoff.Server:
				if _, ok := acceptedGeneratedDefaultServers[fmt.Sprintf("0x%04X|0x%04X", ep.DeviceType, s.MatterClusterID())]; ok {
					continue
				}
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

// acceptedGeneratedDefaultServers declares tolerated device-layer default
// servers, keyed "<device type>|<cluster id>".
var acceptedGeneratedDefaultServers = map[string]string{}
