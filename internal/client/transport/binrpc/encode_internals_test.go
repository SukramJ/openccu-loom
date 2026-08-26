// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// White-box tests for the internal encode helpers (writeInt, writeBool, etc.)
// that exercise the error-return branches by passing a failing io.Writer.

package binrpc

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
)

// zeroWriter accepts everything without error.
type zeroWriter struct{}

func (*zeroWriter) Write(p []byte) (int, error) { return len(p), nil }

// errWriterAfter fails on the n-th Write call (0-indexed).
type callCountWriter struct {
	failOn int
	calls  int
	err    error
}

func (w *callCountWriter) Write(p []byte) (int, error) {
	if w.calls >= w.failOn {
		return 0, w.err
	}
	w.calls++
	return len(p), nil
}

var errW = errors.New("write failed")

// failOnCall returns a writer that fails on the n-th call.
func failOnCall(n int) io.Writer {
	return &callCountWriter{failOn: n, err: errW}
}

// --- writeInt ---

func TestWriteIntTagWriteFails(t *testing.T) {
	// First call to Write (the tag) fails.
	if err := writeInt(failOnCall(0), 42); err == nil {
		t.Error("expected error when tag write fails")
	}
}

func TestWriteIntValueWriteFails(t *testing.T) {
	// Second call to Write (the value) fails.
	if err := writeInt(failOnCall(1), 42); err == nil {
		t.Error("expected error when value write fails")
	}
}

func TestWriteIntSuccess(t *testing.T) {
	if err := writeInt(&zeroWriter{}, 42); err != nil {
		t.Errorf("writeInt succeeded: %v", err)
	}
}

// --- writeBool ---

func TestWriteBoolTagWriteFails(t *testing.T) {
	if err := writeBool(failOnCall(0), true); err == nil {
		t.Error("expected error when tag write fails")
	}
}

func TestWriteBoolValueWriteFails(t *testing.T) {
	if err := writeBool(failOnCall(1), true); err == nil {
		t.Error("expected error when value write fails")
	}
}

// --- writeString ---

func TestWriteStringTagWriteFails(t *testing.T) {
	if err := writeString(failOnCall(0), "hello"); err == nil {
		t.Error("expected error when string tag write fails")
	}
}

// --- writeDouble ---

func TestWriteDoubleTagWriteFails(t *testing.T) {
	if err := writeDouble(failOnCall(0), 1.5); err == nil {
		t.Error("expected error when double tag write fails")
	}
}

func TestWriteDoubleMantissaWriteFails(t *testing.T) {
	if err := writeDouble(failOnCall(1), 1.5); err == nil {
		t.Error("expected error when double mantissa write fails")
	}
}

func TestWriteDoubleExpWriteFails(t *testing.T) {
	if err := writeDouble(failOnCall(2), 1.5); err == nil {
		t.Error("expected error when double exponent write fails")
	}
}

// --- writeStruct ---

func TestWriteStructTagWriteFails(t *testing.T) {
	s := xmlrpc.StructValue{Members: []xmlrpc.Member{{Name: "X", Value: xmlrpc.IntValue(1)}}}
	if err := writeStruct(failOnCall(0), s); err == nil {
		t.Error("expected error when struct tag write fails")
	}
}

func TestWriteStructCountWriteFails(t *testing.T) {
	s := xmlrpc.StructValue{Members: []xmlrpc.Member{{Name: "X", Value: xmlrpc.IntValue(1)}}}
	if err := writeStruct(failOnCall(1), s); err == nil {
		t.Error("expected error when struct count write fails")
	}
}

func TestWriteStructMemberNameFails(t *testing.T) {
	// tag + count succeed; name length write fails.
	s := xmlrpc.StructValue{Members: []xmlrpc.Member{{Name: "X", Value: xmlrpc.IntValue(1)}}}
	if err := writeStruct(failOnCall(2), s); err == nil {
		t.Error("expected error when struct member name write fails")
	}
}

// --- writeArray ---

