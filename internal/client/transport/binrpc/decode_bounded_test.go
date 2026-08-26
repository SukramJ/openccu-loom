// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Tests for readFrame's bounded incremental payload read (io.CopyN into a
// bytes.Buffer seeded with initialPayloadCap) — see wire.go's
// initialPayloadCap doc comment for the threat model: a crafted 8-byte
// header can claim a payload up to MaxMessageSize while sending no body,
// and must not cost the full declared size in allocation before the
// mismatch is detected.

package binrpc

import (
	"bytes"
	"runtime"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
)

// lyingRequestHeader builds an 8-byte BIN-RPC request header that claims
// size bytes of payload without any of them actually following.
func lyingRequestHeader(size uint32) []byte {
	return []byte{
		'B', 'i', 'n', msgTypeRequest,
		byte(size >> 24), byte(size >> 16), byte(size >> 8), byte(size),
	}
}

// TestReadRequestLyingHeaderEmptyBodyErrors verifies that a header
// declaring size == MaxMessageSize with no payload bytes behind it is
// rejected rather than blocking forever or succeeding on a short read.
func TestReadRequestLyingHeaderEmptyBodyErrors(t *testing.T) {
	header := lyingRequestHeader(uint32(MaxMessageSize))
	_, err := ReadRequest(bytes.NewReader(header))
	if err == nil {
		t.Fatal("expected error: header claims MaxMessageSize payload but body is empty")
	}
}

// TestReadRequestRoundTripsValidFrame proves the incremental io.CopyN
// read path still round-trips a normal, correctly-sized request frame —
// the bounded-read change must not regress the happy path.
func TestReadRequestRoundTripsValidFrame(t *testing.T) {
	var buf bytes.Buffer
	params := []xmlrpc.Value{
		xmlrpc.StringValue("HmIP-RF"),
		xmlrpc.StringValue("ABC1234567:1"),
		xmlrpc.StringValue("LEVEL"),
		xmlrpc.DoubleValue(0.5),
	}
	if err := WriteRequest(&buf, "event", params); err != nil {
		t.Fatalf("WriteRequest: %v", err)
	}

	req, err := ReadRequest(&buf)
	if err != nil {
		t.Fatalf("ReadRequest: %v", err)
	}
	if req.Method != "event" {
		t.Errorf("Method = %q, want %q", req.Method, "event")
	}
	if len(req.Params) != len(params) {
		t.Fatalf("Params len = %d, want %d", len(req.Params), len(params))
	}
	iface, err := xmlrpc.AsString(req.Params[0])
	if err != nil {
		t.Fatalf("AsString(Params[0]): %v", err)
	}
	if iface != "HmIP-RF" {
		t.Errorf("Params[0] = %q, want %q", iface, "HmIP-RF")
	}
	addr, err := xmlrpc.AsString(req.Params[1])
	if err != nil {
		t.Fatalf("AsString(Params[1]): %v", err)
	}
	if addr != "ABC1234567:1" {
		t.Errorf("Params[1] = %q, want %q", addr, "ABC1234567:1")
	}
}

// TestReadRequestLyingHeaderBoundsAllocation proves the fix: a header
// claiming the full MaxMessageSize (10 MiB) with an empty body must not
// drive readFrame to allocate anywhere near that much. Before the fix,
// make([]byte, size) committed the full declared size up front, so N
// stalled connections pinned N x 10 MiB. TotalAlloc is a monotonically
// increasing process-wide counter, so this stays deterministic without
// relying on GC timing.
func TestReadRequestLyingHeaderBoundsAllocation(t *testing.T) {
	header := lyingRequestHeader(uint32(MaxMessageSize))

	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, err := ReadRequest(bytes.NewReader(header))
	runtime.ReadMemStats(&after)

	if err == nil {
		t.Fatal("expected error for lying header with empty body")
	}

	const limit = 1024 * 1024 // 1 MiB — generous headroom over initialPayloadCap (64 KiB).
	delta := after.TotalAlloc - before.TotalAlloc
	if delta > limit {
		t.Errorf("ReadRequest on a 10 MiB-claiming, empty-body header allocated %d bytes, want < %d bytes", delta, limit)
	}
}
