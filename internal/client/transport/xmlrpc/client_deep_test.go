// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package xmlrpc

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// buildFaultXML returns a minimal XML-RPC fault response with the
// given faultCode and faultString. Built by string concatenation so
// the intent stays obvious in test output.
func buildFaultXML(code int, message string) string {
	return `<?xml version="1.0" encoding="ISO-8859-1"?>
<methodResponse><fault><value><struct>
  <member><name>faultCode</name><value><i4>` + itoa(code) + `</i4></value></member>
  <member><name>faultString</name><value><string>` + message + `</string></value></member>
</struct></value></fault></methodResponse>`
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// buildStringResponseXML returns a minimal successful XML-RPC response
// with a single string param.
func buildStringResponseXML(s string) string {
	return `<?xml version="1.0" encoding="ISO-8859-1"?>
<methodResponse><params><param><value><string>` + s + `</string></value></param></params></methodResponse>`
}

// staticXMLServer returns a test server that always responds with status+body.
func staticXMLServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/xml; charset=ISO-8859-1")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
}

// TestClientCallReturnsParsedString verifies that a well-formed
// string response is decoded and returned as StringValue.
func TestClientCallReturnsParsedString(t *testing.T) {
	t.Parallel()
	srv := staticXMLServer(t, http.StatusOK, buildStringResponseXML("hello"))
	defer srv.Close()

	c, err := NewClient(Config{URL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	v, err := c.Call(context.Background(), "echo", []Value{StringValue("hello")})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	s, err := AsString(v)
	if err != nil {
		t.Fatalf("AsString: %v", err)
	}
	if s != "hello" {
		t.Fatalf("got %q, want %q", s, "hello")
	}
}

// TestClientCallReturnsFaultAsError verifies that faultCode and
// faultString are both visible in the returned error.
func TestClientCallReturnsFaultAsError(t *testing.T) {
	t.Parallel()
	srv := staticXMLServer(t, http.StatusOK, buildFaultXML(-1, "boom"))
	defer srv.Close()

	c, _ := NewClient(Config{URL: srv.URL})
	_, err := c.Call(context.Background(), "something", nil)
	if err == nil {
		t.Fatal("expected fault error")
	}

	var fault *hmerr.XMLRPCFault
	if !errors.As(err, &fault) {
		t.Fatalf("want *hmerr.XMLRPCFault in chain, got %T: %v", err, err)
	}
	if fault.Code != -1 {
		t.Errorf("fault code: got %d, want -1", fault.Code)
	}
	if !strings.Contains(fault.Message, "boom") {
		t.Errorf("fault message should contain %q, got %q", "boom", fault.Message)
	}
	// Both code and message appear in the error string.
	errStr := err.Error()
	if !strings.Contains(errStr, "-1") {
		t.Errorf("error string %q should mention fault code -1", errStr)
	}
	if !strings.Contains(errStr, "boom") {
		t.Errorf("error string %q should mention %q", errStr, "boom")
	}
}

// TestClientCallFaultCodeMinus8MapsToDutyCycle verifies that fault
// code -8 (XMLRPCFaultDutyCycle) is surfaced as retryable. This is
// critical for the reliability layer to apply the 40s duty-cycle delay
func TestClientCallFaultCodeMinus8MapsToDutyCycle(t *testing.T) {
	t.Parallel()
	srv := staticXMLServer(t, http.StatusOK, buildFaultXML(-8, "INSUFFICIENT_DUTYCYCLE"))
	defer srv.Close()

	c, _ := NewClient(Config{URL: srv.URL})
	_, err := c.Call(context.Background(), "setValue", nil)
	if err == nil {
		t.Fatal("expected fault error")
	}

	var fault *hmerr.XMLRPCFault
	if !errors.As(err, &fault) {
		t.Fatalf("want *hmerr.XMLRPCFault, got %T", err)
	}
	if fault.Code != -8 {
		t.Fatalf("fault code: got %d, want -8", fault.Code)
	}
	if fault.FaultCode() != hmerr.XMLRPCFaultDutyCycle {
		t.Errorf("FaultCode() = %d, want XMLRPCFaultDutyCycle (%d)", fault.FaultCode(), hmerr.XMLRPCFaultDutyCycle)
	}
	if !fault.IsRetryable() {
		t.Error("duty-cycle fault should be retryable")
	}
	// Must also satisfy ErrClientException for coarse-grained callers.
	if !errors.Is(err, hmerr.ErrClientException) {
		t.Error("fault -8 should satisfy errors.Is(err, ErrClientException)")
	}
}

// TestClientCallFaultCodeMinus10MapsToTransmissionPending verifies
// fault code -10 (XMLRPCFaultTransmissionPending) is surfaced as
// retryable. The retrier applies a fixed 5s delay (SPECIFICATION §8.4).
func TestClientCallFaultCodeMinus10MapsToTransmissionPending(t *testing.T) {
	t.Parallel()
	srv := staticXMLServer(t, http.StatusOK, buildFaultXML(-10, "TRANSMISSION_PENDING"))
	defer srv.Close()

	c, _ := NewClient(Config{URL: srv.URL})
	_, err := c.Call(context.Background(), "setValue", nil)
	if err == nil {
		t.Fatal("expected fault error")
	}

	var fault *hmerr.XMLRPCFault
	if !errors.As(err, &fault) {
		t.Fatalf("want *hmerr.XMLRPCFault, got %T", err)
	}
	if fault.Code != -10 {
		t.Fatalf("fault code: got %d, want -10", fault.Code)
	}
	if fault.FaultCode() != hmerr.XMLRPCFaultTransmissionPending {
		t.Errorf("FaultCode() = %d, want XMLRPCFaultTransmissionPending (%d)", fault.FaultCode(), hmerr.XMLRPCFaultTransmissionPending)
	}
	if !fault.IsRetryable() {
		t.Error("transmission-pending fault should be retryable")
	}
}

// TestClientCallNon2xxStatusReturnsError verifies that a 500 response
// returns an error classifiable as ErrInternalBackendException.
// (This is distinct from the existing TestClientHTTP500 which tests
// the same sentinel — this test focuses on the error being non-nil
// and having the right type classification.)
func TestClientCallNon2xxStatusReturnsError(t *testing.T) {
	t.Parallel()
	srv := staticXMLServer(t, http.StatusInternalServerError, "server blew up")
	defer srv.Close()

	c, _ := NewClient(Config{URL: srv.URL, Interface: "HmIP-RF"})
	_, err := c.Call(context.Background(), "init", nil)
	if err == nil {
		t.Fatal("expected error for HTTP 500")
	}
	if !errors.Is(err, hmerr.ErrInternalBackendException) {
		t.Errorf("want ErrInternalBackendException, got %v", err)
	}
	// The error must carry a Context with the method name.
	ctx, ok := hmerr.ErrorContext(err)
	if !ok {
		t.Fatal("error should carry hmerr.Context")
	}
	if ctx.Method != "init" {
		t.Errorf("Context.Method = %q, want %q", ctx.Method, "init")
	}
	if ctx.Interface != "HmIP-RF" {
		t.Errorf("Context.Interface = %q, want %q", ctx.Interface, "HmIP-RF")
	}
}

// TestClientCallEmptyResponseBodyReturnsError verifies that an empty
// body (with HTTP 200) does not panic and returns a decode error.
func TestClientCallEmptyResponseBodyReturnsError(t *testing.T) {
	t.Parallel()
	srv := staticXMLServer(t, http.StatusOK, "")
	defer srv.Close()

	c, _ := NewClient(Config{URL: srv.URL})
	_, err := c.Call(context.Background(), "ping", nil)
	if err == nil {
		t.Fatal("expected error for empty response body")
	}
}

// TestClientCallContextCancelAborts verifies that a cancelled context
// causes Call to return without hanging. The server blocks until the
// test releases a channel.
func TestClientCallContextCancelAborts(t *testing.T) {
	t.Parallel()
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-block
		_, _ = io.WriteString(w, "")
	}))
	defer srv.Close()
	defer close(block)

	c, _ := NewClient(Config{URL: srv.URL})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	_, err := c.Call(ctx, "longRunning", nil)
	if err == nil {
		t.Fatal("expected error when context is cancelled")
	}
	// Error should be context-related (deadline exceeded or cancelled).
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) && !errors.Is(err, hmerr.ErrNoConnection) {
		t.Logf("got error (any context/no-connection error is fine): %v", err)
	}
}

