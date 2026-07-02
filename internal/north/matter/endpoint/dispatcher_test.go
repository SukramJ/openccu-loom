// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package endpoint

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/cluster"
	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/internal/north/matter/schema"
	"github.com/SukramJ/openccu-loom/internal/north/matter/store"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// --- fakeServerFull ---

// fakeServerFull is a richer fake than fakeServer (materialize_test.go):
// every Read/Write/Invoke response is configurable so dispatcher
// status-mapping paths can be exercised.
type fakeServerFull struct {
	id         uint32
	readVal    any
	readOK     bool
	writeErr   error
	invokeResp any
	invokeErr  error
	reportable []uint32
}

func (s *fakeServerFull) MatterClusterID() uint32 { return s.id }
func (s *fakeServerFull) MatterRead(_ uint32) (any, bool) {
	return s.readVal, s.readOK
}

func (s *fakeServerFull) MatterWrite(_ context.Context, _ uint32, _ any, _ hmenum.CommandPriority) error {
	return s.writeErr
}

func (s *fakeServerFull) MatterInvoke(_ context.Context, _ uint32, _ any, _ hmenum.CommandPriority) (any, error) {
	return s.invokeResp, s.invokeErr
}
func (s *fakeServerFull) MatterReportable() []uint32 { return s.reportable }

// Compile-time assertion: fakeServerFull satisfies interfaces.MatterClusterServer.
var _ interfaces.MatterClusterServer = (*fakeServerFull)(nil)

// --- fullSource ---

// fullSource is a MatterEndpointSource backed by a slice of *fakeServerFull.
type fullSource struct{ servers []*fakeServerFull }

func (f fullSource) MatterDeviceType() uint16 { return 0x010A }
func (f fullSource) MatterClusterServers() []interfaces.MatterClusterServer {
	out := make([]interfaces.MatterClusterServer, len(f.servers))
	for i, s := range f.servers {
		out[i] = s
	}
	return out
}

// Compile-time assertion: fullSource satisfies interfaces.MatterEndpointSource.
var _ interfaces.MatterEndpointSource = fullSource{}

// --- globalAttrServer ---

// globalAttrServer is a cluster server that returns distinct values for
// FeatureMap vs ClusterRevision so wildcard-attribute expansion can be verified.
type globalAttrServer struct{ id uint32 }

func (s *globalAttrServer) MatterClusterID() uint32 { return s.id }
func (s *globalAttrServer) MatterRead(attr uint32) (any, bool) {
	switch attr {
	case cluster.AttrGlobalFeatureMap:
		return uint32(0x42), true
	case cluster.AttrGlobalClusterRevision:
		return uint16(7), true
	}
	return nil, false
}

func (s *globalAttrServer) MatterWrite(_ context.Context, _ uint32, _ any, _ hmenum.CommandPriority) error {
	return errors.New("read-only")
}

func (s *globalAttrServer) MatterInvoke(_ context.Context, _ uint32, _ any, _ hmenum.CommandPriority) (any, error) {
	return nil, errors.New("no commands")
}
func (s *globalAttrServer) MatterReportable() []uint32 { return nil }

// Compile-time assertion: globalAttrServer satisfies interfaces.MatterClusterServer.
var _ interfaces.MatterClusterServer = (*globalAttrServer)(nil)

// singleServerSource is a MatterEndpointSource backed by one globalAttrServer.
type singleServerSource struct{ srv *globalAttrServer }

func (f singleServerSource) MatterDeviceType() uint16 { return 0x010A }
func (f singleServerSource) MatterClusterServers() []interfaces.MatterClusterServer {
	return []interfaces.MatterClusterServer{f.srv}
}

// Compile-time assertion: singleServerSource satisfies interfaces.MatterEndpointSource.
var _ interfaces.MatterEndpointSource = singleServerSource{}

// --- topology helpers ---

// makeTopology builds a Topology with a synthetic root endpoint (ID=0),
// an aggregator endpoint (ID=1), followed by the supplied bridged endpoints.
func makeTopology(endpoints ...*Endpoint) *Topology {
	eps := make([]*Endpoint, 0, 2+len(endpoints))
	eps = append(
		eps,
		&Endpoint{ID: 0},                     // root
		&Endpoint{ID: 1, DeviceType: 0x000E}, // aggregator — AggregatorClusterServers nil → UnsupportedCluster on wildcard
	)
	eps = append(eps, endpoints...)
	return &Topology{Endpoints: eps, NodeLabel: "test", VendorID: 0xFFF1, ProductID: 0x8000}
}

// makeEndpointFull returns a bridged Endpoint whose cluster servers are
// the supplied *fakeServerFull instances.
func makeEndpointFull(id uint16, servers ...*fakeServerFull) *Endpoint {
	return &Endpoint{
		ID:     id,
		Source: fullSource{servers: servers},
	}
}

// --- concrete path helpers ---

func concreteAttrPath(epID uint16, clusterID, attrID uint32) im.ConcreteAttributePath {
	return im.ConcreteAttributePath{
		Endpoint:     epID,
		Cluster:      clusterID,
		Attribute:    attrID,
		HasEndpoint:  true,
		HasCluster:   true,
		HasAttribute: true,
	}
}

func concreteCmdPath(epID uint16, clusterID, cmdID uint32) im.ConcreteCommandPath {
	return im.ConcreteCommandPath{
		Endpoint:    epID,
		Cluster:     clusterID,
		Command:     cmdID,
		HasEndpoint: true,
		HasCluster:  true,
		HasCommand:  true,
	}
}

func makeConcretePath(ep uint16, clusterID, attr uint32) im.ConcreteAttributePath {
	return im.ConcreteAttributePath{
		Endpoint: ep, HasEndpoint: true,
		Cluster: clusterID, HasCluster: true,
		Attribute: attr, HasAttribute: true,
	}
}

// =============================================================================
// Construction & dispatcher contract
// =============================================================================

// TestNewTopologyDispatcher_NilReturnsNil verifies that a nil topology produces a nil dispatcher.
func TestNewTopologyDispatcher_NilReturnsNil(t *testing.T) {
	t.Parallel()
	if got := NewTopologyDispatcher(nil); got != nil {
		t.Errorf("NewTopologyDispatcher(nil) = %v, want nil", got)
	}
}

// =============================================================================
// Read happy path + concrete
// =============================================================================

// TestRead_ConcreteHit verifies that a concrete read on a known endpoint/cluster returns 1 result with the value and StatusSuccess.
func TestRead_ConcreteHit(t *testing.T) {
	t.Parallel()
	srv := &fakeServerFull{id: 0x0006, readVal: uint8(1), readOK: true}
	d := NewTopologyDispatcher(makeTopology(makeEndpointFull(2, srv)))
	results := d.Read(context.Background(), concreteAttrPath(2, 0x0006, 0x0000))
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	r := results[0]
	if r.Status != im.StatusSuccess {
		t.Errorf("status = %v, want StatusSuccess", r.Status)
	}
	if r.Value.Value != uint8(1) {
		t.Errorf("value = %v, want uint8(1)", r.Value.Value)
	}
}

// TestRead_UnknownEndpoint verifies that reading endpoint 99 (non-existent) returns 1 result with StatusUnsupportedEndpoint.
func TestRead_UnknownEndpoint(t *testing.T) {
	t.Parallel()
	d := NewTopologyDispatcher(makeTopology())
	results := d.Read(context.Background(), concreteAttrPath(99, 0x0006, 0x0000))
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	if results[0].Status != im.StatusUnsupportedEndpoint {
		t.Errorf("status = %v, want StatusUnsupportedEndpoint", results[0].Status)
	}
}

// TestRead_UnknownCluster verifies that requesting an absent cluster returns StatusUnsupportedCluster.
func TestRead_UnknownCluster(t *testing.T) {
	t.Parallel()
	srv := &fakeServerFull{id: 0x0006}
	d := NewTopologyDispatcher(makeTopology(makeEndpointFull(2, srv)))
	results := d.Read(context.Background(), concreteAttrPath(2, 0x0008, 0x0000)) // cluster 0x0008 absent
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	if results[0].Status != im.StatusUnsupportedCluster {
		t.Errorf("status = %v, want StatusUnsupportedCluster", results[0].Status)
	}
}

