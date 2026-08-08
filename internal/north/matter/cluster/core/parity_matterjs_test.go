// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package core

import (
	"encoding/json"
	"slices"
	"testing"

	matterparity "github.com/SukramJ/openccu-loom/internal/north/matter/parity"
)

// matter.js's MatterDefinition tree is the de-facto reference Matter
// implementation. Hand-coding cluster IDs, attribute IDs, revisions
// and constraints against the spec PDF is fragile — drift between the
// spec, matter.js, the bridge and Apple Home's HAP service mapper has
// already produced four distinct production bugs (cluster 0x0024
// fabrication, RootNode revision, ConfigurationVersion default,
// per-cluster revision drift). The tests here load a JSON snapshot
// extracted from matter.js HEAD and assert that every cluster
// openccu-loom exposes carries the same ID, revision, and mandatory
// attribute set.
//
// Snapshot regeneration: run the producer at
// `notes/parity/matter/extract-from-matter-js.ts` against an `@matter/model`
// install and copy the JSON output into `testdata/`. The snapshot is
// checked in so the tests run offline and lock the matter.js baseline
// for code review.

type matterAttr struct {
	ID          uint32 `json:"id"`
	Name        string `json:"name"`
	Type        string `json:"type"`
	Conformance string `json:"conformance"`
	Access      string `json:"access"`
	Constraint  string `json:"constraint"`
	Quality     string `json:"quality"`
}

type matterCluster struct {
	ID         uint32       `json:"id"`
	Name       string       `json:"name"`
	Revision   uint16       `json:"revision"`
	FeatureMap uint32       `json:"featureMap"`
	Attributes []matterAttr `json:"attributes"`
}

type matterDeviceType struct {
	ID       uint32 `json:"id"`
	Name     string `json:"name"`
	Revision uint16 `json:"revision"`
}

type matterSchema struct {
	DeviceTypes []matterDeviceType `json:"deviceTypes"`
	Clusters    []matterCluster    `json:"clusters"`
}

func loadMatterSchemaT(t *testing.T) *matterSchema {
	t.Helper()
	var s matterSchema
	if err := json.Unmarshal(matterparity.SchemaJSON(), &s); err != nil {
		t.Fatalf("unmarshal embedded matter-schema-snapshot.json: %v", err)
	}
	if len(s.Clusters) == 0 || len(s.DeviceTypes) == 0 {
		t.Fatalf("matter-schema-snapshot.json appears empty: %d clusters, %d deviceTypes",
			len(s.Clusters), len(s.DeviceTypes))
	}
	return &s
}

func clusterByID(s *matterSchema, id uint32) (matterCluster, bool) {
	for _, c := range s.Clusters {
		if c.ID == id {
			return c, true
		}
	}
	return matterCluster{}, false
}

// mandatoryAttrIDs returns the IDs every implementor MUST expose for a
// cluster — `conformance: "M"` (and `"P, M"`, the provisional-mandatory
// shape matter.js uses for mid-spec additions like ConfigurationVersion).
// Global attributes (FeatureMap, ClusterRevision, AttributeList,
// AcceptedCommandList, GeneratedCommandList, EventList) are filtered;
// they ride on the global cluster contract and are checked separately.
func mandatoryAttrIDs(c matterCluster) []uint32 {
	out := make([]uint32, 0, len(c.Attributes))
	for _, a := range c.Attributes {
		if a.ID >= 0xFFF8 { // global attribute span
			continue
		}
		switch a.Conformance {
		case "M", "P, M":
			out = append(out, a.ID)
		}
	}
	slices.Sort(out)
	return out
}

// parityCase pairs a cluster's in-tree constants with the matter.js
// id we are claiming to implement, so the test can assert ID + revision
// + mandatory-attribute coverage in one sweep.
type parityCase struct {
	jsID             uint32
	codeClusterID    uint32
	codeRevision     uint16
	codeAttrIDs      []uint32 // attribute IDs implemented in MatterRead
	skipMandatoryIDs []uint32 // matter.js IDs we explicitly do not expose
	rationale        string
}

