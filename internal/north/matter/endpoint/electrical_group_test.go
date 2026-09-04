// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package endpoint_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/model/custom"
	_ "github.com/SukramJ/openccu-loom/internal/model/custom/builtins"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/generic"
	"github.com/SukramJ/openccu-loom/internal/north/matter/endpoint"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmproto"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
	"github.com/SukramJ/openccu-loom/pkg/mattercontract"
)

// meteringPlugFloat adds a read-only float VALUES data point, the shape the
// CCU hydration produces for a metering channel's parameters.
func meteringPlugFloat(ch *device.Channel, p hmenum.Parameter) {
	ch.Put(generic.NewFloatSensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "iface",
			ChannelAddress: ch.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(p),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeFloat,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
			Min:        json.RawMessage("0.0"),
			Max:        json.RawMessage("100000.0"),
		},
	}))
}

// buildMeteringPlug returns a HmIP-PSM-shaped device: the switch actor on one
// channel, the five electrical parameters on another. The split is what makes
// this device the right witness — the electrical clusters used to be attached
// cross-channel onto the switch's endpoint.
func buildMeteringPlug(t *testing.T) *device.Device {
	t.Helper()
	dev := device.New(device.Config{
		InterfaceID:  "HmIP-RF",
		Interface:    hmenum.InterfaceHmIPRF,
		Address:      "0001PSM01",
		Model:        "HmIP-PSM",
		Manufacturer: hmenum.ManufacturerEQ3,
		ProductGroup: hmenum.ProductGroupHmIP,
	})
	for i := range 7 {
		dev.AddChannel("0001PSM01:"+strconv.Itoa(i), i, "X", hmenum.ParamsetKeyValues)
	}
	ch3 := dev.Channel("0001PSM01:3")
	ch3.Put(generic.NewSwitch(generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "iface",
			ChannelAddress: ch3.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterState),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsWrite | hmenum.OperationsEvent,
		},
	}))
	ch6 := dev.Channel("0001PSM01:6")
	for _, p := range []hmenum.Parameter{
		hmenum.ParameterPower,
		hmenum.ParameterVoltage,
		hmenum.ParameterCurrent,
		hmenum.ParameterFrequency,
		hmenum.ParameterEnergyCounter,
	} {
		meteringPlugFloat(ch6, p)
	}
	if err := custom.CreateCustomDataPoints(dev, custom.DefaultRegistry()); err != nil {
		t.Fatalf("CreateCustomDataPoints: %v", err)
	}
	return dev
}

// assembleTopology runs the real assembler over dev.
func assembleTopology(t *testing.T, dev *device.Device) *endpoint.Topology {
	t.Helper()
	a, err := endpoint.New(newFakeStore(), endpoint.Config{
		VendorID:  0xFFF1,
		ProductID: 0x8000,
		NodeLabel: "electrical-group-test",
	}, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatalf("assembler: %v", err)
	}
	top, err := a.Assemble(context.Background(), []endpoint.Snapshot{
		{CentralName: "c", Devices: []*device.Device{dev}},
	})
	if err != nil {
		t.Fatalf("assemble: %v", err)
	}
	return top
}

// TestMeteringPlugProjectsOneElectricalSensorEndpoint asserts the shape a
// metering plug takes on the Matter side: the switch channel projects a plain
// OnOffPlugInUnit, and the five electrical parameters collapse into ONE
// ElectricalSensor endpoint carrying the three clusters that device type
// specifies.
//
// Two failures this catches, both of which shipped before it existed:
//
//   - The electrical clusters mounted on the OnOffPlugInUnit endpoint, which
//     the Device Library does not specify for 0x010A in any role.
//   - Each electrical parameter projecting as its own endpoint with device
//     type 0 — a DeviceTypeList of [BridgedNode] alone, five times over for
//     one socket.
func TestMeteringPlugProjectsOneElectricalSensorEndpoint(t *testing.T) {
	top := assembleTopology(t, buildMeteringPlug(t))

	byDeviceType := map[uint16][]*endpoint.Endpoint{}
	for _, ep := range top.Bridged() {
		byDeviceType[ep.DeviceType] = append(byDeviceType[ep.DeviceType], ep)
	}

	if got := len(byDeviceType[0]); got != 0 {
		t.Errorf("%d endpoint(s) carry device type 0; their DeviceTypeList would be [BridgedNode] alone", got)
	}
	if got := len(byDeviceType[0x010A]); got != 1 {
		t.Fatalf("expected exactly 1 OnOffPlugInUnit endpoint, got %d", got)
	}
	electrical := byDeviceType[0x0510]
	if len(electrical) != 1 {
		t.Fatalf("expected exactly 1 ElectricalSensor endpoint for five electrical parameters, got %d", len(electrical))
	}
	if key := electrical[0].SourceKey.DPKey; key != endpoint.ElectricalGroupDPKey {
		t.Errorf("ElectricalSensor endpoint keyed on %q, want the consolidated %q", key, endpoint.ElectricalGroupDPKey)
	}

	// The switch endpoint must be free of electrical clusters.
	switchClusters := clusterIDSet(byDeviceType[0x010A][0])
	for _, id := range []uint32{0x0090, 0x0091, 0x009C} {
		if switchClusters[id] {
			t.Errorf("cluster 0x%04X is mounted on the OnOffPlugInUnit endpoint; the Device Library "+
				"specifies it for ElectricalSensor (0x0510), not for 0x010A", id)
		}
	}

	// The ElectricalSensor endpoint must carry all three, PowerTopology
	// included — the Device Library makes it mandatory for the type, and a
	// commissioner reads the mandatory set during pairing.
	electricalClusters := clusterIDSet(electrical[0])
	for _, want := range []struct {
		id   uint32
		name string
	}{
		{0x0090, "ElectricalPowerMeasurement"},
		{0x0091, "ElectricalEnergyMeasurement"},
		{0x009C, "PowerTopology (mandatory for 0x0510)"},
	} {
		if !electricalClusters[want.id] {
			t.Errorf("cluster 0x%04X %s missing from the ElectricalSensor endpoint", want.id, want.name)
		}
	}
}

