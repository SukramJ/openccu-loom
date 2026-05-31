// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package im_test

import (
	"testing"

	"github.com/SukramJ/openccu-loom/internal/north/matter/im"
	"github.com/SukramJ/openccu-loom/internal/north/matter/tlv"
)

// FuzzUnmarshalInvokeRequest exercises the Invoke decoder against
// random byte slices. The decoder MUST NOT panic on any input —
// truncated payloads, malformed containers, oversized integers, etc.
// all need to surface as a returned error. Originated from the
// Welle-7 nil-fieldsReader-panic bug (ADR 0013 §2).
func FuzzUnmarshalInvokeRequest(f *testing.F) {
	// Seeds: a few hand-crafted shapes plus the empty case.
	f.Add([]byte{})
	f.Add([]byte{0x15, 0x18})                                                                   // empty anonymous struct
	f.Add([]byte{0x15, 0x28, 0x00, 0x18})                                                       // SuppressResponse: false
	f.Add([]byte{0x15, 0x36, 0x02, 0x15, 0x37, 0x00, 0x24, 0x01, 0x05, 0x18, 0x18, 0x18, 0x18}) // path-only invoke
	f.Fuzz(func(t *testing.T, payload []byte) {
		dec := tlv.NewDecoder(payload)
		_, _ = im.UnmarshalInvokeRequestTLV(dec, nil)
		// Any panic here would FAIL via the testing harness.
	})
}

// FuzzUnmarshalReadRequest mirrors FuzzUnmarshalInvokeRequest for
// the Read path.
func FuzzUnmarshalReadRequest(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{0x15, 0x18})
	f.Add([]byte{0x15, 0x36, 0x00, 0x18, 0x28, 0x03, 0x18}) // empty AttributeRequests + FabricFiltered=false
	f.Fuzz(func(t *testing.T, payload []byte) {
		dec := tlv.NewDecoder(payload)
		_, _ = im.UnmarshalReadRequestTLV(dec)
	})
}

// FuzzUnmarshalWriteRequest mirrors FuzzUnmarshalInvokeRequest for
// the Write path.
func FuzzUnmarshalWriteRequest(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{0x15, 0x18})
	f.Fuzz(func(t *testing.T, payload []byte) {
		dec := tlv.NewDecoder(payload)
		_, _ = im.UnmarshalWriteRequestTLV(dec, nil)
	})
}

// FuzzUnmarshalSubscribeRequest mirrors FuzzUnmarshalInvokeRequest
// for the Subscribe path.
func FuzzUnmarshalSubscribeRequest(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{0x15, 0x18})
	f.Fuzz(func(t *testing.T, payload []byte) {
		dec := tlv.NewDecoder(payload)
		_, _ = im.UnmarshalSubscribeRequestTLV(dec)
	})
}
