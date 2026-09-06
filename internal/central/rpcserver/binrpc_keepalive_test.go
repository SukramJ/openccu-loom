// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package rpcserver

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client/transport/binrpc"
	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
)

// TestBINRPCServerServesEveryRequestOnOneConnection pins that a peer which
// keeps its socket open across calls — the CCU's own libXmlRpc client does
// (XmlRpcClient setKeepOpen) — gets every callback answered, not only the
// first. The integration simulator dials per call, so this is the only
// place the second request on a connection is ever exercised.
func TestBINRPCServerServesEveryRequestOnOneConnection(t *testing.T) {
	t.Parallel()

	const ifaceID = "loom-ccu-CUxD"
	h := &binrpcRecordingHandlers{}
	srv, err := NewBINRPCServer(BINRPCConfig{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatalf("NewBINRPCServer: %v", err)
	}
	srv.Register(ifaceID, h)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()
	t.Cleanup(func() { cancel(); <-done; _ = srv.Close() })

	conn, err := net.Dial("tcp", srv.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	for i, param := range []string{"STATE", "LEVEL", "PRESS_SHORT"} {
		if err := binrpc.WriteRequest(conn, "event", []xmlrpc.Value{
			xmlrpc.StringValue(ifaceID), xmlrpc.StringValue("CUX2801001:1"),
			xmlrpc.StringValue(param), xmlrpc.StringValue("1"),
		}); err != nil {
			t.Fatalf("request %d: write: %v", i, err)
		}
		if _, err := binrpc.ReadResponse(conn); err != nil {
			t.Fatalf("request %d on the same connection got no response: %v", i, err)
		}
	}
	got := h.recordedEvents()
	if len(got) != 3 {
		t.Fatalf("delivered %d events over one connection, want 3: %v", len(got), got)
	}
}
