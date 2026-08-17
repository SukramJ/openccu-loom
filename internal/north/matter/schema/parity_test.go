// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package schema_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/schema"
)

// TestParityCodeMatchesGeneratedSchema verifies that the codeRev literals
// below — a maintained snapshot of the revision constants hand-coded in
// the bridge's cluster packages — match the corresponding entry in the
// codegen'd schema maps.
//
// This does NOT read the actual constants from the cluster source
// files: codeRev is a transcribed literal, not an import or a
// MatterRead(0xFFFD) off a constructed server, so a hand-edited
// revision constant that this table is not also updated for will not
// fail here. The per-cluster parity tests
// (parity_matterjs_test.go under each cluster package, e.g.
// internal/north/matter/cluster/core/parity_matterjs_test.go) are the
// guard that reads the real constant for the clusters they cover; this
// test only catches a matter.js HEAD schema-snapshot update
// (`make generate-matter-schema`) that this table's codeRev values
// were not updated to track. When matter.js bumps a revision:
//  1. Run `make generate-matter-schema` — clusters.go is updated.
//  2. This test fails, naming the drifted cluster.
//  3. Update the constant in the cluster source file, then update codeRev here.
func TestParityCodeMatchesGeneratedSchema(t *testing.T) {
	t.Parallel()

	// Each pair maps a cluster ID to the revision value currently used in
	// the corresponding cluster source file. Covers all clusters from the
	// Covers: core/, measurement/, and wire/ packages.
	//
	// Source references (package → file → constant):
	//   core/access_control.go        → accessControlClusterRevision
	//   core/basic_information.go     → basicInfoClusterRevision
	//   core/binding.go               → bindingClusterRevision
	//   core/bridged_device_basic_information.go → bridgedBasicInfoClusterRevision
	//   core/descriptor.go            → descriptorClusterRevision
	//   core/diagnostic_logs.go       → diaglogsClusterRevision
	//   core/general_commissioning.go → gencommClusterRevision
	//   core/general_diagnostics.go   → gendiagClusterRevision
	//   core/group_key_management.go  → groupKeyMgmtClusterRevision
	//   core/icd_management.go        → icdClusterRevision
	//   core/identify.go              → identifyClusterRevision
	//   core/network_commissioning.go → netcommClusterRevision
	//   core/operational_credentials.go → opcredsClusterRevision
	//   core/ota_software_update_requestor.go → otaRequestorClusterRevision
	//   core/power_source.go          → powersrcClusterRevision
	//   core/time_synchronization.go  → timeSyncClusterRevision
	//   measurement/measurement.go    → tempMeasClusterRevision, humidityClusterRevision, etc.
	//   wire/genericswitch.go         → switchClusterRevision
	//   wire/admincommissioning.go    → admCommClusterRevision
	//   wire/groups.go                → groupsClusterRevision
	//   model/generic/switch_matter.go → matterGenericOnOffClusterRevision
	//   model/custom/light/matter.go  → matterOnOffClusterRevision, matterLevelControlClusterRevision, etc.
	//   model/custom/climate/matter.go → matterThermClusterRevision, etc.
	//   model/custom/cover/matter.go  → matterWindowCoveringClusterRevision
	//   model/custom/lock/matter.go   → the DoorLock cluster's revision constant
	//     (also covered directly by model/custom/lock/parity_matterjs_test.go,
	//     which reads it via MatterRead(0xFFFD) off a constructed server)
	//   model/custom/siren/matter.go  → matterSmokeCOAlarmClusterRevision
	cases := []struct {
		id      uint32
		name    string // human-readable; used only in error messages
		codeRev uint16
	}{
		// core/ cluster servers
		{0x001F, "AccessControl", 3},
		{0x0028, "BasicInformation", 6},
		{0x001E, "Binding", 1},
		{0x0039, "BridgedDeviceBasicInformation", 6},
		{0x001D, "Descriptor", 3},
		{0x0032, "DiagnosticLogs", 1},
		{0x0030, "GeneralCommissioning", 2},
		{0x0033, "GeneralDiagnostics", 3},
		{0x003F, "GroupKeyManagement", 3},
		{0x0046, "IcdManagement", 3},
		{0x0003, "Identify", 6},
		{0x0031, "NetworkCommissioning", 2},
		{0x003E, "OperationalCredentials", 2},
		{0x002A, "OtaSoftwareUpdateRequestor", 1},
		{0x002F, "PowerSource (core)", 3},
		{0x0038, "TimeSynchronization", 2},

		// measurement/ cluster servers
		{0x0402, "TemperatureMeasurement", 6},
		{0x0405, "RelativeHumidityMeasurement", 5},
		{0x0400, "IlluminanceMeasurement", 5},
		{0x0403, "PressureMeasurement", 5},
		{0x0045, "BooleanState", 3},
		{0x0406, "OccupancySensing", 7},
		{0x040D, "CarbonDioxideConcentrationMeasurement", 5},
		{0x042A, "Pm25ConcentrationMeasurement", 5},
		{0x042D, "Pm10ConcentrationMeasurement", 5},
		{0x0090, "ElectricalPowerMeasurement", 3},
		{0x0091, "ElectricalEnergyMeasurement", 2},

		// wire/ cluster servers
		{0x003B, "Switch (GenericSwitch)", 2},
		{0x003C, "AdministratorCommissioning", 1},

		// model/generic + model/custom cluster servers
		{0x0006, "OnOff", 6},
		{0x0004, "Groups", 4},
		{0x0008, "LevelControl", 7},
		{0x0300, "ColorControl", 9},
		{0x0201, "Thermostat", 11},
		{0x0204, "ThermostatUserInterfaceConfiguration", 2},
		{0x0102, "WindowCovering", 8},
		{0x0101, "DoorLock", 10},
		{0x005C, "SmokeCoAlarm", 2},
		{0x0062, "ScenesManagement", 1},
	}

	for _, c := range cases {
		schemaRev, ok := schema.ClusterRevision(c.id)
		if !ok {
			t.Errorf("cluster 0x%04X (%s) not found in generated schema — refresh matter-schema-snapshot.json and run make generate-matter-schema",
				c.id, c.name)
			continue
		}
		if c.codeRev != schemaRev {
			t.Errorf("cluster 0x%04X (%s): this table's codeRev %d != matter.js schema revision %d — update BOTH the constant in the cluster source file AND codeRev here",
				c.id, c.name, c.codeRev, schemaRev)
		}
	}
}

