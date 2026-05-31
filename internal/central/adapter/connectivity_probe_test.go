// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package adapter

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/client/transport/jsonrpc"
)

// JSONRPCConnectivityProbe wraps `Interface.listInterfaces` and hands its
// result to [coordinators.Reconciler].

func newProbeServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var env struct {
			Method string `json:"method"`
		}
		_ = json.Unmarshal(raw, &env)
		if env.Method != "Interface.listInterfaces" {
			http.Error(w, "unexpected method "+env.Method, 404)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestJSONRPCConnectivityProbeMapsNames(t *testing.T) {
	srv := newProbeServer(t, `{"result":[
		{"name":"BidCos-RF"},
		{"name":"HmIP-RF"},
		{"name":"VirtualDevices"}
	]}`)
	jc, _ := jsonrpc.New(jsonrpc.Config{Endpoint: srv.URL})
	probe := NewJSONRPCConnectivityProbe(jc)

	got, err := probe.Probe(context.Background())
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 interfaces, got %d: %+v", len(got), got)
	}
	for _, ir := range got {
		if !ir.Reachable {
			t.Errorf("default reachable should be true, got %+v", ir)
		}
	}
	if got[0].InterfaceID != "BidCos-RF" || got[2].InterfaceID != "VirtualDevices" {
		t.Fatalf("ordering = %+v", got)
	}
}

func TestJSONRPCConnectivityProbeHonorsConnectedField(t *testing.T) {
	srv := newProbeServer(t, `{"result":[
		{"name":"BidCos-RF","connected":true},
		{"name":"HmIP-RF","connected":false}
	]}`)
	jc, _ := jsonrpc.New(jsonrpc.Config{Endpoint: srv.URL})
	probe := NewJSONRPCConnectivityProbe(jc)

	got, err := probe.Probe(context.Background())
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(got) != 2 || got[0].Reachable != true || got[1].Reachable != false {
		t.Fatalf("connected mapping wrong: %+v", got)
	}
}

func TestJSONRPCConnectivityProbeSkipsAnonymousEntries(t *testing.T) {
	srv := newProbeServer(t, `{"result":[
		{"name":"BidCos-RF"},
		{"info":"missing name"}
	]}`)
	jc, _ := jsonrpc.New(jsonrpc.Config{Endpoint: srv.URL})
	probe := NewJSONRPCConnectivityProbe(jc)

	got, err := probe.Probe(context.Background())
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if len(got) != 1 || got[0].InterfaceID != "BidCos-RF" {
		t.Fatalf("anonymous entry leaked: %+v", got)
	}
}

func TestJSONRPCConnectivityProbeNilClient(t *testing.T) {
	probe := NewJSONRPCConnectivityProbe(nil)
	if _, err := probe.Probe(context.Background()); err == nil {
		t.Fatalf("expected error for nil client")
	}
}
