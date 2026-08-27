//go:build chiptool

// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package chiptool

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/tests/chiptool/harness"
)

// The endpoint topology these tests read is the one the device-type conformance
// work established: a cluster is served on an endpoint whose device type the
// Matter Device Library specifies it for, and nowhere else.
//
// tests/integration checks the same rule against the model, which is the fast
// gate. These read it back through a real commissioner, which is the only place
// the wire representation — the Descriptor a controller actually parses — is
// exercised. A DeviceTypeList that assembles correctly in Go and encodes wrong
// on the wire passes there and fails here.
//
// A test skips when the run's exposure selection surfaced no witnessing
// endpoint, the same contract the rest of the suite follows. A skip is not a
// pass. It is also not a statement about the fleet: godevccu does carry the
// witnesses these tests want — metering plugs of the HM-ES-PMSw1-Pl family and
// 300-odd battery data points, measured over the hydrated fleet — so a
// permanent skip means the selection needs widening, not that the device is
// missing.

// deviceTypeListContains reports whether a chip-tool DeviceTypeList dump names
// the given device type. chip-tool prints either decimal or hex, and the hex
// form is not zero-padded consistently, so all three spellings are accepted —
// the same tolerance TestDescriptor_RootDeviceTypeList applies.
func deviceTypeListContains(out string, deviceType uint32, hexForms ...string) bool {
	needles := make([]string, 0, len(hexForms)+1)
	needles = append(needles, "DeviceType: "+strconv.FormatUint(uint64(deviceType), 10))
	for _, h := range hexForms {
		needles = append(needles, "DeviceType: "+h)
	}
	for _, n := range needles {
		if strings.Contains(out, n) {
			return true
		}
	}
	return false
}

// TestDeviceType_ElectricalSensorCarriesItsMandatorySurface reads back the
// endpoint the electrical measurements now live on.
//
// The clusters used to be attached to the metering plug's OnOff endpoint,
// which the Device Library does not specify for OnOffPlugInUnit (0x010A) in
// any role. Their carrier is ElectricalSensor (0x0510), which also makes
// PowerTopology (0x009C) mandatory — a commissioner reads the mandatory set
// during pairing, so a missing PowerTopology is not a cosmetic gap.
func TestDeviceType_ElectricalSensorCarriesItsMandatorySurface(t *testing.T) {
	b := requireBridge(t)
	eps := discoverEndpointsWith(t, b, 0x0090, 1) // ElectricalPowerMeasurement
	if len(eps) == 0 {
		// Not a fleet gap: godevccu carries metering devices (HM-ES-PMSw1-Pl
		// and relatives report POWER / VOLTAGE / CURRENT / ENERGY_COUNTER,
		// measured over the hydrated fleet). A skip here therefore means the
		// exposure selection this run commissioned with did not include one,
		// not that the device type is untested anywhere.
		t.Skip("no ElectricalPowerMeasurement endpoint exposed in this run's selection")
	}
	ep := eps[0]

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	out, err := b.SharedCtl.ReadAttr(ctx, t, "descriptor", "device-type-list", ep)
	if err != nil {
		t.Fatalf("read device-type-list ep%d: %v", ep, err)
	}
	if !harness.AttrReadOK(out) {
		t.Fatalf("device-type-list read on ep%d did not succeed:\n%s", ep, out)
	}
	if !deviceTypeListContains(out, 0x0510, "0x510", "0x0510") {
		t.Errorf("ep%d serves ElectricalPowerMeasurement but its DeviceTypeList does not name "+
			"ElectricalSensor (0x0510) — the Device Library specifies the cluster for that type, "+
			"and Alexa recognises a bridged endpoint only by the clusters its device type names:\n%s",
			ep, out)
	}

	slOut, err := b.SharedCtl.ReadAttr(ctx, t, "descriptor", "server-list", ep)
	if err != nil {
		t.Fatalf("read server-list ep%d: %v", ep, err)
	}
	perEP := harness.ServerListIDsPerEndpoint(slOut)
	if !harness.HasCluster(perEP[ep], 0x009C) {
		t.Errorf("ep%d is an ElectricalSensor without PowerTopology (0x009C), which the Device "+
			"Library makes mandatory for the type:\n%s", ep, slOut)
	}
}