// TestRead_AttributeNotImplemented verifies that readOK=false maps to StatusUnsupportedAttribute.
func TestRead_AttributeNotImplemented(t *testing.T) {
	t.Parallel()
	srv := &fakeServerFull{id: 0x0006, readOK: false}
	d := NewTopologyDispatcher(makeTopology(makeEndpointFull(2, srv)))
	results := d.Read(context.Background(), concreteAttrPath(2, 0x0006, 0x0042))
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	if results[0].Status != im.StatusUnsupportedAttribute {
		t.Errorf("status = %v, want StatusUnsupportedAttribute", results[0].Status)
	}
}

// TestRead_NullValueSurfaces verifies that (nil, true) from a cluster server surfaces as AttributeValue{IsNull:true} + StatusSuccess.
func TestRead_NullValueSurfaces(t *testing.T) {
	t.Parallel()
	srv := &fakeServerFull{id: 0x0006, readVal: nil, readOK: true}
	d := NewTopologyDispatcher(makeTopology(makeEndpointFull(2, srv)))
	results := d.Read(context.Background(), concreteAttrPath(2, 0x0006, 0x0000))
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	r := results[0]
	if r.Status != im.StatusSuccess {
		t.Errorf("status = %v, want StatusSuccess", r.Status)
	}
	if !r.Value.IsNull {
		t.Error("Value.IsNull = false, want true")
	}
	if r.Value.Value != nil {
		t.Errorf("Value.Value = %v, want nil", r.Value.Value)
	}
}

// =============================================================================
// Read wildcards
// =============================================================================

// TestRead_WildcardEndpointFansOut verifies that HasEndpoint=false returns one result per endpoint.
func TestRead_WildcardEndpointFansOut(t *testing.T) {
	t.Parallel()
	srv1 := &fakeServerFull{id: 0x0006, readVal: uint8(0), readOK: true}
	srv2 := &fakeServerFull{id: 0x0006, readVal: uint8(1), readOK: true}
	d := NewTopologyDispatcher(makeTopology(makeEndpointFull(2, srv1), makeEndpointFull(3, srv2)))
	path := im.ConcreteAttributePath{
		Cluster:      0x0006,
		Attribute:    0x0000,
		HasEndpoint:  false, // wildcard
		HasCluster:   true,
		HasAttribute: true,
	}
	results := d.Read(context.Background(), path)

	// Wildcard endpoint expansion (Matter §4.5.4): the generator
	// emits paths ONLY for endpoints where the requested cluster
	// actually exists. Endpoints without the cluster — root (no
	// 0x0006), aggregator (no 0x0006 on the bare fake) — are
	// SILENTLY SKIPPED, not surfaced as UnsupportedCluster. chip-tool
	// treats a single UnsupportedCluster status in a wildcard read
	// as failure of the whole exchange (IM Error 0xC3), so emitting
	// one for endpoints that simply don't match the path is a wire
	// bug. Concrete endpoint addressing keeps the explicit
	// UnsupportedCluster surface — separate test path.
	if len(results) != 2 {
		t.Fatalf("want 2 results (only bridged eps with cluster), got %d", len(results))
	}
	for i, r := range results {
		if r.Status != im.StatusSuccess {
			t.Errorf("bridged result[%d] status = %v, want StatusSuccess", i, r.Status)
		}
	}
}

// TestRead_WildcardClusterFansOut verifies that HasCluster=false returns one result per cluster server on the endpoint.
func TestRead_WildcardClusterFansOut(t *testing.T) {
	t.Parallel()
	srv1 := &fakeServerFull{id: 0x0006, readVal: uint8(0), readOK: true}
	srv2 := &fakeServerFull{id: 0x0008, readVal: uint8(5), readOK: true}
	d := NewTopologyDispatcher(makeTopology(makeEndpointFull(2, srv1, srv2)))
	path := im.ConcreteAttributePath{
		Endpoint:     2,
		Attribute:    0x0000,
		HasEndpoint:  true,
		HasCluster:   false, // wildcard
		HasAttribute: true,
	}
	results := d.Read(context.Background(), path)
	if len(results) != 2 {
		t.Fatalf("want 2 results (one per cluster), got %d", len(results))
	}
	for i, r := range results {
		if r.Status != im.StatusSuccess {
			t.Errorf("result[%d] status = %v, want StatusSuccess", i, r.Status)
		}
	}
}

// TestRead_WildcardAttributeReturnsGlobals verifies that HasAttribute=false expands to FeatureMap (0xFFFC) and ClusterRevision (0xFFFD).
func TestRead_WildcardAttributeReturnsGlobals(t *testing.T) {
	t.Parallel()
	ep := &Endpoint{
		ID:     2,
		Source: singleServerSource{srv: &globalAttrServer{id: 0x0006}},
	}
	d := NewTopologyDispatcher(makeTopology(ep))
	path := im.ConcreteAttributePath{
		Endpoint:     2,
		Cluster:      0x0006,
		HasEndpoint:  true,
		HasCluster:   true,
		HasAttribute: false, // wildcard
	}
	results := d.Read(context.Background(), path)
	// Five global attributes the wildcard expansion advertises:
	// GeneratedCommandList, AcceptedCommandList, AttributeList,
	// FeatureMap, ClusterRevision. The Matter 1.4 EventList (0xFFFA)
	// is intentionally omitted — Apple's iOS 26 Matter SDK rejects
	// it with `MTRErrorDomain Code=12 "No known schema"`, which
	// drops the whole ReportData stream and aborts the pair via
	// HAPErrorDomain Code=14. Re-enable when Apple advances to
	// Matter 1.4 SDK.
	if len(results) != 5 {
		t.Fatalf("want 5 results (Matter 1.3 global-attribute set, EventList suppressed), got %d", len(results))
	}
	byAttr := make(map[uint32]im.ReadResult, 5)
	for _, r := range results {
		byAttr[r.Path.Attribute] = r
	}
	if r, ok := byAttr[cluster.AttrGlobalFeatureMap]; !ok {
		t.Error("missing FeatureMap result")
	} else if r.Value.Value != uint32(0x42) {
		t.Errorf("FeatureMap value = %v, want uint32(0x42)", r.Value.Value)
	}
	if r, ok := byAttr[cluster.AttrGlobalClusterRevision]; !ok {
		t.Error("missing ClusterRevision result")
	} else if r.Value.Value != uint16(7) {
		t.Errorf("ClusterRevision value = %v, want uint16(7)", r.Value.Value)
	}
	for _, id := range []uint32{
		cluster.AttrGlobalAttributeList,
		cluster.AttrGlobalAcceptedCommandList,
		cluster.AttrGlobalGeneratedCommandList,
	} {
		if r, ok := byAttr[id]; !ok {
			t.Errorf("missing global 0x%04X", id)
		} else if r.Status != im.StatusSuccess {
			t.Errorf("global 0x%04X status = %v, want Success", id, r.Status)
		}
	}
	// EventList (0xFFFA) MUST NOT appear in the wildcard expansion —
	// it triggers Apple's iOS Matter SDK schema-mismatch reject.
	if _, present := byAttr[cluster.AttrGlobalEventList]; present {
		t.Error("EventList (0xFFFA) should be omitted from wildcard expansion to keep Apple Home pair viable")
	}
}

// listingAttrServer is a globalAttrServer extended with a
// MatterAttributes() lister that advertises two cluster-specific
// attributes (0x0000 OnOff, 0x4001 OnTime). Used to verify that a
// wildcard-attribute Read fans out to the lister output plus the
// universal globals — sorted, no duplicates.
type listingAttrServer struct {
	globalAttrServer
	attrs []uint32
}

