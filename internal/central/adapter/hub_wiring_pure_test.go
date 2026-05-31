// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// hub_wiring_pure_test.go covers the pure-logic helpers in
// hub_wiring.go: ccuBaseURLFor, jsonrpcEndpoint, jsonrpcHTTPClient,
// and parseSysvarValue.

package adapter

import (
	"encoding/json"
	"testing"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
)

// ============================================================
// ccuBaseURLFor
// ============================================================

func TestCCUBaseURLForHTTP(t *testing.T) {
	t.Parallel()
	cc := config.CentralConfig{Host: "192.168.1.1"}
	got := ccuBaseURLFor(cc)
	if got != "http://192.168.1.1:80" {
		t.Errorf("ccuBaseURLFor http = %q, want http://192.168.1.1:80", got)
	}
}

func TestCCUBaseURLForHTTPS(t *testing.T) {
	t.Parallel()
	cc := config.CentralConfig{Host: "192.168.1.1", TLS: true}
	got := ccuBaseURLFor(cc)
	if got != "https://192.168.1.1:443" {
		t.Errorf("ccuBaseURLFor https = %q, want https://192.168.1.1:443", got)
	}
}

func TestCCUBaseURLForCustomJSONRPCPort(t *testing.T) {
	t.Parallel()
	cc := config.CentralConfig{Host: "192.168.1.1", JSONRPCPort: 8765}
	got := ccuBaseURLFor(cc)
	if got != "http://192.168.1.1:8765" {
		t.Errorf("ccuBaseURLFor custom port = %q, want http://192.168.1.1:8765", got)
	}
}

func TestCCUBaseURLForHTTPSCustomPort(t *testing.T) {
	t.Parallel()
	cc := config.CentralConfig{Host: "ccu3.local", TLS: true, JSONRPCPort: 9443}
	got := ccuBaseURLFor(cc)
	if got != "https://ccu3.local:9443" {
		t.Errorf("ccuBaseURLFor https+custom port = %q, want https://ccu3.local:9443", got)
	}
}

// ============================================================
// jsonrpcEndpoint
// ============================================================

func TestJSONRPCEndpoint(t *testing.T) {
	t.Parallel()
	cc := config.CentralConfig{Host: "ccu.local"}
	got := jsonrpcEndpoint(cc)
	want := "http://ccu.local:80/api/homematic.cgi"
	if got != want {
		t.Errorf("jsonrpcEndpoint = %q, want %q", got, want)
	}
}

// ============================================================
// jsonrpcHTTPClient
// ============================================================

func TestJSONRPCHTTPClientNoTLS(t *testing.T) {
	t.Parallel()
	cc := config.CentralConfig{Host: "ccu.local"}
	if got := jsonrpcHTTPClient(cc); got != nil {
		t.Errorf("jsonrpcHTTPClient no TLS = %v, want nil", got)
	}
}

func TestJSONRPCHTTPClientTLSNoInsecure(t *testing.T) {
	t.Parallel()
	cc := config.CentralConfig{Host: "ccu.local", TLS: true, TLSInsecureSkipVerify: false}
	if got := jsonrpcHTTPClient(cc); got != nil {
		t.Errorf("jsonrpcHTTPClient TLS no insecure = %v, want nil", got)
	}
}

func TestJSONRPCHTTPClientTLSInsecure(t *testing.T) {
	t.Parallel()
	cc := config.CentralConfig{Host: "ccu.local", TLS: true, TLSInsecureSkipVerify: true}
	got := jsonrpcHTTPClient(cc)
	if got == nil {
		t.Fatal("jsonrpcHTTPClient TLS insecure = nil, want non-nil http.Client")
	}
}

// ============================================================
// parseSysvarValue
// ============================================================

func TestParseSysvarValueEmpty(t *testing.T) {
	t.Parallel()
	_, ok := parseSysvarValue(hmenum.HubValueTypeLogic, nil)
	if ok {
		t.Error("parseSysvarValue empty raw must return false")
	}
}

