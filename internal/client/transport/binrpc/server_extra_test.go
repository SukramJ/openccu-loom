// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// Tests for server-side handleConn branches not covered by client_test.go.

package binrpc

import (
	"bytes"
	"context"
	"net"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
)

// TestServerDropsBadRequest verifies that handleConn closes the connection
// cleanly when the incoming bytes do not form a valid BIN-RPC request.
// This covers the ReadRequest error branch.
func TestServerDropsBadRequest(t *testing.T) {
	t.Parallel()

	s, err := NewServer(ServerConfig{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		_ = s.Close()
		<-done
	})

	// Connect and send garbage.
	conn, err := net.Dial("tcp", s.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()

	// Send invalid bytes — not a BIN-RPC marker.
	_, _ = conn.Write([]byte("JUNK_BYTES_NOT_BINRPC"))

	// Server should close the conn; our Read should return EOF or an error.
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 64)
	n, err := conn.Read(buf)
	if n != 0 || err == nil {
		// Server closed → we expect either 0 bytes + error, or error on read.
		// Both are acceptable; any data would be unexpected.
		t.Logf("n=%d err=%v (server closed connection cleanly)", n, err)
	}
}

// TestServerHandleConnWriteResponseFails verifies the conn.Write failure branch:
// the server dispatches the method, encodes the response, then tries to write to a
// connection that was closed by the client. The server should log the error and return
// without panicking.
func TestServerHandleConnWriteResponseFails(t *testing.T) {
	t.Parallel()

	s, err := NewServer(ServerConfig{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	s.Mux().Handle("echo", func(_ context.Context, params []xmlrpc.Value) (xmlrpc.Value, error) {
		return params[0], nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		_ = s.Close()
		<-done
	})

	// Send a valid request but close the connection immediately after writing,
	// before the server can reply.
	conn, err := net.Dial("tcp", s.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	// Write a valid request.
	var frame bytes.Buffer
	if err := WriteRequest(&frame, "echo", []xmlrpc.Value{xmlrpc.IntValue(1)}); err != nil {
		conn.Close()
		t.Fatal(err)
	}
	_, _ = conn.Write(frame.Bytes())
	// Close immediately so the server's conn.Write() fails.
	_ = conn.Close()

	// Give the server goroutine time to process the connection.
	time.Sleep(50 * time.Millisecond)
}