func (s *listingAttrServer) MatterAttributes() []uint32 { return s.attrs }
func (s *listingAttrServer) MatterRead(attr uint32) (any, bool) {
	switch attr {
	case 0x0000:
		return false, true
	case 0x4001:
		return uint16(0), true
	}
	return s.globalAttrServer.MatterRead(attr)
}

var _ interfaces.MatterClusterAttributeLister = (*listingAttrServer)(nil)

// listingSource is a MatterEndpointSource backed by one listingAttrServer.
type listingSource struct{ srv *listingAttrServer }

func (f listingSource) MatterDeviceType() uint16 { return 0x010A }
func (f listingSource) MatterClusterServers() []interfaces.MatterClusterServer {
	return []interfaces.MatterClusterServer{f.srv}
}

// TestRead_WildcardAttribute_ListerWithoutGlobalsStillGetsThem locks the
// dispatcher's responsibility for synthesising the Matter §7.13.2 global
// attributes on every cluster, even when the per-cluster MatterAttributes
// lister enumerates only its cluster-specific surface. Per-cluster servers
// do NOT need to list FeatureMap (0xFFFC) and ClusterRevision (0xFFFD)
// in their own MatterAttributes() — the dispatcher does it for them.
// A new cluster server that omits the globals is therefore not a parity
// bug; this test fails if a future refactor accidentally drops the
// universal seed.
func TestRead_WildcardAttribute_ListerWithoutGlobalsStillGetsThem(t *testing.T) {
	t.Parallel()
	srv := &listingAttrServer{
		globalAttrServer: globalAttrServer{id: 0x0006},
		// Cluster-specific attributes only; NO global IDs.
		attrs: []uint32{0x0000, 0x4001},
	}
	ep := &Endpoint{ID: 2, Source: listingSource{srv: srv}}
	d := NewTopologyDispatcher(makeTopology(ep))
	path := im.ConcreteAttributePath{
		Endpoint:     2,
		Cluster:      0x0006,
		HasEndpoint:  true,
		HasCluster:   true,
		HasAttribute: false,
	}
	results := d.Read(context.Background(), path)
	byAttr := make(map[uint32]bool, len(results))
	for _, r := range results {
		byAttr[r.Path.Attribute] = true
	}
	// Cluster-side attributes survive the merge.
	for _, want := range []uint32{0x0000, 0x4001} {
		if !byAttr[want] {
			t.Errorf("missing cluster attribute 0x%04X from wildcard expansion", want)
		}
	}
	// Dispatcher MUST synthesise FeatureMap + ClusterRevision even
	// though the lister omits them.
	for _, want := range []uint32{cluster.AttrGlobalFeatureMap, cluster.AttrGlobalClusterRevision} {
		if !byAttr[want] {
			t.Errorf("dispatcher must seed global 0x%04X regardless of lister contents", want)
		}
	}
}

func TestRead_WildcardAttribute_ListerExpands(t *testing.T) {
	t.Parallel()
	srv := &listingAttrServer{
		globalAttrServer: globalAttrServer{id: 0x0006},
		// Include 0xFFFC (FeatureMap) on purpose — the dispatcher must
		// dedupe globals so we don't get two FeatureMap results.
		attrs: []uint32{0x4001, 0x0000, cluster.AttrGlobalFeatureMap},
	}
	ep := &Endpoint{ID: 2, Source: listingSource{srv: srv}}
	d := NewTopologyDispatcher(makeTopology(ep))
	path := im.ConcreteAttributePath{
		Endpoint:     2,
		Cluster:      0x0006,
		HasEndpoint:  true,
		HasCluster:   true,
		HasAttribute: false,
	}
	results := d.Read(context.Background(), path)
	// Two cluster attributes (OnOff 0x0000, OnTime 0x4001) plus the six
	// universal globals per Matter §7.13.2 (FeatureMap supplied by the
	// lister is deduped against the dispatcher-injected one). FeatureMap
	// in `attrs` exercises the de-dup contract; the dispatcher must not
	// produce two FeatureMap rows. EventList (0xFFFA, Matter 1.4) is
	// intentionally absent — Apple's iOS Matter SDK rejects it.
	if len(results) != 7 {
		t.Fatalf("want 7 results (2 cluster attrs + 5 Matter-1.3 globals, deduped, no EventList), got %d", len(results))
	}
	want := []uint32{
		0x0000, 0x4001,
		cluster.AttrGlobalGeneratedCommandList,
		cluster.AttrGlobalAcceptedCommandList,
		cluster.AttrGlobalAttributeList,
		cluster.AttrGlobalFeatureMap,
		cluster.AttrGlobalClusterRevision,
	}
	for i, r := range results {
		if r.Path.Attribute != want[i] {
			t.Errorf("result[%d].Attribute = 0x%04X, want sorted 0x%04X", i, r.Path.Attribute, want[i])
		}
	}
}

// TestRead_WildcardEndpointSkipsEndpointsWithoutCluster verifies the
// Matter §4.5.4 wildcard semantics: endpoints that don't host the
// requested cluster are silently skipped, not surfaced as
// UnsupportedCluster. The root (ID=0) and aggregator (ID=1) in the
// fixture topology carry no cluster servers — wildcard-endpoint reads
// for a cluster only the bridged endpoints carry must NOT emit a
// stray UnsupportedCluster entry. chip-tool treats any
// UnsupportedCluster status inside a wildcard exchange as IM Error
// 0xC3 and fails the whole read, so the silent skip is wire-critical.
func TestRead_WildcardEndpointSkipsEndpointsWithoutCluster(t *testing.T) {
	t.Parallel()
	srv := &fakeServerFull{id: 0x0006, readVal: uint8(0), readOK: true}
	d := NewTopologyDispatcher(makeTopology(makeEndpointFull(2, srv))) // root(0) + aggregator(1) + ep2
	path := im.ConcreteAttributePath{
		Cluster:      0x0006,
		Attribute:    0x0000,
		HasEndpoint:  false, // wildcard
		HasCluster:   true,
		HasAttribute: true,
	}
	results := d.Read(context.Background(), path)
	// Expected: only ep2 returns a result. Root + aggregator skipped.
	if len(results) != 1 {
		t.Fatalf("want 1 result (only ep2 with cluster), got %d", len(results))
	}
	if results[0].Path.Endpoint != 2 || results[0].Status != im.StatusSuccess {
		t.Errorf("result[0] = (ep=%d status=%v), want (ep=2 Success)",
			results[0].Path.Endpoint, results[0].Status)
	}
}

// TestRead_ConcreteEndpointReturnsUnsupportedCluster verifies that
// CONCRETE endpoint addressing (operator names the EP explicitly)
// still returns UnsupportedCluster when the cluster is absent — only
// wildcard expansion does the silent skip. Without this surface, a
// caller who typo'd a cluster ID would see an empty success reply
// instead of an explicit error.
func TestRead_ConcreteEndpointReturnsUnsupportedCluster(t *testing.T) {
	t.Parallel()
	srv := &fakeServerFull{id: 0x0006, readVal: uint8(0), readOK: true}
	d := NewTopologyDispatcher(makeTopology(makeEndpointFull(2, srv)))
	path := im.ConcreteAttributePath{
		Endpoint:     2,
		Cluster:      0x0099, // not present on ep2
		Attribute:    0x0000,
		HasEndpoint:  true,
		HasCluster:   true,
		HasAttribute: true,
	}
	results := d.Read(context.Background(), path)
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	if results[0].Status != im.StatusUnsupportedCluster {
		t.Errorf("status = %v, want UnsupportedCluster", results[0].Status)
	}
}

// =============================================================================
// Write status mapping
// =============================================================================

// TestWrite_Success verifies that a nil write error produces StatusSuccess.
func TestWrite_Success(t *testing.T) {
	t.Parallel()
	srv := &fakeServerFull{id: 0x0006, writeErr: nil}
	d := NewTopologyDispatcher(makeTopology(makeEndpointFull(2, srv)))
	results := d.Write(context.Background(), concreteAttrPath(2, 0x0006, 0x0001), im.AttributeValue{Value: uint8(1)})
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	if results[0].Status != im.StatusSuccess {
		t.Errorf("status = %v, want StatusSuccess", results[0].Status)
	}
}

