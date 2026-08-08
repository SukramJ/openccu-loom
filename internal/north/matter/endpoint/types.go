// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package endpoint

import (
	"sync"

	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/north/matter/store"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
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
	// ModelComplete reports whether this central's initial device load
	// has finished, i.e. Devices is the authoritative full fleet rather
	// than a boot-time (still-empty or partially loaded) view. The
	// assembler only garbage-collects persisted endpoint-ID rows for
	// model-complete snapshots: the topology is first assembled at
	// daemon start, before the readiness-gated CCU device load, and a
	// central that has not loaded yet must keep every persisted
	// endpoint number so its bridged endpoints reappear under their old
	// IDs once the load completes. Mirrors matter.js, which reserves
	// every persisted endpoint number at node initialization
	// (packages/node/src/storage/server/ServerEndpointStores.ts,
	// assignNumber + the load() pre-allocation pass) and releases one
	// only on explicit endpoint deletion
	// (packages/node/src/node/server/ServerEndpointInitializer.ts,
	// eraseDescendant) — never because state has not been populated yet.
	// See BD-Matter-EndpointID-Persistent in notes/parity/by_design.md.
	ModelComplete bool
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

	// versions is the shared per-cluster DataVersion tracker set for a
	// BRIDGED endpoint (keyed by cluster id). Bridged cluster servers are
	// materialised fresh on every dispatch (see [ClusterServers]) and the
	// *Endpoint struct itself is rebuilt on every [Assembler.Assemble], so
	// an instance-embedded tracker would install a new random initial
	// version after every reassembly — controllers' DataVersionFilters
	// then never match and Apple re-transfers every endpoint on each
	// re-subscribe. The set is owned by the assembler's [versionRegistry]
	// and keyed there by the endpoint's stable [Endpoint.SourceKey], so
	// the SAME (device, channel, dp) keeps ONE tracker set across every
	// reassembly for the process lifetime: an already-paired endpoint's
	// DataVersion survives an UNRELATED topology change (a sibling
	// endpoint added / removed), and only a state change on the endpoint
	// itself bumps it. Mirrors matter.js
	// packages/node/src/behavior/state/managed/Datasource.ts:349 (version
	// sampled once per behavior lifetime, bound to the endpoint's own
	// lifecycle) / :949 (increment per committed change). nil for the
	// root + aggregator endpoints (they keep their reattached
	// instance-hosted trackers) and for bare test-constructed endpoints —
	// [Endpoint.clusterTracker] then lazily installs a private set so the
	// endpoint still tracks a version in isolation.
	versionsMu sync.Mutex
	versions   *clusterVersionSet
}

// clusterTracker returns (lazily creating) the DataVersion tracker for
// clusterID. Assembler-built bridged endpoints share the tracker set the
// [versionRegistry] keyed by [Endpoint.SourceKey], so the version
// survives reassembly; a bare endpoint (versions nil) gets a private
// set. Safe for concurrent use.
func (e *Endpoint) clusterTracker(clusterID uint32) *hmtypes.DataVersionTracker {
	e.versionsMu.Lock()
	if e.versions == nil {
		e.versions = newClusterVersionSet()
	}
	set := e.versions
	e.versionsMu.Unlock()
	return set.tracker(clusterID)
}

// clusterVersionSet is a shared, concurrency-safe set of per-cluster
// DataVersion trackers keyed by cluster id. One set is bound to one
// stable endpoint identity for the process lifetime; the same set is
// handed to every *Endpoint the assembler rebuilds for that identity so
// the version survives reassembly. Two *Endpoint instances — the
// outgoing topology still serving in-flight reads and the incoming one —
// may reference the same set concurrently, hence the internal mutex.
type clusterVersionSet struct {
	mu       sync.Mutex
	trackers map[uint32]*hmtypes.DataVersionTracker
}

func newClusterVersionSet() *clusterVersionSet {
	return &clusterVersionSet{trackers: make(map[uint32]*hmtypes.DataVersionTracker)}
}

// tracker returns (lazily creating) the tracker for clusterID.
func (s *clusterVersionSet) tracker(clusterID uint32) *hmtypes.DataVersionTracker {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.trackers[clusterID]
	if t == nil {
		t = &hmtypes.DataVersionTracker{}
		s.trackers[clusterID] = t
	}
	return t
}

// versionRegistry maps a stable endpoint identity ([store.EndpointKey])
// to its [clusterVersionSet]. It is owned by the [Assembler] and lives
// across every [Assembler.Assemble], so a bridged endpoint that persists
// through a reassembly reuses its existing tracker set (stable
// DataVersion) while a genuinely new endpoint gets a fresh set (fresh
// random-seeded version). [store.EndpointKey] is the natural key: it is
// the matter_endpoints primary key — deterministic from the model-side
// {central, device, channel, dp} identity (stable across reassembly for
// the same source) and unique per source (two devices never collide). In
// memory only; a full daemon restart re-randomizes, which matches
// matter.js re-seeding the Datasource version on reboot.
type versionRegistry struct {
	mu   sync.Mutex
	sets map[store.EndpointKey]*clusterVersionSet
}

func newVersionRegistry() *versionRegistry {
	return &versionRegistry{sets: make(map[store.EndpointKey]*clusterVersionSet)}
}

// setFor returns the tracker set for key, creating it on first use. The
// returned pointer is stable for key across the registry's lifetime.
func (r *versionRegistry) setFor(key store.EndpointKey) *clusterVersionSet {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.sets[key]
	if s == nil {
		s = newClusterVersionSet()
		r.sets[key] = s
	}
	return s
}

// retain drops every tracker set whose key is absent from keep. Called
// after each assembly with the set of keys the assembly produced, so a
// removed / de-exposed endpoint releases its version — a later re-add
// then gets a fresh random-seeded one, matching matter.js destroying the
// Datasource on endpoint removal. Bounds registry growth to the live
// topology.
func (r *versionRegistry) retain(keep map[store.EndpointKey]struct{}) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for k := range r.sets {
		if _, ok := keep[k]; !ok {
			delete(r.sets, k)
		}
	}
}

// ClusterDataVersion returns the stable per-(endpoint, cluster)
// DataVersion for a bridged endpoint. First access installs the
// random non-zero initial value (see [hmtypes.DataVersionTracker]).
func (e *Endpoint) ClusterDataVersion(clusterID uint32) uint32 {
	return e.clusterTracker(clusterID).Current()
}

// BumpClusterDataVersion advances the per-(endpoint, cluster)
// DataVersion after a state change so DataVersionFilters miss and
// subscribers receive the fresh value. Mirrors matter.js
// Datasource.ts:949.
func (e *Endpoint) BumpClusterDataVersion(clusterID uint32) {
	e.clusterTracker(clusterID).Bump()
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
