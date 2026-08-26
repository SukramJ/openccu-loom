// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package im_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// TestUnmarshalInvokeRequest_NilFieldsReader verifies that decoding
// an InvokeCommandRequest whose CommandDataIB carries a Fields
// container does not panic when the caller passes a nil fieldsReader.
//
// Regression for the chip-tool full-pairing smoke (Update 24): the
// Cleanup-stage ArmFailSafe(0) command sends a 35-byte InvokeRequest
// with a non-empty Fields container; the bridge's IM dispatcher
// invokes UnmarshalInvokeRequestTLV with fieldsReader=nil, and the
// previous implementation hit a nil-call panic that killed the UDP
// receive goroutine silently.
func TestUnmarshalInvokeRequest_NilFieldsReader(t *testing.T) {
	t.Parallel()

	// Build a synthetic InvokeRequestMessage with a single Invoke that
	// carries a Fields container. Path: endpoint=0, cluster=0x30,
	// command=0 (ArmFailSafe). Fields contain ExpiryLengthSeconds=0.
	enc := tlv.NewEncoder()
	enc.StartStruct(tlv.AnonymousTag())   // top
	enc.PutBool(tlv.ContextTag(0), false) // SuppressResponse
	enc.PutBool(tlv.ContextTag(1), false) // TimedRequest
	enc.StartArray(tlv.ContextTag(2))     // InvokeRequests
	enc.StartStruct(tlv.AnonymousTag())   //   CommandDataIB
	enc.StartList(tlv.ContextTag(0))      //     CommandPathIB
	enc.PutUint(tlv.ContextTag(0), 0)     //       Endpoint: 0
	enc.PutUint(tlv.ContextTag(1), 0x30)  //       Cluster: GeneralCommissioning (0x30)
	enc.PutUint(tlv.ContextTag(2), 0x00)  //       Command: ArmFailSafe (0x00)
	_ = enc.EndContainer()                //     end CommandPathIB
	enc.StartStruct(tlv.ContextTag(1))    //     CommandFields
	enc.PutUint(tlv.ContextTag(0), 60)    //       ExpiryLengthSeconds: 60
	enc.PutUint(tlv.ContextTag(1), 0)     //       Breadcrumb: 0
	_ = enc.EndContainer()                //     end CommandFields
	_ = enc.EndContainer()                //   end CommandDataIB
	_ = enc.EndContainer()                // end InvokeRequests array
	enc.PutUint(tlv.ContextTag(0xFF), 11) // InteractionModelRevision
	_ = enc.EndContainer()                // end top
	wire, err := enc.Bytes()
	if err != nil {
		t.Fatalf("encode: %v", err)
	}

	dec := tlv.NewDecoder(wire)
	req, err := im.UnmarshalInvokeRequestTLV(dec, nil) // <-- nil fieldsReader
	if err != nil {
		t.Fatalf("UnmarshalInvokeRequestTLV: %v", err)
	}
	if len(req.Invokes) != 1 {
		t.Fatalf("Invokes: got %d, want 1", len(req.Invokes))
	}
	inv := req.Invokes[0]
	if inv.Path.Cluster != 0x30 || inv.Path.Command != 0x00 {
		t.Errorf("path: cluster=%x cmd=%x, want cluster=0x30 cmd=0x00", inv.Path.Cluster, inv.Path.Command)
	}
	// Fields are skipped when the reader is nil — the decoder must
	// not panic AND must not surface an error; the path-only Invoke
	// is the contract.
	if inv.Fields != nil {
		t.Errorf("Fields = %v, want nil (nil reader skips)", inv.Fields)
	}
}
