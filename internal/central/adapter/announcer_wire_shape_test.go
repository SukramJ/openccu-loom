// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

// announcer_wire_shape_test.go pins the exact init/deinit calls the daemon
// puts on the wire, for both transports.
//
// Deregistration is the reason this file exists. The CCU keys it on the
// callback URL alone: `init(url)` with the second parameter omitted removes
// the registration made for url. The daemon used to send the inverse —
// `init("", interface_id)` — which no backend reads as a deregistration.
// rfd accepts it as a *registration* of the empty URL and then reports
// `XmlRpcClient error calling event(...) on uds://:/RPC2` once per keepalive
// until the CCU restarts, while the real registration stays live.
//
// Verified against rfd (BidCos-RF) and CUxD on a live CCU: after
// `init("", id)` the PONGs kept arriving; after `init(url)` they stopped.
//
// Asserting the param COUNT is the point. A test that only checked "deinit
// calls init" passed throughout the entire period the bug existed.

package adapter

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client/transport/binrpc"
	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
)

const (
	announcerTestCallbackURL = "http://10.1.2.3:8120/RPC2/ccu"
	announcerTestInitID      = "loom-ccu-BidCos-RF"
)

// recordedCall is one observed wire call: the method plus its params
// rendered as strings, so a test can assert both arity and content.
type recordedCall struct {
	method string
	params []string
}

// --- XML-RPC ----------------------------------------------------------------

// fakeXMLRPCCCU is an XML-RPC endpoint that records every call and answers
// with an empty response, standing in for rfd.
type fakeXMLRPCCCU struct {
	mu    sync.Mutex
	calls []recordedCall
	srv   *httptest.Server
}

func newFakeXMLRPCCCU(t *testing.T) *fakeXMLRPCCCU {
	t.Helper()
	f := &fakeXMLRPCCCU{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		f.mu.Lock()
		f.calls = append(f.calls, parseXMLRPCCall(string(body)))
		f.mu.Unlock()
		w.Header().Set("Content-Type", "text/xml")
		_, _ = w.Write([]byte(`<?xml version="1.0"?><methodResponse><params><param>` +
			`<value><string></string></value></param></params></methodResponse>`))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

// parseXMLRPCCall pulls the method name and the <param> values out of a
// request body. A deliberately literal reader: the assertion is about how
// many <param> elements the daemon emitted, so decoding through the
// project's own encoder would beg the question.
func parseXMLRPCCall(body string) recordedCall {
	out := recordedCall{method: between(body, "<methodName>", "</methodName>")}
	rest := body
	for {
		idx := strings.Index(rest, "<param>")
		if idx < 0 {
			return out
		}
		rest = rest[idx+len("<param>"):]
		end := strings.Index(rest, "</param>")
		if end < 0 {
			return out
		}
		out.params = append(out.params, stripValueTags(rest[:end]))
		rest = rest[end:]
	}
}

func between(s, openTag, closeTag string) string {
	i := strings.Index(s, openTag)
	if i < 0 {
		return ""
	}
	s = s[i+len(openTag):]
	j := strings.Index(s, closeTag)
	if j < 0 {
		return ""
	}
	return s[:j]
}

func stripValueTags(s string) string {
	for _, tag := range []string{"<value>", "</value>", "<string>", "</string>"} {
		s = strings.ReplaceAll(s, tag, "")
	}
	return strings.TrimSpace(s)
}

func (f *fakeXMLRPCCCU) recorded() []recordedCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]recordedCall, len(f.calls))
	copy(out, f.calls)
	return out
}

// --- BIN-RPC ----------------------------------------------------------------

// fakeBINRPCCUxD is a BIN-RPC endpoint that records every call, standing in
// for CUxD.
type fakeBINRPCCUxD struct {
	mu    sync.Mutex
	calls []recordedCall
	addr  string
}

func newFakeBINRPCCUxD(t *testing.T) *fakeBINRPCCUxD {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	f := &fakeBINRPCCUxD{addr: ln.Addr().String()}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go f.handle(conn)
		}
	}()
	return f
}