// TestClientCallNestedStructRoundTrip verifies that a two-level struct
// {a: {b: 1}} is decoded correctly through the network layer.
func TestClientCallNestedStructRoundTrip(t *testing.T) {
	t.Parallel()

	inner := StructValue{Members: []Member{
		{Name: "b", Value: IntValue(1)},
	}}
	outer := StructValue{Members: []Member{
		{Name: "a", Value: inner},
	}}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := &MethodResponse{Params: []Value{outer}}
		var buf bytes.Buffer
		if err := EncodeResponse(&buf, resp); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/xml; charset=ISO-8859-1")
		_, _ = w.Write(buf.Bytes())
	}))
	defer srv.Close()

	c, _ := NewClient(Config{URL: srv.URL})
	v, err := c.Call(context.Background(), "getParams", nil)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	outerStruct, err := AsStruct(v)
	if err != nil {
		t.Fatalf("AsStruct outer: %v", err)
	}
	aVal, ok := outerStruct.Get("a")
	if !ok {
		t.Fatal("outer struct missing key 'a'")
	}
	innerStruct, err := AsStruct(aVal)
	if err != nil {
		t.Fatalf("AsStruct inner: %v", err)
	}
	bVal, err := StructField[IntValue](innerStruct, "b")
	if err != nil {
		t.Fatalf("StructField[IntValue] 'b': %v", err)
	}
	if bVal != 1 {
		t.Errorf("inner b = %d, want 1", bVal)
	}
}