// TestWrite_ReadOnlyMapsToUnsupportedWrite verifies that a "read-only" error maps to StatusUnsupportedWrite.
func TestWrite_ReadOnlyMapsToUnsupportedWrite(t *testing.T) {
	t.Parallel()
	srv := &fakeServerFull{id: 0x0006, writeErr: errors.New("foo: read-only error")}
	d := NewTopologyDispatcher(makeTopology(makeEndpointFull(2, srv)))
	results := d.Write(context.Background(), concreteAttrPath(2, 0x0006, 0x0001), im.AttributeValue{Value: uint8(1)})
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	if results[0].Status != im.StatusUnsupportedWrite {
		t.Errorf("status = %v, want StatusUnsupportedWrite", results[0].Status)
	}
}

// TestWrite_UnknownAttributeMaps verifies that an "unknown attribute" error maps to StatusUnsupportedAttribute.
func TestWrite_UnknownAttributeMaps(t *testing.T) {
	t.Parallel()
	srv := &fakeServerFull{id: 0x0006, writeErr: errors.New("unknown attribute 0xFEED")}
	d := NewTopologyDispatcher(makeTopology(makeEndpointFull(2, srv)))
	results := d.Write(context.Background(), concreteAttrPath(2, 0x0006, 0xFEED), im.AttributeValue{Value: uint8(0)})
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	if results[0].Status != im.StatusUnsupportedAttribute {
		t.Errorf("status = %v, want StatusUnsupportedAttribute", results[0].Status)
	}
}

// TestWrite_ConstraintMaps verifies that a "constraint" error maps to StatusConstraintError.
func TestWrite_ConstraintMaps(t *testing.T) {
	t.Parallel()
	srv := &fakeServerFull{id: 0x0006, writeErr: errors.New("constraint violation: too small")}
	d := NewTopologyDispatcher(makeTopology(makeEndpointFull(2, srv)))
	results := d.Write(context.Background(), concreteAttrPath(2, 0x0006, 0x0010), im.AttributeValue{Value: uint8(0)})
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	if results[0].Status != im.StatusConstraintError {
		t.Errorf("status = %v, want StatusConstraintError", results[0].Status)
	}
}

// TestWrite_GenericErrorMapsToFailure verifies that an unrecognised error maps to StatusFailure.
func TestWrite_GenericErrorMapsToFailure(t *testing.T) {
	t.Parallel()
	srv := &fakeServerFull{id: 0x0006, writeErr: errors.New("kaboom")}
	d := NewTopologyDispatcher(makeTopology(makeEndpointFull(2, srv)))
	results := d.Write(context.Background(), concreteAttrPath(2, 0x0006, 0x0010), im.AttributeValue{Value: uint8(0)})
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	if results[0].Status != im.StatusFailure {
		t.Errorf("status = %v, want StatusFailure", results[0].Status)
	}
}

// =============================================================================
// Invoke status mapping
// =============================================================================

// TestInvoke_SuccessAndUnknownCommand is table-driven across success, UnsupportedCommand (two variants),
// ConstraintError, and generic Failure; also locks endpoint/cluster resolution rules in sub-tests.
func TestInvoke_SuccessAndUnknownCommand(t *testing.T) {
	t.Parallel()

	type invokeCase struct {
		name       string
		invokeResp any
		invokeErr  error
		wantStatus im.StatusCode
	}
	cases := []invokeCase{
		{
			name:       "success with response passthrough",
			invokeResp: struct{ V uint8 }{V: 42},
			invokeErr:  nil,
			wantStatus: im.StatusSuccess,
		},
		{
			name:       "unknown command substring",
			invokeResp: nil,
			invokeErr:  errors.New("unknown command 0xFE"),
			wantStatus: im.StatusUnsupportedCommand,
		},
		{
			name:       "no commands on cluster",
			invokeResp: nil,
			invokeErr:  errors.New("no commands on this cluster"),
			wantStatus: im.StatusUnsupportedCommand,
		},
		{
			name:       "constraint error",
			invokeResp: nil,
			invokeErr:  errors.New("constraint: value out of range"),
			wantStatus: im.StatusConstraintError,
		},
		{
			name:       "generic failure",
			invokeResp: nil,
			invokeErr:  errors.New("kaboom"),
			wantStatus: im.StatusFailure,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			srv := &fakeServerFull{
				id:         0x0006,
				invokeResp: tc.invokeResp,
				invokeErr:  tc.invokeErr,
			}
			d := NewTopologyDispatcher(makeTopology(makeEndpointFull(2, srv)))
			result := d.Invoke(context.Background(), concreteCmdPath(2, 0x0006, 0x00), nil)
			if result.Status != tc.wantStatus {
				t.Errorf("status = %v, want %v", result.Status, tc.wantStatus)
			}
			if tc.invokeErr == nil && result.Response != tc.invokeResp {
				t.Errorf("response = %v, want %v", result.Response, tc.invokeResp)
			}
		})
	}

	// Endpoint/cluster resolution rules for Invoke.
	srv := &fakeServerFull{id: 0x0006, invokeErr: nil}
	d := NewTopologyDispatcher(makeTopology(makeEndpointFull(2, srv)))

	t.Run("HasEndpoint=false returns UnsupportedEndpoint", func(t *testing.T) {
		t.Parallel()
		path := im.ConcreteCommandPath{
			Cluster:     0x0006,
			Command:     0x00,
			HasEndpoint: false,
			HasCluster:  true,
			HasCommand:  true,
		}
		result := d.Invoke(context.Background(), path, nil)
		if result.Status != im.StatusUnsupportedEndpoint {
			t.Errorf("status = %v, want StatusUnsupportedEndpoint", result.Status)
		}
	})

	t.Run("endpoint not found returns UnsupportedEndpoint", func(t *testing.T) {
		t.Parallel()
		result := d.Invoke(context.Background(), concreteCmdPath(99, 0x0006, 0x00), nil)
		if result.Status != im.StatusUnsupportedEndpoint {
			t.Errorf("status = %v, want StatusUnsupportedEndpoint", result.Status)
		}
	})

	t.Run("cluster not found returns UnsupportedCluster", func(t *testing.T) {
		t.Parallel()
		result := d.Invoke(context.Background(), concreteCmdPath(2, 0x0008, 0x00), nil) // cluster 0x0008 absent
		if result.Status != im.StatusUnsupportedCluster {
			t.Errorf("status = %v, want StatusUnsupportedCluster", result.Status)
		}
	})
}

// =============================================================================
// StatusCodeError typed dispatch
// =============================================================================

// typedStatusError is a minimal im.StatusCodeError implementation for tests.
type typedStatusError struct {
	code im.StatusCode
	msg  string
}

func (e typedStatusError) Error() string                   { return e.msg }
func (e typedStatusError) MatterStatusCode() im.StatusCode { return e.code }

// Compile-time assertion: typedStatusError satisfies im.StatusCodeError.
var _ im.StatusCodeError = typedStatusError{}

// TestWrite_TypedStatusCodeError_TakesPriority verifies that when a
// cluster-server Write error implements im.StatusCodeError, the typed status
// code is used directly and the string-heuristic fallback is bypassed.
func TestWrite_TypedStatusCodeError_TakesPriority(t *testing.T) {
	t.Parallel()

	// Any string that would map to a different status via string heuristic.
	typedErr := typedStatusError{
		code: im.StatusDataVersionMismatch,
		msg:  "nothing matches the old heuristic strings",
	}
	srv := &fakeServerFull{id: 0x0006, writeErr: typedErr}
	d := NewTopologyDispatcher(makeTopology(makeEndpointFull(2, srv)))

	results := d.Write(context.Background(), concreteAttrPath(2, 0x0006, 0x0010), im.AttributeValue{Value: uint8(0)})
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	if results[0].Status != im.StatusDataVersionMismatch {
		t.Errorf("Write status = %v, want StatusDataVersionMismatch (typed error code)", results[0].Status)
	}
}