// TestDeviceTypeRevisionLookup verifies the DeviceTypeRevision helper returns
// the expected revisions for the device types the bridge currently advertises.
// Mirrors the cases in internal/north/matter/cluster/core/parity_matterjs_test.go
// TestParityMatterJS_DeviceTypeRevisions.
func TestDeviceTypeRevisionLookup(t *testing.T) {
	t.Parallel()

	cases := []struct {
		id      uint32
		name    string
		wantRev uint16
	}{
		{0x0016, "RootNode", 4},
		{0x000E, "Aggregator", 2},
		{0x0013, "BridgedNode", 3},
		{0x0015, "ContactSensor", 2},
		{0x0043, "WaterLeakDetector", 2},
		{0x002C, "AirQualitySensor", 1},
		{0x0076, "SmokeCoAlarm", 1},
		{0x0106, "LightSensor", 4},
		{0x0107, "OccupancySensor", 4},
		{0x0302, "TemperatureSensor", 3},
		{0x0305, "PressureSensor", 3},
		{0x0307, "HumiditySensor", 3},
		{0x000F, "GenericSwitch", 3},
		{0x0100, "OnOffLight", 3},
		{0x0101, "DimmableLight", 3},
		{0x010A, "OnOffPlugInUnit", 4},
		{0x010C, "ColorTemperatureLight", 4},
		{0x010D, "ExtendedColorLight", 4},
		{0x0202, "WindowCovering", 6},
		{0x0301, "Thermostat", 6},
		{0x000A, "DoorLock", 4},
	}

	for _, c := range cases {
		rev, ok := schema.DeviceTypeRevision(c.id)
		if !ok {
			t.Errorf("device type 0x%04X (%s) not found in generated schema", c.id, c.name)
			continue
		}
		if rev != c.wantRev {
			t.Errorf("device type 0x%04X (%s): schema revision %d != expected %d",
				c.id, c.name, rev, c.wantRev)
		}
	}
}

// TestSchemaCompleteness verifies the generated maps are non-empty and
// contain the baseline cluster + device-type count from matter.js HEAD.
// A sudden drop here means the snapshot was truncated or the generator broke.
func TestSchemaCompleteness(t *testing.T) {
	t.Parallel()

	const (
		minClusters    = 100 // matter.js HEAD has 115; allow some slack for future removals
		minDeviceTypes = 60  // matter.js HEAD has 72
	)

	if got := len(schema.ClusterRevisions); got < minClusters {
		t.Errorf("ClusterRevisions has %d entries, want at least %d — regenerate schema", got, minClusters)
	}
	if got := len(schema.ClusterNames); got != len(schema.ClusterRevisions) {
		t.Errorf("ClusterNames (%d) and ClusterRevisions (%d) have different lengths", len(schema.ClusterNames), len(schema.ClusterRevisions))
	}
	if got := len(schema.DeviceTypeRevisions); got < minDeviceTypes {
		t.Errorf("DeviceTypeRevisions has %d entries, want at least %d — regenerate schema", got, minDeviceTypes)
	}
	if got := len(schema.DeviceTypeNames); got != len(schema.DeviceTypeRevisions) {
		t.Errorf("DeviceTypeNames (%d) and DeviceTypeRevisions (%d) have different lengths", len(schema.DeviceTypeNames), len(schema.DeviceTypeRevisions))
	}
}
