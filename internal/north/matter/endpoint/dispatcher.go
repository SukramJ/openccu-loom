// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package endpoint

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/internal/north/matter/schema"
	"github.com/SukramJ/openccu-loom/internal/north/matter/store"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// clusterDataVersion returns the current DataVersion for srv by
// type-asserting to [interfaces.MatterClusterDataVersion]. Falls back
// to 0 when the cluster does not implement the capability — the IM
// layer then encodes 1 (the spec-minimum) per AttributeDataIB rules.
func clusterDataVersion(srv interfaces.MatterClusterServer) uint32 {
	if dv, ok := srv.(interfaces.MatterClusterDataVersion); ok {
		return dv.MatterDataVersion()
	}
	return 0
}

// clusterDataVersionFor resolves the DataVersion for srv on ep. Root
// and Aggregator servers are persistent instances, so their embedded
// tracker is authoritative. Bridged endpoints materialise their
// cluster servers fresh on every dispatch ([ClusterServers]) — an
// instance-embedded tracker would report a new random initial version
// on every read, so their version identity lives on the persistent
// [Endpoint] keyed by cluster id instead. Mirrors matter.js
// Datasource.ts:349 (version set once per lifetime, not per access).
func clusterDataVersionFor(ep *Endpoint, srv interfaces.MatterClusterServer) uint32 {
	if ep == nil || ep.IsRoot() || ep.IsAggregator() {
		return clusterDataVersion(srv)
	}
	return ep.ClusterDataVersion(srv.MatterClusterID())
}

// TopologyDispatcher bridges the assembled [Topology] to the
// Interaction-Model [im.Dispatcher] surface. Each Read / Write /
// Invoke call resolves the addressed endpoint via the topology, then
// the cluster server via [ClusterServers], then the attribute /
// command on the server.
//
// Concurrency: TopologyDispatcher is safe for concurrent calls from
// multiple IM workers. The underlying Topology is treated as
// immutable for the dispatcher's lifetime — when the model changes,
// the bridge constructs a fresh Topology via [Assembler.Assemble]
// and swaps the dispatcher's reference.
type TopologyDispatcher struct {
	topology *Topology
	acl      ACLLister
}

// ACLLister is the subset of the Matter ACL store [TopologyDispatcher.CheckACL]
// consults. Production daemons wire the SQLite-backed `matter/store.Store`.
// Leaving it nil does NOT disable enforcement — [TopologyDispatcher.CheckACL]
// then denies every operational request; a setup that deliberately runs
// without stored entries wires [UnenforcedACL] instead.
type ACLLister interface {
	ListACL(ctx context.Context, fabricIndex uint8) ([]store.ACLEntry, error)
}

// UnenforcedACL is an [ACLLister] that grants every operational request on
// every fabric.
//
// It exists so that running without stored AccessControl entries is a
// decision written down in one greppable place. The alternative — treating a
// missing lister as "enforcement off" — made the whole gate depend on a
// single wiring line whose removal no test could observe: the capability kept
// passing its own tests, the pin that watched the wiring only looked for the
// method name in the source, and every ACL entry a controller had written was
// silently unenforced on operational reads, writes and invokes.
//
// Production never wires this. Tests and local development do, where the
// alternative is that no request at all is answered.
// loom:reachable:reason="the documented opt-out for a deployment that deliberately runs the Matter bridge without stored access-control entries, selected by an operator rather than by the daemon; nothing in production selects it, which is the point — access control now fails closed by default"
type UnenforcedACL struct{}

// ListACL returns one wildcard CASE entry at Administer privilege: no
// subject restriction, no target restriction, every fabric.
func (UnenforcedACL) ListACL(context.Context, uint8) ([]store.ACLEntry, error) {
	return []store.ACLEntry{{Privilege: store.PrivilegeAdminister, AuthMode: store.AuthModeCASE}}, nil
}

// NewTopologyDispatcher wraps t. Returns nil when t is nil so the
// caller can short-circuit before dispatching.
func NewTopologyDispatcher(t *Topology) *TopologyDispatcher {
	if t == nil {
		return nil
	}
	return &TopologyDispatcher{topology: t}
}

// SetACLLister wires the ACL source that [TopologyDispatcher.CheckACL]
// enforces against (see Bridge.AttachACLLister).
//
// Passing nil leaves the gate closed rather than open: every operational
// (CASE) request is denied until a source is wired. Pass [UnenforcedACL] to
// run without stored entries on purpose.
func (d *TopologyDispatcher) SetACLLister(l ACLLister) {
	if d != nil {
		d.acl = l
	}
}

// Compile-time assertion: TopologyDispatcher satisfies im.Dispatcher
// and the optional im.DataVersionReader + im.AttributeReadPrivilegeProvider
// + im.ACLChecker + im.AuthorizingWriter interfaces.
var (
	_ im.Dispatcher                     = (*TopologyDispatcher)(nil)
	_ im.DataVersionReader              = (*TopologyDispatcher)(nil)
	_ im.AttributeReadPrivilegeProvider = (*TopologyDispatcher)(nil)
	_ im.ACLChecker                     = (*TopologyDispatcher)(nil)
	_ im.AuthorizingWriter              = (*TopologyDispatcher)(nil)
)

