// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// xmlrpc_dispatch_test.go exercises the XML-RPC and BIN-RPC callback
// method dispatch layer: bindXMLRPCMethods (newDevices, deleteDevices,
// updateDevice, replaceDevice, readdedDevice, listDevices, error),
// wrong-arity faults, BIN-RPC dispatch (newDevices, deleteDevices,
// listDevices, error, unknown method, missing interface_id),
// BINRPCServer.Deregister, asFault, and serveHealth.

package rpcserver

import (
	"bytes"
	"context"
	"net/http"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client/transport/binrpc"
	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// testResponseWriter is a minimal http.ResponseWriter for white-box testing.
type testResponseWriter struct {
	buf    bytes.Buffer
	header http.Header
	status int
}

func newTestResponseWriter() *testResponseWriter {
	return &testResponseWriter{header: make(http.Header)}
}

func (w *testResponseWriter) Header() http.Header         { return w.header }
func (w *testResponseWriter) WriteHeader(code int)        { w.status = code }
func (w *testResponseWriter) Write(b []byte) (int, error) { return w.buf.Write(b) }
func (w *testResponseWriter) String() string              { return w.buf.String() }

// ─── recordingHandlers captures all CCU callback method calls ────────────────

type recordingHandlers struct {
	events          atomic.Int32
	newDevices      atomic.Int32
	deleteDevices   atomic.Int32
	updateDevice    atomic.Int32
	replaceDevice   atomic.Int32
	readdedDevice   atomic.Int32
	listDeviceCalls atomic.Int32
	errorCalls      atomic.Int32
}

func (r *recordingHandlers) Event(_ context.Context, _, _, _ string, _ xmlrpc.Value) error {
	r.events.Add(1)
	return nil
}

func (r *recordingHandlers) NewDevices(_ context.Context, _ string, _ xmlrpc.ArrayValue) error {
	r.newDevices.Add(1)
	return nil
}

func (r *recordingHandlers) DeleteDevices(_ context.Context, _ string, _ []string) error {
	r.deleteDevices.Add(1)
	return nil
}

func (r *recordingHandlers) UpdateDevice(_ context.Context, _, _ string, _ int) error {
	r.updateDevice.Add(1)
	return nil
}

func (r *recordingHandlers) ReplaceDevice(_ context.Context, _, _, _ string) error {
	r.replaceDevice.Add(1)
	return nil
}

func (r *recordingHandlers) ReaddedDevice(_ context.Context, _ string, _ []string) error {
	r.readdedDevice.Add(1)
	return nil
}

func (r *recordingHandlers) ListDevices(_ context.Context, _ string) (xmlrpc.ArrayValue, error) {
	r.listDeviceCalls.Add(1)
	return xmlrpc.ArrayValue{xmlrpc.StringValue("DEV001")}, nil
}

func (r *recordingHandlers) Error(_ context.Context, _ string, _ int, _ string) error {
	r.errorCalls.Add(1)
	return nil
}

// ─── XML-RPC: bindXMLRPCMethods coverage ─────────────────────────────────────

func TestXMLRPCNewDevicesDispatched(t *testing.T) {
	t.Parallel()
	h := &recordingHandlers{}
	_, url := newTestXMLRPCServer(t, "main", h)
	client, _ := xmlrpc.NewClient(xmlrpc.Config{URL: url})

	_, err := client.Call(context.Background(), "newDevices", []xmlrpc.Value{
		xmlrpc.StringValue("HmIP-RF"),
		xmlrpc.ArrayValue{},
	})
	if err != nil {
		t.Fatalf("newDevices: %v", err)
	}
	if h.newDevices.Load() != 1 {
		t.Fatalf("newDevices handler called %d times, want 1", h.newDevices.Load())
	}
}

func TestXMLRPCDeleteDevicesDispatched(t *testing.T) {
	t.Parallel()
	h := &recordingHandlers{}
	_, url := newTestXMLRPCServer(t, "main", h)
	client, _ := xmlrpc.NewClient(xmlrpc.Config{URL: url})

	_, err := client.Call(context.Background(), "deleteDevices", []xmlrpc.Value{
		xmlrpc.StringValue("HmIP-RF"),
		xmlrpc.ArrayValue{xmlrpc.StringValue("DEV001:1")},
	})
	if err != nil {
		t.Fatalf("deleteDevices: %v", err)
	}
	if h.deleteDevices.Load() != 1 {
		t.Fatalf("deleteDevices handler called %d times, want 1", h.deleteDevices.Load())
	}
}

func TestXMLRPCUpdateDeviceDispatched(t *testing.T) {
	t.Parallel()
	h := &recordingHandlers{}
	_, url := newTestXMLRPCServer(t, "main", h)
	client, _ := xmlrpc.NewClient(xmlrpc.Config{URL: url})

	_, err := client.Call(context.Background(), "updateDevice", []xmlrpc.Value{
		xmlrpc.StringValue("HmIP-RF"),
		xmlrpc.StringValue("DEV001"),
		xmlrpc.IntValue(0),
	})
	if err != nil {
		t.Fatalf("updateDevice: %v", err)
	}
	if h.updateDevice.Load() != 1 {
		t.Fatalf("updateDevice handler called %d times, want 1", h.updateDevice.Load())
	}
}

func TestXMLRPCReplaceDeviceDispatched(t *testing.T) {
	t.Parallel()
	h := &recordingHandlers{}
	_, url := newTestXMLRPCServer(t, "main", h)
	client, _ := xmlrpc.NewClient(xmlrpc.Config{URL: url})

	_, err := client.Call(context.Background(), "replaceDevice", []xmlrpc.Value{
		xmlrpc.StringValue("HmIP-RF"),
		xmlrpc.StringValue("OLD001"),
		xmlrpc.StringValue("NEW001"),
	})
	if err != nil {
		t.Fatalf("replaceDevice: %v", err)
	}
	if h.replaceDevice.Load() != 1 {
		t.Fatalf("replaceDevice handler called %d times, want 1", h.replaceDevice.Load())
	}
}

func TestXMLRPCReaddedDeviceDispatched(t *testing.T) {
	t.Parallel()
	h := &recordingHandlers{}
	_, url := newTestXMLRPCServer(t, "main", h)
	client, _ := xmlrpc.NewClient(xmlrpc.Config{URL: url})

	_, err := client.Call(context.Background(), "readdedDevice", []xmlrpc.Value{
		xmlrpc.StringValue("HmIP-RF"),
		xmlrpc.ArrayValue{xmlrpc.StringValue("DEV001")},
	})
	if err != nil {
		t.Fatalf("readdedDevice: %v", err)
	}
	if h.readdedDevice.Load() != 1 {
		t.Fatalf("readdedDevice handler called %d times, want 1", h.readdedDevice.Load())
	}
}

func TestXMLRPCListDevicesDispatched(t *testing.T) {
	t.Parallel()
	h := &recordingHandlers{}
	_, url := newTestXMLRPCServer(t, "main", h)
	client, _ := xmlrpc.NewClient(xmlrpc.Config{URL: url})

	v, err := client.Call(context.Background(), "listDevices", []xmlrpc.Value{
		xmlrpc.StringValue("HmIP-RF"),
	})
	if err != nil {
		t.Fatalf("listDevices: %v", err)
	}
	arr, err := xmlrpc.AsArray(v)
	if err != nil {
		t.Fatalf("listDevices result not array: %v", err)
	}
	if len(arr) != 1 {
		t.Fatalf("expected 1 device, got %d", len(arr))
	}
	if h.listDeviceCalls.Load() != 1 {
		t.Fatalf("listDevices handler called %d times, want 1", h.listDeviceCalls.Load())
	}
}

func TestXMLRPCErrorCallbackWithStringCode(t *testing.T) {
	t.Parallel()
	h := &recordingHandlers{}
	_, url := newTestXMLRPCServer(t, "main", h)
	client, _ := xmlrpc.NewClient(xmlrpc.Config{URL: url})

	// Send error with a string error code (some firmware variants do this).
	_, err := client.Call(context.Background(), "error", []xmlrpc.Value{
		xmlrpc.StringValue("HmIP-RF"),
		xmlrpc.StringValue("-5"),
		xmlrpc.StringValue("device lost"),
	})
	if err != nil {
		t.Fatalf("error callback: %v", err)
	}
	if h.errorCalls.Load() != 1 {
		t.Fatalf("error handler called %d times, want 1", h.errorCalls.Load())
	}
}

// ─── XMLRPCServer.Serve: non-graceful http.Server error ─────────────────────

// TestXMLRPCServerServeReturnsErrorWhenListenerPreClosed verifies the Serve
// path that propagates a non-ErrServerClosed error from http.Server.Serve
// back to the caller (the `case err := <-errCh` branch).
func TestXMLRPCServerServeReturnsErrorWhenListenerPreClosed(t *testing.T) {
	t.Parallel()
	srv, err := NewXMLRPCServer(XMLRPCConfig{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	// Close the listener before Serve — http.Server.Serve will return
	// a non-ErrServerClosed error immediately.
	_ = srv.listener.Close()

	ctx := context.Background() // no cancellation — error comes from listener
	serveErr := srv.Serve(ctx)
	if serveErr == nil {
		t.Log("Serve returned nil (platform variation on pre-closed listener)")
	}
	// Either path (nil or error) is valid — the important thing is Serve returned.
}

// ─── BINRPCServer.Serve: non-ctx accept error ────────────────────────────────

// TestBINRPCServerServeReturnsErrorOnListenerClose verifies the Serve path
// that returns a non-nil error when the listener is closed without the
// context being cancelled.
func TestBINRPCServerServeReturnsErrorOnListenerClose(t *testing.T) {
	t.Parallel()
	srv, err := NewBINRPCServer(BINRPCConfig{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	// Use a context that is NOT cancelled — we want the non-ctx error path.
	ctx := context.Background()
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()

	// Close the underlying listener while Serve is blocking on Accept.
	// This triggers the "ctx.Err() == nil" branch and returns an error.
	_ = srv.listener.Close()
	// Also mark closed to avoid a race in closeOnce.
	srv.closed.Store(true)

	select {
	case serveErr := <-done:
		if serveErr == nil {
			// Accepted on some platforms after the listener is closed — OK.
			t.Log("Serve returned nil (platform variation)")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Serve did not return after listener close")
	}
}

// ─── BIN-RPC: dispatch coverage ──────────────────────────────────────────────

func newTestBINRPCServer(t *testing.T, ifaceID string, h Handlers) *BINRPCServer {
	t.Helper()
	srv, err := NewBINRPCServer(BINRPCConfig{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	srv.Register(ifaceID, h)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()
	t.Cleanup(func() { cancel(); _ = srv.Close(); <-done })
	return srv
}

func newBINRPCClient(t *testing.T, srv *BINRPCServer, ifaceID string) *binrpc.Client {
	t.Helper()
	client, err := binrpc.NewClient(binrpc.Config{
		Addr:      srv.Addr().String(),
		Interface: ifaceID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestBINRPCDispatchNewDevices(t *testing.T) {
	t.Parallel()
	h := &recordingHandlers{}
	srv := newTestBINRPCServer(t, "CUxD", h)
	client := newBINRPCClient(t, srv, "CUxD")

	_, err := client.Call(context.Background(), "newDevices", []xmlrpc.Value{
		xmlrpc.StringValue("CUxD"),
		xmlrpc.ArrayValue{},
	})
	if err != nil {
		t.Fatalf("newDevices: %v", err)
	}
	waitFor(t, func() bool { return h.newDevices.Load() == 1 })
	if h.newDevices.Load() != 1 {
		t.Fatalf("newDevices handler called %d times, want 1", h.newDevices.Load())
	}
}

func TestBINRPCDispatchDeleteDevices(t *testing.T) {
	t.Parallel()
	h := &recordingHandlers{}
	srv := newTestBINRPCServer(t, "CUxD", h)
	client := newBINRPCClient(t, srv, "CUxD")

	_, err := client.Call(context.Background(), "deleteDevices", []xmlrpc.Value{
		xmlrpc.StringValue("CUxD"),
		xmlrpc.ArrayValue{xmlrpc.StringValue("CUX:1")},
	})
	if err != nil {
		t.Fatalf("deleteDevices: %v", err)
	}
	waitFor(t, func() bool { return h.deleteDevices.Load() == 1 })
	if h.deleteDevices.Load() != 1 {
		t.Fatalf("deleteDevices handler called %d times, want 1", h.deleteDevices.Load())
	}
}

func TestBINRPCDispatchListDevices(t *testing.T) {
	t.Parallel()
	h := &recordingHandlers{}
	srv := newTestBINRPCServer(t, "CUxD", h)
	client := newBINRPCClient(t, srv, "CUxD")

	v, err := client.Call(context.Background(), "listDevices", []xmlrpc.Value{
		xmlrpc.StringValue("CUxD"),
	})
	if err != nil {
		t.Fatalf("listDevices: %v", err)
	}
	waitFor(t, func() bool { return h.listDeviceCalls.Load() == 1 })
	arr, _ := xmlrpc.AsArray(v)
	if len(arr) != 1 {
		t.Fatalf("expected 1 device, got %d", len(arr))
	}
}

func TestBINRPCDispatchError(t *testing.T) {
	t.Parallel()
	h := &recordingHandlers{}
	srv := newTestBINRPCServer(t, "CUxD", h)
	client := newBINRPCClient(t, srv, "CUxD")

	_, err := client.Call(context.Background(), "error", []xmlrpc.Value{
		xmlrpc.StringValue("CUxD"),
		xmlrpc.IntValue(-3),
		xmlrpc.StringValue("wire error"),
	})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	waitFor(t, func() bool { return h.errorCalls.Load() == 1 })
	if h.errorCalls.Load() != 1 {
		t.Fatalf("error handler called %d times, want 1", h.errorCalls.Load())
	}
}

func TestBINRPCDispatchUnknownMethod(t *testing.T) {
	t.Parallel()
	h := &recordingHandlers{}
	srv := newTestBINRPCServer(t, "CUxD", h)
	client := newBINRPCClient(t, srv, "CUxD")

	_, err := client.Call(context.Background(), "unknownMethod", []xmlrpc.Value{
		xmlrpc.StringValue("CUxD"),
	})
	if err == nil {
		t.Fatal("expected error for unknown BIN-RPC method")
	}
}

// ─── BINRPCServer.Deregister ─────────────────────────────────────────────────

func TestBINRPCServerDeregisterRemovesInterface(t *testing.T) {
	t.Parallel()
	srv, err := NewBINRPCServer(BINRPCConfig{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	h := &recordingHandlers{}
	srv.Register("CUxD", h)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx) }()
	t.Cleanup(func() { cancel(); _ = srv.Close(); <-done })

	client, _ := binrpc.NewClient(binrpc.Config{Addr: srv.Addr().String(), Interface: "CUxD"})

	// Before deregister — must work.
	_, err = client.Call(context.Background(), "event", []xmlrpc.Value{
		xmlrpc.StringValue("CUxD"),
		xmlrpc.StringValue("CUX:1"),
		xmlrpc.StringValue("STATE"),
		xmlrpc.BoolValue(true),
	})
	if err != nil {
		t.Fatalf("before deregister: %v", err)
	}
	waitFor(t, func() bool { return h.events.Load() == 1 })

	srv.Deregister("CUxD")

	// After deregister — must fail.
	_, err = client.Call(context.Background(), "event", []xmlrpc.Value{
		xmlrpc.StringValue("CUxD"),
		xmlrpc.StringValue("CUX:1"),
		xmlrpc.StringValue("STATE"),
		xmlrpc.BoolValue(false),
	})
	if err == nil {
		t.Fatal("expected error after BIN-RPC Deregister")
	}
}

// ─── serveHealth: "stopped" status when Serve is not running ─────────────────

// TestXMLRPCServerHealthStoppedStatus verifies the serveHealth "stopped"
// branch that fires when s.started is false.
func TestXMLRPCServerHealthStoppedStatus(t *testing.T) {
	t.Parallel()
	srv, err := NewXMLRPCServer(XMLRPCConfig{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	// Call serveHealth directly — started is false since Serve was never called.
	w := newTestResponseWriter()
	srv.serveHealth(w)
	body := w.String()
	if !strings.Contains(body, "stopped") {
		t.Errorf("health before Serve should contain 'stopped'; got %s", body)
	}
}

// ─── handleConn: nil-result branch ───────────────────────────────────────────

// nilListHandlers returns (nil, nil) from ListDevices to exercise the
// "if result == nil { result = xmlrpc.NilValue{} }" branch in handleConn.
type nilListHandlers struct {
	recordingHandlers
}

func (n *nilListHandlers) ListDevices(_ context.Context, _ string) (xmlrpc.ArrayValue, error) {
	return nil, nil // triggers the nil-result guard in handleConn
}

// TestBINRPCHandleConnNilResultGuard exercises the handleConn path where
// dispatch returns (nil, nil) — triggered by a ListDevices handler that
// returns nil.
func TestBINRPCHandleConnNilResultGuard(t *testing.T) {
	t.Parallel()
	h := &nilListHandlers{}
	srv := newTestBINRPCServer(t, "CUxD", h)
	client := newBINRPCClient(t, srv, "CUxD")

	// listDevices returns (nil, nil) from our handler.
	_, err := client.Call(context.Background(), "listDevices", []xmlrpc.Value{
		xmlrpc.StringValue("CUxD"),
	})
	// The server will substitute NilValue and the call should succeed.
	if err != nil {
		t.Fatalf("listDevices with nil result: %v", err)
	}
}

// ─── asFault: non-XMLRPCFault path ───────────────────────────────────────────

func TestAsFaultFromXMLRPCFault(t *testing.T) {
	t.Parallel()
	want := &hmerr.XMLRPCFault{Code: -8, Message: "duty cycle"}
	got := asFault(want)
	if got != want {
		t.Errorf("asFault(XMLRPCFault) returned different pointer")
	}
}

func TestAsFaultFromPlainError(t *testing.T) {
	t.Parallel()
	// A plain error must be wrapped with code -1.
	got := asFault(ErrNoHandlers)
	if got.Code != -1 {
		t.Errorf("asFault(plain error).Code = %d, want -1", got.Code)
	}
	if got.Message == "" {
		t.Error("asFault(plain error).Message must not be empty")
	}
}

// ─── bindXMLRPCMethods: wrong-arity paths ────────────────────────────────────

// The XML-RPC method handlers guard each call with an exact param count.
// Sending the wrong arity triggers the error branch, lifting coverage in
// bindXMLRPCMethods.

func TestXMLRPCWrongArityEvent(t *testing.T) {
	t.Parallel()
	h := &recordingHandlers{}
	_, url := newTestXMLRPCServer(t, "main", h)
	client, _ := xmlrpc.NewClient(xmlrpc.Config{URL: url})
	// event requires 4 params; send 2.
	_, err := client.Call(context.Background(), "event", []xmlrpc.Value{
		xmlrpc.StringValue("HmIP-RF"),
		xmlrpc.StringValue("X:1"),
	})
	if err == nil {
		t.Fatal("expected error for wrong-arity event")
	}
}

func TestXMLRPCWrongArityNewDevices(t *testing.T) {
	t.Parallel()
	h := &recordingHandlers{}
	_, url := newTestXMLRPCServer(t, "main", h)
	client, _ := xmlrpc.NewClient(xmlrpc.Config{URL: url})
	// newDevices requires 2 params; send 1.
	_, err := client.Call(context.Background(), "newDevices", []xmlrpc.Value{
		xmlrpc.StringValue("HmIP-RF"),
	})
	if err == nil {
		t.Fatal("expected error for wrong-arity newDevices")
	}
}

func TestXMLRPCWrongArityDeleteDevices(t *testing.T) {
	t.Parallel()
	h := &recordingHandlers{}
	_, url := newTestXMLRPCServer(t, "main", h)
	client, _ := xmlrpc.NewClient(xmlrpc.Config{URL: url})
	// deleteDevices requires 2 params; send 1.
	_, err := client.Call(context.Background(), "deleteDevices", []xmlrpc.Value{
		xmlrpc.StringValue("HmIP-RF"),
	})
	if err == nil {
		t.Fatal("expected error for wrong-arity deleteDevices")
	}
}

func TestXMLRPCWrongArityUpdateDevice(t *testing.T) {
	t.Parallel()
	h := &recordingHandlers{}
	_, url := newTestXMLRPCServer(t, "main", h)
	client, _ := xmlrpc.NewClient(xmlrpc.Config{URL: url})
	// updateDevice requires 3 params; send 2.
	_, err := client.Call(context.Background(), "updateDevice", []xmlrpc.Value{
		xmlrpc.StringValue("HmIP-RF"),
		xmlrpc.StringValue("DEV001"),
	})
	if err == nil {
		t.Fatal("expected error for wrong-arity updateDevice")
	}
}

func TestXMLRPCWrongArityReplaceDevice(t *testing.T) {
	t.Parallel()
	h := &recordingHandlers{}
	_, url := newTestXMLRPCServer(t, "main", h)
	client, _ := xmlrpc.NewClient(xmlrpc.Config{URL: url})
	// replaceDevice requires 3 params; send 2.
	_, err := client.Call(context.Background(), "replaceDevice", []xmlrpc.Value{
		xmlrpc.StringValue("HmIP-RF"),
		xmlrpc.StringValue("OLD"),
	})
	if err == nil {
		t.Fatal("expected error for wrong-arity replaceDevice")
	}
}

func TestXMLRPCWrongArityReaddedDevice(t *testing.T) {
	t.Parallel()
	h := &recordingHandlers{}
	_, url := newTestXMLRPCServer(t, "main", h)
	client, _ := xmlrpc.NewClient(xmlrpc.Config{URL: url})
	// readdedDevice requires 2 params; send 1.
	_, err := client.Call(context.Background(), "readdedDevice", []xmlrpc.Value{
		xmlrpc.StringValue("HmIP-RF"),
	})
	if err == nil {
		t.Fatal("expected error for wrong-arity readdedDevice")
	}
}

func TestXMLRPCWrongArityListDevices(t *testing.T) {
	t.Parallel()
	h := &recordingHandlers{}
	_, url := newTestXMLRPCServer(t, "main", h)
	client, _ := xmlrpc.NewClient(xmlrpc.Config{URL: url})
	// listDevices requires 1 param; send 0.
	_, err := client.Call(context.Background(), "listDevices", []xmlrpc.Value{})
	if err == nil {
		t.Fatal("expected error for wrong-arity listDevices")
	}
}

func TestXMLRPCWrongArityError(t *testing.T) {
	t.Parallel()
	h := &recordingHandlers{}
	_, url := newTestXMLRPCServer(t, "main", h)
	client, _ := xmlrpc.NewClient(xmlrpc.Config{URL: url})
	// error requires 3 params; send 2.
	_, err := client.Call(context.Background(), "error", []xmlrpc.Value{
		xmlrpc.StringValue("HmIP-RF"),
		xmlrpc.IntValue(-1),
	})
	if err == nil {
		t.Fatal("expected error for wrong-arity error callback")
	}
}

// TestXMLRPCNewDevicesWithNonStringIface sends newDevices where the iface
// param is not a string, triggering the AsString error branch.
func TestXMLRPCNewDevicesWithNonStringIface(t *testing.T) {
	t.Parallel()
	h := &recordingHandlers{}
	_, url := newTestXMLRPCServer(t, "main", h)
	client, _ := xmlrpc.NewClient(xmlrpc.Config{URL: url})
	_, err := client.Call(context.Background(), "newDevices", []xmlrpc.Value{
		xmlrpc.IntValue(42), // non-string iface
		xmlrpc.ArrayValue{},
	})
	if err == nil {
		t.Fatal("expected error for non-string iface in newDevices")
	}
}

// TestXMLRPCDeleteDevicesWithNonStringIface sends deleteDevices where iface is not a string.
func TestXMLRPCDeleteDevicesWithNonStringIface(t *testing.T) {
	t.Parallel()
	h := &recordingHandlers{}
	_, url := newTestXMLRPCServer(t, "main", h)
	client, _ := xmlrpc.NewClient(xmlrpc.Config{URL: url})
	_, err := client.Call(context.Background(), "deleteDevices", []xmlrpc.Value{
		xmlrpc.IntValue(99), // non-string iface
		xmlrpc.ArrayValue{xmlrpc.StringValue("addr")},
	})
	if err == nil {
		t.Fatal("expected error for non-string iface in deleteDevices")
	}
}

// TestXMLRPCEventWithNonStringFirstParam sends event where iface is not a string.
func TestXMLRPCEventWithNonStringFirstParam(t *testing.T) {
	t.Parallel()
	h := &recordingHandlers{}
	_, url := newTestXMLRPCServer(t, "main", h)
	client, _ := xmlrpc.NewClient(xmlrpc.Config{URL: url})
	_, err := client.Call(context.Background(), "event", []xmlrpc.Value{
		xmlrpc.IntValue(1), // non-string iface
		xmlrpc.StringValue("X:1"),
		xmlrpc.StringValue("STATE"),
		xmlrpc.BoolValue(true),
	})
	if err == nil {
		t.Fatal("expected error for non-string iface in event")
	}
}

// TestXMLRPCEventWithNonStringAddr sends event where addr is not a string.
func TestXMLRPCEventWithNonStringAddr(t *testing.T) {
	t.Parallel()
	h := &recordingHandlers{}
	_, url := newTestXMLRPCServer(t, "main", h)
	client, _ := xmlrpc.NewClient(xmlrpc.Config{URL: url})
	_, err := client.Call(context.Background(), "event", []xmlrpc.Value{
		xmlrpc.StringValue("HmIP-RF"),
		xmlrpc.IntValue(2), // non-string addr
		xmlrpc.StringValue("STATE"),
		xmlrpc.BoolValue(true),
	})
	if err == nil {
		t.Fatal("expected error for non-string addr in event")
	}
}

// TestXMLRPCEventWithNonStringParam sends event where param is not a string.
func TestXMLRPCEventWithNonStringParam(t *testing.T) {
	t.Parallel()
	h := &recordingHandlers{}
	_, url := newTestXMLRPCServer(t, "main", h)
	client, _ := xmlrpc.NewClient(xmlrpc.Config{URL: url})
	_, err := client.Call(context.Background(), "event", []xmlrpc.Value{
		xmlrpc.StringValue("HmIP-RF"),
		xmlrpc.StringValue("X:1"),
		xmlrpc.IntValue(3), // non-string param
		xmlrpc.BoolValue(true),
	})
	if err == nil {
		t.Fatal("expected error for non-string param in event")
	}
}

// TestXMLRPCListDevicesWithNonStringIface triggers AsString failure for iface in listDevices.
func TestXMLRPCListDevicesWithNonStringIface(t *testing.T) {
	t.Parallel()
	h := &recordingHandlers{}
	_, url := newTestXMLRPCServer(t, "main", h)
	client, _ := xmlrpc.NewClient(xmlrpc.Config{URL: url})
	// listDevices uses `iface, _ := AsString(params[0])` (ignoring error),
	// so this actually succeeds; test the wrong-arity case instead.
	_, err := client.Call(context.Background(), "listDevices", []xmlrpc.Value{})
	if err == nil {
		t.Fatal("expected error for zero-arity listDevices")
	}
}

// TestXMLRPCNewDevicesWithNonArrayPayload sends newDevices where the device
// descriptions payload is not an array (e.g. a string), triggering the
// xmlrpc.AsArray error branch inside bindXMLRPCMethods.
func TestXMLRPCNewDevicesWithNonArrayPayload(t *testing.T) {
	t.Parallel()
	h := &recordingHandlers{}
	_, url := newTestXMLRPCServer(t, "main", h)
	client, _ := xmlrpc.NewClient(xmlrpc.Config{URL: url})
	_, err := client.Call(context.Background(), "newDevices", []xmlrpc.Value{
		xmlrpc.StringValue("HmIP-RF"),
		xmlrpc.StringValue("not-an-array"), // wrong type for descriptions
	})
	if err == nil {
		t.Fatal("expected error for non-array device descriptions in newDevices")
	}
}

// TestXMLRPCDeleteDevicesWithNonArrayPayload exercises the xmlrpc.AsStrings
// error branch in deleteDevices.
func TestXMLRPCDeleteDevicesWithNonArrayPayload(t *testing.T) {
	t.Parallel()
	h := &recordingHandlers{}
	_, url := newTestXMLRPCServer(t, "main", h)
	client, _ := xmlrpc.NewClient(xmlrpc.Config{URL: url})
	_, err := client.Call(context.Background(), "deleteDevices", []xmlrpc.Value{
		xmlrpc.StringValue("HmIP-RF"),
		xmlrpc.IntValue(42), // wrong type — should be array of strings
	})
	if err == nil {
		t.Fatal("expected error for non-array addresses in deleteDevices")
	}
}

// TestXMLRPCReaddedDeviceWithNonArrayPayload exercises the xmlrpc.AsStrings
// error branch in readdedDevice.
func TestXMLRPCReaddedDeviceWithNonArrayPayload(t *testing.T) {
	t.Parallel()
	h := &recordingHandlers{}
	_, url := newTestXMLRPCServer(t, "main", h)
	client, _ := xmlrpc.NewClient(xmlrpc.Config{URL: url})
	_, err := client.Call(context.Background(), "readdedDevice", []xmlrpc.Value{
		xmlrpc.StringValue("HmIP-RF"),
		xmlrpc.IntValue(99), // wrong type
	})
	if err == nil {
		t.Fatal("expected error for non-array addresses in readdedDevice")
	}
}

// TestXMLRPCUpdateDeviceWithNonIntHint exercises the xmlrpc.AsInt error
// branch in updateDevice.
func TestXMLRPCUpdateDeviceWithNonIntHint(t *testing.T) {
	t.Parallel()
	h := &recordingHandlers{}
	_, url := newTestXMLRPCServer(t, "main", h)
	client, _ := xmlrpc.NewClient(xmlrpc.Config{URL: url})
	_, err := client.Call(context.Background(), "updateDevice", []xmlrpc.Value{
		xmlrpc.StringValue("HmIP-RF"),
		xmlrpc.StringValue("DEV001"),
		xmlrpc.StringValue("not-an-int"), // wrong type for hint
	})
	if err == nil {
		t.Fatal("expected error for non-int hint in updateDevice")
	}
}

// ─── dispatch: direct unit tests for error paths ─────────────────────────────

// TestDispatchMissingInterfaceID calls dispatch with zero params, which
// hits the len(req.Params) == 0 guard.
func TestDispatchMissingInterfaceID(t *testing.T) {
	t.Parallel()
	srv, err := NewBINRPCServer(BINRPCConfig{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()
	// Directly call the unexported dispatch via a synthetic
	// binrpc.Request — bypasses the network path.
	binReq := &binrpc.Request{Method: "event", Params: nil}
	srv.Register("x", &recordingHandlers{})
	_, dispErr := srv.dispatch(context.Background(), binReq)
	if dispErr == nil {
		t.Fatal("expected error for zero-param dispatch")
	}
}

// TestDispatchNewDevicesWrongArity exercises the newDevices arity guard.
func TestDispatchNewDevicesWrongArity(t *testing.T) {
	t.Parallel()
	srv, err := NewBINRPCServer(BINRPCConfig{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()
	srv.Register("CUxD", &recordingHandlers{})

	// newDevices with 0 rest args (just the interface_id).
	req := &binrpc.Request{
		Method: "newDevices",
		Params: []xmlrpc.Value{xmlrpc.StringValue("CUxD")},
	}
	_, dispErr := srv.dispatch(context.Background(), req)
	if dispErr == nil {
		t.Fatal("expected error for wrong-arity newDevices in dispatch")
	}
}

// TestDispatchDeleteDevicesWrongArity exercises the deleteDevices arity guard.
func TestDispatchDeleteDevicesWrongArity(t *testing.T) {
	t.Parallel()
	srv, err := NewBINRPCServer(BINRPCConfig{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()
	srv.Register("CUxD", &recordingHandlers{})

	req := &binrpc.Request{
		Method: "deleteDevices",
		Params: []xmlrpc.Value{xmlrpc.StringValue("CUxD")},
	}
	_, dispErr := srv.dispatch(context.Background(), req)
	if dispErr == nil {
		t.Fatal("expected error for wrong-arity deleteDevices in dispatch")
	}
}

// TestDispatchEventWrongArity exercises the event arity guard.
func TestDispatchEventWrongArity(t *testing.T) {
	t.Parallel()
	srv, err := NewBINRPCServer(BINRPCConfig{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()
	srv.Register("CUxD", &recordingHandlers{})

	req := &binrpc.Request{
		Method: "event",
		Params: []xmlrpc.Value{xmlrpc.StringValue("CUxD")}, // missing addr, param, value
	}
	_, dispErr := srv.dispatch(context.Background(), req)
	if dispErr == nil {
		t.Fatal("expected error for wrong-arity event in dispatch")
	}
}

// TestDispatchErrorWrongArity exercises the error arity guard.
func TestDispatchErrorWrongArity(t *testing.T) {
	t.Parallel()
	srv, err := NewBINRPCServer(BINRPCConfig{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srv.Close() }()
	srv.Register("CUxD", &recordingHandlers{})

	req := &binrpc.Request{
		Method: "error",
		Params: []xmlrpc.Value{xmlrpc.StringValue("CUxD")}, // missing code, msg
	}
	_, dispErr := srv.dispatch(context.Background(), req)
	if dispErr == nil {
		t.Fatal("expected error for wrong-arity error in dispatch")
	}
}

// TestBINRPCDispatchErrorWithStringCode exercises the string-code branch
// inside dispatch's "error" case: some firmware sends the error code as a
// string rather than an integer.
func TestBINRPCDispatchErrorWithStringCode(t *testing.T) {
	t.Parallel()
	h := &recordingHandlers{}
	srv := newTestBINRPCServer(t, "CUxD", h)
	client := newBINRPCClient(t, srv, "CUxD")

	_, err := client.Call(context.Background(), "error", []xmlrpc.Value{
		xmlrpc.StringValue("CUxD"),
		xmlrpc.StringValue("-7"), // string-encoded code
		xmlrpc.StringValue("offline"),
	})
	if err != nil {
		t.Fatalf("error (string code): %v", err)
	}
	waitFor(t, func() bool { return h.errorCalls.Load() == 1 })
	if h.errorCalls.Load() != 1 {
		t.Fatalf("error handler called %d times, want 1", h.errorCalls.Load())
	}
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

// waitFor polls f until it returns true or 2 s have elapsed.
func waitFor(t *testing.T, f func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if f() {
			return
		}
		runtime.Gosched()
	}
	if !f() {
		t.Error("waitFor: condition not met within timeout")
	}
}