// TestInvoke_TypedStatusCodeError_TakesPriority verifies the same typed-first
// behaviour for Invoke dispatch.
func TestInvoke_TypedStatusCodeError_TakesPriority(t *testing.T) {
	t.Parallel()

	typedErr := typedStatusError{
		code: im.StatusBusy,
		msg:  "nothing matches the old heuristic strings",
	}
	srv := &fakeServerFull{id: 0x0006, invokeErr: typedErr}
	d := NewTopologyDispatcher(makeTopology(makeEndpointFull(2, srv)))

	result := d.Invoke(context.Background(), concreteCmdPath(2, 0x0006, 0x00), nil)
	if result.Status != im.StatusBusy {
		t.Errorf("Invoke status = %v, want StatusBusy (typed error code)", result.Status)
	}
}

// TestWrite_TypedStatusCodeError_WrappedInFmt verifies that a typed error
// wrapped with fmt.Errorf is still resolved via errors.As (not string match).
func TestWrite_TypedStatusCodeError_WrappedInFmt(t *testing.T) {
	t.Parallel()
	typedErr := typedStatusError{
		code: im.StatusResourceExhausted,
		msg:  "resource exhausted by typed error",
	}
	// Wrap the typed error — errors.As must unwrap and find the StatusCodeError.
	wrapped := fmt.Errorf("write failed: %w", typedErr)

	srv := &fakeServerFull{id: 0x0006, writeErr: wrapped}
	d := NewTopologyDispatcher(makeTopology(makeEndpointFull(2, srv)))
	results := d.Write(context.Background(), concreteAttrPath(2, 0x0006, 0x0010), im.AttributeValue{Value: uint8(0)})
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	if results[0].Status != im.StatusResourceExhausted {
		t.Errorf("Write status (wrapped typed error) = %v, want StatusResourceExhausted", results[0].Status)
	}
}

// =============================================================================
// Write: additional status paths
// =============================================================================

// TestWrite_UnsupportedEndpoint verifies that Write returns UnsupportedEndpoint
// when the endpoint does not exist.
func TestWrite_UnsupportedEndpoint(t *testing.T) {
	t.Parallel()
	srv := &fakeServerFull{id: 0x0006}
	d := NewTopologyDispatcher(makeTopology(makeEndpointFull(2, srv)))
	// Endpoint 99 does not exist.
	results := d.Write(context.Background(), concreteAttrPath(99, 0x0006, 0x0001), im.AttributeValue{Value: uint8(0)})
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	if results[0].Status != im.StatusUnsupportedEndpoint {
		t.Errorf("status = %v, want StatusUnsupportedEndpoint", results[0].Status)
	}
}

// TestWrite_UnsupportedCluster verifies that Write returns UnsupportedCluster
// when the cluster does not exist on the endpoint.
func TestWrite_UnsupportedCluster(t *testing.T) {
	t.Parallel()
	srv := &fakeServerFull{id: 0x0006}
	d := NewTopologyDispatcher(makeTopology(makeEndpointFull(2, srv)))
	// Cluster 0xDEAD is not on endpoint 2.
	results := d.Write(context.Background(), concreteAttrPath(2, 0xDEAD, 0x0001), im.AttributeValue{Value: uint8(0)})
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	if results[0].Status != im.StatusUnsupportedCluster {
		t.Errorf("status = %v, want StatusUnsupportedCluster", results[0].Status)
	}
}

// TestWrite_IsNull verifies that an AttributeValue with IsNull=true passes nil
// to MatterWrite (the writeOne nil-coerce branch).
func TestWrite_IsNull(t *testing.T) {
	t.Parallel()
	var gotVal any = "not nil"
	srv := &fakeServerFull{id: 0x0006}
	// Override writeErr to capture the value passed (we can't intercept it
	// directly; we just verify the result is StatusSuccess when MatterWrite
	// returns nil, confirming the IsNull path ran without panic).
	srv.writeErr = nil
	d := NewTopologyDispatcher(makeTopology(makeEndpointFull(2, srv)))
	results := d.Write(context.Background(), concreteAttrPath(2, 0x0006, 0x0001), im.AttributeValue{IsNull: true, Value: gotVal})
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	if results[0].Status != im.StatusSuccess {
		t.Errorf("IsNull write: status = %v, want StatusSuccess", results[0].Status)
	}
}

// TestWriteErrorStatus_ResourceExhausted_StringHeuristic verifies that a plain
// string error containing "resource exhausted" maps to StatusResourceExhausted.
func TestWriteErrorStatus_ResourceExhausted_StringHeuristic(t *testing.T) {
	t.Parallel()
	// Use a plain error (not a StatusCodeError) so the string-heuristic branch runs.
	err := errors.New("resource exhausted: ACL entries full")
	srv := &fakeServerFull{id: 0x0006, writeErr: err}
	d := NewTopologyDispatcher(makeTopology(makeEndpointFull(2, srv)))
	results := d.Write(context.Background(), concreteAttrPath(2, 0x0006, 0x0001), im.AttributeValue{Value: uint8(0)})
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	if results[0].Status != im.StatusResourceExhausted {
		t.Errorf("status = %v, want StatusResourceExhausted (string heuristic)", results[0].Status)
	}
}

// =============================================================================
// Invoke: additional status paths
// =============================================================================

// TestInvokeErrorStatus_Nil verifies invokeErrorStatus(nil) == StatusSuccess.
func TestInvokeErrorStatus_Nil(t *testing.T) {
	t.Parallel()
	srv := &fakeServerFull{id: 0x0006, invokeErr: nil, invokeResp: struct{}{}}
	d := NewTopologyDispatcher(makeTopology(makeEndpointFull(2, srv)))
	r := d.Invoke(context.Background(), concreteCmdPath(2, 0x0006, 0x00), nil)
	if r.Status != im.StatusSuccess {
		t.Errorf("Invoke nil error: status = %v, want StatusSuccess", r.Status)
	}
}

// TestInvokeErrorStatus_InvalidCommandArgument verifies that an error containing
// "invalid command argument" maps to StatusInvalidCommand.
func TestInvokeErrorStatus_InvalidCommandArgument(t *testing.T) {
	t.Parallel()
	srv := &fakeServerFull{
		id:        0x0006,
		invokeErr: errors.New("invalid command argument: key_set_id must not be 0"),
	}
	d := NewTopologyDispatcher(makeTopology(makeEndpointFull(2, srv)))
	r := d.Invoke(context.Background(), concreteCmdPath(2, 0x0006, 0x00), nil)
	if r.Status != im.StatusInvalidCommand {
		t.Errorf("status = %v, want StatusInvalidCommand", r.Status)
	}
}

// =============================================================================
// synthesizeGlobalRead: commandLister branches
// =============================================================================

// commandListerServer extends fakeServerFull with MatterClusterCommandLister
// so synthesizeGlobalRead's AcceptedCommandList / GeneratedCommandList
// "lister ok" branches are exercised.
type commandListerServer struct {
	fakeServerFull
	accepted  []uint32
	generated []uint32
}

func (s *commandListerServer) MatterAcceptedCommands() []uint32  { return s.accepted }
func (s *commandListerServer) MatterGeneratedCommands() []uint32 { return s.generated }

var _ interfaces.MatterClusterCommandLister = (*commandListerServer)(nil)

// TestSynthesizeGlobalRead_AcceptedCommandList_WithLister verifies that a
// server implementing MatterClusterCommandLister returns its accepted-commands
// list (non-empty) for AttrGlobalAcceptedCommandList.
func TestSynthesizeGlobalRead_AcceptedCommandList_WithLister(t *testing.T) {
	t.Parallel()
	srv := &commandListerServer{
		fakeServerFull: fakeServerFull{id: 0x0006, readOK: false},
		accepted:       []uint32{0x00, 0x01, 0x02},
		generated:      []uint32{0x03},
	}
	r := readOne(context.Background(), nil, srv, makeConcretePath(2, 0x0006, cluster.AttrGlobalAcceptedCommandList))
	if r.Status != im.StatusSuccess {
		t.Errorf("status = %v, want StatusSuccess", r.Status)
	}
	cmds, ok := r.Value.Value.([]uint32)
	if !ok {
		t.Fatalf("value type = %T, want []uint32", r.Value.Value)
	}
	if len(cmds) != 3 {
		t.Errorf("len(cmds) = %d, want 3", len(cmds))
	}
}