// TestElectricalSensorClusterSetDoesNotDependOnReportedValues pins the
// ServerList against the reading state: a Matter endpoint's cluster set is
// quasi-static, so deciding it from whether a value has arrived yet would have
// ElectricalEnergyMeasurement appear mid-session, after controllers cached the
// list. The first draft of the consolidation had exactly that bug.
func TestElectricalSensorClusterSetDoesNotDependOnReportedValues(t *testing.T) {
	dev := buildMeteringPlug(t)

	before := clusterIDSet(findByDeviceType(t, assembleTopology(t, dev), 0x0510))

	// Feed every parameter, then reassemble.
	ch6 := dev.Channel("0001PSM01:6")
	for _, p := range []hmenum.Parameter{
		hmenum.ParameterPower,
		hmenum.ParameterVoltage,
		hmenum.ParameterCurrent,
		hmenum.ParameterFrequency,
		hmenum.ParameterEnergyCounter,
	} {
		dp := ch6.Parameter(p)
		if dp == nil {
			t.Fatalf("parameter %s vanished from the metering channel", p)
		}
		// Typed on purpose. A loose `interface{ OnEvent(any) }` assertion
		// silently fails to match Sensor[float64].OnEvent(float64), leaving
		// the values unfed — the test then compares two identical
		// value-less topologies and passes no matter what the code does.
		fed, ok := dp.(interface{ OnEvent(float64) })
		if !ok {
			t.Fatalf("%s is %T, which cannot be fed a float64 value; the test would measure nothing", p, dp)
		}
		fed.OnEvent(42.0)
		src, ok := dp.(mattercontract.FloatMeasurementSource)
		if !ok {
			t.Fatalf("%s is %T, not a Matter measurement source", p, dp)
		}
		if _, observed := src.MatterFloatValue(); !observed {
			t.Fatalf("%s still reports no observation after OnEvent; the test would measure nothing", p)
		}
	}
	after := clusterIDSet(findByDeviceType(t, assembleTopology(t, dev), 0x0510))

	if len(before) != len(after) {
		t.Fatalf("ElectricalSensor cluster set changed once values arrived: before=%v after=%v", before, after)
	}
	for id := range after {
		if !before[id] {
			t.Errorf("cluster 0x%04X appeared only after a value was reported", id)
		}
	}
}

// findByDeviceType returns the single bridged endpoint with the given device
// type, failing when there is not exactly one.
func findByDeviceType(t *testing.T, top *endpoint.Topology, deviceType uint16) *endpoint.Endpoint {
	t.Helper()
	var found []*endpoint.Endpoint
	for _, ep := range top.Bridged() {
		if ep.DeviceType == deviceType {
			found = append(found, ep)
		}
	}
	if len(found) != 1 {
		t.Fatalf("expected exactly 1 endpoint of device type 0x%04X, got %d", deviceType, len(found))
	}
	return found[0]
}

// clusterIDSet materialises an endpoint's cluster servers and returns their IDs.
func clusterIDSet(ep *endpoint.Endpoint) map[uint32]bool {
	out := map[uint32]bool{}
	for _, s := range endpoint.ClusterServers(ep) {
		out[s.MatterClusterID()] = true
	}
	return out
}

// TestBatteryRidesOnADeviceEndpointRatherThanItsOwn asserts that a
// battery-powered device serves PowerSource (0x002F) on one of its function
// endpoints instead of spawning an endpoint for it.
//
// A battery has no device type of its own. Letting the LOWBAT data point
// materialise as a measurement endpoint produced one whose
// Descriptor.DeviceTypeList was [BridgedNode] alone — no primary type, which
// Apple files under its "Other" fallback. BridgedNode (0x0013) is the
// secondary device type every bridged endpoint carries and it specifies
// PowerSource as a server cluster, so mounting it on a function endpoint is
// both conformant and what a bridge is supposed to do with power-source info.
func TestBatteryRidesOnADeviceEndpointRatherThanItsOwn(t *testing.T) {
	dev := buildMeteringPlug(t)
	// LOWBAT sits on the maintenance channel, as it does on a real device.
	ch0 := dev.Channel("0001PSM01:0")
	ch0.Put(generic.NewBinarySensor(generic.Spec{
		Key: hmtypes.DataPointKey{
			InterfaceID:    "iface",
			ChannelAddress: ch0.Address,
			ParamsetKey:    hmenum.ParamsetKeyValues,
			Parameter:      string(hmenum.ParameterLowBat),
		},
		Descriptor: hmproto.ParameterData{
			Type:       hmenum.ParameterTypeBool,
			Operations: hmenum.OperationsRead | hmenum.OperationsEvent,
		},
	}))

	top := assembleTopology(t, dev)

	carriers := 0
	for _, ep := range top.Bridged() {
		if ep.DeviceType == 0 {
			t.Errorf("EP %d has device type 0; its DeviceTypeList would be [BridgedNode] alone", ep.ID)
		}
		if clusterIDSet(ep)[0x002F] {
			carriers++
		}
	}

	// Exactly one: a device has one battery, and advertising it on every
	// endpoint would show the operator several battery levels for one device.
	if carriers != 1 {
		t.Errorf("PowerSource (0x002F) is served on %d endpoints, want exactly 1", carriers)
	}
}