// TestDeviceType_ThermostatDoesNotServeMeasurementClusters reads back the other
// half of the same change.
//
// The Device Library names TemperatureMeasurement (0x0402) and
// RelativeHumidityMeasurement (0x0405) for Thermostat (0x0301) as
// element=clientCluster: a thermostat consumes those readings from another
// endpoint rather than serving them. Serving them made every thermostat
// endpoint non-conformant. The readings still reach controllers — as their own
// sensor endpoints, which the sibling test below checks.
func TestDeviceType_ThermostatDoesNotServeMeasurementClusters(t *testing.T) {
	b := requireBridge(t)
	eps := discoverEndpointsWith(t, b, 0x0201, 0) // every Thermostat endpoint
	if len(eps) == 0 {
		t.Skip("no Thermostat endpoint exposed in this run's selection")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	slOut, err := b.SharedCtl.ReadAttr(ctx, t, "descriptor", "server-list", 0xFFFF)
	if err != nil {
		t.Fatalf("wildcard server-list: %v", err)
	}
	perEP := harness.ServerListIDsPerEndpoint(slOut)

	for _, ep := range eps {
		for _, forbidden := range []struct {
			id   uint32
			name string
		}{
			{0x0402, "TemperatureMeasurement"},
			{0x0405, "RelativeHumidityMeasurement"},
		} {
			if harness.HasCluster(perEP[ep], forbidden.id) {
				t.Errorf("thermostat ep%d serves %s (0x%04X); the Device Library names it for device "+
					"type 0x0301 as a CLIENT cluster, so the endpoint consumes it rather than "+
					"serving it", ep, forbidden.name, forbidden.id)
			}
		}
	}
}

// TestDeviceType_TemperatureReadingHasASensorEndpoint is the other side of the
// previous test: dropping the cluster from the thermostat only holds up if the
// reading still reaches a controller somewhere.
//
// A thermostat channel's ACTUAL_TEMPERATURE materialises as its own
// TemperatureSensor endpoint. Without this test the previous one is satisfied
// by a bridge that simply stopped reporting temperature.
func TestDeviceType_TemperatureReadingHasASensorEndpoint(t *testing.T) {
	b := requireBridge(t)
	if thermostats := discoverEndpointsWith(t, b, 0x0201, 1); len(thermostats) == 0 {
		t.Skip("no Thermostat endpoint exposed in this run's selection")
	}
	eps := discoverEndpointsWith(t, b, 0x0402, 1)
	if len(eps) == 0 {
		t.Fatal("the fleet has a thermostat but no endpoint serving TemperatureMeasurement (0x0402) " +
			"— the reading reaches no controller at all")
	}
	ep := eps[0]

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	out, err := b.SharedCtl.ReadAttr(ctx, t, "descriptor", "device-type-list", ep)
	if err != nil {
		t.Fatalf("read device-type-list ep%d: %v", ep, err)
	}
	if !deviceTypeListContains(out, 0x0302, "0x302", "0x0302") {
		t.Errorf("ep%d serves TemperatureMeasurement but is not a TemperatureSensor (0x0302):\n%s", ep, out)
	}
}

// TestDeviceType_NoEndpointLacksAPrimaryDeviceType asserts that no bridged
// endpoint advertises BridgedNode as its only device type.
//
// A battery data point used to materialise as an endpoint of its own with no
// device type, so its DeviceTypeList held BridgedNode (0x0013) alone. Apple
// files such an endpoint under its "Other" fallback. PowerSource now rides on
// one of the device's function endpoints, where BridgedNode specifies it as a
// server cluster.
//
// The check reads every endpoint's list in one wildcard, so it covers whatever
// the fleet happens to expose rather than a device chosen in advance.
func TestDeviceType_NoEndpointLacksAPrimaryDeviceType(t *testing.T) {
	b := requireBridge(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	out, err := b.SharedCtl.ReadAttr(ctx, t, "descriptor", "device-type-list", 0xFFFF)
	if err != nil {
		t.Fatalf("wildcard device-type-list: %v", err)
	}
	if !harness.AttrReadOK(out) {
		t.Fatalf("wildcard device-type-list read did not succeed:\n%s", out)
	}

	// A bridged endpoint's list is [primary, BridgedNode]. An endpoint whose
	// entry count equals its BridgedNode count has no primary type. Counting
	// over the whole dump rather than per endpoint keeps this independent of
	// chip-tool's grouping format; a mismatch names the dump for the reader.
	entries := strings.Count(out, "DeviceType: ")
	bridgedNode := strings.Count(out, "DeviceType: 19") +
		strings.Count(out, "DeviceType: 0x13") +
		strings.Count(out, "DeviceType: 0x0013")
	if entries == 0 {
		t.Fatal("wildcard device-type-list produced no entries — the read or the parser is broken " +
			"and this check would pass vacuously")
	}
	if bridgedNode == 0 {
		t.Fatalf("no BridgedNode entry in the dump, so this bridge exposes no bridged endpoint at "+
			"all — the check has nothing to measure:\n%s", out)
	}
	if entries <= bridgedNode {
		t.Errorf("every DeviceTypeList entry is BridgedNode (%d of %d): at least one endpoint has no "+
			"primary device type, which Apple files under its \"Other\" fallback:\n%s",
			bridgedNode, entries, out)
	}
}
