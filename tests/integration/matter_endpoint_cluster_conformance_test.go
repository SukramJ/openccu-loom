//go:build integration

// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package integration

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/go-fabric/schema"

	"github.com/SukramJ/openccu-loom/internal/ccudata"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/client/backends"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/store/visibility"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// A bridged endpoint advertises one primary device type and a set of server
// clusters. The Matter Device Library specifies, per device type, which
// clusters it may serve — and, separately, which clusters it CONSUMES from
// other endpoints as a client. Serving a cluster the type names only as a
// client is as non-conformant as serving one it does not name at all.
//
// Ecosystems react to that in ways ranging from ignoring the extra cluster to
// mis-categorising the whole accessory: Alexa recognises a bridged endpoint
// only by the clusters its device type specifies and drops the rest
// (matter.js docs/ECOSYSTEMS, "Composed Devices"), and Apple's HAP-service
// mapper keys on the first DeviceTypeList entry.
//
// This guard walks the hydrated fleet, takes every custom data point that is
// an [interfaces.MatterEndpointSource], and checks its own declared device
// type against its own declared cluster servers. It uses the fleet rather
// than a hand-kept type table because the cluster set is dynamic — a Climate
// mounts humidity only when the device has it, a Switch mounts power
// measurement only once AttachPowerEnergySources found it — so only real
// devices exercise the branches that actually ship.
//
// The measurement-source half of the same contract is static and lives in
// tests/contract/matter_measurement_devicetype_conformance_test.go.

// matterDeviceTypeBridgedNode is the secondary device type every bridged
// endpoint carries alongside its primary one. endpoint.ClusterServers builds
// Descriptor.DeviceTypeList as [primary, BridgedNode], and an endpoint's
// permitted server clusters are the union of BOTH types' requirements — which
// is what makes PowerSource (0x002F) legitimate on a bridged endpoint: the
// Device Library names it for BridgedNode, not for the primary type.
const matterDeviceTypeBridgedNode = 0x0013

// bridgeMountedClusters are mounted by the bridge layer on every bridged
// endpoint regardless of device type. Only Identify remains here: it is
// mandatory on every endpoint but Root and NetworkCommissioning (spec §1.4)
// and materialize.go mounts it unconditionally, yet the utility device types
// (ElectricalSensor 0x0510, PowerSource 0x0011) carry no Identify requirement
// of their own. Descriptor and BridgedDeviceBasicInformation are NOT listed —
// BridgedNode requires both, so the union below already permits them, and
// whitelisting a cluster the schema can vouch for only hides drift.
var bridgeMountedClusters = map[uint32]string{
	0x0003: "Identify — mandatory on every endpoint but Root/NetworkCommissioning (spec §1.4)",
}

// endpointAllowsServerCluster reports whether a bridged endpoint whose primary
// device type is deviceType may serve clusterID, taking the BridgedNode
// secondary type into account.
func endpointAllowsServerCluster(deviceType, clusterID uint32) bool {
	if allowed, _ := schema.DeviceTypeAllowsServerCluster(deviceType, clusterID); allowed {
		return true
	}
	allowed, _ := schema.DeviceTypeAllowsServerCluster(matterDeviceTypeBridgedNode, clusterID)
	return allowed
}

// nonConformantEndpointClusters records cluster/device-type pairs that are
// mounted today and that the Matter Device Library does not permit. These are
// known defects, not exemptions: each entry names what the Device Library
// actually says and what the fix has to establish.
//
// Kept separate from a by-design divergence list on purpose. A divergence
// that is deliberate is recorded in notes/parity/by_design.md and carries a
// reason the spec is knowingly departed from; an entry here carries no such
// reason, because there is none — it is simply not fixed yet. Merging the two
// would let a defect inherit a rationale it does not have.
//
// Emptying this map is the goal. Key format: "0x%04X/0x%04X" (device type,
// cluster).
var nonConformantEndpointClusters = map[string]string{}