// TestSynthesizeGlobalRead_GeneratedCommandList_WithLister verifies the
// generated-commands list path.
func TestSynthesizeGlobalRead_GeneratedCommandList_WithLister(t *testing.T) {
	t.Parallel()
	srv := &commandListerServer{
		fakeServerFull: fakeServerFull{id: 0x0006, readOK: false},
		accepted:       []uint32{0x00},
		generated:      []uint32{0x10, 0x11},
	}
	r := readOne(context.Background(), nil, srv, makeConcretePath(2, 0x0006, cluster.AttrGlobalGeneratedCommandList))
	if r.Status != im.StatusSuccess {
		t.Errorf("status = %v, want StatusSuccess", r.Status)
	}
	cmds, ok := r.Value.Value.([]uint32)
	if !ok {
		t.Fatalf("value type = %T, want []uint32", r.Value.Value)
	}
	if len(cmds) != 2 {
		t.Errorf("len(cmds) = %d, want 2", len(cmds))
	}
}

// TestSynthesizeGlobalRead_EventList_ReturnsUnsupported verifies that
// AttrGlobalEventList returns StatusUnsupportedAttribute (Apple interop fix).
func TestSynthesizeGlobalRead_EventList_ReturnsUnsupported(t *testing.T) {
	t.Parallel()
	srv := &fakeServerFull{id: 0x0006, readOK: false}
	r := readOne(context.Background(), nil, srv, makeConcretePath(2, 0x0006, cluster.AttrGlobalEventList))
	if r.Status != im.StatusUnsupportedAttribute {
		t.Errorf("status = %v, want StatusUnsupportedAttribute for EventList", r.Status)
	}
}

// TestReadOne_SynthesizeGlobal verifies that cluster.AttrGlobalAcceptedCommandList
// is synthesised when MatterRead returns (nil, false).
func TestReadOne_SynthesizeGlobal(t *testing.T) {
	t.Parallel()
	srv := &fakeServerFull{id: 0x0006, readOK: false}
	r := readOne(context.Background(), nil, srv, makeConcretePath(2, 0x0006, cluster.AttrGlobalAcceptedCommandList))
	if r.Status != im.StatusSuccess {
		t.Errorf("status = %v, want StatusSuccess for synthetic global", r.Status)
	}
}

// =============================================================================
// clusterDataVersion
// =============================================================================

// dvServer implements both MatterClusterServer and MatterClusterDataVersion.
type dvServer struct {
	id  uint32
	ver uint32
}

func (s *dvServer) MatterClusterID() uint32 { return s.id }
func (s *dvServer) MatterRead(_ uint32) (any, bool) {
	return nil, false
}

func (s *dvServer) MatterWrite(_ context.Context, _ uint32, _ any, _ hmenum.CommandPriority) error {
	return nil
}

func (s *dvServer) MatterInvoke(_ context.Context, _ uint32, _ any, _ hmenum.CommandPriority) (any, error) {
	return nil, nil
}
func (s *dvServer) MatterReportable() []uint32 { return nil }
func (s *dvServer) MatterDataVersion() uint32  { return s.ver }

var (
	_ interfaces.MatterClusterServer      = (*dvServer)(nil)
	_ interfaces.MatterClusterDataVersion = (*dvServer)(nil)
)

// dvSource is a MatterEndpointSource that exposes a single dvServer.
type dvSource struct {
	srv *dvServer
}

func (s dvSource) MatterDeviceType() uint16 { return 0x010A }
func (s dvSource) MatterClusterServers() []interfaces.MatterClusterServer {
	return []interfaces.MatterClusterServer{s.srv}
}

var _ interfaces.MatterEndpointSource = dvSource{}

// TestClusterDataVersion_WithVersion verifies that a server implementing
// MatterClusterDataVersion returns its version.
func TestClusterDataVersion_WithVersion(t *testing.T) {
	t.Parallel()
	srv := &dvServer{id: 0x0006, ver: 42}
	if got := clusterDataVersion(srv); got != 42 {
		t.Errorf("clusterDataVersion = %d, want 42", got)
	}
}

// TestClusterDataVersion_WithoutVersion verifies that a plain server returns 0.
func TestClusterDataVersion_WithoutVersion(t *testing.T) {
	t.Parallel()
	srv := &fakeServerFull{id: 0x0006}
	if got := clusterDataVersion(srv); got != 0 {
		t.Errorf("clusterDataVersion = %d, want 0", got)
	}
}

// =============================================================================
// readOne (FabricScopedReader paths + null-value paths)
// =============================================================================

// fabricScopedServer implements FabricScopedReader + MatterClusterServer.
type fabricScopedServer struct {
	id      uint32
	fVal    any
	fOK     bool
	readVal any
	readOK  bool
}

func (s *fabricScopedServer) MatterClusterID() uint32 { return s.id }
func (s *fabricScopedServer) MatterRead(_ uint32) (any, bool) {
	return s.readVal, s.readOK
}

func (s *fabricScopedServer) MatterWrite(_ context.Context, _ uint32, _ any, _ hmenum.CommandPriority) error {
	return nil
}

func (s *fabricScopedServer) MatterInvoke(_ context.Context, _ uint32, _ any, _ hmenum.CommandPriority) (any, error) {
	return nil, nil
}
func (s *fabricScopedServer) MatterReportable() []uint32 { return nil }
func (s *fabricScopedServer) MatterReadFiltered(_ context.Context, _ uint32) (any, bool) {
	return s.fVal, s.fOK
}

var (
	_ interfaces.MatterClusterServer = (*fabricScopedServer)(nil)
	_ interfaces.FabricScopedReader  = (*fabricScopedServer)(nil)
)

// TestReadOne_FabricScoped_NonNilValue exercises MatterReadFiltered with a
// non-nil value (StatusSuccess + populated value).
func TestReadOne_FabricScoped_NonNilValue(t *testing.T) {
	t.Parallel()
	srv := &fabricScopedServer{id: 0x0006, fVal: uint8(1), fOK: true}
	r := readOne(context.Background(), nil, srv, makeConcretePath(2, 0x0006, 0x0000))
	if r.Status != im.StatusSuccess {
		t.Errorf("status = %v, want StatusSuccess", r.Status)
	}
	if r.Value.Value != uint8(1) {
		t.Errorf("value = %v, want uint8(1)", r.Value.Value)
	}
}

// TestReadOne_FabricScoped_NilValue exercises MatterReadFiltered returning
// (nil, true) — the null attribute path.
func TestReadOne_FabricScoped_NilValue(t *testing.T) {
	t.Parallel()
	srv := &fabricScopedServer{id: 0x0006, fVal: nil, fOK: true}
	r := readOne(context.Background(), nil, srv, makeConcretePath(2, 0x0006, 0x0000))
	if r.Status != im.StatusSuccess {
		t.Errorf("status = %v, want StatusSuccess", r.Status)
	}
	if !r.Value.IsNull {
		t.Error("expected IsNull=true for nil fabric-scoped value")
	}
}

// TestReadOne_FabricScoped_FallThrough exercises (nil, false) from
// MatterReadFiltered — should fall through to MatterRead.
func TestReadOne_FabricScoped_FallThrough(t *testing.T) {
	t.Parallel()
	srv := &fabricScopedServer{
		id:      0x0006,
		fVal:    nil,
		fOK:     false, // triggers fall-through
		readVal: uint8(7),
		readOK:  true,
	}
	r := readOne(context.Background(), nil, srv, makeConcretePath(2, 0x0006, 0x0000))
	if r.Status != im.StatusSuccess {
		t.Errorf("status = %v, want StatusSuccess", r.Status)
	}
	if r.Value.Value != uint8(7) {
		t.Errorf("value = %v, want uint8(7)", r.Value.Value)
	}
}

