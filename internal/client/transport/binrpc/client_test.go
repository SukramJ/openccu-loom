// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package binrpc

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// testServer starts a Server on 127.0.0.1:0 and returns it along with
// its effective address. Caller must close via t.Cleanup.
func testServer(t *testing.T) *Server {
	t.Helper()
	s, err := NewServer(ServerConfig{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	serveErr := make(chan error, 1)
	go func() { serveErr <- s.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		_ = s.Close()
		<-serveErr
	})
	return s
}

func testClient(t *testing.T, s *Server) *Client {
	t.Helper()
	c, err := NewClient(Config{Addr: s.Addr().String(), Interface: "CUxD"})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestClientHappyPath(t *testing.T) {
	s := testServer(t)
	var sawArg atomic.Value
	s.Mux().Handle("getValue", func(_ context.Context, params []xmlrpc.Value) (xmlrpc.Value, error) {
		addr, _ := xmlrpc.AsString(params[0])
		sawArg.Store(addr)
		return xmlrpc.IntValue(42), nil
	})

	c := testClient(t, s)
	v, err := c.Call(context.Background(), "getValue", []xmlrpc.Value{xmlrpc.StringValue("ABC:1")})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	n, _ := xmlrpc.AsInt(v)
	if n != 42 {
		t.Fatalf("result=%d", n)
	}
	if sawArg.Load() != "ABC:1" {
		t.Fatalf("server saw arg=%v", sawArg.Load())
	}
}

func TestClientFaultRoundTrip(t *testing.T) {
	s := testServer(t)
	s.Mux().Handle("bad", func(context.Context, []xmlrpc.Value) (xmlrpc.Value, error) {
		return nil, &hmerr.XMLRPCFault{Code: -5, Message: "nope"}
	})

	c := testClient(t, s)
	_, err := c.Call(context.Background(), "bad", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	var fault *hmerr.XMLRPCFault
	if !errors.As(err, &fault) || fault.Code != -5 {
		t.Fatalf("got %v, want fault -5", err)
	}
	if !errors.Is(err, hmerr.ErrClientException) {
		t.Error("fault should classify as ErrClientException")
	}
	ctx, ok := hmerr.ErrorContext(err)
	if !ok || ctx.Protocol != "bin-rpc" || ctx.Method != "bad" {
		t.Fatalf("context=%+v", ctx)
	}
}

func TestServerGenericErrorBecomesFaultMinusOne(t *testing.T) {
	s := testServer(t)
	s.Mux().Handle("boom", func(context.Context, []xmlrpc.Value) (xmlrpc.Value, error) {
		return nil, errors.New("oops")
	})
	c := testClient(t, s)
	_, err := c.Call(context.Background(), "boom", nil)
	var fault *hmerr.XMLRPCFault
	if !errors.As(err, &fault) || fault.Code != -1 {
		t.Fatalf("got %v, want fault -1", err)
	}
}

func TestClientNoConnection(t *testing.T) {
	c, err := NewClient(Config{Addr: "127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = c.Call(context.Background(), "ping", nil)
	if !errors.Is(err, hmerr.ErrNoConnection) {
		t.Fatalf("got %v, want ErrNoConnection", err)
	}
}

func TestClientRespectsContextCancellation(t *testing.T) {
	// A listener that never speaks back; accept succeeds, nothing is written.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			// Hold the connection open; never write.
			go func() {
				defer conn.Close()
				time.Sleep(5 * time.Second)
			}()
		}
	}()

	c, _ := NewClient(Config{Addr: ln.Addr().String(), IOTimeout: time.Hour})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err = c.Call(ctx, "stuck", nil)
	if err == nil {
		t.Fatal("expected cancellation error")
	}
}

func TestUnknownMethodReturnsFault32601(t *testing.T) {
	s := testServer(t)
	c := testClient(t, s)
	_, err := c.Call(context.Background(), "nope", nil)
	var fault *hmerr.XMLRPCFault
	if !errors.As(err, &fault) || fault.Code != -32601 {
		t.Fatalf("got %v, want fault -32601", err)
	}
}

func TestConcurrentCalls(t *testing.T) {
	s := testServer(t)
	s.Mux().Handle("echo", func(_ context.Context, params []xmlrpc.Value) (xmlrpc.Value, error) {
		return params[0], nil
	})
	c := testClient(t, s)

	const n = 20
	var wg sync.WaitGroup
	wg.Add(n)
	errs := make(chan error, n)
	for i := range n {
		go func() {
			defer wg.Done()
			v, err := c.Call(context.Background(), "echo", []xmlrpc.Value{xmlrpc.IntValue(int32(i))}) //nolint:gosec // test
			if err != nil {
				errs <- err
				return
			}
			got, _ := xmlrpc.AsInt(v)
			if got != i {
				errs <- errors.New("mismatch")
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent call error: %v", err)
	}
}

func TestNewClientRejectsEmptyAddr(t *testing.T) {
	if _, err := NewClient(Config{}); err == nil {
		t.Fatal("expected error")
	}
}

func TestNewServerRejectsEmptyAddr(t *testing.T) {
	if _, err := NewServer(ServerConfig{}); err == nil {
		t.Fatal("expected error")
	}
}

func TestServerCloseIsIdempotent(t *testing.T) {
	s, err := NewServer(ServerConfig{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	// Second close is allowed and must not panic.
	_ = s.Close()
}

func TestServerNilResultEncodedAsEmptyString(t *testing.T) {
	s := testServer(t)
	s.Mux().Handle("void", func(context.Context, []xmlrpc.Value) (xmlrpc.Value, error) {
		return nil, nil
	})
	c := testClient(t, s)
	v, err := c.Call(context.Background(), "void", nil)
	if err != nil {
		t.Fatal(err)
	}
	// NilValue is encoded as empty string on the wire; the decoder
	// returns a StringValue.
	s2, err := xmlrpc.AsString(v)
	if err != nil {
		t.Fatal(err)
	}
	if s2 != "" {
		t.Fatalf("want empty string, got %q", s2)
	}
}
