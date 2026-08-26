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
	"sync/atomic"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/pkg/hmerr"
)

// newStaticServer wraps a fixed response body for tests that do not care
// about the request content.
func newStaticServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/xml; charset=ISO-8859-1")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
}

// echoServer decodes the incoming call and sends a response built by fn.
func echoServer(t *testing.T, fn func(*MethodCall) *MethodResponse) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		call, err := DecodeCall(bytes.NewReader(raw))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		resp := fn(call)
		var body bytes.Buffer
		if err := EncodeResponse(&body, resp); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/xml; charset=ISO-8859-1")
		_, _ = w.Write(body.Bytes())
	}))
}

func TestClientHappyPath(t *testing.T) {
	srv := echoServer(t, func(c *MethodCall) *MethodResponse {
		if c.Method != "getValue" {
			return &MethodResponse{Fault: &hmerr.XMLRPCFault{Code: -1, Message: "wrong method"}}
		}
		return &MethodResponse{Params: []Value{IntValue(42)}}
	})
	defer srv.Close()

	c, err := NewClient(Config{URL: srv.URL, Interface: "HmIP-RF"})
	if err != nil {
		t.Fatal(err)
	}
	v, err := c.Call(context.Background(), "getValue", []Value{StringValue("ABC:1"), StringValue("LEVEL")})
	if err != nil {
		t.Fatalf("Call: %v", err)
	}
	n, err := AsInt(v)
	if err != nil || n != 42 {
		t.Fatalf("result=%v err=%v", v, err)
	}
}

func TestClientFaultMapsToXMLRPCFault(t *testing.T) {
	srv := echoServer(t, func(*MethodCall) *MethodResponse {
		return &MethodResponse{Fault: &hmerr.XMLRPCFault{Code: -3, Message: "unknown device"}}
	})
	defer srv.Close()

	c, _ := NewClient(Config{URL: srv.URL})
	_, err := c.Call(context.Background(), "setValue", nil)
	if err == nil {
		t.Fatal("expected error")
	}
	var fault *hmerr.XMLRPCFault
	if !errors.As(err, &fault) {
		t.Fatalf("want *hmerr.XMLRPCFault, got %T", err)
	}
	if fault.Code != -3 || fault.Message != "unknown device" {
		t.Fatalf("fault=%+v", fault)
	}
	if !errors.Is(err, hmerr.ErrClientException) {
		t.Error("fault should classify as ErrClientException")
	}
	ctx, ok := hmerr.ErrorContext(err)
	if !ok || ctx.Protocol != "xml-rpc" || ctx.Method != "setValue" {
		t.Fatalf("context=%+v", ctx)
	}
}

func TestClientHTTP401MapsToAuthFailure(t *testing.T) {
	srv := newStaticServer(t, http.StatusUnauthorized, "")
	defer srv.Close()

	c, _ := NewClient(Config{URL: srv.URL})
	_, err := c.Call(context.Background(), "init", nil)
	if !errors.Is(err, hmerr.ErrAuthFailure) {
		t.Fatalf("got %v, want ErrAuthFailure", err)
	}
}

func TestClientHTTP500MapsToInternalBackend(t *testing.T) {
	srv := newStaticServer(t, http.StatusInternalServerError, "")
	defer srv.Close()

	c, _ := NewClient(Config{URL: srv.URL})
	_, err := c.Call(context.Background(), "init", nil)
	if !errors.Is(err, hmerr.ErrInternalBackendException) {
		t.Fatalf("got %v, want ErrInternalBackendException", err)
	}
}

func TestClientHTTP404MapsToClientException(t *testing.T) {
	srv := newStaticServer(t, http.StatusNotFound, "")
	defer srv.Close()

	c, _ := NewClient(Config{URL: srv.URL})
	_, err := c.Call(context.Background(), "noop", nil)
	if !errors.Is(err, hmerr.ErrClientException) {
		t.Fatalf("got %v, want ErrClientException", err)
	}
}

func TestClientNoConnection(t *testing.T) {
	// Reserved port; connection refused.
	c, _ := NewClient(Config{URL: "http://127.0.0.1:1/"})
	_, err := c.Call(context.Background(), "ping", nil)
	if !errors.Is(err, hmerr.ErrNoConnection) {
		t.Fatalf("got %v, want ErrNoConnection", err)
	}
}

func TestClientRespectsContextCancellation(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		<-block
	}))
	defer srv.Close()
	defer close(block)

	c, _ := NewClient(Config{URL: srv.URL})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := c.Call(ctx, "ping", nil)
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestClientResponseSizeLimit(t *testing.T) {
	// 1024 bytes of XML inside a string value should exceed a 64-byte limit.
	big := strings.Repeat("x", 2048)
	srv := echoServer(t, func(*MethodCall) *MethodResponse {
		return &MethodResponse{Params: []Value{StringValue(big)}}
	})
	defer srv.Close()

	c, _ := NewClient(Config{URL: srv.URL, ResponseLimit: 64})
	_, err := c.Call(context.Background(), "big", nil)
	if err == nil {
		t.Fatal("expected size-limit error")
	}
	if !errors.Is(err, hmerr.ErrClientException) {
		t.Fatalf("size-limit error should classify as ErrClientException, got %v", err)
	}
}

func TestClientSendsBasicAuth(t *testing.T) {
	var gotUser atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok {
			gotUser.Store("<none>")
		} else {
			gotUser.Store(u + ":" + p)
		}
		mr := &MethodResponse{Params: []Value{StringValue("ok")}}
		var buf bytes.Buffer
		_ = EncodeResponse(&buf, mr)
		w.Header().Set("Content-Type", "text/xml; charset=ISO-8859-1")
		_, _ = w.Write(buf.Bytes())
	}))
	defer srv.Close()

	c, _ := NewClient(Config{URL: srv.URL, Username: "alice", Password: "sekret"})
	if _, err := c.Call(context.Background(), "ping", nil); err != nil {
		t.Fatal(err)
	}
	if gotUser.Load() != "alice:sekret" {
		t.Fatalf("got auth=%v", gotUser.Load())
	}
}

func TestClientEmptyURLRejected(t *testing.T) {
	if _, err := NewClient(Config{}); err == nil || !strings.Contains(err.Error(), "URL") {
		t.Fatalf("got %v, want URL error", err)
	}
}