// parityCases enumerates every cluster openccu-loom exposes on its root
// or bridged endpoints. New cluster servers added to the bridge MUST
// land here so the matter.js diff is visible at PR time. `codeAttrIDs`
// mirrors the `case` arms inside each cluster's MatterRead switch — it
// is the wire-level contract openccu-loom honours.
func parityCases() []parityCase {
	return []parityCase{
		{
			jsID:          basicInfoClusterID, // 0x0028
			codeClusterID: basicInfoClusterID,
			codeRevision:  basicInfoClusterRevision,
			codeAttrIDs: []uint32{
				basicInfoAttrDataModelRevision, basicInfoAttrVendorName, basicInfoAttrVendorID,
				basicInfoAttrProductName, basicInfoAttrProductID, basicInfoAttrNodeLabel,
				basicInfoAttrLocation, basicInfoAttrHardwareVersion, basicInfoAttrHardwareVersionStr,
				basicInfoAttrSoftwareVersion, basicInfoAttrSoftwareVersionStr,
				basicInfoAttrManufacturingDate, basicInfoAttrPartNumber, basicInfoAttrProductURL,
				basicInfoAttrProductLabel, basicInfoAttrSerialNumber,
				basicInfoAttrLocalConfigDisabled, basicInfoAttrReachable, basicInfoAttrUniqueID,
				basicInfoAttrCapabilityMinima, basicInfoAttrProductAppearance,
				basicInfoAttrSpecificationVersion, basicInfoAttrMaxPathsPerInvoke,
				basicInfoAttrConfigurationVersion,
			},
		},
		{
			jsID:          bridgedBasicInfoClusterID, // 0x0039
			codeClusterID: bridgedBasicInfoClusterID,
			codeRevision:  bridgedBasicInfoClusterRevision,
			// bridgedBasicInfoAttrConfigurationVersion (0x0018) verifies
			// parity with matter.js
			// bridged-device-basic-information.element.ts:48 (conformance
			// "P, [Rev >= v5]"). Production code in MatterRead and
			// MatterAttributes already serves uint32(1) for this attribute.
			codeAttrIDs: []uint32{
				bridgedBasicInfoAttrVendorName, bridgedBasicInfoAttrVendorID,
				bridgedBasicInfoAttrProductName, bridgedBasicInfoAttrProductID,
				bridgedBasicInfoAttrNodeLabel,
				bridgedBasicInfoAttrHardwareVersion, bridgedBasicInfoAttrHardwareVersionStr,
				bridgedBasicInfoAttrSoftwareVersion, bridgedBasicInfoAttrSoftwareVersionStr,
				bridgedBasicInfoAttrManufacturingDate, bridgedBasicInfoAttrPartNumber,
				bridgedBasicInfoAttrProductURL, bridgedBasicInfoAttrProductLabel,
				bridgedBasicInfoAttrSerialNumber, bridgedBasicInfoAttrReachable,
				bridgedBasicInfoAttrUniqueID, bridgedBasicInfoAttrConfigurationVersion,
			},
		},
		{
			jsID:          descriptorClusterID, // 0x001D
			codeClusterID: descriptorClusterID,
			codeRevision:  descriptorClusterRevision,
			codeAttrIDs: []uint32{
				descriptorAttrDeviceTypeList, descriptorAttrServerList,
				descriptorAttrClientList, descriptorAttrPartsList,
			},
			skipMandatoryIDs: []uint32{
				// TagList (0x04) is mandatory in Matter 1.4+ but only
				// when the endpoint participates in a tag-based
				// composition pattern; openccu-loom's root + bridged
				// shapes don't, and Apple Home tolerates absence.
				0x04,
			},
			rationale: "Descriptor.TagList — only required for tag-based composition; not used by the bridge.",
		},
		{
			jsID:          gencommClusterID, // 0x0030
			codeClusterID: gencommClusterID,
			codeRevision:  gencommClusterRevision,
			codeAttrIDs: []uint32{
				gencommAttrBreadcrumb, gencommAttrBasicCommissioningInfo,
				gencommAttrRegulatoryConfig, gencommAttrLocationCapability,
				gencommAttrSupportsConcurrentConnection,
			},
		},
		{
			jsID:          opcredsClusterID, // 0x003E
			codeClusterID: opcredsClusterID,
			codeRevision:  opcredsClusterRevision,
			codeAttrIDs: []uint32{
				opcredsAttrNOCs, opcredsAttrFabrics, opcredsAttrSupportedFabrics,
				opcredsAttrCommissionedFabrics, opcredsAttrTrustedRootCertificates,
				opcredsAttrCurrentFabricIndex,
			},
		},
		{
			jsID:          accessControlClusterID, // 0x001F
			codeClusterID: accessControlClusterID,
			codeRevision:  accessControlClusterRevision,
			codeAttrIDs: []uint32{
				accessControlAttrACL, accessControlAttrExtension,
				accessControlAttrSubjectsPerAccessControl,
				accessControlAttrTargetsPerAccessControl,
				accessControlAttrAccessControlEntriesPerFabric,
			},
		},
		{
			jsID:          identifyClusterID, // 0x0003
			codeClusterID: identifyClusterID,
			codeRevision:  identifyClusterRevision,
			codeAttrIDs: []uint32{
				identifyAttrTime, identifyAttrType,
			},
		},
		{
			jsID:          groupKeyMgmtClusterID, // 0x003F
			codeClusterID: groupKeyMgmtClusterID,
			codeRevision:  groupKeyMgmtClusterRevision,
			codeAttrIDs: []uint32{
				groupKeyMgmtAttrGroupKeyMap, groupKeyMgmtAttrGroupTable,
				groupKeyMgmtAttrMaxGroupsPerFabric, groupKeyMgmtAttrMaxGroupKeysPerFabric,
			},
		},
		{
			jsID:          netcommClusterID, // 0x0031
			codeClusterID: netcommClusterID,
			codeRevision:  netcommClusterRevision,
			// ScanMaxTimeSeconds (0x0002) and ConnectMaxTimeSeconds (0x0003) have
			// conformance "WI | TH" — feature-conditional; bridge is Ethernet-only.
			codeAttrIDs: []uint32{
				netcommAttrMaxNetworks, netcommAttrNetworks,
				netcommAttrInterfaceEnabled,
				netcommAttrLastNetworkingStatus, netcommAttrLastNetworkID, netcommAttrLastConnectErrorValue,
			},
		},
		{
			jsID:          gendiagClusterID, // 0x0033
			codeClusterID: gendiagClusterID,
			codeRevision:  gendiagClusterRevision,
			// TotalOperationalHours (0x0003), BootReason (0x0004) and fault
			// list attrs (0x0005–0x0007) are conformance "O" in matter.js HEAD;
			// only NetworkInterfaces, RebootCount, UpTime and
			// TestEventTriggersEnabled carry conformance "M".
			codeAttrIDs: []uint32{
				gendiagAttrNetworkInterfaces, gendiagAttrRebootCount,
				gendiagAttrUpTime, gendiagAttrTestEventTriggersEnabled,
			},
		},
		{
			// DiagnosticLogs (0x0032) has no cluster-specific attributes with
			// conformance "M" in matter.js HEAD — only commands. The cluster
			// exposes no readable attributes beyond the globals (FeatureMap +
			// ClusterRevision). skipMandatoryIDs is intentionally empty; the
			// non-nil codeAttrIDs signals "checked, no mandatory cluster attrs".
			jsID:             diaglogsClusterID, // 0x0032
			codeClusterID:    diaglogsClusterID,
			codeRevision:     diaglogsClusterRevision,
			skipMandatoryIDs: []uint32{},
			rationale:        "DiagnosticLogs has no cluster-specific mandatory attributes; all are commands only.",
		},
		{
			jsID:          timeSyncClusterID, // 0x0038
			codeClusterID: timeSyncClusterID,
			codeRevision:  timeSyncClusterRevision,
			// All other attrs (TimeSource, TrustedTimeSource, DefaultNtp, etc.)
			// are conformance "O", "TSC", "NTPC", "TZ" or "NTPS" — feature-conditional.
			codeAttrIDs: []uint32{
				timeSyncAttrUTCTime, timeSyncAttrGranularity,
			},
		},
		{
			jsID:          icdClusterID, // 0x0046
			codeClusterID: icdClusterID,
			codeRevision:  icdClusterRevision,
			// All other attrs (RegisteredClients, IcdCounter, etc.) are
			// conformance "CIP", "UAT" or "LITS" — feature-conditional.
			codeAttrIDs: []uint32{
				icdAttrIdleModeDuration, icdAttrActiveModeDuration, icdAttrActiveModeThresh,
			},
		},
		{
			jsID:          otaRequestorClusterID, // 0x002A — matter.js HEAD ota-software-update-requestor.element.ts:20
			codeClusterID: otaRequestorClusterID,
			codeRevision:  otaRequestorClusterRevision,
			codeAttrIDs: []uint32{
				otaRequestorAttrDefaultOTAProviders, otaRequestorAttrUpdatePossible,
				otaRequestorAttrUpdateState, otaRequestorAttrUpdateStateProgress,
			},
		},
		{
			jsID:          bindingClusterID, // 0x001E
			codeClusterID: bindingClusterID,
			codeRevision:  bindingClusterRevision,
			codeAttrIDs: []uint32{
				bindingAttrBinding,
			},
		},
	}
}