func TestParseSysvarValueLogicTrue(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`"true"`)
	pv, ok := parseSysvarValue(hmenum.HubValueTypeLogic, raw)
	if !ok {
		t.Fatal("parseSysvarValue logic true must succeed")
	}
	if !pv.Bool {
		t.Errorf("pv.Bool = %v, want true", pv.Bool)
	}
}

func TestParseSysvarValueLogicFalse(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`"false"`)
	pv, ok := parseSysvarValue(hmenum.HubValueTypeLogic, raw)
	if !ok {
		t.Fatal("parseSysvarValue logic false must succeed")
	}
	if pv.Bool {
		t.Errorf("pv.Bool = %v, want false", pv.Bool)
	}
}

func TestParseSysvarValueAlarmTrue(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`"1"`)
	pv, ok := parseSysvarValue(hmenum.HubValueTypeAlarm, raw)
	if !ok {
		t.Fatal("parseSysvarValue alarm 1 must succeed")
	}
	_ = pv
}

func TestParseSysvarValueNumber(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`"42.5"`)
	pv, ok := parseSysvarValue(hmenum.HubValueTypeNumber, raw)
	if !ok {
		t.Fatal("parseSysvarValue number must succeed")
	}
	_ = pv
}

func TestParseSysvarValueFloat(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`"3.14"`)
	pv, ok := parseSysvarValue(hmenum.HubValueTypeFloat, raw)
	if !ok {
		t.Fatal("parseSysvarValue float must succeed")
	}
	_ = pv
}

func TestParseSysvarValueInteger(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`"7"`)
	pv, ok := parseSysvarValue(hmenum.HubValueTypeInteger, raw)
	if !ok {
		t.Fatal("parseSysvarValue integer must succeed")
	}
	_ = pv
}

func TestParseSysvarValueString(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`"hello world"`)
	pv, ok := parseSysvarValue(hmenum.HubValueTypeString, raw)
	if !ok {
		t.Fatal("parseSysvarValue string must succeed")
	}
	_ = pv
}

func TestParseSysvarValueBareNumber(t *testing.T) {
	t.Parallel()
	// bare numeric JSON (no quotes) — falls through to json.Number path
	raw := json.RawMessage(`42`)
	pv, ok := parseSysvarValue(hmenum.HubValueTypeString, raw)
	if !ok {
		t.Fatal("parseSysvarValue bare number must succeed")
	}
	_ = pv
}

func TestParseSysvarValueBareInvalidJSON(t *testing.T) {
	t.Parallel()
	// Invalid JSON → both Unmarshal branches fail → false
	raw := json.RawMessage(`{invalid}`)
	_, ok := parseSysvarValue(hmenum.HubValueTypeLogic, raw)
	if ok {
		t.Error("parseSysvarValue invalid JSON must return false")
	}
}

// ============================================================
// HubAdapter.Hub — nil registry and empty registry
// ============================================================

func TestHubAdapterHubNilRegistry(t *testing.T) {
	t.Parallel()
	a := NewHubAdapter(nil)
	got := a.Hub()
	if got != nil {
		t.Errorf("nil registry Hub() = %v, want nil", got)
	}
}

func TestHubAdapterHubEmptyRegistry(t *testing.T) {
	t.Parallel()
	// Non-nil registry but no centrals registered → len(list) == 0
	reg := central.NewRegistry()
	a := NewHubAdapter(reg)
	got := a.Hub()
	if got != nil {
		t.Errorf("empty registry Hub() = %v, want nil", got)
	}
}

// ============================================================
// EventBridge.onCentralState — nil bus path
// ============================================================

func TestOnCentralStateNilWSHub(t *testing.T) {
	t.Parallel()
	// EventBridge with nil wsHub → onCentralState returns early, no panic.
	b := &EventBridge{}
	e := hmevent.CentralStateChangedEvent{}
	b.onCentralState("ccu-01", e)
}
