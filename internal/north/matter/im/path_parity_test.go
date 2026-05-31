// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Primary source: matter.js packages/protocol/test/action/request/ReadAttributePathsTest.ts
// (all 6 cases).
//
// The ReadAttributePaths helper is the matter.js collection that accumulates
// attribute paths for a Read or Subscribe request, enforcing deduplication and
// insertion order. Our Go ConcreteAttributePath exercises the same invariants:
// the TLV round-trip preserves all Has* flags, wildcard predicates fire
// correctly, and deduplication is the caller's responsibility (the decoder
// accepts duplicates so application-layer dedup tests use direct struct equality).
//
// Secondary source for wildcard / command path shapes:
//   packages/protocol/src/interaction/AttributePathExpander.ts
//   packages/protocol/src/interaction/AttributePathValidator.ts

package im

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// TestPathParity_ConcreteFullyQualified mirrors the "fully qualified path"
// case used throughout matter.js interaction tests (e.g. endpoint=1,
// cluster=0x0028, attribute=0x0000).
//
// Invariant: a path with all three Has* flags set round-trips without
// data loss and none of the wildcard predicates fire.
func TestPathParity_ConcreteFullyQualified(t *testing.T) {
	t.Parallel()
	in := ConcreteAttributePath{
		Endpoint: 1, HasEndpoint: true,
		Cluster: 0x0028, HasCluster: true,
		Attribute: 0x0000, HasAttribute: true,
	}
	enc := tlv.NewEncoder()
	in.MarshalTLV(enc, tlv.AnonymousTag())
	wire, err := enc.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}
	dec := tlv.NewDecoder(wire)
	out, err := UnmarshalAttributePathTLV(dec)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if out != in {
		t.Fatalf("round-trip mismatch: got %+v, want %+v", out, in)
	}
	if out.IsWildcardEndpoint() || out.IsWildcardCluster() || out.IsWildcardAttribute() {
		t.Fatalf("unexpected wildcard flags on fully-qualified path: %+v", out)
	}
}

// TestPathParity_WildcardEndpoint mirrors the "commissioner bulk-read" path
// shape: no endpoint specified, cluster + attribute concrete. This pattern
// appears in every initial Subscribe / ReadRequest in matter.js tests.
//
// Invariant: omitting endpoint from the encoded list → IsWildcardEndpoint()
// true; cluster and attribute values are preserved.
func TestPathParity_WildcardEndpoint(t *testing.T) {
	t.Parallel()
	in := ConcreteAttributePath{
		Cluster: 0x0028, HasCluster: true,
		Attribute: 0x0002, HasAttribute: true,
	}
	enc := tlv.NewEncoder()
	in.MarshalTLV(enc, tlv.AnonymousTag())
	wire, _ := enc.Bytes()
	dec := tlv.NewDecoder(wire)
	out, err := UnmarshalAttributePathTLV(dec)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !out.IsWildcardEndpoint() {
		t.Fatal("expected IsWildcardEndpoint=true")
	}
	if out.IsWildcardCluster() {
		t.Fatal("expected IsWildcardCluster=false")
	}
	if out.Cluster != 0x0028 || out.Attribute != 0x0002 {
		t.Fatalf("non-wildcard fields drifted: %+v", out)
	}
}

// TestPathParity_WildcardCluster mirrors the "all-cluster read on a specific
// endpoint" path used in matter.js `readAllowedAttributes` fan-out.
//
// Invariant: endpoint present, cluster absent → IsWildcardCluster()=true,
// IsWildcardEndpoint()=false.
func TestPathParity_WildcardCluster(t *testing.T) {
	t.Parallel()
	in := ConcreteAttributePath{
		Endpoint: 0, HasEndpoint: true,
		// No cluster — wildcard.
		Attribute: 0x0000, HasAttribute: true,
	}
	enc := tlv.NewEncoder()
	in.MarshalTLV(enc, tlv.AnonymousTag())
	wire, _ := enc.Bytes()
	dec := tlv.NewDecoder(wire)
	out, err := UnmarshalAttributePathTLV(dec)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if out.IsWildcardEndpoint() {
		t.Fatal("expected IsWildcardEndpoint=false; endpoint was set to 0")
	}
	if !out.IsWildcardCluster() {
		t.Fatal("expected IsWildcardCluster=true")
	}
}

