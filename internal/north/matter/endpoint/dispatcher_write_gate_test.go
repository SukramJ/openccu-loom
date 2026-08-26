// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package endpoint

import (
	"context"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/interfaces"
)

// recordingServer records every attribute ID passed to MatterWrite so a
// test can prove a read-only write is (or is not) dispatched to the cluster
// server. It advertises a configurable attribute list so wildcard-attribute
// expansion reaches both a read-only and a writable attribute.
type recordingServer struct {
	id         uint32
	attrs      []uint32
	writeCalls []uint32
}

func (s *recordingServer) MatterClusterID() uint32         { return s.id }
func (s *recordingServer) MatterRead(_ uint32) (any, bool) { return nil, false }
func (s *recordingServer) MatterReportable() []uint32      { return s.attrs }
func (s *recordingServer) MatterAttributes() []uint32      { return s.attrs }

func (s *recordingServer) MatterWrite(_ context.Context, attr uint32, _ any, _ hmenum.CommandPriority) error {
	s.writeCalls = append(s.writeCalls, attr)
	return nil
}

func (s *recordingServer) MatterInvoke(_ context.Context, _ uint32, _ any, _ hmenum.CommandPriority) (any, error) {
	return nil, nil
}

var (
	_ interfaces.MatterClusterServer          = (*recordingServer)(nil)
	_ interfaces.MatterClusterAttributeLister = (*recordingServer)(nil)
)

// recordingSource is a MatterEndpointSource backed by one recordingServer.
type recordingSource struct{ srv *recordingServer }

func (s recordingSource) MatterDeviceType() uint16 { return 0x010A }
func (s recordingSource) MatterClusterServers() []interfaces.MatterClusterServer {
	return []interfaces.MatterClusterServer{s.srv}
}

var _ interfaces.MatterEndpointSource = recordingSource{}

func recordedWrite(calls []uint32, attr uint32) bool {
	for _, a := range calls {
		if a == attr {
			return true
		}
	}
	return false
}

// TestWrite_ConcreteReadOnlyAttributeRejected verifies a concrete
// WriteRequest to a schema-read-only attribute (OnOff.OnOff 0x0000, access
// "R V") returns UNSUPPORTED_WRITE and never reaches the cluster server —
// so the write cannot be dispatched to the CCU. Mirrors matter.js
// AttributeWriteResponse.ts:229-231.
func TestWrite_ConcreteReadOnlyAttributeRejected(t *testing.T) {
	t.Parallel()
	srv := &recordingServer{id: 0x0006} // OnOff
	ep := &Endpoint{ID: 2, Source: recordingSource{srv: srv}}
	d := NewTopologyDispatcher(makeTopology(ep))

	results := d.Write(context.Background(), concreteAttrPath(2, 0x0006, 0x0000), im.AttributeValue{Value: true})
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	if results[0].Status != im.StatusUnsupportedWrite {
		t.Errorf("status = %v, want StatusUnsupportedWrite", results[0].Status)
	}
	if len(srv.writeCalls) != 0 {
		t.Errorf("read-only write reached the cluster server (calls=%v); it must be rejected before dispatch", srv.writeCalls)
	}
}

// TestWrite_ConcreteWritableAttributeProceeds verifies a concrete write to a
// schema-writable attribute (OnOff.OnTime 0x4001, access "RW VO") is
// dispatched to the cluster server and reported successful.
func TestWrite_ConcreteWritableAttributeProceeds(t *testing.T) {
	t.Parallel()
	srv := &recordingServer{id: 0x0006}
	ep := &Endpoint{ID: 2, Source: recordingSource{srv: srv}}
	d := NewTopologyDispatcher(makeTopology(ep))

	results := d.Write(context.Background(), concreteAttrPath(2, 0x0006, 0x4001), im.AttributeValue{Value: uint16(5)})
	if len(results) != 1 {
		t.Fatalf("want 1 result, got %d", len(results))
	}
	if results[0].Status != im.StatusSuccess {
		t.Errorf("status = %v, want StatusSuccess", results[0].Status)
	}
	if len(srv.writeCalls) != 1 || srv.writeCalls[0] != 0x4001 {
		t.Errorf("writable write did not reach the cluster server exactly once; calls=%v", srv.writeCalls)
	}
}

