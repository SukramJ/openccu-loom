// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

//go:build integration

// matter_device_layer_conformance_test.go — the device-layer guards over the
// category smoke fleet: every bridged endpoint of one topology holding a
// device of each category must be built through go-fabric's device layer and
// must satisfy the device types it lists.
package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/SukramJ/go-fabric/cluster/onoff"
	"github.com/SukramJ/go-fabric/cluster/spec"
	"github.com/SukramJ/go-fabric/contract"
	"github.com/SukramJ/go-fabric/endpoint"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/north/matteradapter"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// acceptedCategoryDeviceTypeViolations declares known device-type conformance
// departures of the category fleet. Key: "<device type>|<requirement>|<kind>".
var acceptedCategoryDeviceTypeViolations = map[string]string{
	"OnOffLight|OnOff.LT|missing":                   "finding: the OnOff smoke fixture is a single-cluster stub without the LT feature, see report",
	"ExtendedColorLight|ColorControl.CT|missing":    "finding: loom's ColorControl server carries no colour-temperature feature, see report",
	"SmokeCoAlarm|device:PowerSource|instanceCount": "finding: the smoke siren endpoint has no PowerSource composition in the smoke fleet, see report",
}

// makeTemperatureSensorDevice returns a device whose temperature channel
// carries the shape CCU hydration produces for an analog parameter: a
// read-only float VALUES data point, which the assembler builds as a
// sensor endpoint from its measurement class.
func makeTemperatureSensorDevice(addr, name string) *device.Device {
	dev := device.New(device.Config{Address: addr, Name: name})
	ch := dev.AddChannel(addr+":1", 1, "SENSOR", hmenum.ParamsetKeyValues)
	ch.Put(generic.NewFloatSensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "iface",
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterActualTemperature),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
			Min:        json.RawMessage("-40.0"),
			Max:        json.RawMessage("80.0"),
		},
	}))
	return dev
}

func buildCategoryFleetTopology(t *testing.T) *endpoint.Topology {
	t.Helper()
	devs := []*device.Device{
		makeSmokeOnOffDevice("DLAYER_ONOFF", "OnOff"),
		makeTemperatureSensorDevice("DLAYER_TEMP", "Temp"),
		makeSmokeClimateDevice("DLAYER_CLIM", "Climate"),
		makeSmokeLockDevice("DLAYER_LOCK", "Lock"),
		makeSmokeCoverDevice("DLAYER_COVR", "Cover"),
		makeSmokeColorLightDevice("DLAYER_COLR", "ColorLight"),
		makeSmokeSmokeSirenDevice("DLAYER_SMOK", "SmokeSiren"),
	}
	cfg := smokeValidConfig(t)
	cfg.IncludeMeasurements = true
	a, err := matteradapter.New(newSmokeFakeStore(), cfg, nil)
	if err != nil {
		t.Fatalf("matteradapter.New: %v", err)
	}
	top, err := a.AssembleDevices(context.Background(), []matteradapter.DeviceSnapshot{
		{CentralName: "ccu1", Devices: devs, ModelComplete: true},
	})
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}
	return top
}

func TestMatterDeviceLayerBuildsEveryCategoryEndpoint(t *testing.T) {
	top := buildCategoryFleetTopology(t)
	bridged := top.Bridged()
	if len(bridged) == 0 {
		t.Fatal("no bridged endpoints assembled")
	}
	// Two shapes, as in the assembler's own guard: a cluster-serving
	// endpoint built through the device layer, and a sensor endpoint built
	// from its measurement, which the device layer has no form for since
	// go-fabric v0.10.0 (ADR 0084, amendment for go-fabric v0.10.0).
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
	if deviceLayer == 0 || sensors == 0 {
		t.Errorf("device-layer endpoints = %d, sensor endpoints = %d; the category fleet must exercise both", deviceLayer, sensors)
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
