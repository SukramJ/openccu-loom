// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package im

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// TestParity_Subscribe_InitialDecode_ChunkBoundary verifies that
// UnmarshalSubscribeRequestTLV correctly decodes a SubscribeRequest
// containing multiple AttributeRequests in a single TLV payload. Apple
// Home's MTRDevice sends a wildcard read on the initial Subscribe that
// includes several AttributePath entries; decoding must not stop early
// at an internal chunk boundary in the array (missing paths would
// produce an incomplete initial report and cause the subscription to
// re-establish immediately). Mirrors matter.js
// packages/types/src/protocol/types/TlvSubscribeRequest.ts round-trip
// behaviour for multi-path subscribes.
func TestParity_Subscribe_InitialDecode_ChunkBoundary(t *testing.T) {
	t.Parallel()

	// Build a SubscribeRequest with three AttributeRequests: two cluster-scoped
	// paths (cluster 0x0006, cluster 0x0008) and one wildcard (all clusters
	// on endpoint 1). This is representative of the Apple Home initial
	// Subscribe payload seen in practice.
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())
	enc.PutBool(tlv.ContextTag(tagSubReqKeepSubscriptions), false)
	enc.PutUint(tlv.ContextTag(tagSubReqMinIntervalFloor), 1)
	enc.PutUint(tlv.ContextTag(tagSubReqMaxIntervalCeiling), 60)

	// AttributeRequests array (context tag 2) — three paths.
	enc.StartArray(tlv.ContextTag(tagSubReqAttributeRequests))
	for _, clusterID := range []uint32{0x0006, 0x0008, 0x0201} {
		// AttributePathIB is encoded as a List (0x17), not a Struct,
		// per Matter Core Spec §10.5.4 and chip src/lib/core/TLVUtilities.cpp.
		enc.StartList(tlv.AnonymousTag())
		enc.PutUint(tlv.ContextTag(2), 1)                 // EndpointID
		enc.PutUint(tlv.ContextTag(3), uint64(clusterID)) // ClusterID
		if err := enc.EndContainer(); err != nil {
			t.Fatalf("EndContainer path: %v", err)
		}
	}
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("EndContainer array: %v", err)
	}

	enc.PutBool(tlv.ContextTag(tagSubReqFabricFiltered), true)
	if err := enc.EndContainer(); err != nil {
		t.Fatalf("EndContainer struct: %v", err)
	}
	wire, err := enc.Bytes()
	if err != nil {
		t.Fatalf("Bytes: %v", err)
	}

	dec := tlv.NewDecoder(wire)
	req, err := UnmarshalSubscribeRequestTLV(dec)
	if err != nil {
		t.Fatalf("UnmarshalSubscribeRequestTLV: %v", err)
	}
	if got := len(req.AttributeRequests); got != 3 {
		t.Errorf("AttributeRequests len = %d, want 3 (chunk-boundary decode must not drop paths)", got)
	}
	if !req.FabricFiltered {
		t.Error("FabricFiltered = false, want true")
	}
}
