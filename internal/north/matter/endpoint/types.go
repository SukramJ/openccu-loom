// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package endpoint

import (
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/north/matter/store"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// Snapshot is one central's contribution to a topology assembly.
// The caller (typically the daemon bootstrap or the bridge core)
// builds snapshots by reading each Unit.ModelRegistry and
// passes the slice to [Assembler.Assemble].
type Snapshot struct {
	// CentralName scopes every endpoint produced from Devices to
	// this central — required for multi-CCU correctness.
	CentralName string
	// Devices is the list of model devices visible on this central
	// at snapshot time. nil-safe — empty slice produces zero
	// endpoints.
	Devices []*device.Device
}

// Endpoint describes one Matter endpoint in the assembled topology.
//
// Topology mirrors matter.js's bridge pattern
// (`examples/device-bridge-onoff/src/BridgedDevicesNode.ts`):
//
//   - ID 0 = RootNode endpoint (DeviceType 0x0016). Carries
//     BasicInformation, OperationalCredentials, GeneralCommissioning,
//     NetworkCommissioning, etc.; PartsList=[1].
//   - ID 1 = Aggregator endpoint (DeviceType 0x000E). Carries only
//     Descriptor (mandatory) + optional Identify; PartsList enumerates
//     every bridged endpoint.
//   - ID ≥ 2 = bridged endpoints (Source / Measurement set,
//     BridgedDeviceBasicInformation + per-device clusters).
//
// Apple Home iOS rejects the older "composed bridge" pattern where
// EP 0 carries both RootNode + Aggregator device types and lists
// bridged endpoints directly: HMMTRAccessoryServerBrowser fails to
// locate the Aggregator and renders the bridge as empty even though
// the pair lands. matter.js HEAD has never supported that layout —
// `ServerNode.create` always produces the three-tier shape above.
type Endpoint struct {
	// ID is the Matter endpoint identifier. 0 = root, 1 = aggregator,
	// 2..65534 = bridged.
	ID uint16
	// DeviceType is the Matter Device Type ID this endpoint
	// advertises (e.g. 0x010A OnOffPlugInUnit, 0x0301 Thermostat,
	// 0x000E AggregatorEndpoint for the root).
	DeviceType uint16
	// Reachable mirrors the underlying device's availability. Maps
	// to BridgedDeviceBasicInformation.Reachable (Matter §9.13.3).
	// Always true for the root endpoint.
	Reachable bool
	// FriendlyName is the human-readable label used by
	// BridgedDeviceBasicInformation.NodeLabel. Empty for the root
	// endpoint (the root carries the bridge's NodeLabel directly,
	// supplied via Config).
	FriendlyName string
	// BridgedDevice points to the source device, or nil for the
	// root endpoint. The bridge reads availability + product name
	// from here.
	BridgedDevice *device.Device
	// Channel points to the source channel, or nil for the root
	// endpoint. Sensor sub-endpoints (DPKindMeasurement) point at
	// the channel that hosts the measurement DP — the measurement
	// itself rides in Measurement.
	Channel *device.Channel
	// Source is the rich-model implementation of the cluster
	// surface. nil for the root endpoint and for measurement-only
	// sub-endpoints (which use Measurement instead).
	Source interfaces.MatterEndpointSource
	// Measurement is set on standalone sensor endpoints assembled
	// from MatterMeasurementSource implementers. nil otherwise.
	Measurement interfaces.MatterMeasurementSource
	// SourceKey is the persisted endpoint identity. Empty for the
	// root endpoint.
	SourceKey store.EndpointKey

	// BridgeVendorID / BridgeProductID carry the bridge-wide VID/PID
	// the assembler copies from the topology. The bridged-endpoint
	// BridgedDeviceBasicInformation cluster server reports these so an
	// operator-supplied production VID/PID surfaces on every bridged
	// endpoint instead of the CSA test pair (0xFFF1/0x8001). Zero
	// values trigger the test-pair fallback for the dev workflow.
	BridgeVendorID  uint16
	BridgeProductID uint16

	// ParentEndpointID is the Matter endpoint ID of the parent
	// endpoint in the bridge hierarchy. For bridged endpoints (ID ≥ 2)
	// the parent is always the Aggregator (ID 1). For the root (ID 0)
	// and Aggregator (ID 1) themselves it is 0 (no parent).
	//
	// chip bridge-app sets parentEndpointId = 1 for every bridged
	// endpoint via AddDeviceEndpoint(..., parentEndpointId). matter.js
	// establishes the same relationship via aggregator.add(child).
	// Per Matter §9.5.3, ParentEndpoint is optional for flat bridges
	// (all bridged directly under Aggregator) but chip populates it
	// for every bridged endpoint. We mirror chip's behaviour.
	// Mirrors chip examples/bridge-app/linux/main.cpp:261-276
	// AddDeviceEndpoint(..., parentEndpointId=1).
	ParentEndpointID uint16
	// HasParentEndpointID is true when ParentEndpointID carries a
	// meaningful value (i.e. for any bridged endpoint with ID ≥ 2).
	HasParentEndpointID bool

	// RootClusterServers carries the cluster servers the daemon
	// constructs and attaches to the root endpoint (BasicInformation,
	// GeneralCommissioning, OperationalCredentials, NetworkCommissioning,
	// etc.). Populated only when [Endpoint.IsRoot] is true; nil for
	// every bridged endpoint. The dispatcher consults this slice via
	// [ClusterServers] when routing root-endpoint reads / writes.
	RootClusterServers []interfaces.MatterClusterServer

	// AggregatorClusterServers carries the cluster servers attached to
	// the Aggregator endpoint (ID 1) — Descriptor (mandatory) +
	// optionally Identify. Populated only when [Endpoint.IsAggregator]
	// is true; nil for every other endpoint. Mirrors matter.js's
	// AggregatorEndpoint requirements (Parts + Index mandatory, Identify
	// optional) in `packages/node/src/endpoints/aggregator.ts`.
	AggregatorClusterServers []interfaces.MatterClusterServer
}

// IsRoot reports whether this is the root bridge endpoint (ID 0).
func (e *Endpoint) IsRoot() bool { return e.ID == 0 }

// IsAggregator reports whether this is the Aggregator endpoint (ID 1).
// The aggregator hosts only the Descriptor (mandatory) + optional
// Identify clusters; its PartsList enumerates every bridged endpoint.
func (e *Endpoint) IsAggregator() bool { return e.ID == 1 }

// Topology is the assembled output: an ordered list of endpoints
// and the bridge metadata used to populate the BasicInformation
// cluster on the root endpoint.
type Topology struct {
	// Endpoints is the assembled list. Index 0 is always the root
	// bridge endpoint; the rest are bridged endpoints sorted by
	// endpoint_id ascending.
	Endpoints []*Endpoint
	// VendorID is the bridge's IANA-assigned vendor identifier
	// (BasicInformation.VendorID, Matter §11.1.5.2).
	VendorID uint16
	// ProductID is the bridge's vendor-assigned product identifier
	// (BasicInformation.ProductID, Matter §11.1.5.4).
	ProductID uint16
	// NodeLabel is the bridge's user-visible label
	// (BasicInformation.NodeLabel, Matter §11.1.5.6).
	NodeLabel string
}

// FindByID returns the endpoint with id ID, or nil when no such
// endpoint exists. The lookup is linear; topologies fit in cache so
// the cost is negligible.
func (t *Topology) FindByID(id uint16) *Endpoint {
	for _, ep := range t.Endpoints {
		if ep.ID == id {
			return ep
		}
	}
	return nil
}

// Bridged returns endpoints with ID >= 2 (everything except root and
// the aggregator). Apple's HAP service mapper iterates this set when
// it builds the per-device list; the aggregator itself is structural
// scaffolding and never surfaces as a HomeKit accessory.
func (t *Topology) Bridged() []*Endpoint {
	if len(t.Endpoints) <= 2 {
		return nil
	}
	out := make([]*Endpoint, 0, len(t.Endpoints)-2)
	for _, ep := range t.Endpoints {
		if ep.IsRoot() || ep.IsAggregator() {
			continue
		}
		out = append(out, ep)
	}
	return out
}
