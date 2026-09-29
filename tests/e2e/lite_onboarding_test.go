// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/SukramJ/godevccu/pkg/litefake"

	"github.com/SukramJ/openccu-loom/tests/e2e/harness"
)

const pairedCentral = "paired-e2e"

// TestLiteOnboardingPairsAndAdoptsABox pins onboarding end to end against
// the built daemon: an operator probes an openccu-lite box, pairs with it,
// the box's administrator approves the code, and a central created with
// system type "auto" and the pairing's id comes up ready on the token the
// pairing yielded, resolves and persists its system type — while no answer
// the operator received carried that token.
func TestLiteOnboardingPairsAndAdoptsABox(t *testing.T) {
	t.Parallel()
	h := harness.Start(t, harness.Options{})
	if err := h.REST().LoginSession(harness.AdminUser, harness.AdminPass); err != nil {
		t.Fatalf("login: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// The box starts with no token the daemon could know: pairing is the
	// only way in.
	box, err := litefake.Start(ctx, litefake.Options{Tokens: map[string][]string{}})
	if err != nil {
		t.Fatalf("litefake.Start: %v", err)
	}
	t.Cleanup(func() { _ = box.Close() })
	u, _ := url.Parse(box.URL())
	host, portStr, _ := net.SplitHostPort(u.Host)
	port, _ := strconv.Atoi(portStr)
	target := `"host":"` + host + `","port":` + portStr

	var bodies []string
	call := func(method, path, body string, want int) []byte {
		t.Helper()
		var rd io.Reader = http.NoBody
		if body != "" {
			rd = bytes.NewBufferString(body)
		}
		req, err := h.REST().NewRequest(method, path, rd)
		if err != nil {
			t.Fatalf("request: %v", err)
		}
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := h.REST().Do(req)
		if err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		bodies = append(bodies, string(b))
		if resp.StatusCode != want {
			t.Fatalf("%s %s: status %d, want %d, body %s", method, path, resp.StatusCode, want, b)
		}
		return b
	}

	var probe struct {
		SystemType string `json:"system_type"`
		Ready      bool   `json:"ready"`
		Lite       *struct {
			PairingAvailable bool `json:"pairing_available"`
		} `json:"lite"`
	}
	_ = json.Unmarshal(call(http.MethodPost, "/api/v1/centrals/probe", "{"+target+"}", http.StatusOK), &probe)
	if probe.SystemType != "openccu-lite" || !probe.Ready || probe.Lite == nil || !probe.Lite.PairingAvailable {
		t.Fatalf("probe = %+v, want a ready openccu-lite box offering pairing", probe)
	}

	var started struct {
		PairingID string `json:"pairing_id"`
		Code      string `json:"code"`
	}
	_ = json.Unmarshal(call(http.MethodPost, "/api/v1/centrals/pairing", "{"+target+"}", http.StatusCreated), &started)
	call(http.MethodGet, "/api/v1/centrals/pairing/"+started.PairingID, "", http.StatusOK)
	pending := box.Pairings()
	if len(pending) != 1 || pending[0].Code != started.Code {
		t.Fatalf("the box holds %+v; want one request showing code %s", pending, started.Code)
	}
	if _, err := box.ApprovePairing(pending[0].ID, started.Code); err != nil {
		t.Fatalf("ApprovePairing: %v", err)
	}
	var status struct {
		State string `json:"state"`
	}
	_ = json.Unmarshal(call(http.MethodGet, "/api/v1/centrals/pairing/"+started.PairingID+"?wait=10", "", http.StatusOK), &status)
	if status.State != "approved" {
		t.Fatalf("pairing state = %q, want approved", status.State)
	}

	call(http.MethodPost, "/api/v1/centrals", `{"name":"`+pairedCentral+`","host":"`+host+`","json_rpc_port":`+
		strconv.Itoa(port)+`,"system_type":"auto","enabled":true,"interfaces":[{"name":"HmIP-RF"}],"pairing_id":"`+
		started.PairingID+`"}`, http.StatusCreated)

	deadline := time.Now().Add(90 * time.Second)
	for !centralReady(t, h, pairedCentral) {
		if time.Now().After(deadline) {
			t.Fatalf("central %s never became ready on the paired token", pairedCentral)
		}
		time.Sleep(500 * time.Millisecond)
	}
	var persisted string
	for time.Now().Before(deadline) {
		if persisted = centralSystemType(t, h, pairedCentral); persisted == "openccu-lite" {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if persisted != "openccu-lite" {
		t.Errorf("stored system type = %q, want the resolved openccu-lite", persisted)
	}

	for _, b := range bodies {
		if strings.Contains(b, "olt_") {
			t.Errorf("an answer to the operator carried a token: %s", b)
		}
	}
}

// centralReady reports whether GET /system/ccu shows the central's
// south-bound bring-up complete.
func centralReady(t *testing.T, h *harness.Harness, name string) bool {
	t.Helper()
	body, code, err := getBody(h, "/api/v1/system/ccu")
	if err != nil || code != http.StatusOK {
		return false
	}
	var out struct {
		Entries []struct {
			Name      string `json:"name"`
			Readiness struct {
				Ready bool `json:"ready"`
			} `json:"readiness"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode /system/ccu: %v", err)
	}
	for _, e := range out.Entries {
		if e.Name == name {
			return e.Readiness.Ready
		}
	}
	return false
}

// centralSystemType is the stored system type of a configured central.
func centralSystemType(t *testing.T, h *harness.Harness, name string) string {
	t.Helper()
	for _, it := range getJSONArrayOnce(t, h, "/api/v1/centrals", "") {
		row, _ := it.(map[string]any)
		if row["name"] == name {
			s, _ := row["system_type"].(string)
			return s
		}
	}
	return ""
}