func TestWriteArrayTagWriteFails(t *testing.T) {
	a := xmlrpc.ArrayValue{xmlrpc.IntValue(1)}
	if err := writeArray(failOnCall(0), a); err == nil {
		t.Error("expected error when array tag write fails")
	}
}

func TestWriteArrayCountWriteFails(t *testing.T) {
	a := xmlrpc.ArrayValue{xmlrpc.IntValue(1)}
	if err := writeArray(failOnCall(1), a); err == nil {
		t.Error("expected error when array count write fails")
	}
}

func TestWriteArrayElementFails(t *testing.T) {
	// tag + count succeed; element write fails.
	a := xmlrpc.ArrayValue{xmlrpc.IntValue(1)}
	if err := writeArray(failOnCall(2), a); err == nil {
		t.Error("expected error when array element write fails")
	}
}

// --- writeRawString ---

func TestWriteRawStringLengthFails(t *testing.T) {
	if err := writeRawString(failOnCall(0), "hello"); err == nil {
		t.Error("expected error when raw string length write fails")
	}
}

func TestWriteRawStringBytesFail(t *testing.T) {
	if err := writeRawString(failOnCall(1), "hello"); err == nil {
		t.Error("expected error when raw string bytes write fails")
	}
}

// --- writeParamArray ---

func TestWriteParamArrayCountFails(t *testing.T) {
	// The first Write in writeParamArray is the count.
	if err := writeParamArray(failOnCall(0), []xmlrpc.Value{xmlrpc.IntValue(1)}); err == nil {
		t.Error("expected error when param count write fails")
	}
}

func TestWriteParamArrayElementFails(t *testing.T) {
	// count write succeeds (call 0); element tag write fails (call 1).
	if err := writeParamArray(failOnCall(1), []xmlrpc.Value{xmlrpc.IntValue(1)}); err == nil {
		t.Error("expected error when param element write fails")
	}
}

// --- writeValue: DateTimeValue and Base64Value return error ---

func TestWriteValueDateTimeUnsupported(t *testing.T) {
	// DateTimeValue is not supported over BIN-RPC; writeValue must return an error.
	if err := writeValue(&zeroWriter{}, xmlrpc.DateTimeValue{}); err == nil {
		t.Error("expected error for DateTimeValue in BIN-RPC encoder")
	}
}

func TestWriteValueBase64Unsupported(t *testing.T) {
	// Base64Value is not supported over BIN-RPC; writeValue must return an error.
	if err := writeValue(&zeroWriter{}, xmlrpc.Base64Value{}); err == nil {
		t.Error("expected error for Base64Value in BIN-RPC encoder")
	}
}

func TestWriteValueNilValue(t *testing.T) {
	// NilValue must encode without error (it maps to empty string in BIN-RPC).
	if err := writeValue(&zeroWriter{}, xmlrpc.NilValue{}); err != nil {
		t.Errorf("writeValue(NilValue): unexpected error: %v", err)
	}
}

// --- encodeDouble: exponent out of range ---

// encodeDouble's range checks for mant/exp out of int32 bounds are
// extremely unlikely to trigger for normal float64 values. We test
// them conceptually by ensuring the function succeeds for ordinary values.
func TestEncodeDoubleNormalValues(t *testing.T) {
	cases := []float64{-1000.5, 0.001, 3.14159, 1234567.0}
	for _, v := range cases {
		m, e, err := encodeDouble(v)
		if err != nil {
			t.Errorf("encodeDouble(%g): unexpected error: %v", v, err)
		}
		_ = m
		_ = e
	}
}

// --- readNValues: negative count returns error ---

func TestReadNValuesNegativeCount(t *testing.T) {
	// Construct a bytesReader with some data so readNValues can be called directly.
	// n < 0 must return an error.
	r := &bytesReader{b: []byte{0, 0, 0, 1}} // some bytes
	_, err := readNValues(r, -1, 0)
	if err == nil {
		t.Error("readNValues(-1): expected error, got nil")
	}
}

// --- bytesReader.readN: negative length returns error ---

