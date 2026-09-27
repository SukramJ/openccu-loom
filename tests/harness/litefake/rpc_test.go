// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package litefake_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/client/transport/xmlrpc"
	"github.com/SukramJ/openccu-loom/pkg/hmerr"
	"github.com/SukramJ/openccu-loom/tests/harness/litefake"
)

const (
	readToken    = "olt_11111111111111111111111111111111"
	operateToken = "olt_22222222222222222222222222222222"
	metaToken    = "olt_33333333333333333333333333333333"
)

// TestFakeRefusesMethodsAboveTokenTier pins the per-method tier check:
// a method above the token's tier is a fault -1 naming method and tier
// over HTTP 200, also as an inner multicall call; a token that holds
// the tier passes the same call.
func TestFakeRefusesMethodsAboveTokenTier(t *testing.T) {
	f := startFake(t, litefake.Options{Tokens: map[string][]string{
		readToken:    {"rpc:read"},
		operateToken: {"rpc:operate"},
	}})
	ctx := context.Background()
	reader := rpcClient(t, f, bidcosRF, readToken)
	operator := rpcClient(t, f, bidcosRF, operateToken)

	setValue := []xmlrpc.Value{xmlrpc.StringValue(switchChannel), xmlrpc.StringValue("STATE"), xmlrpc.BoolValue(true)}
	cases := []struct {
		method string
		params []xmlrpc.Value
		want   string
	}{
		{"setValue", setValue, "not permitted: setValue needs rpc:operate"},
		{"putParamset", []xmlrpc.Value{xmlrpc.StringValue(switchChannel), xmlrpc.StringValue("values"), xmlrpc.StructValue{}}, "not permitted: putParamset needs rpc:operate"},
		{"putParamset", []xmlrpc.Value{xmlrpc.StringValue(switchChannel), xmlrpc.StringValue("MASTER"), xmlrpc.StructValue{}}, "not permitted: putParamset needs rpc:configure"},
		{"setInstallMode", []xmlrpc.Value{xmlrpc.BoolValue(true)}, "not permitted: setInstallMode needs rpc:configure"},
		{"logLevel", []xmlrpc.Value{xmlrpc.IntValue(1)}, "not permitted: logLevel needs rpc:configure"},
		{"deleteDevice", []xmlrpc.Value{xmlrpc.StringValue("VCU0000321"), xmlrpc.IntValue(0)}, "not permitted: deleteDevice needs rpc:admin"},
		{"setInstallModeWithWhitelist", nil, "not permitted: setInstallModeWithWhitelist needs rpc:admin"},
	}
	for _, tc := range cases {
		_, err := reader.Call(ctx, tc.method, tc.params)
		fault := asFault(t, err)
		if fault.Code != -1 || fault.Message != tc.want {
			t.Errorf("%s: fault %d %q, want -1 %q", tc.method, fault.Code, fault.Message, tc.want)
		}
	}

	if _, err := reader.Call(ctx, "getValue", []xmlrpc.Value{xmlrpc.StringValue(switchChannel), xmlrpc.StringValue("STATE")}); err != nil {
		t.Errorf("getValue with rpc:read: %v", err)
	}
	if _, err := reader.Call(ctx, "logLevel", nil); err != nil {
		var fault *hmerr.XMLRPCFault
		if errors.As(err, &fault) && fault.Message == "not permitted: logLevel needs rpc:configure" {
			t.Errorf("logLevel without params needs only rpc:read")
		}
	}

	_, err := reader.Call(ctx, "system.multicall", []xmlrpc.Value{multicall(
		xmlrpc.MethodCall{Method: "getVersion"},
		xmlrpc.MethodCall{Method: "setValue", Params: setValue},
	)})
	if fault := asFault(t, err); fault.Message != "not permitted: setValue needs rpc:operate" {
		t.Errorf("multicall inner tier: %q", fault.Message)
	}
	_, err = reader.Call(ctx, "system.multicall", []xmlrpc.Value{xmlrpc.StringValue("not an array")})
	if fault := asFault(t, err); fault.Message != "not permitted: system.multicall needs rpc:admin" {
		t.Errorf("malformed multicall: %q", fault.Message)
	}

	if _, err := operator.Call(ctx, "setValue", setValue); err != nil {
		t.Errorf("setValue with rpc:operate: %v", err)
	}
}

