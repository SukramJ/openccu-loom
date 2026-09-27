// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package occulited_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client/transport/occulited"
	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
	"github.com/SukramJ/openccu-loom/tests/harness/litefake"
)

// seen is what a recording server saw of one request.
type seen struct {
	method, auth, length, body string
}

// recorder answers every request with status/body and records it.
func recorder(t *testing.T, status int, body string, hdr map[string]string) (srv *httptest.Server, requests func() []seen) {
	t.Helper()
	var mu sync.Mutex
	var got []seen
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = append(got, seen{method: r.Method, auth: r.Header.Get("Authorization"), length: r.Header.Get("Content-Length"), body: string(b)})
		mu.Unlock()
		for k, v := range hdr {
			w.Header().Set(k, v)
		}
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, func() []seen {
		mu.Lock()
		defer mu.Unlock()
		return append([]seen(nil), got...)
	}
}

func TestNewRejectsBadConfig(t *testing.T) {
	for _, cfg := range []occulited.Config{
		{},
		{BaseURL: "ftp://box"},
		{BaseURL: "http://"},
		{BaseURL: "http://box", TLSFingerprint: "abcd"},
	} {
		if _, err := occulited.New(cfg); err == nil {
			t.Errorf("New(%+v) accepted", cfg)
		}
	}
}

func TestXMLRPCURLEscapesTheInterface(t *testing.T) {
	c := newClient(t, "https://box.local/", "")
	if got, want := c.XMLRPCURL("HmIP-RF"), "https://box.local/api/rpc/v1/xmlrpc/HmIP-RF"; got != want {
		t.Errorf("XMLRPCURL %q, want %q", got, want)
	}
	if got := c.XMLRPCURL("a b"); !strings.HasSuffix(got, "/a%20b") {
		t.Errorf("XMLRPCURL %q not escaped", got)
	}
	if c.BaseURL() != "https://box.local" {
		t.Errorf("BaseURL %q", c.BaseURL())
	}
}

// TestTransportSendsBearerAndBodyRule pins the request rules: the bearer
// credential, and "{}" with a Content-Length on every body method that
// has nothing to send, while GET stays bodiless.
func TestTransportSendsBearerAndBodyRule(t *testing.T) {
	srv, got := recorder(t, http.StatusOK, "{}", nil)
	c := newClient(t, srv.URL, "olt_secret")
	hc := c.HTTPClient()
	ctx := context.Background()
	for _, m := range []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		req, _ := http.NewRequestWithContext(ctx, m, srv.URL+"/api/x", http.NoBody)
		resp, err := hc.Do(req)
		if err != nil {
			t.Fatalf("%s: %v", m, err)
		}
		_ = resp.Body.Close()
	}
	// A body of unknown length is buffered to get a length.
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/api/x", io.MultiReader(strings.NewReader(`{"a":1}`)))
	req.ContentLength = -1
	resp, err := hc.Do(req)
	if err != nil {
		t.Fatalf("streamed POST: %v", err)
	}
	_ = resp.Body.Close()

	rows := got()
	if len(rows) != 6 {
		t.Fatalf("%d requests", len(rows))
	}
	for _, r := range rows {
		if r.auth != "Bearer olt_secret" {
			t.Errorf("%s auth %q", r.method, r.auth)
		}
	}
	if rows[0].body != "" || rows[0].length != "" {
		t.Errorf("GET carried a body %q (length %q)", rows[0].body, rows[0].length)
	}
	for _, r := range rows[1:5] {
		if r.body != "{}" || r.length != "2" {
			t.Errorf("%s body %q length %q, want {} / 2", r.method, r.body, r.length)
		}
	}
	if rows[5].body != `{"a":1}` || rows[5].length != "7" {
		t.Errorf("streamed POST body %q length %q", rows[5].body, rows[5].length)
	}
}

func TestTransportWithoutTokenSendsNoCredential(t *testing.T) {
	srv, got := recorder(t, http.StatusOK, "{}", nil)
	c := newClient(t, srv.URL, "")
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, http.NoBody)
	resp, err := c.HTTPClient().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if a := got()[0].auth; a != "" {
		t.Errorf("auth %q, want none", a)
	}
}

