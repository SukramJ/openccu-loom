// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

package binrpc

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// A peer that reads the whole request frame and closes without answering
// leaves the call executed and its reply lost.
func TestClientReportsAReplyLostAfterTheFrameWasSent(t *testing.T) {
	t.Parallel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		// Frame: "Bin" + type byte + uint32 payload length + payload.
		head := make([]byte, 8)
		if _, err := io.ReadFull(conn, head); err != nil {
			return
		}
		_, _ = io.CopyN(io.Discard, conn, int64(binary.BigEndian.Uint32(head[4:])))
	}()

	c, err := NewClient(Config{Addr: ln.Addr().String()})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err = c.Call(ctx, "setValue", []xmlrpc.Value{xmlrpc.StringValue("CUX2801001:1"), xmlrpc.StringValue("STATE"), xmlrpc.BoolValue(true)})
	if !errors.Is(err, hmerr.ErrReplyLost) {
		t.Fatalf("Call = %v, want ErrReplyLost", err)
	}
}

func TestClientKeepsARefusedDialANoConnection(t *testing.T) {
	t.Parallel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	c, err := NewClient(Config{Addr: addr})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Call(context.Background(), "setValue", []xmlrpc.Value{xmlrpc.StringValue("CUX2801001:1")})
	if !errors.Is(err, hmerr.ErrNoConnection) || errors.Is(err, hmerr.ErrReplyLost) {
		t.Fatalf("Call = %v, want ErrNoConnection without ErrReplyLost", err)
	}
}
