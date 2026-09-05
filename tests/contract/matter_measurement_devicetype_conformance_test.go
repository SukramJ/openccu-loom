// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package contract

import (
	"fmt"
	"strings"
	"testing"

	mattercontract "github.com/SukramJ/go-fabric/contract"
	"github.com/SukramJ/go-fabric/schema"

	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// A Matter endpoint is a device type plus the clusters that device type is
// specified to carry. The Device Library says, per type, which clusters may
// be mounted as a SERVER and which the type merely CONSUMES from elsewhere as
// a client. Mount a cluster the type does not specify as a server and the
// endpoint is non-conformant — an ecosystem is free to ignore the cluster,
// mis-categorise the accessory, or refuse the whole bridged node, and each of
// those has been observed in the field.
//
// The two guards below hold the measurement side of that contract: every
// [interfaces.MatterMeasurementClass] must name a device type that exists,
// and that device type must permit the class's cluster as a server. The
// oracle is schema.DeviceTypeServerClusters, codegen'd from matter.js HEAD via
// `make generate-matter-schema` — so the rules move when the spec does,
// without anyone re-reading a PDF.
//
// The endpoint-source side of the same contract — a custom DP's
// MatterDeviceType against its MatterClusterServers — needs real devices to
// materialise against and lives in
// tests/integration/matter_endpoint_cluster_conformance_test.go.

// measurementClasses enumerates every class the model layer can return.
// Kept as an explicit list rather than a range over the iota block: adding a
// class without adding it here leaves it unguarded, and
// TestMeasurementClassEnumerationIsComplete makes that fail.
var measurementClasses = []interfaces.MatterMeasurementClass{
	interfaces.MatterMeasurementTemperature,
	interfaces.MatterMeasurementHumidity,
	interfaces.MatterMeasurementIlluminance,
	interfaces.MatterMeasurementPressure,
	interfaces.MatterMeasurementCO2,
	interfaces.MatterMeasurementPM25,
	interfaces.MatterMeasurementPM10,
	interfaces.MatterMeasurementOccupancy,
	interfaces.MatterMeasurementContact,
	interfaces.MatterMeasurementLeak,
	interfaces.MatterMeasurementBattery,
	interfaces.MatterMeasurementPower,
	interfaces.MatterMeasurementEnergy,
	interfaces.MatterMeasurementMomentarySwitch,
	interfaces.MatterMeasurementElectrical,
}

// hostRiddenMeasurementClasses are the classes that map to device type 0
// because their cluster is mounted on an existing host endpoint instead of on
// a sensor endpoint of their own, and that have a seam doing the mounting.
//
// An entry is a claim about wiring, not a licence: the assembler builds a
// standalone endpoint for any measurement class it is handed, and an endpoint
// with device type 0 advertises DeviceTypeList=[BridgedNode] alone. So each
// entry names the seam, and TestHostRiddenMeasurementClassesHaveAHost checks
// that a conformant host device type for the cluster exists at all.
//
// What an entry does NOT assert is that the seam suppresses the standalone
// endpoint. That is what TestMeteringPlugProjectsOneElectricalSensorEndpoint
// in internal/north/matter/endpoint observes, on an assembled topology.
var hostRiddenMeasurementClasses = map[interfaces.MatterMeasurementClass]string{
	interfaces.MatterMeasurementPower: "folded into a generic.ElectricalGroup by the assembler, which projects one " +
		"ElectricalSensor (0x0510) endpoint per channel; the per-parameter class never builds an endpoint",
	interfaces.MatterMeasurementEnergy: "folded into the same generic.ElectricalGroup as Power",
	interfaces.MatterMeasurementBattery: "endpoint.attachPowerSource mounts 0x002F on one of the device's own " +
		"endpoints, which BridgedNode (0x0013) specifies as a server cluster; the class never builds an endpoint",
}

// measurementClassesUnderInvestigation are known defects, not exemptions.
// Kept separate from hostRiddenMeasurementClasses on purpose: that map says
// "this is fine and here is why", this one says "this is broken and not yet
// fixed". Merging them would let a defect inherit the other map's rationale
// and disappear.
//
// Emptying this map is the goal. An entry states what is wrong and what the
// fix has to establish.
var measurementClassesUnderInvestigation = map[interfaces.MatterMeasurementClass]string{}

// TestMeasurementClassProjectsOntoAConformantDeviceType asserts that every
// measurement class names a device type whose Device Library entry permits
// the class's cluster as a server.
//
// Without this, a class can name a plausible-looking device type that does
// not actually specify the cluster — the endpoint then advertises a cluster
// the type does not have, which is the shape that makes Alexa fall back to
// the first recognised type and drop the rest of the endpoint's surface.
func TestMeasurementClassProjectsOntoAConformantDeviceType(t *testing.T) {
	t.Parallel()

	checked := 0
	for _, class := range measurementClasses {
		deviceType := uint32(mattercontract.MeasurementClassDeviceType(class))
		clusterID := mattercontract.MeasurementClassClusterID(class)

		if clusterID == 0 {
			t.Errorf("measurement class %d maps to cluster 0 — it would be StateUnmappable "+
				"in eligibility.Classify and never reach an endpoint at all", class)
			continue
		}
		if deviceType == 0 {
			// Device type 0 is only defensible when something else mounts
			// the cluster; the sibling guard checks that claim.
			_, hostRidden := hostRiddenMeasurementClasses[class]
			_, knownDefect := measurementClassesUnderInvestigation[class]
			switch {
			case hostRidden && knownDefect:
				t.Errorf("measurement class %d is in both hostRiddenMeasurementClasses and "+
					"measurementClassesUnderInvestigation; it is either fine or it is not", class)
			case hostRidden, knownDefect:
				// Accounted for.
			default:
				t.Errorf("measurement class %d (cluster 0x%04X) maps to device type 0: "+
					"endpoint.makeMeasurementEndpoint builds a standalone endpoint whose "+
					"Descriptor.DeviceTypeList is [BridgedNode] alone, with no primary type. "+
					"Apple drops such an endpoint into its \"Other\" fallback. Give the class a "+
					"device type, or declare it in hostRiddenMeasurementClasses with the seam "+
					"that mounts its cluster on a host endpoint", class, clusterID)
			}
			continue
		}

		checked++
		allowed, known := schema.DeviceTypeAllowsServerCluster(deviceType, clusterID)
		if !known {
			t.Errorf("measurement class %d names device type 0x%04X, which the matter.js HEAD "+
				"schema snapshot does not know — either the id is wrong or the snapshot is stale "+
				"(`make generate-matter-schema`)", class, deviceType)
			continue
		}
		if !allowed {
			t.Errorf("measurement class %d mounts cluster 0x%04X (%s) as a server on device type "+
				"0x%04X (%s), which the Matter Device Library does not specify for it. "+
				"Permitted server clusters: %s",
				class, clusterID, clusterName(clusterID), deviceType, deviceTypeName(deviceType),
				serverClusterList(deviceType))
		}
	}

	if checked == 0 {
		t.Fatal("no measurement class produced a non-zero device type — the enumeration or the " +
			"schema table is empty and this guard would pass vacuously")
	}
}

// TestHostRiddenMeasurementClassesHaveAHost checks the claim each
// hostRiddenMeasurementClasses entry makes: that some device type specifies
// the class's cluster as a server, so there is a host endpoint the cluster
// can legitimately be mounted on.
//
// A class whose cluster no device type specifies has no host anywhere, and
// declaring it host-ridden only hides that.
func TestHostRiddenMeasurementClassesHaveAHost(t *testing.T) {
	t.Parallel()

	for class, reason := range hostRiddenMeasurementClasses {
		clusterID := mattercontract.MeasurementClassClusterID(class)
		if clusterID == 0 {
			t.Errorf("host-ridden class %d has no cluster at all; the entry %q describes nothing", class, reason)
			continue
		}
		hosts := 0
		for deviceType, clusters := range schema.DeviceTypeServerClusters {
			for _, id := range clusters {
				if id == clusterID {
					hosts++
					_ = deviceType
					break
				}
			}
		}
		if hosts == 0 {
			t.Errorf("host-ridden class %d claims cluster 0x%04X rides on a host endpoint (%q), but "+
				"no device type in the matter.js HEAD schema specifies that cluster as a server — "+
				"there is no conformant host to ride on", class, clusterID, reason)
		}
	}
}

// TestMeasurementClassEnumerationIsComplete fails when a class is added to
// the iota block without being added to measurementClasses, which would leave
// it silently unguarded.
//
// It works by walking upward from the last enumerated class: the iota block
// is dense, so the first value with no cluster and no device type past the
// end of the list is the end of the enum.
func TestMeasurementClassEnumerationIsComplete(t *testing.T) {
	t.Parallel()

	highest := interfaces.MatterMeasurementClass(0)
	for _, c := range measurementClasses {
		if c > highest {
			highest = c
		}
	}
	next := highest + 1
	if mattercontract.MeasurementClassClusterID(next) != 0 ||
		mattercontract.MeasurementClassDeviceType(next) != 0 {
		t.Errorf("measurement class %d projects onto a cluster or device type but is missing from "+
			"measurementClasses — add it there so the conformance guards cover it", next)
	}
}

// clusterName renders a cluster id for a failure message.
func clusterName(id uint32) string {
	if name, ok := schema.ClusterName(id); ok {
		return name
	}
	return "unknown cluster"
}

// deviceTypeName renders a device-type id for a failure message.
func deviceTypeName(id uint32) string {
	if name, ok := schema.DeviceTypeName(id); ok {
		return name
	}
	return "unknown device type"
}

// serverClusterList renders a device type's permitted server clusters for a
// failure message, so the reader sees the alternative without opening the
// generated table.
func serverClusterList(deviceType uint32) string {
	ids, ok := schema.DeviceTypeServerClusters[deviceType]
	if !ok || len(ids) == 0 {
		return "(none)"
	}
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, fmt.Sprintf("0x%04X %s", id, clusterName(id)))
	}
	return strings.Join(parts, ", ")
}

// TestMeasurementClassesUnderInvestigationAreStillBroken keeps the known-defect
// list honest in the other direction: an entry whose defect has been fixed must
// be deleted, not left behind. A stale entry silences the guard for a class that
// is now fine, and the next regression on that class passes unnoticed.
func TestMeasurementClassesUnderInvestigationAreStillBroken(t *testing.T) {
	t.Parallel()

	for class, defect := range measurementClassesUnderInvestigation {
		if mattercontract.MeasurementClassDeviceType(class) != 0 {
			t.Errorf("measurement class %d now maps to device type 0x%04X, so the defect recorded "+
				"in measurementClassesUnderInvestigation is fixed — delete the entry so the class "+
				"is guarded again. Recorded defect: %s",
				class, mattercontract.MeasurementClassDeviceType(class), defect)
		}
	}
}