// TestWrite_WildcardSkipsReadOnlyAttribute verifies a wildcard-attribute
// write silently skips the read-only attribute (no result entry, no
// dispatch) while still writing the writable one. Mirrors matter.js
// AttributeWriteResponse.ts:329-331 (`if (!attribute.limits.writable)
// return;`).
func TestWrite_WildcardSkipsReadOnlyAttribute(t *testing.T) {
	t.Parallel()
	// OnOff cluster exposing a read-only (0x0000) and a writable (0x4001) attr.
	srv := &recordingServer{id: 0x0006, attrs: []uint32{0x0000, 0x4001}}
	ep := &Endpoint{ID: 2, Source: recordingSource{srv: srv}}
	d := NewTopologyDispatcher(makeTopology(ep))

	path := im.ConcreteAttributePath{
		Endpoint:     2,
		Cluster:      0x0006,
		HasEndpoint:  true,
		HasCluster:   true,
		HasAttribute: false, // wildcard attribute
	}
	results := d.Write(context.Background(), path, im.AttributeValue{Value: uint16(1)})

	// The read-only attr is skipped silently: no result entry for it.
	for _, r := range results {
		if r.Path.Attribute == 0x0000 {
			t.Errorf("wildcard write produced a result for read-only attr 0x0000 (status %v); it must be skipped silently", r.Status)
		}
	}
	// And it never reaches the cluster server.
	if recordedWrite(srv.writeCalls, 0x0000) {
		t.Errorf("wildcard write dispatched read-only attr 0x0000 to the cluster server; calls=%v", srv.writeCalls)
	}
	// The writable attr is still written.
	if !recordedWrite(srv.writeCalls, 0x4001) {
		t.Errorf("wildcard write did not dispatch writable attr 0x4001; calls=%v", srv.writeCalls)
	}
}

// TestWrite_WildcardEndpointSkipsReadOnlyAttributeSilently verifies that a
// path is only "concrete" when it names the endpoint too: a wildcard-endpoint
// write to a concrete read-only attribute must skip every match silently
// instead of reporting one UNSUPPORTED_WRITE per endpoint hosting the
// cluster. Those statuses enumerate the bridge's topology to a peer whose
// authorization has not even been evaluated yet — the read-only verdict runs
// before the access-control gate. matter.js routes exactly this shape
// (endpointId undefined, clusterId + attributeId set) into
// #writeAttributeForWildcard, which returns silently
// (AttributeWriteResponse.ts:329-331).
func TestWrite_WildcardEndpointSkipsReadOnlyAttributeSilently(t *testing.T) {
	t.Parallel()
	// Two endpoints hosting OnOff — the wildcard resolves to both.
	srvA := &recordingServer{id: 0x0006, attrs: []uint32{0x0000, 0x4001}}
	srvB := &recordingServer{id: 0x0006, attrs: []uint32{0x0000, 0x4001}}
	epA := &Endpoint{ID: 3, Source: recordingSource{srv: srvA}}
	epB := &Endpoint{ID: 4, Source: recordingSource{srv: srvB}}
	d := NewTopologyDispatcher(makeTopology(epA, epB))

	path := im.ConcreteAttributePath{
		Cluster:      0x0006,
		Attribute:    0x0000, // OnOff.OnOff — access "R V"
		HasEndpoint:  false,  // wildcard endpoint
		HasCluster:   true,
		HasAttribute: true,
	}
	results := d.Write(context.Background(), path, im.AttributeValue{Value: true})

	if len(results) != 0 {
		t.Errorf("wildcard-endpoint write to a read-only attribute produced %d status(es) %+v; matter.js skips silently", len(results), results)
	}
	if len(srvA.writeCalls) != 0 || len(srvB.writeCalls) != 0 {
		t.Errorf("read-only write reached a cluster server; calls=%v %v", srvA.writeCalls, srvB.writeCalls)
	}
}
