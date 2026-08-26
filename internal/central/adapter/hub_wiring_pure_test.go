// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

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
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
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

// TestParseSysvarValueLogicBareBool is the regression guard for the bare
// JSON boolean shape: godevccu's SysVar.getAll can return {"value":true}
// rather than the quoted-string shape every other case in this file
// exercises. The string-unmarshal fast path fails on that shape, and the
// pre-fix fallback only tried json.Number — which also fails on `true` —
// so the sysvar was recorded with no value at all.
func TestParseSysvarValueLogicBareBool(t *testing.T) {
	t.Parallel()
	cases := []struct {
		raw  string
		want bool
	}{
		{`true`, true},
		{`false`, false},
	}
	for _, tc := range cases {
		pv, ok := parseSysvarValue(hmenum.HubValueTypeLogic, json.RawMessage(tc.raw))
		if !ok {
			t.Fatalf("parseSysvarValue(LOGIC, %s) ok=false, want true", tc.raw)
		}
		if pv.Kind != hmtypes.ValueKindBool || pv.Bool != tc.want {
			t.Errorf("parseSysvarValue(LOGIC, %s) = %+v, want bool %v", tc.raw, pv, tc.want)
		}
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

// A boolean-typed variable whose payload is not a boolean records NO
// value. Falling back to the string kind gives the data point a kind no
// downstream bool dispatch matches and puts that raw token on the state
// topic of an entity declared with payload_on / payload_off, where it
// matches neither and the entity stays unknown. The CCU makes this
// reachable in normal operation: it reports an empty value for every
// ALARM variable.
func TestParseSysvarValueBooleanTypesRejectNonBooleanPayload(t *testing.T) {
	t.Parallel()
	for _, vt := range []hmenum.HubValueType{hmenum.HubValueTypeLogic, hmenum.HubValueTypeAlarm} {
		for _, raw := range []string{`""`, `"ausgelöst"`, `"2"`} {
			pv, ok := parseSysvarValue(vt, json.RawMessage(raw))
			if ok {
				t.Errorf("parseSysvarValue(%s, %s) = %#v, ok=true; want no value", vt, raw, pv)
			}
		}
	}
}

// The numeric assertions below pin the ParamValue KIND, not just the
// parse success: the CCU delivers every sysvar value as a quoted
// string, and a value that stays ValueKindString silently breaks every
// downstream type dispatch — most visibly the LIST index→label mapping
// on the MQTT state topic (sysvarStateForMQTT), where HA then rejects
// the raw index against the discovery's enum options.

func TestParseSysvarValueNumber(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`"42.5"`)
	pv, ok := parseSysvarValue(hmenum.HubValueTypeNumber, raw)
	if !ok {
		t.Fatal("parseSysvarValue number must succeed")
	}
	if pv.Kind != hmtypes.ValueKindFloat || pv.Float != 42.5 {
		t.Errorf("number value = %+v, want float 42.5", pv)
	}
}

func TestParseSysvarValueFloat(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`"3.14"`)
	pv, ok := parseSysvarValue(hmenum.HubValueTypeFloat, raw)
	if !ok {
		t.Fatal("parseSysvarValue float must succeed")
	}
	if pv.Kind != hmtypes.ValueKindFloat || pv.Float != 3.14 {
		t.Errorf("float value = %+v, want float 3.14", pv)
	}
}

func TestParseSysvarValueInteger(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`"7"`)
	pv, ok := parseSysvarValue(hmenum.HubValueTypeInteger, raw)
	if !ok {
		t.Fatal("parseSysvarValue integer must succeed")
	}
	if pv.Kind != hmtypes.ValueKindInt || pv.Int != 7 {
		t.Errorf("integer value = %+v, want int 7", pv)
	}
}

func TestParseSysvarValueListIndexIsInt(t *testing.T) {
	t.Parallel()
	// LIST sysvars report the zero-based index into the value list as a
	// quoted string. It must parse to an int so the publish path can
	// resolve it to its label.
	raw := json.RawMessage(`"0"`)
	pv, ok := parseSysvarValue(hmenum.HubValueTypeList, raw)
	if !ok {
		t.Fatal("parseSysvarValue list must succeed")
	}
	if pv.Kind != hmtypes.ValueKindInt || pv.Int != 0 {
		t.Errorf("list value = %+v, want int 0", pv)
	}
}

func TestParseSysvarValueListBareIndex(t *testing.T) {
	t.Parallel()
	// Bare numeric JSON (no quotes) rides the json.Number fallback.
	raw := json.RawMessage(`2`)
	pv, ok := parseSysvarValue(hmenum.HubValueTypeList, raw)
	if !ok {
		t.Fatal("parseSysvarValue bare list index must succeed")
	}
	if pv.Kind != hmtypes.ValueKindInt || pv.Int != 2 {
		t.Errorf("list value = %+v, want int 2", pv)
	}
}

func TestParseSysvarValueListNonNumericFallsBackToString(t *testing.T) {
	t.Parallel()
	// A non-numeric LIST payload degrades to the string fallback so the
	// caller still observes something instead of dropping the update.
	raw := json.RawMessage(`"garbled"`)
	pv, ok := parseSysvarValue(hmenum.HubValueTypeList, raw)
	if !ok {
		t.Fatal("parseSysvarValue non-numeric list must still succeed")
	}
	if pv.Kind != hmtypes.ValueKindString || pv.String != "garbled" {
		t.Errorf("list fallback value = %+v, want string \"garbled\"", pv)
	}
}

func TestParseSysvarValueString(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`"hello world"`)
	pv, ok := parseSysvarValue(hmenum.HubValueTypeString, raw)
	if !ok {
		t.Fatal("parseSysvarValue string must succeed")
	}
	if pv.Kind != hmtypes.ValueKindString || pv.String != "hello world" {
		t.Errorf("string value = %+v, want string \"hello world\"", pv)
	}
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