// TestBridgedEndpointClustersConformToTheirDeviceType walks the fleet and
// checks every MatterEndpointSource's cluster set against the Device Library
// entry for the device type it declares.
func TestBridgedEndpointClustersConformToTheirDeviceType(t *testing.T) {
	devices := hydrateFleetForMatterConformance(t)

	type finding struct {
		deviceType uint32
		clusterID  uint32
		witness    string // a concrete model/channel to look at
	}

	var (
		findings     []finding
		seenPairs    = map[string]bool{}
		usedEntries  = map[string]bool{}
		sourcesSeen  int
		clustersSeen int
	)

	for _, dev := range devices {
		for _, ch := range dev.Channels() {
			cdp := ch.CustomDataPoint()
			if cdp == nil {
				continue
			}
			src, ok := cdp.(interfaces.MatterEndpointSource)
			if !ok {
				continue
			}
			sourcesSeen++

			deviceType := uint32(src.MatterDeviceType())
			if deviceType == 0 {
				t.Errorf("%s channel %d (%T) is a MatterEndpointSource declaring device type 0: its "+
					"endpoint would advertise DeviceTypeList=[BridgedNode] with no primary type",
					dev.Model, ch.Number, cdp)
				continue
			}
			if _, known := schema.DeviceTypeServerClusters[deviceType]; !known {
				t.Errorf("%s channel %d (%T) declares device type 0x%04X, absent from the matter.js "+
					"HEAD schema snapshot — wrong id, or the snapshot is stale "+
					"(`make generate-matter-schema`)", dev.Model, ch.Number, cdp, deviceType)
				continue
			}

			for _, server := range src.MatterClusterServers() {
				if server == nil {
					continue
				}
				clusterID := server.MatterClusterID()
				clustersSeen++

				if _, isBridgeMounted := bridgeMountedClusters[clusterID]; isBridgeMounted {
					continue
				}
				if endpointAllowsServerCluster(deviceType, clusterID) {
					continue
				}

				key := conformanceKey(deviceType, clusterID)
				if _, declared := nonConformantEndpointClusters[key]; declared {
					usedEntries[key] = true
					continue
				}
				if seenPairs[key] {
					continue // one finding per (device type, cluster), not per device
				}
				seenPairs[key] = true
				findings = append(findings, finding{
					deviceType: deviceType,
					clusterID:  clusterID,
					witness:    fmt.Sprintf("%s channel %d (%T)", dev.Model, ch.Number, cdp),
				})
			}
		}
	}

	// Negative control: a walk that reaches no endpoint source, or no cluster,
	// would report a clean fleet no matter how broken the mapping is.
	if sourcesSeen == 0 {
		t.Fatal("the fleet produced no MatterEndpointSource at all — the hydration or the walk is " +
			"broken and this guard would pass vacuously")
	}
	if clustersSeen == 0 {
		t.Fatalf("%d endpoint sources produced no cluster servers at all — MatterClusterServers is "+
			"returning empty and this guard would pass vacuously", sourcesSeen)
	}

	sort.Slice(findings, func(i, j int) bool {
		if findings[i].deviceType != findings[j].deviceType {
			return findings[i].deviceType < findings[j].deviceType
		}
		return findings[i].clusterID < findings[j].clusterID
	})
	for _, f := range findings {
		t.Errorf("device type 0x%04X (%s) serves cluster 0x%04X (%s), which the Matter Device "+
			"Library does not specify for it as a server. Seen on %s. Permitted server clusters: %s. "+
			"A cluster the type names only as a client is consumed from another endpoint, not served "+
			"here — expose it as its own endpoint of the type that does specify it",
			f.deviceType, deviceTypeNameOrUnknown(f.deviceType), f.clusterID,
			clusterNameOrUnknown(f.clusterID), f.witness,
			serverClustersOf(f.deviceType)+", plus BridgedNode's: "+serverClustersOf(matterDeviceTypeBridgedNode))
	}

	// A declared defect that the fleet no longer reaches is either fixed or
	// unreachable; both mean the entry must go, so a real regression on that
	// pair is not silently absorbed.
	for key, defect := range nonConformantEndpointClusters {
		if !usedEntries[key] {
			t.Errorf("nonConformantEndpointClusters[%q] was not hit by any device in the fleet — "+
				"the defect is fixed, or no fleet device exercises it any more. Delete the entry. "+
				"Recorded defect: %s", key, defect)
		}
	}

	t.Logf("checked %d endpoint sources across %d devices, %d cluster mounts, %d declared defects",
		sourcesSeen, len(devices), clustersSeen, len(nonConformantEndpointClusters))
}

// hydrateFleetForMatterConformance brings up the embedded device fleet through
// the real ingest pipeline, so the custom data points under test are the ones
// a running daemon builds.
func hydrateFleetForMatterConformance(t *testing.T) []*device.Device {
	t.Helper()

	srv := startMockCCUWithDevices(t, snapshotDevices(t))
	xmlClient := newXMLRPCClient(t, srv.URL())
	backend := backends.NewCcuBackend(&xmlrpcBackendCaller{client: xmlClient}, nil, nil)

	c, err := central.New(central.Config{Name: "matter-conformance-ccu"})
	if err != nil {
		t.Fatalf("central: %v", err)
	}
	translations, err := ccudata.LoadTranslationsEmbedded()
	if err != nil {
		t.Fatalf("translations: %v", err)
	}
	pipeline := adapter.NewDevicePipeline(c).
		WithTranslations(translations, snapshotLocale()).
		WithVisibility(visibility.NewRegistry())

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	if err := pipeline.IngestFromBackend(
		ctx, "HmIP-RF", hmenum.InterfaceHmIPRF, backend, nil, nil, slog.New(slog.DiscardHandler),
	); err != nil {
		t.Fatalf("ingest: %v", err)
	}

	devices := c.ModelRegistry.List()
	if len(devices) == 0 {
		t.Fatal("the fleet hydrated no devices at all — the walk is broken and this test would pass vacuously")
	}
	return devices
}

// conformanceKey renders the (device type, cluster) pair used to key
// nonConformantEndpointClusters.
func conformanceKey(deviceType, clusterID uint32) string {
	return fmt.Sprintf("0x%04X/0x%04X", deviceType, clusterID)
}

func clusterNameOrUnknown(id uint32) string {
	if name, ok := schema.ClusterName(id); ok {
		return name
	}
	return "unknown cluster"
}

func deviceTypeNameOrUnknown(id uint32) string {
	if name, ok := schema.DeviceTypeName(id); ok {
		return name
	}
	return "unknown device type"
}

// serverClustersOf renders a device type's permitted server clusters so a
// failure names the alternative without the reader opening the generated table.
func serverClustersOf(deviceType uint32) string {
	ids, ok := schema.DeviceTypeServerClusters[deviceType]
	if !ok || len(ids) == 0 {
		return "(none)"
	}
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, fmt.Sprintf("0x%04X %s", id, clusterNameOrUnknown(id)))
	}
	return strings.Join(parts, ", ")
}