func (f *fakeBINRPCCUxD) handle(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	req, err := binrpc.ReadRequest(io.LimitReader(conn, binrpc.MaxMessageSize+8))
	if err != nil {
		return
	}
	rec := recordedCall{method: req.Method}
	for _, p := range req.Params {
		s, _ := xmlrpc.AsString(p)
		rec.params = append(rec.params, s)
	}
	f.mu.Lock()
	f.calls = append(f.calls, rec)
	f.mu.Unlock()

	var buf []byte
	_ = binrpc.WriteResponse(writerFunc(func(p []byte) (int, error) {
		buf = append(buf, p...)
		return len(p), nil
	}), xmlrpc.StringValue(""))
	_, _ = conn.Write(buf)
}

type writerFunc func([]byte) (int, error)

func (w writerFunc) Write(p []byte) (int, error) { return w(p) }

func (f *fakeBINRPCCUxD) recorded() []recordedCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]recordedCall, len(f.calls))
	copy(out, f.calls)
	return out
}

// --- the guards -------------------------------------------------------------

// assertInitDeinitShape holds the shared contract: init carries (url, id),
// deinit carries the url alone.
func assertInitDeinitShape(t *testing.T, transport string, calls []recordedCall) {
	t.Helper()
	if len(calls) != 2 {
		t.Fatalf("%s: want 2 wire calls (init, deinit), got %d: %+v", transport, len(calls), calls)
	}
	initCall, deinitCall := calls[0], calls[1]

	if initCall.method != "init" || len(initCall.params) != 2 {
		t.Fatalf("%s init: want init with 2 params, got %s/%d: %+v",
			transport, initCall.method, len(initCall.params), initCall)
	}
	if initCall.params[0] != announcerTestCallbackURL || initCall.params[1] != announcerTestInitID {
		t.Errorf("%s init params = %v, want [%q %q]",
			transport, initCall.params, announcerTestCallbackURL, announcerTestInitID)
	}

	if deinitCall.method != "init" {
		t.Fatalf("%s deinit: want method init, got %q", transport, deinitCall.method)
	}
	if len(deinitCall.params) != 1 {
		t.Fatalf("%s deinit: want exactly 1 param (the callback URL, second omitted), got %d: %v — "+
			"sending the interface id instead registers the empty URL and the CCU keeps it forever",
			transport, len(deinitCall.params), deinitCall.params)
	}
	if deinitCall.params[0] != announcerTestCallbackURL {
		t.Errorf("%s deinit param = %q, want the callback URL %q",
			transport, deinitCall.params[0], announcerTestCallbackURL)
	}
	if deinitCall.params[0] == "" {
		t.Errorf("%s deinit sent an empty URL — that is a registration, not a deregistration", transport)
	}
}

// TestXMLRPCAnnouncerDeinitSendsURLOnly pins the rfd-facing wire shape.
func TestXMLRPCAnnouncerDeinitSendsURLOnly(t *testing.T) {
	t.Parallel()

	ccu := newFakeXMLRPCCCU(t)
	client, err := xmlrpc.NewClient(xmlrpc.Config{URL: ccu.srv.URL, Interface: "BidCos-RF"})
	if err != nil {
		t.Fatalf("xmlrpc.NewClient: %v", err)
	}
	a := newXMLRPCAnnouncer(client)
	ctx := context.Background()

	if err := a.Init(ctx, announcerTestInitID, announcerTestCallbackURL); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := a.Deinit(ctx, announcerTestCallbackURL); err != nil {
		t.Fatalf("Deinit: %v", err)
	}
	assertInitDeinitShape(t, "xml-rpc", ccu.recorded())
}

// TestBINRPCAnnouncerDeinitSendsURLOnly pins the CUxD-facing wire shape.
func TestBINRPCAnnouncerDeinitSendsURLOnly(t *testing.T) {
	t.Parallel()

	cuxd := newFakeBINRPCCUxD(t)
	client, err := binrpc.NewClient(binrpc.Config{Addr: cuxd.addr, Interface: "CUxD"})
	if err != nil {
		t.Fatalf("binrpc.NewClient: %v", err)
	}
	a := newBINRPCAnnouncer(client)
	ctx := context.Background()

	if err := a.Init(ctx, announcerTestInitID, announcerTestCallbackURL); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := a.Deinit(ctx, announcerTestCallbackURL); err != nil {
		t.Fatalf("Deinit: %v", err)
	}
	assertInitDeinitShape(t, "bin-rpc", cuxd.recorded())
}