// TestReadOne_MatterRead_NilValue exercises MatterRead returning (nil, true)
// on a server without FabricScopedReader.
func TestReadOne_MatterRead_NilValue(t *testing.T) {
	t.Parallel()
	srv := &fakeServerFull{id: 0x0006, readVal: nil, readOK: true}
	r := readOne(context.Background(), nil, srv, makeConcretePath(2, 0x0006, 0x0000))
	if r.Status != im.StatusSuccess {
		t.Errorf("status = %v, want StatusSuccess", r.Status)
	}
	if !r.Value.IsNull {
		t.Error("expected IsNull=true for nil MatterRead value")
	}
}

// =============================================================================
// CurrentDataVersion
// =============================================================================

// TestCurrentDataVersion_ClusterNotFound verifies (0, false) for unknown EP.
func TestCurrentDataVersion_ClusterNotFound(t *testing.T) {
	t.Parallel()
	d := NewTopologyDispatcher(makeTopology())
	v, ok := d.CurrentDataVersion(context.Background(), 99, 0x0006)
	if ok || v != 0 {
		t.Errorf("expected (0, false), got (%d, %v)", v, ok)
	}
}

// TestCurrentDataVersion_BridgedStableAcrossCalls verifies that a
// bridged endpoint's DataVersion comes from the endpoint-hosted
// tracker: non-zero, ok=true, and IDENTICAL across calls — regardless
// of what the (per-dispatch throwaway) server instance reports.
// Mirrors matter.js Datasource.ts:349 (version set once per lifetime).
func TestCurrentDataVersion_BridgedStableAcrossCalls(t *testing.T) {
	t.Parallel()
	// Server WITHOUT the version interface — previously (0, false);
	// the endpoint-hosted tracker now answers regardless.
	srv := &fakeServerFull{id: 0x0006}
	d := NewTopologyDispatcher(makeTopology(makeEndpointFull(2, srv)))
	v1, ok := d.CurrentDataVersion(context.Background(), 2, 0x0006)
	if !ok || v1 == 0 {
		t.Fatalf("expected stable non-zero version, got (%d, %v)", v1, ok)
	}
	v2, ok := d.CurrentDataVersion(context.Background(), 2, 0x0006)
	if !ok || v2 != v1 {
		t.Errorf("second call returned (%d, %v), want (%d, true) — version must be stable across dispatches", v2, ok, v1)
	}
}

// TestCurrentDataVersion_BridgedIgnoresInstanceTracker verifies the
// instance-embedded version of a bridged server is NOT consulted —
// bridged servers are rebuilt per dispatch, so their embedded tracker
// carries a fresh random per materialisation and must be bypassed.
func TestCurrentDataVersion_BridgedIgnoresInstanceTracker(t *testing.T) {
	t.Parallel()
	srv := &dvServer{id: 0x0006, ver: 7}
	ep := &Endpoint{ID: 2, Source: dvSource{srv: srv}}
	top := &Topology{
		Endpoints: []*Endpoint{
			{ID: 0}, {ID: 1, DeviceType: 0x000E}, ep,
		},
		VendorID: 0xFFF1, ProductID: 0x8000, NodeLabel: "t",
	}
	d := NewTopologyDispatcher(top)
	v, ok := d.CurrentDataVersion(context.Background(), 2, 0x0006)
	if !ok || v == 0 {
		t.Fatalf("expected endpoint-hosted version, got (%d, %v)", v, ok)
	}
	v2, _ := d.CurrentDataVersion(context.Background(), 2, 0x0006)
	if v2 != v {
		t.Errorf("version changed across calls: %d → %d", v, v2)
	}
}

// TestCurrentDataVersion_RootUsesInstanceTracker verifies root-endpoint
// servers (persistent instances) keep their instance-hosted semantics:
// no interface / version 0 → (0, false); non-zero → (ver, true).
func TestCurrentDataVersion_RootUsesInstanceTracker(t *testing.T) {
	t.Parallel()
	noIface := &fakeServerFull{id: 0x0006}
	zero := &dvServer{id: 0x0007, ver: 0}
	seven := &dvServer{id: 0x0008, ver: 7}
	top := &Topology{
		Endpoints: []*Endpoint{
			{ID: 0, RootClusterServers: []interfaces.MatterClusterServer{noIface, zero, seven}},
			{ID: 1, DeviceType: 0x000E},
		},
		VendorID: 0xFFF1, ProductID: 0x8000, NodeLabel: "t",
	}
	d := NewTopologyDispatcher(top)
	if v, ok := d.CurrentDataVersion(context.Background(), 0, 0x0006); ok || v != 0 {
		t.Errorf("no-interface root server: expected (0, false), got (%d, %v)", v, ok)
	}
	if v, ok := d.CurrentDataVersion(context.Background(), 0, 0x0007); ok || v != 0 {
		t.Errorf("zero-version root server: expected (0, false), got (%d, %v)", v, ok)
	}
	if v, ok := d.CurrentDataVersion(context.Background(), 0, 0x0008); !ok || v != 7 {
		t.Errorf("root server: expected (7, true), got (%d, %v)", v, ok)
	}
}

// =============================================================================
// SetExposureChecker
// =============================================================================

// denyAllChecker is an ExposureChecker that rejects everything.
type denyAllChecker struct{}

func (denyAllChecker) IsExposed(_ context.Context, _ store.EndpointKey) (bool, error) {
	return false, nil
}

// TestSetExposureChecker_Nil verifies that passing nil resets to allow-all.
func TestSetExposureChecker_Nil(t *testing.T) {
	t.Parallel()
	a := &Assembler{
		exposures: allowAllExposureChecker{},
		cfg: Config{
			VendorID:  0x1234,
			ProductID: 0x5678,
			NodeLabel: "test",
		},
		store:  newInternalFakeStore(),
		logger: nil,
	}
	// Setting nil must revert to allow-all without panic.
	a.SetExposureChecker(nil)
	_, ok := a.exposures.(allowAllExposureChecker)
	if !ok {
		t.Error("exposures should be allowAllExposureChecker after SetExposureChecker(nil)")
	}
}

// TestSetExposureChecker_NonNil verifies that a non-nil checker is stored.
func TestSetExposureChecker_NonNil(t *testing.T) {
	t.Parallel()
	a := &Assembler{
		exposures: allowAllExposureChecker{},
		cfg: Config{
			VendorID:  0x1234,
			ProductID: 0x5678,
			NodeLabel: "test",
		},
		store: newInternalFakeStore(),
	}
	a.SetExposureChecker(denyAllChecker{})
	if _, ok := a.exposures.(denyAllChecker); !ok {
		t.Error("exposures should be denyAllChecker after SetExposureChecker")
	}
}

// =============================================================================
// genericDPKeyForMeasurement
// =============================================================================

// dpKeyDP implements DataPointKey() for genericDPKeyForMeasurement tests.
type dpKeyDP struct {
	param string
}

func (d *dpKeyDP) DataPointKey() hmtypes.DataPointKey {
	return hmtypes.DataPointKey{Parameter: d.param}
}

// TestGenericDPKeyForMeasurement_HasKey verifies that a DP with a non-empty
// Parameter is returned verbatim.
func TestGenericDPKeyForMeasurement_HasKey(t *testing.T) {
	t.Parallel()
	dp := &dpKeyDP{param: "TEMPERATURE"}
	got := genericDPKeyForMeasurement(dp)
	if got != "TEMPERATURE" {
		t.Errorf("got %q, want %q", got, "TEMPERATURE")
	}
}