// Read implements [im.Dispatcher]. Wildcards expand as follows:
//
//   - HasEndpoint=false  → iterate every endpoint in the topology.
//   - HasCluster=false   → iterate every cluster server on the endpoint.
//   - HasAttribute=false → globals (FeatureMap + ClusterRevision)
//     PLUS every attribute the cluster server advertises via the
//     [interfaces.MatterClusterAttributeLister] optional interface.
//     Cluster servers that do not implement that interface fall back
//     to globals-only — that is the v1.1 baseline preserved for
//     backwards compatibility.
//
// Concrete (non-wildcard) reads return exactly one ReadResult.
//
// ctx carries the FabricFiltered flag + FabricIndex via
// [im.WithFabricFilter] / [im.FabricFilterFromContext]. Cluster servers
// that implement [interfaces.FabricScopedReader] receive MatterReadFiltered
// instead of MatterRead so they can project fabric-sensitive list
// attributes (OperationalCredentials.Fabrics, AccessControl.ACL) down
// to the requesting fabric. Mirrors matter.js InteractionServer.ts:
// startReadInteraction → OnlineContext.forFabricFilteredRead.
func (d *TopologyDispatcher) Read(ctx context.Context, path im.ConcreteAttributePath) []im.ReadResult {
	endpoints := d.resolveEndpoints(path)
	if len(endpoints) == 0 {
		return []im.ReadResult{{Path: path, Status: im.StatusUnsupportedEndpoint}}
	}
	wildcardEndpoint := !path.HasEndpoint
	var results []im.ReadResult
	for _, ep := range endpoints {
		ePath := path
		ePath.Endpoint = ep.ID
		ePath.HasEndpoint = true

		servers := d.serversFor(ep, path)
		if len(servers) == 0 {
			// Wildcard endpoint expansion (Matter §4.5.4 "Path
			// Generation"): paths that match no concrete cluster on
			// this endpoint are SILENTLY SKIPPED, not surfaced as
			// UnsupportedCluster — the spec says the generator only
			// emits paths where the cluster/attribute actually
			// exists. Concrete endpoints (operator named the EP
			// explicitly) keep the error surface so callers see why
			// their read returned nothing.
			if wildcardEndpoint {
				continue
			}
			results = append(results, im.ReadResult{Path: ePath, Status: im.StatusUnsupportedCluster})
			continue
		}
		for _, srv := range servers {
			cPath := ePath
			cPath.Cluster = srv.MatterClusterID()
			cPath.HasCluster = true

			attrs := d.attributesFor(srv, path)
			for _, attrID := range attrs {
				aPath := cPath
				aPath.Attribute = attrID
				aPath.HasAttribute = true
				results = append(results, readOne(ctx, ep, srv, aPath))
			}
		}
	}
	return results
}

// Write implements [im.Dispatcher]. Wildcards expand the same way as
// Read, but every match is dispatched separately and one
// [im.WriteResult] is returned per match. Unauthorized callers must go
// through [TopologyDispatcher.WriteAuthorized] — Write itself applies no
// access control.
func (d *TopologyDispatcher) Write(ctx context.Context, path im.ConcreteAttributePath, value im.AttributeValue) []im.WriteResult {
	return d.WriteAuthorized(ctx, path, value, nil)
}

// WriteAuthorized implements [im.AuthorizingWriter]. It is
// [TopologyDispatcher.Write] with an access-control gate evaluated at
// every RESOLVED (endpoint, cluster, attribute) — the only place a
// wildcard-endpoint write can be authorized, since the requested path
// names no endpoint. authorize may be nil, which dispatches without a
// gate.
func (d *TopologyDispatcher) WriteAuthorized(ctx context.Context, path im.ConcreteAttributePath, value im.AttributeValue, authorize im.WriteAuthorizer) []im.WriteResult {
	endpoints := d.resolveEndpoints(path)
	if len(endpoints) == 0 {
		return []im.WriteResult{{Path: path, Status: im.StatusUnsupportedEndpoint}}
	}
	wildcardEndpoint := !path.HasEndpoint
	var results []im.WriteResult
	for _, ep := range endpoints {
		ePath := path
		ePath.Endpoint = ep.ID
		ePath.HasEndpoint = true

		servers := d.serversFor(ep, path)
		if len(servers) == 0 {
			// Wildcard endpoint expansion: skip endpoints that don't
			// host the requested cluster (Matter §4.5.4). Concrete
			// endpoint addressing keeps the explicit error surface.
			if wildcardEndpoint {
				continue
			}
			results = append(results, im.WriteResult{Path: ePath, Status: im.StatusUnsupportedCluster})
			continue
		}
		for _, srv := range servers {
			cPath := ePath
			cPath.Cluster = srv.MatterClusterID()
			cPath.HasCluster = true

			attrs := d.attributesFor(srv, path)
			for _, attrID := range attrs {
				aPath := cPath
				aPath.Attribute = attrID
				aPath.HasAttribute = true

				// Read-only-attribute write gate (Matter §8.6). matter.js
				// rejects a WriteRequest to a non-writable attribute BEFORE
				// any behavior runs, so the write never reaches the cluster
				// server (and, downstream, the CCU). The writability verdict
				// derives from the matter.js schema access string via
				// schema.AttributeWritable. A concrete path is answered with
				// UNSUPPORTED_WRITE (mirrors
				// ../matter.js/packages/protocol/src/action/server/AttributeWriteResponse.ts:229-231
				// `if (!limits.writable) … return this.#asStatus(path, Status.UnsupportedWrite)`);
				// a wildcard path silently skips the attribute (mirrors
				// AttributeWriteResponse.ts:329-331
				// `if (!attribute.limits.writable) return;`). Attributes with
				// no read-only record — globals, writable attrs, clusters
				// outside the table (known == false) — fall through unchanged.
				if writable, known := schema.AttributeWritable(cPath.Cluster, attrID); known && !writable {
					// "Concrete" means endpoint AND cluster AND attribute are
					// all named — the split matter.js makes before it picks a
					// write path (AttributeWriteResponse.ts:65). A wildcard
					// endpoint is a wildcard path even with a concrete
					// attribute, and reporting a status per resolved endpoint
					// would both diverge from the spec and enumerate which
					// endpoints host the cluster to a peer whose authorization
					// the gate below has not evaluated yet.
					if !wildcardEndpoint && path.HasAttribute {
						results = append(results, im.WriteResult{Path: aPath, Status: im.StatusUnsupportedWrite})
					}
					continue
				}

				// Access-control gate at the RESOLVED location, evaluated
				// after the writability verdict and before the value is
				// applied — the order matter.js uses in
				// ../matter.js/packages/protocol/src/action/server/AttributeWriteResponse.ts:324-343
				// (`if (!attribute.limits.writable) return;` then
				// `session.authorityAt(attribute.limits.writeLevel, location)`).
				// A denied wildcard-endpoint location is skipped silently so
				// the response discloses only authorized endpoints; a denied
				// concrete path keeps the explicit status.
				if authorize != nil {
					if status := authorize(aPath.Endpoint, aPath.Cluster, aPath.Attribute); !status.IsSuccess() {
						if path.HasEndpoint {
							results = append(results, im.WriteResult{Path: aPath, Status: status})
						}
						continue
					}
				}

				res := writeOne(ctx, srv, aPath, value)
				// A successful write mutated cluster state; advance the
				// endpoint-hosted DataVersion so DataVersionFilters miss
				// and subscribers see the change (matter.js
				// Datasource.ts:949). Root/Aggregator servers bump their
				// own persistent trackers inside the write handler.
				if res.Status == im.StatusSuccess && !ep.IsRoot() && !ep.IsAggregator() {
					ep.BumpClusterDataVersion(cPath.Cluster)
				}
				results = append(results, res)
			}
		}
	}
	return results
}

