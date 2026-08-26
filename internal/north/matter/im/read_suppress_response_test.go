// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package im

import (
	"context"
	"testing"
)

// TestHandleReadRequest_SuppressResponseTrue pins the plain-Read
// SuppressResponse contract: a non-subscribe Read's ReportData carries
// SuppressResponse=true, so the controller answers the terminal chunk with
// only a Standalone MRP-Ack rather than an IM StatusResponse. Mirrors matter.js
// packages/node/src/node/server/InteractionServer.ts:346-350,371-374
// (handleReadRequest returns `dataReport: { suppressResponse: true }`) and chip
// src/app/ReadHandler.cpp:340 (`responseExpected = IsType(Subscribe) ||
// aMoreChunks` → false on a read's final chunk). Subscribe priming builds its
// own report and is unaffected — HandleReadRequest is only reached on the plain
// read path.
func TestHandleReadRequest_SuppressResponseTrue(t *testing.T) {
	d := &fakeDispatcher{
		readVal:  AttributeValue{Value: true},
		readStat: StatusSuccess,
	}
	req := ReadRequest{
		AttributeRequests: []ConcreteAttributePath{
			{Endpoint: 1, HasEndpoint: true, Cluster: 0x0006, HasCluster: true, Attribute: 0, HasAttribute: true},
		},
	}
	rd := HandleReadRequest(context.Background(), d, req)
	if !rd.SuppressResponse {
		t.Fatal("plain Read ReportData must carry SuppressResponse=true (matter.js handleReadRequest)")
	}
}

// TestHandleReadRequest_SuppressResponseTrue_EmptyRead covers the empty-read
// corner: matter.js returns `dataReport: { suppressResponse: true }` even when
// the request names no attribute or event paths (InteractionServer.ts:344-350).
func TestHandleReadRequest_SuppressResponseTrue_EmptyRead(t *testing.T) {
	d := &fakeDispatcher{}
	rd := HandleReadRequest(context.Background(), d, ReadRequest{})
	if !rd.SuppressResponse {
		t.Fatal("empty Read ReportData must still carry SuppressResponse=true")
	}
	if len(rd.Reports) != 0 {
		t.Fatalf("empty Read should yield no attribute reports, got %d", len(rd.Reports))
	}
}