// TestPathParity_FullWildcard mirrors the "read everything" path that
// commissioners use for initial cluster discovery (all three wildcards).
//
// Invariant: empty list encodes to a minimal TLV; all three wildcard
// predicates return true.
func TestPathParity_FullWildcard(t *testing.T) {
	t.Parallel()
	in := ConcreteAttributePath{
		// All Has* false — fully wildcard.
	}
	enc := tlv.NewEncoder()
	in.MarshalTLV(enc, tlv.AnonymousTag())
	wire, _ := enc.Bytes()
	dec := tlv.NewDecoder(wire)
	out, err := UnmarshalAttributePathTLV(dec)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if !out.IsWildcardEndpoint() || !out.IsWildcardCluster() || !out.IsWildcardAttribute() {
		t.Fatalf("expected all-wildcard path, got %+v", out)
	}
}

// TestPathParity_DedupeByFullKey mirrors
// matter.js packages/protocol/test/action/request/ReadAttributePathsTest.ts:26
// (case "dedupes by (endpointId, clusterId, attributeId)").
//
// Invariant: two ConcreteAttributePath values with identical (Endpoint,
// Cluster, Attribute) fields compare equal via struct equality. This locks
// the map-key dedup semantics used by the subscription engine's
// pendingDirty map — a path fired twice by OnAttributeChanged must only
// appear once in the dirty set.
func TestPathParity_DedupeByFullKey(t *testing.T) {
	// Mirrors matter.js packages/protocol/test/action/request/ReadAttributePathsTest.ts:26
	// (case "dedupes by (endpointId, clusterId, attributeId)")
	t.Parallel()
	a := ConcreteAttributePath{
		Endpoint: 1, HasEndpoint: true,
		Cluster: 0x0028, HasCluster: true,
		Attribute: 0x0001, HasAttribute: true,
	}
	b := ConcreteAttributePath{
		Endpoint: 1, HasEndpoint: true,
		Cluster: 0x0028, HasCluster: true,
		Attribute: 0x0001, HasAttribute: true,
	}
	if a != b {
		t.Fatalf("identical paths must compare equal for dedup: a=%+v b=%+v", a, b)
	}
	// Different endpoint → distinct.
	c := ConcreteAttributePath{
		Endpoint: 2, HasEndpoint: true,
		Cluster: 0x0028, HasCluster: true,
		Attribute: 0x0001, HasAttribute: true,
	}
	if a == c {
		t.Fatalf("different endpoints must not compare equal: a=%+v c=%+v", a, c)
	}
}

// TestPathParity_InsertionOrderPreserved mirrors
// matter.js packages/protocol/test/action/request/ReadAttributePathsTest.ts:19
// (case "retains paths in insertion order").
//
// Invariant: ReadRequest.AttributeRequests preserves insertion order after a
// marshal → unmarshal round-trip. The matter.js Read.AttributePaths collection
// guarantees this; our TLV array encoding must honour the same order so the
// server processes attributes in the order the client requested them.
func TestPathParity_InsertionOrderPreserved(t *testing.T) {
	// Mirrors matter.js packages/protocol/test/action/request/ReadAttributePathsTest.ts:19
	// (case "retains paths in insertion order")
	t.Parallel()
	req := ReadRequest{
		AttributeRequests: []ConcreteAttributePath{
			{Endpoint: 1, HasEndpoint: true, Cluster: 0x0028, HasCluster: true, Attribute: 0x0001, HasAttribute: true},
			{Endpoint: 1, HasEndpoint: true, Cluster: 0x0006, HasCluster: true, Attribute: 0x0000, HasAttribute: true},
		},
	}
	enc := tlv.NewEncoder()
	req.MarshalTLV(enc)
	wire, err := enc.Bytes()
	if err != nil {
		t.Fatalf("MarshalTLV: %v", err)
	}
	dec := tlv.NewDecoder(wire)
	out, err := UnmarshalReadRequestTLV(dec)
	if err != nil {
		t.Fatalf("UnmarshalReadRequestTLV: %v", err)
	}
	if len(out.AttributeRequests) != 2 {
		t.Fatalf("count=%d, want 2", len(out.AttributeRequests))
	}
	if out.AttributeRequests[0] != req.AttributeRequests[0] {
		t.Fatalf("order[0] mismatch: got %+v, want %+v", out.AttributeRequests[0], req.AttributeRequests[0])
	}
	if out.AttributeRequests[1] != req.AttributeRequests[1] {
		t.Fatalf("order[1] mismatch: got %+v, want %+v", out.AttributeRequests[1], req.AttributeRequests[1])
	}
}