// Invoke implements [im.Dispatcher]. CommandPath has no wildcard
// fields in our supported subset (Matter §10.6.7 lets EndpointID
// wildcard for InvokeRequest only when the cluster is universally
// addressable — none of ours are), so this is always a concrete
// dispatch returning exactly one [im.InvokeResult].
func (d *TopologyDispatcher) Invoke(ctx context.Context, path im.ConcreteCommandPath, fields any) im.InvokeResult {
	if !path.HasEndpoint {
		return im.InvokeResult{Path: path, Status: im.StatusUnsupportedEndpoint}
	}
	ep := d.topology.FindByID(path.Endpoint)
	if ep == nil {
		return im.InvokeResult{Path: path, Status: im.StatusUnsupportedEndpoint}
	}
	for _, srv := range ClusterServers(ep) {
		if srv.MatterClusterID() != path.Cluster {
			continue
		}
		resp, err := srv.MatterInvoke(ctx, path.Command, fields, hmenum.CommandPriorityHigh)
		if err != nil {
			status, cs, hasCS := classifyError(err, invokeErrorStatus)
			return im.InvokeResult{Path: path, Response: resp, Status: status, ClusterStatus: cs, HasClusterStatus: hasCS}
		}
		return im.InvokeResult{Path: path, Response: resp, Status: im.StatusSuccess}
	}
	return im.InvokeResult{Path: path, Status: im.StatusUnsupportedCluster}
}

// resolveEndpoints expands the wildcard-aware endpoint selector. A
// concrete endpoint that does not exist returns an empty slice so
// the caller can synthesise an UnsupportedEndpoint status. Wildcard
// endpoint includes the root (endpoint 0) — Apple Home's
// post-CommissioningComplete wildcard subscribe needs Endpoint 0
// reports (BasicInformation.VendorID/ProductID/NodeLabel, …) to
// build its HAP service map. Without them Apple shows
// `<MTRDevice ... VID: Unknown, PID: Unknown>` and aborts pairing
// at HMMTRAccessoryPairingStep_BuildingHAPServicesAndCharacteristicsFromCHIP
// with HAPErrorDomain Code 24.
func (d *TopologyDispatcher) resolveEndpoints(path im.ConcreteAttributePath) []*Endpoint {
	if path.HasEndpoint {
		ep := d.topology.FindByID(path.Endpoint)
		if ep == nil {
			return nil
		}
		return []*Endpoint{ep}
	}
	// All endpoints, root first so wildcard reports surface
	// device-identity attributes (VendorID/ProductID/UniqueID on the
	// root) before the bridged endpoints. The aggregator endpoint
	// (EP 1) MUST be included between root and the bridged endpoints:
	// Apple Home reads Aggregator.Descriptor.{DeviceTypeList, PartsList,
	// ServerList} from the Subscribe-Initial cache during HAP service
	// rebuild — a missing aggregator report makes
	// `_attributeValueDictionaryForAttributePath` log "PartsList absent
	// from cache" and Apple aborts the pair with HAPErrorDomain Code 14
	// ("Could not construct any of the services of node ...").
	// `Topology.Bridged()` excludes both root and aggregator, so we
	// surface the aggregator explicitly here.
	out := make([]*Endpoint, 0, len(d.topology.Endpoints))
	if root := d.topology.FindByID(0); root != nil {
		out = append(out, root)
	}
	if agg := d.topology.FindByID(1); agg != nil {
		out = append(out, agg)
	}
	out = append(out, d.topology.Bridged()...)
	return out
}

// serversFor narrows the cluster-server set for ep based on the
// path's cluster selector. Returns a slice (possibly empty) of cluster
// servers; an empty slice on a concrete cluster means the endpoint
// exists but the cluster is absent.
func (d *TopologyDispatcher) serversFor(ep *Endpoint, path im.ConcreteAttributePath) []interfaces.MatterClusterServer {
	all := ClusterServers(ep)
	if !path.HasCluster {
		return all
	}
	for _, srv := range all {
		if srv.MatterClusterID() == path.Cluster {
			return []interfaces.MatterClusterServer{srv}
		}
	}
	return nil
}