// TestFakeAnswersDownWhenInterfaceMarkedDown pins the outage answer:
// 503 {"error":"down"} as JSON (not a fault), running false in the
// interface list, and service again once the interface is back up.
func TestFakeAnswersDownWhenInterfaceMarkedDown(t *testing.T) {
	f := startFake(t, litefake.Options{})
	if err := f.SetInterfaceDown(hmipRF, true); err != nil {
		t.Fatal(err)
	}

	resp, body := rawRPC(t, f, hmipRF, litefake.DefaultToken, "getVersion")
	if resp.StatusCode != http.StatusServiceUnavailable || errorCode(t, body) != "down" {
		t.Fatalf("down interface: %d %s", resp.StatusCode, body)
	}
	_, err := rpcClient(t, f, hmipRF, litefake.DefaultToken).Call(context.Background(), "getVersion", nil)
	if !errors.Is(err, hmerr.ErrInternalBackendException) {
		t.Errorf("Loom client on 503: %v", err)
	}

	resp, body = get(t, f, "/api/rpc/v1/interfaces", litefake.DefaultToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("interfaces: %d", resp.StatusCode)
	}
	var list []struct {
		Name     string `json:"name"`
		Protocol string `json:"protocol"`
		URLPath  string `json:"url_path"`
		Running  bool   `json:"running"`
	}
	if err := json.Unmarshal(body, &list); err != nil {
		t.Fatal(err)
	}
	want := []string{bidcosRF, hmipRF, "VirtualDevices"}
	if len(list) != len(want) {
		t.Fatalf("interfaces %s", body)
	}
	for i, row := range list {
		if row.Name != want[i] || row.Protocol != "xmlrpc" || row.URLPath != "/api/rpc/v1/xmlrpc/"+want[i] {
			t.Errorf("row %d: %+v", i, row)
		}
		if row.Running == (row.Name == hmipRF) {
			t.Errorf("%s running=%v", row.Name, row.Running)
		}
	}

	if err := f.SetInterfaceDown(hmipRF, false); err != nil {
		t.Fatal(err)
	}
	if resp, body := rawRPC(t, f, hmipRF, litefake.DefaultToken, "getVersion"); resp.StatusCode != http.StatusOK {
		t.Errorf("after up: %d %s", resp.StatusCode, body)
	}
}

// TestFakeRejectsUnknownInterfaceAndBadCalls pins the proxy's request
// errors: an interface name matched case-sensitively, and a body that
// is not a methodCall.
func TestFakeRejectsUnknownInterfaceAndBadCalls(t *testing.T) {
	f := startFake(t, litefake.Options{})
	resp, body := rawRPC(t, f, "hmip-rf", litefake.DefaultToken, "getVersion")
	if resp.StatusCode != http.StatusNotFound || errorCode(t, body) != "unknown-interface" {
		t.Errorf("wrong-case interface: %d %s", resp.StatusCode, body)
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		f.URL()+"/api/rpc/v1/xmlrpc/"+hmipRF, http.NoBody)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+litefake.DefaultToken)
	r, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = r.Body.Close()
	if r.StatusCode != http.StatusBadRequest {
		t.Errorf("empty body: %d, want 400", r.StatusCode)
	}
}

// TestFakeServesKeptValuesOnState pins /api/rpc/v1/state: a kept
// datapoint reported by a device appears as a confirmed entry, and
// event_id is a stream position of the current boot.
func TestFakeServesKeptValuesOnState(t *testing.T) {
	f := startFake(t, litefake.Options{})
	fireSwitch(t, f, true)

	type stateAnswer struct {
		Entries []struct {
			Interface string          `json:"interface"`
			Address   string          `json:"address"`
			Datapoint string          `json:"datapoint"`
			Value     json.RawMessage `json:"value"`
			Confirmed bool            `json:"confirmed"`
			Source    string          `json:"source"`
		} `json:"entries"`
		Total       int    `json:"total"`
		Unconfirmed int    `json:"unconfirmed"`
		EventID     string `json:"event_id"`
	}
	var ans stateAnswer
	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, body := get(t, f, "/api/rpc/v1/state?address=VCU0000321&key=STATE", litefake.DefaultToken)
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("state: %d %s", resp.StatusCode, body)
		}
		ans = decode[stateAnswer](t, string(body))
		if ans.Total > 0 || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if ans.Total != 1 {
		t.Fatalf("entries %+v", ans)
	}
	e := ans.Entries[0]
	if e.Interface != bidcosRF || e.Address != switchChannel || string(e.Value) != "true" || !e.Confirmed || e.Source != "event" {
		t.Errorf("entry %+v", e)
	}
	if len(ans.EventID) < 18 || ans.EventID[:16] != f.BootID() {
		t.Errorf("event_id %q, boot %q", ans.EventID, f.BootID())
	}

	if err := f.RestartBoot(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, body := get(t, f, "/api/rpc/v1/state?key=STATE", litefake.DefaultToken)
	ans = decode[stateAnswer](t, string(body))
	if ans.Unconfirmed != 1 || ans.Entries[0].Source != "restored" || ans.Entries[0].Confirmed {
		t.Errorf("after restart: %+v", ans)
	}
}
