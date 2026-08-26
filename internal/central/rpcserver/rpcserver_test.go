// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package rpcserver

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client/transport/binrpc"
	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
)

type stubHandlers struct {
	events    atomic.Int32
	lastIface atomic.Value
}

func (s *stubHandlers) Event(_ context.Context, iface, addr, param string, _ xmlrpc.Value) error {
	s.events.Add(1)
	s.lastIface.Store(iface)
	_ = addr
	_ = param
	return nil
}
func (s *stubHandlers) NewDevices(context.Context, string, xmlrpc.ArrayValue) error { return nil }
func (s *stubHandlers) DeleteDevices(context.Context, string, []string) error       { return nil }
func (s *stubHandlers) UpdateDevice(context.Context, string, string, int) error     { return nil }
func (s *stubHandlers) ReplaceDevice(context.Context, string, string, string) error { return nil }
func (s *stubHandlers) ReaddedDevice(context.Context, string, []string) error       { return nil }
func (s *stubHandlers) ListDevices(context.Context, string) (xmlrpc.ArrayValue, error) {
	return xmlrpc.ArrayValue{xmlrpc.StringValue("stub")}, nil
}
func (s *stubHandlers) Error(context.Context, string, int, string) error { return nil }

func TestXMLRPCServerRoutesByCentralName(t *testing.T) {
	srv, err := NewXMLRPCServer(XMLRPCConfig{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()

	h := &stubHandlers{}
	srv.Register("main", h)

	client, err := xmlrpc.NewClient(xmlrpc.Config{
		URL:       "http://" + srv.Addr().String() + "/RPC2/main",
		Interface: "HmIP-RF",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Call(context.Background(), "event",
		[]xmlrpc.Value{
			xmlrpc.StringValue("HmIP-RF"),
			xmlrpc.StringValue("ABC:1"),
			xmlrpc.StringValue("LEVEL"),
			xmlrpc.DoubleValue(0.5),
		})
	if err != nil {
		t.Fatalf("Call event: %v", err)
	}
	if h.events.Load() != 1 {
		t.Fatalf("events=%d", h.events.Load())
	}
	if h.lastIface.Load() != "HmIP-RF" {
		t.Fatalf("iface=%v", h.lastIface.Load())
	}
	cancel()
	<-done
}

func TestXMLRPCServerUnknownCentralReturns404(t *testing.T) {
	srv, err := NewXMLRPCServer(XMLRPCConfig{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()
	defer func() { cancel(); <-done }()

	client, _ := xmlrpc.NewClient(xmlrpc.Config{
		URL:       "http://" + srv.Addr().String() + "/RPC2/ghost",
		Interface: "HmIP-RF",
	})
	_, err = client.Call(context.Background(), "event",
		[]xmlrpc.Value{xmlrpc.StringValue("HmIP-RF"), xmlrpc.StringValue("X"), xmlrpc.StringValue("P"), xmlrpc.IntValue(0)})
	if err == nil {
		t.Fatal("expected error on unknown central")
	}
}

func TestBINRPCServerRoutesByInterfaceID(t *testing.T) {
	srv, err := NewBINRPCServer(BINRPCConfig{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()

	h := &stubHandlers{}
	srv.Register("CUxD", h)

	client, err := binrpc.NewClient(binrpc.Config{
		Addr:      srv.Addr().String(),
		Interface: "CUxD",
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Call(context.Background(), "event", []xmlrpc.Value{
		xmlrpc.StringValue("CUxD"),
		xmlrpc.StringValue("CUX:1"),
		xmlrpc.StringValue("STATE"),
		xmlrpc.BoolValue(true),
	})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	// give the server goroutine a moment to record the event
	deadline := time.Now().Add(100 * time.Millisecond)
	for time.Now().Before(deadline) && h.events.Load() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if h.events.Load() != 1 {
		t.Fatalf("events=%d", h.events.Load())
	}

	cancel()
	_ = srv.Close()
	<-done
}

func TestBINRPCServerRejectsUnknownInterface(t *testing.T) {
	srv, err := NewBINRPCServer(BINRPCConfig{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()
	defer func() { cancel(); _ = srv.Close(); <-done }()

	client, _ := binrpc.NewClient(binrpc.Config{Addr: srv.Addr().String()})
	_, err = client.Call(context.Background(), "event", []xmlrpc.Value{
		xmlrpc.StringValue("ghost"),
		xmlrpc.StringValue("X"),
		xmlrpc.StringValue("P"),
		xmlrpc.IntValue(0),
	})
	if err == nil {
		t.Fatal("expected error")
	}
}

// TestXMLRPCServerEffectivePortFromDynamicBind verifies that a
// :0-bind actually obtains an OS-assigned port and surfaces it via
// Addr() — the value the client uses to re-advertise during init().
// SPECIFICATION §11.2 marks this contract as critical.
func TestXMLRPCServerEffectivePortFromDynamicBind(t *testing.T) {
	srv, err := NewXMLRPCServer(XMLRPCConfig{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()
	defer func() { cancel(); <-done }()

	addr := srv.Addr()
	if addr == nil {
		t.Fatal("Addr() must not be nil after Serve")
	}
	tcp, ok := addr.(*net.TCPAddr)
	if !ok {
		t.Fatalf("Addr() type=%T want *net.TCPAddr", addr)
	}
	if tcp.Port == 0 {
		t.Fatal("dynamic bind must yield a non-zero effective port")
	}
}

// TestBINRPCServerEffectivePortFromDynamicBind is the BIN-RPC analogue
// of [TestXMLRPCServerEffectivePortFromDynamicBind].
func TestBINRPCServerEffectivePortFromDynamicBind(t *testing.T) {
	srv, err := NewBINRPCServer(BINRPCConfig{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()
	defer func() { cancel(); _ = srv.Close(); <-done }()

	addr := srv.Addr()
	if addr == nil {
		t.Fatal("Addr() must not be nil after Serve")
	}
	tcp, ok := addr.(*net.TCPAddr)
	if !ok {
		t.Fatalf("Addr() type=%T want *net.TCPAddr", addr)
	}
	if tcp.Port == 0 {
		t.Fatal("dynamic bind must yield a non-zero effective port")
	}
}

// TestXMLRPCServerHealthEndpoint verifies that GET /health on the XML-RPC
// callback server returns a JSON body with the required keys and a 200
// status.
func TestXMLRPCServerHealthEndpoint(t *testing.T) {
	t.Parallel()
	srv, err := NewXMLRPCServer(XMLRPCConfig{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()
	defer func() { cancel(); <-done }()

	// Register two centrals so centrals_count reflects 2.
	srv.Register("alpha", &stubHandlers{})
	srv.Register("beta", &stubHandlers{})

	resp, err := http.Get("http://" + srv.Addr().String() + "/health") //nolint:noctx // test helper — no cancellation needed
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	ct := resp.Header.Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Fatalf("Content-Type = %q, want application/json", ct)
	}

	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode JSON body: %v", err)
	}

	requiredKeys := []string{
		"status", "started", "centrals_count", "centrals",
		"request_count", "error_count", "listen_address",
	}
	for _, k := range requiredKeys {
		if _, ok := body[k]; !ok {
			t.Errorf("missing key %q in /health response; got %v", k, body)
		}
	}
	if got, ok := body["status"].(string); !ok || got != "healthy" {
		t.Errorf("status = %v, want \"healthy\"", body["status"])
	}
	if got, ok := body["started"].(bool); !ok || !got {
		t.Errorf("started = %v, want true", body["started"])
	}
	// centrals_count is a JSON number which Go unmarshals to float64.
	if got, ok := body["centrals_count"].(float64); !ok || int(got) != 2 {
		t.Errorf("centrals_count = %v, want 2", body["centrals_count"])
	}
}
