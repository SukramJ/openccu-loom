// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/tests/e2e/harness"
)

const liteCentral = "ccu-e2e"

// problemFeature is the feature member of a feature_unavailable problem.
type problemFeature struct {
	Central string `json:"central"`
	Key     string `json:"key"`
	Reason  string `json:"reason"`
	Scope   string `json:"scope"`
}

// TestLiteFeatureContract pins "absent is explicit" end to end: a daemon
// runs an openccu-lite central whose token holds only the read tier, and
// every operation that needs a feature the central lacks — because the
// system has no such thing, or because the token lacks the scope — is
// answered with 422 feature_unavailable naming the feature, the reason
// and the missing scope, on REST and on WebSocket, while GET /system/ccu
// reports the same feature absent. A route that let the refusal fall
// through to a generic answer fails here.
func TestLiteFeatureContract(t *testing.T) {
	t.Parallel()
	h := harness.Start(t, harness.Options{Backend: harness.BackendOpenCCULite, LiteScopes: []string{"rpc:read"}})
	if err := h.REST().LoginSession(harness.AdminUser, harness.AdminPass); err != nil {
		t.Fatalf("login: %v", err)
	}
	devices := getJSONArray(t, h, "/api/v1/devices", "items")
	if len(devices) == 0 {
		t.Fatal("the lite central loaded no devices; the contract below would test nothing")
	}
	addr, _ := devices[0].(map[string]any)["address"].(string)
	if addr == "" {
		t.Fatalf("first device carries no address: %v", devices[0])
	}

	central := "/api/v1/system/ccu/" + liteCentral
	for _, tc := range []struct {
		method, path, body string
		key, reason, scope string
	}{
		{"POST", central + "/reboot", "", "system.reboot", "missing_scope", "power"},
		{"POST", central + "/poweroff", "", "system.poweroff", "missing_scope", "power"},
		{"POST", central + "/recovery-mode", "", "system.recovery_mode", "missing_scope", "power"},
		{"POST", central + "/safe-mode", "", "system.safe_mode", "not_supported_by_system", ""},
		{"PUT", central + "/position", `{"longitude":10,"latitude":50}`, "system.position", "not_supported_by_system", ""},
		{"POST", "/api/v1/system/firmware/download", `{"central":"` + liteCentral + `"}`, "hub.system_update.install", "missing_scope", "power"},
		{"POST", "/api/v1/backups", `{"central_name":"` + liteCentral + `"}`, "system.backup.create", "missing_scope", "backup"},
		{"GET", "/api/v1/groups?central=" + liteCentral, "", "heating_groups.read", "missing_scope", "system:read"},
		{"POST", "/api/v1/sysvars", `{"name":"Loom Test","value_type":"BOOL"}`, "hub.sysvars", "not_supported_by_system", ""},
		{"POST", "/api/v1/service-messages/ack-all", "", "hub.service_messages.ack", "not_supported_by_system", ""},
		{"POST", "/api/v1/alarm-messages/ack-all", "", "hub.alarm_messages", "not_supported_by_system", ""},
		{"PATCH", "/api/v1/devices/" + addr, `{"name":"Renamed"}`, "device.rename", "missing_scope", "meta:write"},
		{"PATCH", "/api/v1/devices/" + addr, `{"rooms":["Kitchen"]}`, "taxonomy.assign", "missing_scope", "meta:write"},
	} {
		t.Run(tc.method+" "+tc.path+" "+tc.key, func(t *testing.T) {
			var body io.Reader = http.NoBody
			if tc.body != "" {
				body = bytes.NewBufferString(tc.body)
			}
			req, err := h.REST().NewRequest(tc.method, tc.path, body)
			if err != nil {
				t.Fatalf("request: %v", err)
			}
			if tc.body != "" {
				req.Header.Set("Content-Type", "application/json")
			}
			resp, err := h.REST().Do(req)
			if err != nil {
				t.Fatalf("do: %v", err)
			}
			defer resp.Body.Close()
			raw, _ := io.ReadAll(resp.Body)
			var p struct {
				Code    string          `json:"code"`
				Feature *problemFeature `json:"feature"`
			}
			_ = json.Unmarshal(raw, &p)
			if resp.StatusCode != http.StatusUnprocessableEntity || p.Code != "feature_unavailable" || p.Feature == nil {
				t.Fatalf("status %d, body %s; want 422 feature_unavailable", resp.StatusCode, raw)
			}
			want := problemFeature{Central: liteCentral, Key: tc.key, Reason: tc.reason, Scope: tc.scope}
			if *p.Feature != want {
				t.Errorf("feature = %+v, want %+v", *p.Feature, want)
			}
		})
	}

	t.Run("system/ccu reports the absent features", func(t *testing.T) {
		entries := getJSONArray(t, h, "/api/v1/system/ccu", "entries")
		for _, e := range entries {
			m, _ := e.(map[string]any)
			if m["name"] != liteCentral {
				continue
			}
			feats, _ := m["features"].(map[string]any)
			reboot, _ := feats["system.reboot"].(map[string]any)
			if reboot["available"] != false || reboot["reason"] != "missing_scope" || reboot["scope"] != "power" {
				t.Errorf("system.reboot on /system/ccu = %v", reboot)
			}
			sysvars, _ := feats["hub.sysvars"].(map[string]any)
			if sysvars["available"] != false || sysvars["reason"] != "not_supported_by_system" {
				t.Errorf("hub.sysvars on /system/ccu = %v", sysvars)
			}
			return
		}
		t.Fatalf("no /system/ccu entry for %s", liteCentral)
	})

	t.Run("websocket", func(t *testing.T) {
		wsc, err := h.REST().DialWS("/api/v1/events")
		if err != nil {
			t.Fatalf("dial WS: %v", err)
		}
		defer wsc.Close()
		for i, tc := range []struct {
			command string
			args    map[string]any
			key     string
		}{
			{"backup.trigger", map[string]any{"central_name": liteCentral}, "system.backup.create"},
			{"service_messages.ack_all", map[string]any{"central_name": liteCentral}, "hub.service_messages.ack"},
		} {
			res, err := wsc.Call("c"+string(rune('0'+i)), tc.command, tc.args, 10*time.Second)
			if err != nil {
				t.Fatalf("%s: %v", tc.command, err)
			}
			if res.Error == nil || res.Error.Code != "feature_unavailable" {
				t.Errorf("%s: error = %+v, want feature_unavailable", tc.command, res.Error)
				continue
			}
			var d problemFeature
			if err := json.Unmarshal(res.Error.Details, &d); err != nil || d.Key != tc.key || d.Central != liteCentral {
				t.Errorf("%s: details %s, want key %s", tc.command, res.Error.Details, tc.key)
			}
		}
	})
}
