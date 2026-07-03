// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package im

import (
	"context"
	"testing"
)

// endpointScopedACLDispatcher expands a wildcard-endpoint read into concrete
// results on endpoints 1, 2 and 3 for the requested cluster, and grants ACL on
// exactly one endpoint. It reproduces the partial-wildcard-read scenario where
// an endpoint-scoped ACL entry must be re-checked per RESOLVED endpoint rather
// than once against endpoint 0. Mirrors the resolve-then-authorize order in
// matter.js AttributeReadResponse.ts:238-274.
type endpointScopedACLDispatcher struct {
	allowedEndpoint uint16
}

func (d *endpointScopedACLDispatcher) Read(_ context.Context, p ConcreteAttributePath) []ReadResult {
	if p.HasEndpoint {
		r := p
		if !r.HasAttribute {
			r.Attribute, r.HasAttribute = 0, true
		}
		return []ReadResult{{Path: r, Value: AttributeValue{Value: true}, Status: StatusSuccess}}
	}
	// Wildcard endpoint → expand across endpoints 1, 2, 3, resolving the
	// concrete cluster the request named (p.HasCluster == true here).
	out := make([]ReadResult, 0, 3)
	for _, ep := range []uint16{1, 2, 3} {
		r := p
		r.Endpoint, r.HasEndpoint = ep, true
		r.Attribute, r.HasAttribute = 0, true
		out = append(out, ReadResult{Path: r, Value: AttributeValue{Value: true}, Status: StatusSuccess})
	}
	return out
}

func (d *endpointScopedACLDispatcher) Write(_ context.Context, p ConcreteAttributePath, _ AttributeValue) []WriteResult {
	return []WriteResult{{Path: p, Status: StatusSuccess}}
}

func (d *endpointScopedACLDispatcher) Invoke(_ context.Context, p ConcreteCommandPath, _ any) InvokeResult {
	return InvokeResult{Path: p, Status: StatusSuccess}
}

func (d *endpointScopedACLDispatcher) CheckACL(_ context.Context, fabricIndex uint8, _ uint64, _ []uint32, endpoint uint16, _ uint32, _ uint8) StatusCode {
	if fabricIndex == 0 {
		return StatusSuccess // PASE bypass
	}
	if endpoint == d.allowedEndpoint {
		return StatusSuccess
	}
	return StatusUnsupportedAccess
}

var (
	_ Dispatcher = (*endpointScopedACLDispatcher)(nil)
	_ ACLChecker = (*endpointScopedACLDispatcher)(nil)
)

// TestHandleReadRequest_WildcardEndpoint_PerEndpointACL verifies that a
// wildcard-endpoint + concrete-cluster read is authorized PER RESOLVED
// endpoint: with an ACL granting cluster 0x0006 only on endpoint 2, the read
// returns exactly one data report for endpoint 2, and endpoints 1 and 3 are
// SILENTLY OMITTED (Matter §8.4.3.2 — a wildcard read discloses only
// authorized paths), never surfaced as an UnsupportedAccess status.
//
// Reproduces the pre-fix bug where the ACL pre-check ran once against
// endpoint 0 (the wildcard placeholder) and then expanded to every endpoint
// with no per-endpoint recheck — leaking or wrongly denying whole clusters.
func TestHandleReadRequest_WildcardEndpoint_PerEndpointACL(t *testing.T) {
	t.Parallel()
	d := &endpointScopedACLDispatcher{allowedEndpoint: 2}
	req := ReadRequest{
		AttributeRequests: []ConcreteAttributePath{
			// Wildcard endpoint (HasEndpoint=false), concrete cluster 0x0006.
			{Cluster: 0x0006, HasCluster: true},
		},
	}
	ctx := WithFabricFilter(context.Background(), true, 1) // fabricIndex=1

	rd := HandleReadRequest(ctx, d, req)

	if len(rd.Reports) != 1 {
		t.Fatalf("reports=%d, want 1 (only the authorized endpoint), got %+v", len(rd.Reports), rd.Reports)
	}
	rep := rd.Reports[0]
	if rep.IsStatus {
		t.Fatalf("wildcard-expanded report must be data, not a status; got status=%v", rep.Status.Status)
	}
	if rep.Path.Endpoint != 2 {
		t.Fatalf("report endpoint=%d, want 2 (the only ACL-granted endpoint)", rep.Path.Endpoint)
	}
}

// TestHandleReadRequest_ConcreteDenied_StillReturnsStatus verifies that a
// FULLY-CONCRETE denied read (explicit endpoint + cluster + attribute) still
// returns an AttributeStatusIB(UnsupportedAccess) — the wildcard-omit change
// must not hide a legitimately-denied concrete read. Mirrors matter.js
// AttributeReadResponse.ts addConcrete (error status on denial).
func TestHandleReadRequest_ConcreteDenied_StillReturnsStatus(t *testing.T) {
	t.Parallel()
	d := &endpointScopedACLDispatcher{allowedEndpoint: 2}
	req := ReadRequest{
		AttributeRequests: []ConcreteAttributePath{
			// Concrete endpoint 3 (denied), cluster 0x0006, attribute 0.
			{Endpoint: 3, HasEndpoint: true, Cluster: 0x0006, HasCluster: true, Attribute: 0, HasAttribute: true},
		},
	}
	ctx := WithFabricFilter(context.Background(), true, 1)

	rd := HandleReadRequest(ctx, d, req)

	if len(rd.Reports) != 1 {
		t.Fatalf("reports=%d, want 1 status report", len(rd.Reports))
	}
	rep := rd.Reports[0]
	if !rep.IsStatus {
		t.Fatal("denied concrete read must return an IsStatus report")
	}
	if rep.Status.Status != StatusUnsupportedAccess {
		t.Fatalf("status=%v, want UnsupportedAccess (0x7E)", rep.Status.Status)
	}
}

// TestHandleReadRequest_WildcardEndpoint_AllDenied_OmitsAll verifies that when
// no endpoint is authorized, a wildcard read returns ZERO reports (every
// expanded result omitted) rather than a status report.
func TestHandleReadRequest_WildcardEndpoint_AllDenied_OmitsAll(t *testing.T) {
	t.Parallel()
	d := &endpointScopedACLDispatcher{allowedEndpoint: 99} // matches no expanded endpoint
	req := ReadRequest{
		AttributeRequests: []ConcreteAttributePath{
			{Cluster: 0x0006, HasCluster: true},
		},
	}
	ctx := WithFabricFilter(context.Background(), true, 1)

	rd := HandleReadRequest(ctx, d, req)

	if len(rd.Reports) != 0 {
		t.Fatalf("reports=%d, want 0 (all wildcard-expanded results omitted), got %+v", len(rd.Reports), rd.Reports)
	}
}