// TestClientCallArrayOfMixedTypesRoundTrip verifies that an array
// containing [int, string, bool, double] is decoded preserving both
// types and order.
func TestClientCallArrayOfMixedTypesRoundTrip(t *testing.T) {
	t.Parallel()

	arr := ArrayValue{
		IntValue(7),
		StringValue("hi"),
		BoolValue(true),
		DoubleValue(3.14),
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		resp := &MethodResponse{Params: []Value{arr}}
		var buf bytes.Buffer
		if err := EncodeResponse(&buf, resp); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/xml; charset=ISO-8859-1")
		_, _ = w.Write(buf.Bytes())
	}))
	defer srv.Close()

	c, _ := NewClient(Config{URL: srv.URL})
	v, err := c.Call(context.Background(), "listValues", nil)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	a, err := AsArray(v)
	if err != nil {
		t.Fatalf("AsArray: %v", err)
	}
	if len(a) != 4 {
		t.Fatalf("array len = %d, want 4", len(a))
	}
	if a[0].Kind() != KindInt {
		t.Errorf("elem[0] kind = %s, want int", a[0].Kind())
	}
	if a[1].Kind() != KindString {
		t.Errorf("elem[1] kind = %s, want string", a[1].Kind())
	}
	if a[2].Kind() != KindBool {
		t.Errorf("elem[2] kind = %s, want boolean", a[2].Kind())
	}
	if a[3].Kind() != KindDouble {
		t.Errorf("elem[3] kind = %s, want double", a[3].Kind())
	}

	n, _ := AsInt(a[0])
	if n != 7 {
		t.Errorf("elem[0] value = %d, want 7", n)
	}
	s, _ := AsString(a[1])
	if s != "hi" {
		t.Errorf("elem[1] value = %q, want %q", s, "hi")
	}
	b, _ := AsBool(a[2])
	if !b {
		t.Error("elem[2] should be true")
	}
	d, _ := AsDouble(a[3])
	if d != 3.14 {
		t.Errorf("elem[3] value = %v, want 3.14", d)
	}
}

// TestClientCallParamsAreSerializedInOrder verifies that parameters
// arrive at the server in the order they were passed to Call.
func TestClientCallParamsAreSerializedInOrder(t *testing.T) {
	t.Parallel()

	var capturedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		capturedBody = body

		// Respond with a minimal success so Call doesn't error.
		resp := &MethodResponse{Params: []Value{NilValue{}}}
		var buf bytes.Buffer
		_ = EncodeResponse(&buf, resp)
		w.Header().Set("Content-Type", "text/xml; charset=ISO-8859-1")
		_, _ = w.Write(buf.Bytes())
	}))
	defer srv.Close()

	c, _ := NewClient(Config{URL: srv.URL})
	_, err := c.Call(context.Background(), "testMethod", []Value{
		StringValue("a"),
		IntValue(42),
	})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}

	bodyStr := string(capturedBody)

	// Both params should appear in the correct order.
	idxStr := strings.Index(bodyStr, "string")
	idxInt := strings.Index(bodyStr, "<int>")
	if idxStr < 0 || idxInt < 0 {
		t.Fatalf("request body missing expected type tags: %s", bodyStr)
	}
	if idxStr > idxInt {
		t.Errorf("string param should appear before int param in request; body:\n%s", bodyStr)
	}

	// Values are present.
	if !strings.Contains(bodyStr, ">a<") {
		t.Errorf("request body should contain string value 'a': %s", bodyStr)
	}
	if !strings.Contains(bodyStr, ">42<") {
		t.Errorf("request body should contain int value 42: %s", bodyStr)
	}
}

// TestClientCallNoAuthHeaderWhenNotConfigured verifies that when no
// credentials are set in Config, the Authorization header is absent.
func TestClientCallNoAuthHeaderWhenNotConfigured(t *testing.T) {
	t.Parallel()

	var sawAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = r.Header.Get("Authorization")
		resp := &MethodResponse{Params: []Value{NilValue{}}}
		var buf bytes.Buffer
		_ = EncodeResponse(&buf, resp)
		w.Header().Set("Content-Type", "text/xml; charset=ISO-8859-1")
		_, _ = w.Write(buf.Bytes())
	}))
	defer srv.Close()

	// No Username set → no Auth header.
	c, _ := NewClient(Config{URL: srv.URL})
	_, err := c.Call(context.Background(), "ping", nil)
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	if sawAuth != "" {
		t.Errorf("expected no Authorization header, got %q", sawAuth)
	}
}

// TestClientWrapErrorIncludesMethodName verifies that any error returned
// from Call carries the method name in its string representation, making
// logs and traces self-identifying.
func TestClientWrapErrorIncludesMethodName(t *testing.T) {
	t.Parallel()
	// Trigger an error via a 500 response so we exercise c.wrap().
	srv := staticXMLServer(t, http.StatusInternalServerError, "")
	defer srv.Close()

	c, _ := NewClient(Config{URL: srv.URL})
	_, err := c.Call(context.Background(), "mySpecialMethod", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "mySpecialMethod") {
		t.Errorf("error %q should mention the method name", err.Error())
	}
}