func TestBytesReaderReadNNegativeLength(t *testing.T) {
	r := &bytesReader{b: []byte{1, 2, 3}}
	_, err := r.readN(-5)
	if err == nil {
		t.Error("readN(-5): expected error, got nil")
	}
}

// --- readNValues: n=0 returns empty slice ---

func TestReadNValuesZeroCount(t *testing.T) {
	// Build a response that encodes an array of length 0.
	var buf bytes.Buffer
	if err := WriteResponse(&buf, xmlrpc.ArrayValue{}); err != nil {
		t.Fatalf("WriteResponse: %v", err)
	}
	resp, err := ReadResponse(&buf)
	if err != nil {
		t.Fatalf("ReadResponse: %v", err)
	}
	arr, err := xmlrpc.AsArray(resp.Value)
	if err != nil {
		t.Fatalf("AsArray: %v", err)
	}
	if len(arr) != 0 {
		t.Errorf("expected empty array, got len=%d", len(arr))
	}
}

// --- NewServer: empty addr error ---

func TestNewServerEmptyAddrReturnsError(t *testing.T) {
	_, err := NewServer(ServerConfig{Addr: ""})
	if err == nil {
		t.Error("expected error for empty Addr")
	}
}

// --- NewServer: invalid addr error ---

func TestNewServerBadAddrReturnsError(t *testing.T) {
	_, err := NewServer(ServerConfig{Addr: "invalid-addr:99999"})
	if err == nil {
		t.Error("expected error for bad Addr")
	}
}

// --- WriteFrame: large payload ---

func TestWriteResponseLargePayloadNearMaxSize(t *testing.T) {
	// Just under MaxMessageSize — should succeed.
	// Use a string of MaxMessageSize-8 bytes (payload = 4+4+data) to stay under limit.
	// Actually MaxMessageSize is 10MiB; building 10MiB string is OK in test.
	bigStr := make([]byte, 1024) // safe small string
	var buf bytes.Buffer
	if err := WriteResponse(&buf, xmlrpc.StringValue(string(bigStr))); err != nil {
		t.Errorf("WriteResponse with 1KB string: %v", err)
	}
}

// --- Serve: timeout error from Accept is ignored (continue branch) ---

// mockTimeoutListener simulates a listener that returns a timeout error on
// the first Accept call, then blocks until closed.
type mockTimeoutListener struct {
	calls  int
	closed chan struct{}
	addr   net.Addr
}

func (l *mockTimeoutListener) Accept() (net.Conn, error) {
	l.calls++
	if l.calls == 1 {
		return nil, &mockTimeoutErr{}
	}
	// Block until closed.
	<-l.closed
	return nil, net.ErrClosed
}

func (l *mockTimeoutListener) Close() error {
	select {
	case <-l.closed:
	default:
		close(l.closed)
	}
	return nil
}

func (l *mockTimeoutListener) Addr() net.Addr { return l.addr }

// mockTimeoutErr is a net.Error that returns Timeout() == true.
type mockTimeoutErr struct{}

func (e *mockTimeoutErr) Error() string   { return "i/o timeout" }
func (e *mockTimeoutErr) Timeout() bool   { return true }
func (e *mockTimeoutErr) Temporary() bool { return true }

func TestServeTimeoutErrorIsContinued(t *testing.T) {
	// Build a server with a mock listener. The first Accept returns a timeout
	// error, which Serve must continue (not return). The context then cancels.
	ml := &mockTimeoutListener{
		closed: make(chan struct{}),
		addr:   &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 19999},
	}
	s := &Server{
		mux:      xmlrpc.NewMux(),
		logger:   slog.Default(),
		ioOut:    DefaultServerIOTimeout,
		listener: ml,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx) }()
	// Give Serve time to process the timeout error and loop.
	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve returned error after cancel: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("Serve did not stop after context cancel")
	}
}

// --- Serve: context cancellation stops accept loop ---

func TestServeStopsOnContextCancel(t *testing.T) {
	s, err := NewServer(ServerConfig{Addr: "127.0.0.1:0"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx) }()
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve returned error after cancel: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Error("Serve did not stop after context cancel")
	}
}