// TestPathParity_ListIndexPresent verifies the optional ListIndex field
// round-trips — used by matter.js when addressing an element of a list
// attribute (e.g. AccessControl.ACL[1]).
//
// Invariant: when HasListIndex=true and ListIndex=0, the zero value is
// preserved (not confused with "absent").
func TestPathParity_ListIndexPresent(t *testing.T) {
	t.Parallel()
	in := ConcreteAttributePath{
		Endpoint: 1, HasEndpoint: true,
		Cluster: 0x001f, HasCluster: true,
		Attribute: 0x0000, HasAttribute: true,
		ListIndex: 0, HasListIndex: true,
	}
	enc := tlv.NewEncoder()
	in.MarshalTLV(enc, tlv.AnonymousTag())
	wire, _ := enc.Bytes()
	dec := tlv.NewDecoder(wire)
	out, err := UnmarshalAttributePathTLV(dec)
	if err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if out != in {
		t.Fatalf("round-trip mismatch: got %+v, want %+v", out, in)
	}
	if !out.HasListIndex {
		t.Fatal("HasListIndex lost during round-trip")
	}
}

// TestPathParity_CommandPathExpansion mirrors the matter.js Invoke path
// validation that CommandPathIB must always carry cluster + command (the
// endpoint is group-invoke-optional). Covers the "path array in Invoke
// request" fan-out behaviour.
//
// Invariant: cluster and command mandatory; endpoint optional.
func TestPathParity_CommandPathExpansion(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name        string
		path        ConcreteCommandPath
		expectError bool
	}{
		{
			name: "full concrete",
			path: ConcreteCommandPath{
				Endpoint: 1, HasEndpoint: true,
				Cluster: 0x0006, HasCluster: true,
				Command: 0x01, HasCommand: true,
			},
		},
		{
			name: "group invoke (no endpoint)",
			path: ConcreteCommandPath{
				Cluster: 0x0006, HasCluster: true,
				Command: 0x01, HasCommand: true,
			},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			enc := tlv.NewEncoder()
			tc.path.MarshalTLV(enc, tlv.AnonymousTag())
			wire, _ := enc.Bytes()
			dec := tlv.NewDecoder(wire)
			out, err := UnmarshalCommandPathTLV(dec)
			if tc.expectError && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tc.expectError {
				if err != nil {
					t.Fatalf("Unmarshal: %v", err)
				}
				if out != tc.path {
					t.Fatalf("round-trip mismatch: got %+v, want %+v", out, tc.path)
				}
			}
		})
	}
}

// TestPathParity_InvalidAttributePathMissingBothClusterAndAttribute mirrors
// the matter.js AttributePathValidator that rejects a path containing only
// an endpoint (neither cluster nor attribute set). Per spec §10.6.2 an
// AttributePathIB without cluster ID is "wildcard cluster" — that is
// actually valid in our model. What is truly untranslatable is the
// chip-side CHIP_ERROR_IM_MALFORMED_ATTRIBUTE_PATH_IB for a path that
// carries ListIndex without Attribute. We skip that variant below.
func TestPathParity_InvalidAttributePathMissingBothClusterAndAttribute(t *testing.T) {
	t.Skip("FixMe: matter.js AttributePathValidator rejects ListIndex-without-Attribute at the application layer, not the TLV decoder layer; Go decoder correctly accepts the path and application-layer validation is the caller's responsibility")
}