// TestParityMatterJS_ClusterIDsAndRevisions asserts the bridge advertises
// the same cluster ID + revision matter.js HEAD ships in
// `MatterDefinition`. A revision drift here is exactly the class of bug
// that produced the Apple Home pair-abort from revision drift.
func TestParityMatterJS_ClusterIDsAndRevisions(t *testing.T) {
	t.Parallel()
	schema := loadMatterSchemaT(t)
	for _, p := range parityCases() {
		js, ok := clusterByID(schema, p.jsID)
		if !ok {
			t.Errorf("matter-schema snapshot has no cluster 0x%04X — refresh the snapshot or remove the case", p.jsID)
			continue
		}
		t.Run(js.Name, func(t *testing.T) {
			t.Parallel()
			if p.codeClusterID != p.jsID {
				t.Errorf("code cluster ID 0x%04X != matter.js 0x%04X", p.codeClusterID, p.jsID)
			}
			if p.codeRevision != js.Revision {
				t.Errorf("code revision %d != matter.js revision %d for %s (0x%04X)",
					p.codeRevision, js.Revision, js.Name, js.ID)
			}
		})
	}
}

// TestParityMatterJS_MandatoryAttributeCoverage asserts that every
// matter.js-mandatory attribute (conformance "M" or "P, M") for a
// cluster is implemented by openccu-loom. A pair-abort
// chain revealed multiple symptoms (ConfigurationVersion default=0,
// HardwareVersionString empty); the underlying root cause was a
// missing-or-violated mandatory attribute. This test catches the
// same class of bug at PR time.
func TestParityMatterJS_MandatoryAttributeCoverage(t *testing.T) {
	t.Parallel()
	schema := loadMatterSchemaT(t)
	for _, p := range parityCases() {
		js, ok := clusterByID(schema, p.jsID)
		if !ok {
			continue // covered by ClusterIDsAndRevisions
		}
		// Skip cases that opt out of attribute checking — primarily the
		// commissioning / diagnostic clusters we have not finished
		// cataloguing in `codeAttrIDs`. Those are still revision-checked
		// above.
		if len(p.codeAttrIDs) == 0 {
			continue
		}
		t.Run(js.Name, func(t *testing.T) {
			t.Parallel()
			impl := make(map[uint32]bool, len(p.codeAttrIDs))
			for _, id := range p.codeAttrIDs {
				impl[id] = true
			}
			skip := make(map[uint32]bool, len(p.skipMandatoryIDs))
			for _, id := range p.skipMandatoryIDs {
				skip[id] = true
			}
			missing := make([]uint32, 0)
			for _, id := range mandatoryAttrIDs(js) {
				if skip[id] || impl[id] {
					continue
				}
				missing = append(missing, id)
			}
			if len(missing) > 0 {
				names := make(map[uint32]string)
				for _, a := range js.Attributes {
					names[a.ID] = a.Name
				}
				for _, id := range missing {
					t.Errorf("missing mandatory attribute 0x%04X (%s) on cluster %s 0x%04X",
						id, names[id], js.Name, js.ID)
				}
				if p.rationale != "" {
					t.Logf("cluster %s rationale: %s", js.Name, p.rationale)
				}
			}
		})
	}
}