// TestTransportClassifiesRefusals pins the answer rules: 503 down and
// starting are connection failures, 401 an auth failure, 403 with a
// scope a missing scope; everything else passes through.
func TestTransportClassifiesRefusals(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		check  func(t *testing.T, err error)
	}{
		{"down", 503, `{"error":"down","message":"x"}`, func(t *testing.T, err error) {
			t.Helper()
			if !errors.Is(err, hmerr.ErrNoConnection) {
				t.Errorf("%v not ErrNoConnection", err)
			}
		}},
		{"starting", 503, `{"error":"starting","message":"x"}`, func(t *testing.T, err error) {
			t.Helper()
			var apiErr *occulited.APIError
			if !errors.Is(err, hmerr.ErrNoConnection) || !errors.As(err, &apiErr) || apiErr.RetryAfter.Seconds() != 5 {
				t.Errorf("%v: not a starting refusal with Retry-After 5", err)
			}
		}},
		{"unauthenticated", 401, `{"error":"unauthenticated","message":"login required"}`, func(t *testing.T, err error) {
			t.Helper()
			if !errors.Is(err, hmerr.ErrAuthFailure) || errors.Is(err, hmerr.ErrNoConnection) {
				t.Errorf("%v not a pure auth failure", err)
			}
		}},
		{"scope", 403, `{"error":"forbidden","message":"the scope power is required","scope":"power"}`, func(t *testing.T, err error) {
			t.Helper()
			var sm *hmerr.ScopeMissingError
			if !errors.As(err, &sm) || sm.Scope != "power" || !errors.Is(err, hmerr.ErrScopeMissing) {
				t.Errorf("%v not ScopeMissing(power)", err)
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, _ := recorder(t, tc.status, tc.body, map[string]string{"Retry-After": "5", "Content-Type": "application/json"})
			c := newClient(t, srv.URL, "t")
			req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, http.NoBody)
			resp, err := c.HTTPClient().Do(req)
			if err == nil {
				_ = resp.Body.Close()
				t.Fatal("no error")
			}
			tc.check(t, err)
		})
	}
}

func TestTransportPassesOtherAnswersThrough(t *testing.T) {
	for _, tc := range []struct {
		status int
		body   string
	}{
		{503, "plain text outage"},
		{503, `{"error":"busy"}`},
		{403, `{"error":"pairing-off","message":"x"}`},
		{404, `{"error":"unknown-object"}`},
	} {
		srv, _ := recorder(t, tc.status, tc.body, nil)
		c := newClient(t, srv.URL, "t")
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, srv.URL, http.NoBody)
		resp, err := c.HTTPClient().Do(req)
		if err != nil {
			t.Errorf("%d %s: %v", tc.status, tc.body, err)
			continue
		}
		b, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != tc.status || string(b) != tc.body {
			t.Errorf("got %d %q, want %d %q", resp.StatusCode, b, tc.status, tc.body)
		}
	}
}

// TestXMLRPCThroughTheTransport drives Loom's XML-RPC client over the
// transport against the fake: a call succeeds, a down interface process
// is a connection failure, a tier refusal classifies as a missing scope.
func TestXMLRPCThroughTheTransport(t *testing.T) {
	f := startFake(t, litefake.Options{Tokens: map[string][]string{"reader": {"rpc:read"}}})
	c := newClient(t, f.URL(), "reader")
	x, err := xmlrpc.NewClient(xmlrpc.Config{URL: c.XMLRPCURL(bidcosRF), HTTPClient: c.HTTPClient(), Interface: bidcosRF})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := x.Call(ctx, "listDevices", nil); err != nil {
		t.Fatalf("listDevices: %v", err)
	}
	_, err = x.Call(ctx, "setValue", []xmlrpc.Value{xmlrpc.StringValue(switchChannel), xmlrpc.StringValue("STATE"), xmlrpc.BoolValue(true)})
	var sm *hmerr.ScopeMissingError
	if err = occulited.ClassifyFault(err); !errors.As(err, &sm) || sm.Scope != "rpc:operate" || sm.Operation != "setValue" {
		t.Errorf("setValue: %v, want ScopeMissing(rpc:operate)", err)
	}
	if err := f.SetInterfaceDown(bidcosRF, true); err != nil {
		t.Fatal(err)
	}
	_, err = x.Call(ctx, "listDevices", nil)
	var apiErr *occulited.APIError
	if !errors.Is(err, hmerr.ErrNoConnection) || !errors.As(err, &apiErr) || apiErr.Code != occulited.CodeDown {
		t.Errorf("down interface: %v", err)
	}
}