// attributesFor selects the attribute IDs for a Read / Write across
// the cluster server. Concrete attribute → single-element slice.
// Wildcard attribute → the universal globals merged with the
// cluster's full attribute surface.
//
// Cluster servers expose their attribute set via the optional
// [interfaces.MatterClusterAttributeLister] interface. When a cluster
// does not implement it, we fall back to [interfaces.MatterClusterServer.MatterReportable]
// — that is at least the subscribable attribute surface, which gives
// Apple Home / matter.js something to bind to. The strict spec
// behaviour (every defined attribute) is achieved by implementing
// MatterAttributes on every cluster; the fallback is the safety net
// for cluster servers that have not been migrated yet.
func (d *TopologyDispatcher) attributesFor(srv interfaces.MatterClusterServer, path im.ConcreteAttributePath) []uint32 {
	if path.HasAttribute {
		return []uint32{path.Attribute}
	}
	// All six global attributes per Matter Core Spec §7.13.2 are
	// mandatory on every cluster server. matter.js's behaviour layer
	// auto-generates them at the cluster level; we synthesize them in
	// the dispatcher (see [synthesizeGlobalRead]) when the cluster
	// itself does not return a value. Apple Home's HAP service rebuild
	// reads `AttributeList` (0xFFFB) on every cluster — without it the
	// build fails with HAPErrorDomain Code=24 even when every other
	// cluster surface is correct.
	// EventList (0xFFFA) is a Matter 1.4 global attribute. Apple Home's
	// iOS Matter SDK (still on Matter 1.3 baseline as of iOS 26.4)
	// rejects it with `MTRErrorDomain Code=12 "No known schema for
	// decoding attribute value."` on every cluster that exposes it,
	// then drops the entire ReportData stream — the post-Subscribe-
	// Initial Descriptor.PartsList read returns empty, HAP-Service-
	// Build aborts with HAPErrorDomain Code=14 "No Endpoints In Use",
	// and the controller fires RemoveFabric ~5 s later. Spec §7.13.2
	// allows omitting unsupported global attributes; we drop EventList
	// from wildcard expansion (chip-tool / matter.js controllers do
	// not require it). Re-enable when Apple advances to Matter 1.4.
	out := []uint32{
		cluster.AttrGlobalGeneratedCommandList,
		cluster.AttrGlobalAcceptedCommandList,
		cluster.AttrGlobalAttributeList,
		cluster.AttrGlobalFeatureMap,
		cluster.AttrGlobalClusterRevision,
	}
	var extra []uint32
	if lister, ok := srv.(interfaces.MatterClusterAttributeLister); ok {
		extra = lister.MatterAttributes()
	} else {
		// Fallback: subscribable attribute surface. Better than
		// globals-only for wildcard reads — Apple Home builds its
		// HAP service map from these reports.
		extra = srv.MatterReportable()
	}
	if len(extra) == 0 {
		return out
	}
	seen := make(map[uint32]struct{}, len(out)+len(extra))
	for _, id := range out {
		seen[id] = struct{}{}
	}
	for _, id := range extra {
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	slices.Sort(out)
	return out
}

// readOne dispatches a single Read against srv and packages the
// outcome. Global attributes (Matter §7.13.2) that the cluster server
// does not implement directly fall through to [synthesizeGlobalRead],
// so every cluster surfaces a consistent global-attribute set without
// each cluster server having to repeat the boilerplate.
//
// ctx carries the FabricFiltered flag + FabricIndex. When srv
// implements [interfaces.FabricScopedReader], MatterReadFiltered is
// called in preference to MatterRead so the server can project
// fabric-sensitive lists (e.g. OperationalCredentials.Fabrics) to
// the requesting fabric. Mirrors matter.js InteractionServer.ts:
// startReadInteraction → OnlineContext.forFabricFilteredRead.
//
// DataVersion: when srv implements [interfaces.MatterClusterDataVersion]
// its current DataVersion is stamped on the ReadResult. The IM layer
// uses this in HandleReadRequest for DataVersionFilter evaluation.
// Mirrors matter.js InteractionServer.ts attributeReportPayload building.
func readOne(ctx context.Context, ep *Endpoint, srv interfaces.MatterClusterServer, path im.ConcreteAttributePath) im.ReadResult {
	dv := clusterDataVersionFor(ep, srv)
	// Fabric-scoped attributes: prefer MatterReadFiltered when the
	// cluster server opts in by implementing FabricScopedReader.
	if fsr, ok := srv.(interfaces.FabricScopedReader); ok {
		v, ok := fsr.MatterReadFiltered(ctx, path.Attribute)
		if ok {
			if v == nil {
				return im.ReadResult{Path: path, Value: im.AttributeValue{IsNull: true}, Status: im.StatusSuccess, DataVersion: dv}
			}
			return im.ReadResult{Path: path, Value: im.AttributeValue{Value: v}, Status: im.StatusSuccess, DataVersion: dv}
		}
		// FabricScopedReader returned (nil, false) — fall through to
		// the universal MatterRead path so non-fabric attributes on
		// the same server are still served.
	}
	v, ok := srv.MatterRead(path.Attribute)
	if ok {
		if v == nil {
			return im.ReadResult{Path: path, Value: im.AttributeValue{IsNull: true}, Status: im.StatusSuccess, DataVersion: dv}
		}
		return im.ReadResult{Path: path, Value: im.AttributeValue{Value: v}, Status: im.StatusSuccess, DataVersion: dv}
	}
	// Cluster did not handle the read. Try synthesising the value if
	// it is a global-attribute ID — this lets every cluster expose
	// AttributeList / AcceptedCommandList / GeneratedCommandList /
	// EventList automatically. The cluster-internal MatterRead still
	// wins when it returns a value (so clusters that want a richer
	// AcceptedCommandList can override).
	if synth, synthOK := synthesizeGlobalRead(srv, path.Attribute); synthOK {
		return im.ReadResult{Path: path, Value: im.AttributeValue{Value: synth}, Status: im.StatusSuccess, DataVersion: dv}
	}
	return im.ReadResult{Path: path, Status: im.StatusUnsupportedAttribute}
}

// synthesizeGlobalRead returns the default value for a Matter global
// attribute (Spec §7.13.2) when the cluster server does not handle it
// itself. Returns (value, true) for handled IDs; (nil, false) for any
// non-global attribute. The synthesized values are computed from the
// optional [interfaces.MatterClusterAttributeLister] /
// [interfaces.MatterClusterCommandLister] /
// [interfaces.MatterClusterEventLister] surfaces — clusters that don't
// implement those lister interfaces get an empty list, which is the
// spec-compliant minimum (a cluster with no commands legitimately
// reports `AcceptedCommandList = []`).
//
// AttributeList includes both the cluster's own attributes (via
// MatterAttributes / MatterReportable) and the six global IDs, per
// Matter §7.13.2.4 ("AttributeList SHALL include the IDs of every
// attribute the cluster server implements, including the global
// attributes").
func synthesizeGlobalRead(srv interfaces.MatterClusterServer, attrID uint32) (any, bool) {
	switch attrID {
	case cluster.AttrGlobalAttributeList:
		// EventList (0xFFFA) intentionally omitted — see comment in
		// expandWildcardForCluster. Apple's iOS Matter SDK rejects the
		// whole ReportData stream when AttributeList advertises
		// EventList but the SDK lacks the Matter 1.4 schema for it.
		seen := map[uint32]struct{}{
			cluster.AttrGlobalGeneratedCommandList: {},
			cluster.AttrGlobalAcceptedCommandList:  {},
			cluster.AttrGlobalAttributeList:        {},
			cluster.AttrGlobalFeatureMap:           {},
			cluster.AttrGlobalClusterRevision:      {},
		}
		out := []uint32{
			cluster.AttrGlobalGeneratedCommandList,
			cluster.AttrGlobalAcceptedCommandList,
			cluster.AttrGlobalAttributeList,
			cluster.AttrGlobalFeatureMap,
			cluster.AttrGlobalClusterRevision,
		}
		var clusterAttrs []uint32
		if lister, ok := srv.(interfaces.MatterClusterAttributeLister); ok {
			clusterAttrs = lister.MatterAttributes()
		} else {
			clusterAttrs = srv.MatterReportable()
		}
		for _, id := range clusterAttrs {
			if _, dup := seen[id]; dup {
				continue
			}
			seen[id] = struct{}{}
			out = append(out, id)
		}
		slices.Sort(out)
		return out, true
	case cluster.AttrGlobalAcceptedCommandList:
		if lister, ok := srv.(interfaces.MatterClusterCommandLister); ok {
			return lister.MatterAcceptedCommands(), true
		}
		return []uint32{}, true
	case cluster.AttrGlobalGeneratedCommandList:
		if lister, ok := srv.(interfaces.MatterClusterCommandLister); ok {
			return lister.MatterGeneratedCommands(), true
		}
		return []uint32{}, true
	case cluster.AttrGlobalEventList:
		// Apple's iOS 26 Matter SDK does not yet ship a schema for
		// the Matter 1.4 EventList attribute. Returning [] (or any
		// other value) makes Apple drop the whole ReportData stream
		// with `MTRErrorDomain Code=12 "No known schema for decoding
		// attribute value."` and abort the pair via HAPErrorDomain
		// Code=14. Treat the attribute as unsupported (return false)
		// so the dispatcher emits StatusUnsupportedAttribute, which
		// Apple's IM-Decoder handles cleanly. matter.js / chip-tool
		// tolerate the absence; re-enable once Apple advances to
		// Matter 1.4 SDK schema.
		return nil, false
	}
	return nil, false
}

// writeOne dispatches a single Write against srv. Errors are mapped
// to spec-coded status values where the cluster surfaces them
// distinctly; the catch-all is StatusFailure.
func writeOne(ctx context.Context, srv interfaces.MatterClusterServer, path im.ConcreteAttributePath, value im.AttributeValue) im.WriteResult {
	v := value.Value
	if value.IsNull {
		v = nil
	}
	if err := srv.MatterWrite(ctx, path.Attribute, v, hmenum.CommandPriorityHigh); err != nil {
		status, cs, hasCS := classifyError(err, writeErrorStatus)
		return im.WriteResult{Path: path, Status: status, ClusterStatus: cs, HasClusterStatus: hasCS}
	}
	return im.WriteResult{Path: path, Status: im.StatusSuccess}
}

// classifyError inspects err first for a [im.MatterClusterStatusError]
// (so the cluster-specific code surfaces into StatusIB.ClusterStatus
// per Matter §10.6.2.2) and falls back to the supplied IM-status
// classifier for the generic Status byte. Returns (status, clusterStatus,
// hasClusterStatus).
func classifyError(err error, classify func(error) im.StatusCode) (im.StatusCode, uint8, bool) {
	if err == nil {
		return im.StatusSuccess, 0, false
	}
	if cse, ok := errors.AsType[im.MatterClusterStatusError](err); ok {
		return cse.MatterStatusCode(), cse.MatterClusterStatus(), true
	}
	return classify(err), 0, false
}

// writeErrorStatus maps a cluster-server write error to a Matter IM
// StatusCode. The type-assert against [im.StatusCodeError] takes
// priority — it lets cluster packages carry an exact status code
// without the dispatcher importing their error types. The string
// heuristic below is the legacy fallback for callers that have not
// yet been migrated to the typed interface.
func writeErrorStatus(err error) im.StatusCode {
	if err == nil {
		return im.StatusSuccess
	}
	// Type-assert first: StatusCodeError carries an exact code.
	if sce, ok := errors.AsType[im.StatusCodeError](err); ok {
		return sce.MatterStatusCode()
	}
	// Legacy string-heuristic fallback — migrate callers to StatusCodeError.
	msg := err.Error()
	switch {
	case containsAny(msg, "read-only", "read only"):
		return im.StatusUnsupportedWrite
	case containsAny(msg, "unknown attribute"):
		return im.StatusUnsupportedAttribute
	case containsAny(msg, "resource exhausted"):
		// Mirrors matter.js StatusResponseError(ResourceExhausted) used
		// by AccessControlServer.ts when AccessControlEntriesPerFabric /
		// SubjectsPerAccessControlEntry / TargetsPerAccessControlEntry
		// caps are exceeded.
		return im.StatusResourceExhausted
	case containsAny(msg, "constraint"):
		return im.StatusConstraintError
	}
	return im.StatusFailure
}

// invokeErrorStatus mirrors writeErrorStatus for command dispatch.
// Type-asserts against [im.StatusCodeError] first; falls back to
// the string-heuristic for legacy callers.
func invokeErrorStatus(err error) im.StatusCode {
	if err == nil {
		return im.StatusSuccess
	}
	// Type-assert first: StatusCodeError carries an exact code.
	if sce, ok := errors.AsType[im.StatusCodeError](err); ok {
		return sce.MatterStatusCode()
	}
	// Legacy string-heuristic fallback — migrate callers to StatusCodeError.
	msg := err.Error()
	switch {
	case containsAny(msg, "unknown command", "no commands"):
		return im.StatusUnsupportedCommand
	case containsAny(msg, "constraint"):
		return im.StatusConstraintError
	case containsAny(msg, "invalid command argument"):
		// Mirrors matter.js StatusResponseError(InvalidCommand) — used
		// by cluster handlers (e.g. GroupKeyManagement.KeySetRemove(0))
		// to surface a "command supported but argument rejected" path
		// distinct from UnsupportedCommand (cluster does not implement
		// the command).
		return im.StatusInvalidCommand
	}
	return im.StatusFailure
}

// containsAny reports whether any of subs is present in s.
func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// MinReadPrivilege implements [im.AttributeReadPrivilegeProvider]. It
// looks up the cluster server for (endpoint, clusterID) and consults
// the server's [interfaces.MatterClusterAttributeReadPrivilege] optional
// interface for the given attrID. Returns 1 (View) when the cluster is
// not found, does not implement the interface, or reports no elevated
// requirement for the attribute. Returns the server-reported value (e.g.
// 5 = Administer for AccessControl ACL/Extension) otherwise. Mirrors the
// per-cluster read-access annotations in chip
// src/app/clusters/access-control-server/access-control-server.cpp and
// matter.js packages/model/src/standard/elements/access-control.element.ts.
func (d *TopologyDispatcher) MinReadPrivilege(endpoint uint16, clusterID, attrID uint32) uint8 {
	ep := d.topology.FindByID(endpoint)
	if ep == nil {
		return 1
	}
	for _, srv := range ClusterServers(ep) {
		if srv.MatterClusterID() != clusterID {
			continue
		}
		priv, ok := srv.(interfaces.MatterClusterAttributeReadPrivilege)
		if !ok {
			return 1
		}
		return priv.MinReadPrivilege(attrID)
	}
	return 1
}

// MinWritePrivilege implements [im.AttributeWritePrivilegeProvider]. It
// looks up the cluster server for (endpoint, clusterID) and consults the
// server's [interfaces.MatterClusterAttributeWritePrivilege] optional
// interface for the given attrID. Returns 3 (Operate) — the Matter
// §9.10.4.4 default write privilege — when the cluster is not found, does
// not implement the interface, or reports no elevated requirement.
// Returns the server-reported value (e.g. 4=Manage for
// BasicInformation.NodeLabel, 5=Administer for AccessControl.ACL)
// otherwise. Mirrors the writeAccess bits in matter.js
// packages/model/src/standard/elements/*.element.ts.
func (d *TopologyDispatcher) MinWritePrivilege(endpoint uint16, clusterID, attrID uint32) uint8 {
	ep := d.topology.FindByID(endpoint)
	if ep == nil {
		return 3
	}
	for _, srv := range ClusterServers(ep) {
		if srv.MatterClusterID() != clusterID {
			continue
		}
		priv, ok := srv.(interfaces.MatterClusterAttributeWritePrivilege)
		if !ok {
			return 3
		}
		return priv.MinWritePrivilege(attrID)
	}
	return 3
}

// MinInvokePrivilege implements [im.CommandInvokePrivilegeProvider]. It
// looks up the cluster server for (endpoint, clusterID) and consults the
// server's [interfaces.MatterClusterCommandInvokePrivilege] optional
// interface for the given cmdID. Returns 3 (Operate) — the Matter
// §9.10.4.4 default invoke privilege — when the cluster is not found,
// does not implement the interface, or reports no elevated requirement.
// Returns the server-reported value (e.g. 5=Administer for
// OperationalCredentials.RemoveFabric) otherwise. Mirrors the
// invokeAccess bits in matter.js
// packages/model/src/standard/elements/*.element.ts.
func (d *TopologyDispatcher) MinInvokePrivilege(endpoint uint16, clusterID, cmdID uint32) uint8 {
	ep := d.topology.FindByID(endpoint)
	if ep == nil {
		return 3
	}
	for _, srv := range ClusterServers(ep) {
		if srv.MatterClusterID() != clusterID {
			continue
		}
		priv, ok := srv.(interfaces.MatterClusterCommandInvokePrivilege)
		if !ok {
			return 3
		}
		return priv.MinInvokePrivilege(cmdID)
	}
	return 3
}

// CheckACL implements [im.ACLChecker] (Matter §9.10). It grants the request
// when the requesting fabric holds a CASE ACL entry whose subject covers
// (subjectNodeID, subjectCATs), whose target covers (endpoint, clusterID),
// and whose privilege is at least requiredPrivilege; otherwise it returns
// UnsupportedAccess (0x7e). PASE sessions (fabricIndex 0) are already
// bypassed by the IM gate before this is called.
//
// Every path that is not an explicit grant denies, including the one where
// no ACL source is wired at all — see [TopologyDispatcher.SetACLLister].
//
// Mirrors connectedhomeip/src/access/AccessControl.cpp:441-559 — the
// chip iterator walks every ACE on the fabric and applies the AuthMode →
// Privilege → Subjects → Targets filter chain in that order. Subjects with
// an empty list match any subject on the fabric (wildcard); otherwise each
// entry-subject is interpreted per chip src/lib/core/NodeId.h:
// operational node IDs match equality, CAT subjects (range
// 0xFFFF'FFFD'0000'0000..0xFFFF'FFFD'FFFF'FFFF) match via
// [matchesCATSubject] against the requester's CAT set.
func (d *TopologyDispatcher) CheckACL(ctx context.Context, fabricIndex uint8, subjectNodeID uint64, subjectCATs []uint32, endpoint uint16, clusterID uint32, requiredPrivilege uint8) im.StatusCode {
	if fabricIndex == 0 {
		return im.StatusSuccess // PASE / no fabric — commissioning
	}
	if d == nil || d.acl == nil {
		// Fail closed. A dispatcher without an ACL source cannot tell an
		// authorised controller from any other node that completed CASE, so
		// answering the request would serve exactly what the AccessControl
		// entries exist to gate. The commissioning path is unaffected: it
		// runs over PASE and returned above. A deployment that means to run
		// without stored entries wires [UnenforcedACL].
		return im.StatusUnsupportedAccess
	}
	entries, err := d.acl.ListACL(ctx, fabricIndex)
	if err != nil {
		// Fail closed: an ACL that cannot be evaluated must not grant access.
		return im.StatusUnsupportedAccess
	}
	// Resolve the endpoint once for device-type-restricted targets;
	// nil (no topology / unknown endpoint) conservatively fails those.
	var ep *Endpoint
	if d.topology != nil {
		ep = d.topology.FindByID(endpoint)
	}
	var best store.Privilege
	for _, e := range entries {
		// Operational unicast sessions are CASE-authenticated; only CASE
		// entries apply (Group = multicast, PASE = commissioning).
		if e.AuthMode != store.AuthModeCASE {
			continue
		}
		if !aclSubjectMatches(e.Subjects, subjectNodeID, subjectCATs) {
			continue
		}
		if !aclTargetMatches(e.Targets, ep, endpoint, clusterID) {
			continue
		}
		if e.Privilege > best {
			best = e.Privilege
		}
	}
	if privilegeRank(uint8(best)) >= privilegeRank(requiredPrivilege) {
		return im.StatusSuccess
	}
	return im.StatusUnsupportedAccess
}

// aclSubjectMatches reports whether an ACL entry's Subjects list covers
// the requesting (subjectNodeID, subjectCATs). An empty list means
// "any subject on this fabric" (Matter §9.10.5.6).
//
// Mirrors connectedhomeip/src/access/AccessControl.cpp:463-509 — for each
// listed subject:
//   - operational node id (0x0000'0000'0000'0001..0xFFFF'FFEF'FFFF'FFFF):
//     match when the requester's node id equals it exactly.
//   - CAT (0xFFFF'FFFD'0000'0000..0xFFFF'FFFD'FFFF'FFFF): unpack to
//     identifier+version per CASEAuthTag.h:46-49 and match via
//     [matchesCATSubject] against the requester's CATs.
//
// A non-empty Subjects list that contains no covering entry denies the
// request even when the target+privilege would otherwise grant.
func aclSubjectMatches(subjects []uint64, subjectNodeID uint64, subjectCATs []uint32) bool {
	if len(subjects) == 0 {
		return true
	}
	for _, s := range subjects {
		if isCASEAuthTagSubject(s) {
			if matchesCATSubject(uint32(s&kMaskCASEAuthTag), subjectCATs) {
				return true
			}
			continue
		}
		// Operational node id: exact match. Group ids cannot appear under
		// AuthMode=CASE — chip src/access/AccessControl.cpp:492 enforces
		// the AuthMode-vs-subject coupling; we conservatively drop them.
		if subjectNodeID != 0 && s == subjectNodeID {
			return true
		}
	}
	return false
}

// Matter CASEAuthTag NodeID encoding (chip src/lib/core/NodeId.h:47-49):
// node IDs 0xFFFF'FFFD'0000'0000..0xFFFF'FFFD'FFFF'FFFF are reserved as
// CASE Authenticated Tag subjects; the low 32 bits split into a 16-bit
// identifier (upper) and a 16-bit version (lower) — see
// src/lib/core/CASEAuthTag.h:32-34.
const (
	kMinCASEAuthTag    uint64 = 0xFFFFFFFD00000000
	kMaxCASEAuthTag    uint64 = 0xFFFFFFFDFFFFFFFF
	kMaskCASEAuthTag   uint64 = 0x00000000FFFFFFFF
	kCATIdentifierMask uint32 = 0xFFFF0000
	kCATVersionMask    uint32 = 0x0000FFFF
)

func isCASEAuthTagSubject(s uint64) bool {
	return s >= kMinCASEAuthTag && s <= kMaxCASEAuthTag
}

// matchesCATSubject reports whether any CAT in subjectCATs is granted by
// the entry's CAT subject. Mirrors
// connectedhomeip/src/lib/core/CASEAuthTag.h:174-190
// (CATValues::CheckSubjectAgainstCATs): identifiers must match exactly,
// and the requester's CAT version must be present (>0) and at least the
// entry's version (so a v1 grant covers a v1-NOC holder but a v2 grant
// does not cover a v1-NOC holder).
func matchesCATSubject(entryCAT uint32, subjectCATs []uint32) bool {
	entryID := uint16(((entryCAT & kCATIdentifierMask) >> 16) & 0xFFFF)
	entryVer := uint16((entryCAT & kCATVersionMask) & 0xFFFF)
	for _, sub := range subjectCATs {
		if sub == 0 {
			continue
		}
		subID := uint16(((sub & kCATIdentifierMask) >> 16) & 0xFFFF)
		subVer := uint16((sub & kCATVersionMask) & 0xFFFF)
		if subID != entryID {
			continue
		}
		if subVer == 0 {
			continue // chip CASEAuthTag.h:184 — only present versions match
		}
		if subVer >= entryVer {
			return true
		}
	}
	return false
}

// endpointHasDeviceType reports whether ep advertises deviceType in
// its Descriptor DeviceTypeList — the resolver behind device-type ACL
// targets (chip AccessControl.h:53 DeviceTypeResolver /
// ProviderDeviceTypeResolver.h:34, backed by the data model's
// per-endpoint device-type list). The list mirrors what the Descriptor
// cluster serves: RootNode for EP 0, Aggregator for EP 1, and the
// primary device type + BridgedNode for bridged endpoints (see
// materialize.go DeviceTypeList assembly).
func endpointHasDeviceType(ep *Endpoint, deviceType uint32) bool {
	if ep == nil {
		return false
	}
	switch {
	case ep.IsRoot():
		return deviceType == deviceTypeRootNode
	case ep.IsAggregator():
		return deviceType == deviceTypeAggregator
	default:
		return deviceType == uint32(ep.DeviceType) || deviceType == matterDeviceTypeBridgedNode
	}
}

// aclTargetMatches reports whether an ACL entry's target list covers
// (endpoint, cluster). An empty list means "all targets" (Matter §9.10.4.5).
// A DeviceType field restricts the target to endpoints hosting that
// device type — chip AccessControl.cpp:529-530
// `IsDeviceTypeOnEndpoint(target.deviceType, requestPath.endpoint)`;
// ep may be nil (unknown endpoint), which conservatively fails any
// device-type-restricted target.
func aclTargetMatches(targets []store.ACLTarget, ep *Endpoint, endpoint uint16, clusterID uint32) bool {
	if len(targets) == 0 {
		return true
	}
	for _, t := range targets {
		if t.Cluster != nil && *t.Cluster != clusterID {
			continue
		}
		if t.Endpoint != nil && *t.Endpoint != endpoint {
			continue
		}
		if t.DeviceType != nil && !endpointHasDeviceType(ep, *t.DeviceType) {
			continue
		}
		return true
	}
	return false
}

// privilegeRank maps a Matter privilege to its hierarchy rank so a higher
// privilege satisfies a lower requirement (Administer > Manage > Operate >
// View; ProxyView grants View-level access). Matter §9.10.5.3.
func privilegeRank(p uint8) int {
	switch store.Privilege(p) {
	case store.PrivilegeAdminister:
		return 4
	case store.PrivilegeManage:
		return 3
	case store.PrivilegeOperate:
		return 2
	case store.PrivilegeView, store.PrivilegeProxyView:
		return 1
	default:
		return 0
	}
}

// CurrentDataVersion implements [im.DataVersionReader]. It looks up the
// cluster server for (endpoint, clusterID) and returns its current
// DataVersion when the server implements [interfaces.MatterClusterDataVersion].
//
// Mirrors matter.js packages/node/src/node/server/InteractionServer.ts
// DataVersion check before write dispatch. Returns (0, false) when the
// cluster is not found or does not implement version tracking — callers
// treat (0, false) as "version not constrained, proceed with the write".
func (d *TopologyDispatcher) CurrentDataVersion(_ context.Context, endpoint uint16, clusterID uint32) (uint32, bool) {
	ep := d.topology.FindByID(endpoint)
	if ep == nil {
		return 0, false
	}
	for _, srv := range ClusterServers(ep) {
		if srv.MatterClusterID() != clusterID {
			continue
		}
		// Bridged endpoints host the version on the Endpoint (their
		// server instances are rebuilt per dispatch) — see
		// clusterDataVersionFor.
		if !ep.IsRoot() && !ep.IsAggregator() {
			return ep.ClusterDataVersion(clusterID), true
		}
		dv, ok := srv.(interfaces.MatterClusterDataVersion)
		if !ok {
			return 0, false
		}
		v := dv.MatterDataVersion()
		if v == 0 {
			// Cluster implements the interface but version is 0 (initial /
			// untracked). Return (0, false) — callers treat 0 as "unconstrained".
			return 0, false
		}
		return v, true
	}
	return 0, false
}