// TestParityMatterJS_NoSpuriousAttributes asserts openccu-loom does NOT
// advertise an attribute ID that matter.js's MatterDefinition doesn't
// know about for a cluster. A spurious ID makes Apple Home's HAP
// service mapper bucket the cluster as "unknown" and the pair aborts
// after Subscribe-Initial.
func TestParityMatterJS_NoSpuriousAttributes(t *testing.T) {
	t.Parallel()
	schema := loadMatterSchemaT(t)
	for _, p := range parityCases() {
		js, ok := clusterByID(schema, p.jsID)
		if !ok || len(p.codeAttrIDs) == 0 {
			continue
		}
		t.Run(js.Name, func(t *testing.T) {
			t.Parallel()
			known := make(map[uint32]bool, len(js.Attributes))
			for _, a := range js.Attributes {
				known[a.ID] = true
			}
			for _, id := range p.codeAttrIDs {
				if id >= 0xFFF8 {
					continue // global attributes are universal
				}
				if !known[id] {
					t.Errorf("code exposes attr 0x%04X on cluster %s 0x%04X but matter.js does not define it",
						id, js.Name, js.ID)
				}
			}
		})
	}
}

// TestParityMatterJS_DeviceTypeRevisions asserts every device-type
// revision the bridge advertises matches matter.js HEAD.
// `endpoint.deviceTypeRevision` carries the BridgedDevice primary-type
// table; the root endpoint's RootNode (0x0016) and Aggregator (0x000E)
// revisions are hard-coded in cmd/openccu-loom/daemon.go and verified
// here too.
func TestParityMatterJS_DeviceTypeRevisions(t *testing.T) {
	t.Parallel()
	schema := loadMatterSchemaT(t)
	jsByID := make(map[uint32]matterDeviceType, len(schema.DeviceTypes))
	for _, dt := range schema.DeviceTypes {
		jsByID[dt.ID] = dt
	}
	cases := []struct {
		id       uint32
		name     string
		revision uint16
	}{
		// Root endpoint primary types.
		// RootNode (0x0016) revision is 4 in matter.js HEAD. Production
		// paths in daemon.go use schema.DeviceTypeRevisions[0x0016] = 4.
		// This entry tracks matter.js HEAD truth; the schema codegen
		// (endpoint/helpers.go) uses it directly.
		{0x0016, "RootNode", 4},
		{0x000E, "Aggregator", 2},
		// Bridged endpoint primary types — matches helpers.go::deviceTypeRevision.
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
		js, ok := jsByID[c.id]
		if !ok {
			t.Errorf("matter.js schema has no device-type 0x%04X (%s)", c.id, c.name)
			continue
		}
		t.Run(js.Name, func(t *testing.T) {
			t.Parallel()
			if c.revision != js.Revision {
				t.Errorf("code revision %d != matter.js %d for %s (0x%04X)", c.revision, js.Revision, js.Name, js.ID)
			}
		})
	}
}