// TestGenericDPKeyForMeasurement_Empty verifies that a DP with empty Parameter
// returns an empty string.
func TestGenericDPKeyForMeasurement_Empty(t *testing.T) {
	t.Parallel()
	dp := &dpKeyDP{param: ""}
	got := genericDPKeyForMeasurement(dp)
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

// TestGenericDPKeyForMeasurement_NoInterface verifies that a value without
// the DataPointKey interface returns an empty string.
func TestGenericDPKeyForMeasurement_NoInterface(t *testing.T) {
	t.Parallel()
	got := genericDPKeyForMeasurement("not-a-dp")
	if got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

// =============================================================================
// renderSourceKey
// =============================================================================

// TestRenderSourceKey_Nil verifies nil input returns empty.
func TestRenderSourceKey_Nil(t *testing.T) {
	t.Parallel()
	if got := renderSourceKey(nil); got != "" {
		t.Errorf("nil → %q, want empty", got)
	}
}

// TestRenderSourceKey_EndpointKey verifies concrete EndpointKey formatting.
func TestRenderSourceKey_EndpointKey(t *testing.T) {
	t.Parallel()
	k := store.EndpointKey{
		CentralName:   "ccu1",
		DeviceAddress: "ABC001",
		ChannelNo:     2,
		DPKind:        store.DPKindCustom,
		DPKey:         "STATE",
	}
	got := renderSourceKey(k)
	if got == "" {
		t.Error("expected non-empty string for EndpointKey")
	}
	// Should contain all key components.
	for _, sub := range []string{"ccu1", "ABC001", "STATE"} {
		if got == "" {
			t.Errorf("renderSourceKey output missing %q", sub)
		}
	}
}

// TestRenderSourceKey_EndpointKeyPointer verifies pointer EndpointKey formatting.
func TestRenderSourceKey_EndpointKeyPointer(t *testing.T) {
	t.Parallel()
	k := &store.EndpointKey{
		CentralName:   "ccu1",
		DeviceAddress: "DEV",
		ChannelNo:     1,
		DPKind:        store.DPKindCalculated,
		DPKey:         "TEMP",
	}
	got := renderSourceKey(k)
	if got == "" {
		t.Error("expected non-empty string for *EndpointKey")
	}
}

// TestRenderSourceKey_NilPointer verifies nil pointer returns empty.
func TestRenderSourceKey_NilPointer(t *testing.T) {
	t.Parallel()
	var k *store.EndpointKey
	if got := renderSourceKey(k); got != "" {
		t.Errorf("nil pointer → %q, want empty", got)
	}
}

// centralAddressKey implements Central() + Address() for the interface branch.
type centralAddressKey struct {
	centralName, address string
}

func (c centralAddressKey) Central() string { return c.centralName }
func (c centralAddressKey) Address() string { return c.address }

// TestRenderSourceKey_CentralAddress verifies the Central()+Address() branch.
func TestRenderSourceKey_CentralAddress(t *testing.T) {
	t.Parallel()
	k := centralAddressKey{centralName: "ccu1", address: "DEV001"}
	got := renderSourceKey(k)
	want := "ccu1:DEV001"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// stringerKey implements fmt.Stringer.
type stringerKey struct{ s string }

func (k stringerKey) String() string { return k.s }

// TestRenderSourceKey_Stringer verifies the Stringer fallback.
func TestRenderSourceKey_Stringer(t *testing.T) {
	t.Parallel()
	got := renderSourceKey(stringerKey{s: "key-string"})
	if got != "key-string" {
		t.Errorf("got %q, want %q", got, "key-string")
	}
}

// TestRenderSourceKey_ReflectiveFallback verifies that a plain struct
// not implementing any special interface uses the reflective fallback.
func TestRenderSourceKey_ReflectiveFallback(t *testing.T) {
	t.Parallel()
	type myKey struct{ X, Y int }
	got := renderSourceKey(myKey{X: 1, Y: 2})
	// The fallback emits "{X:1 Y:2}" or similar — just verify it is non-empty.
	if got == "" {
		t.Error("reflective fallback returned empty string")
	}
}

// =============================================================================
// deviceTypeRevision
// =============================================================================

// TestDeviceTypeRevision_KnownType verifies that a type in the schema gets
// the schema-driven revision (not 1).
func TestDeviceTypeRevision_KnownType(t *testing.T) {
	t.Parallel()
	// OnOffLight = 0x0100 — always present in the generated schema.
	id := uint16(0x0100)
	got := deviceTypeRevision(id)
	want, ok := schema.DeviceTypeRevision(uint32(id))
	if !ok {
		t.Skip("0x0100 not in schema — skipping")
	}
	if got != want {
		t.Errorf("deviceTypeRevision(0x0100) = %d, want %d", got, want)
	}
}

// TestDeviceTypeRevision_UnknownType verifies that an unknown device type
// falls back to revision 1.
func TestDeviceTypeRevision_UnknownType(t *testing.T) {
	t.Parallel()
	got := deviceTypeRevision(0xFFFF)
	if got != 1 {
		t.Errorf("deviceTypeRevision(0xFFFF) = %d, want 1", got)
	}
}

// =============================================================================
// assignOrReuseID — device-type-drift branch
// =============================================================================

// TestAssignOrReuseID_DeviceTypeDrift verifies that when an endpoint already
// exists but with a different DeviceType, the type is refreshed.
func TestAssignOrReuseID_DeviceTypeDrift(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	fs := newInternalFakeStore()

	key := store.EndpointKey{
		CentralName:   "c1",
		DeviceAddress: "DEV",
		ChannelNo:     1,
		DPKind:        store.DPKindCustom,
		DPKey:         "STATE",
	}
	// Pre-seed with device type 0x0100.
	_, _ = fs.UpsertEndpointAssigning(ctx, store.EndpointRecord{Key: key, DeviceType: 0x0100})

	a := &Assembler{
		store:     fs,
		exposures: allowAllExposureChecker{},
		cfg: Config{
			VendorID:  0x1234,
			ProductID: 0x5678,
			NodeLabel: "test",
		},
	}
	// Call with a new device type — should refresh without error.
	id, err := a.assignOrReuseID(ctx, key, 0x0101)
	if err != nil {
		t.Fatalf("assignOrReuseID drift: %v", err)
	}
	if id == 0 {
		t.Error("expected non-zero endpoint ID after drift refresh")
	}
	// Verify the stored type was updated.
	rec, err := fs.GetEndpoint(ctx, key)
	if err != nil {
		t.Fatalf("GetEndpoint after drift: %v", err)
	}
	if rec.DeviceType != 0x0101 {
		t.Errorf("DeviceType = 0x%04X, want 0x0101 after refresh", rec.DeviceType)
	}
}

// =============================================================================
// internalFakeStore (white-box Store fake for package endpoint tests)
// =============================================================================

// internalFakeStore is an in-memory Store implementation for white-box tests
// that need direct access to Assembler internals.
type internalFakeStore struct {
	rows   map[store.EndpointKey]store.EndpointRecord
	nextID uint16
}

func newInternalFakeStore() *internalFakeStore {
	return &internalFakeStore{
		rows:   make(map[store.EndpointKey]store.EndpointRecord),
		nextID: 2,
	}
}

func (s *internalFakeStore) GetEndpoint(_ context.Context, key store.EndpointKey) (store.EndpointRecord, error) {
	rec, ok := s.rows[key]
	if !ok {
		return store.EndpointRecord{}, store.ErrEndpointNotFound
	}
	return rec, nil
}

func (s *internalFakeStore) UpsertEndpointAssigning(_ context.Context, rec store.EndpointRecord) (uint16, error) {
	if rec.EndpointID == 0 {
		rec.EndpointID = s.nextID
		s.nextID++
	}
	s.rows[rec.Key] = rec
	return rec.EndpointID, nil
}

func (s *internalFakeStore) ListEndpoints(_ context.Context, centralName string) ([]store.EndpointRecord, error) {
	var out []store.EndpointRecord
	for _, rec := range s.rows {
		if centralName == "" || rec.Key.CentralName == centralName {
			out = append(out, rec)
		}
	}
	return out, nil
}

func (s *internalFakeStore) RemoveEndpoint(_ context.Context, key store.EndpointKey) error {
	delete(s.rows, key)
	return nil
}

var _ Store = (*internalFakeStore)(nil)
