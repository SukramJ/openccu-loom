// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

//go:build e2e

package e2e

import (
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/SukramJ/openccu-loom/tests/e2e/harness"
)

type projectionAnswer struct {
	Address string `json:"address"`
	Central string `json:"central"`
	Planes  []struct {
		Plane   string            `json:"plane"`
		State   string            `json:"state"`
		Reason  string            `json:"reason"`
		Entries []json.RawMessage `json:"entries"`
	} `json:"planes"`
}

func getProjection(t *testing.T, h *harness.Harness, path string) (int, projectionAnswer) {
	t.Helper()
	req, err := h.REST().NewRequest(http.MethodGet, path, nil)
	if err != nil {
		t.Fatalf("request %s: %v", path, err)
	}
	resp, err := h.REST().Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var out projectionAnswer
	if resp.StatusCode == http.StatusOK {
		if err := json.Unmarshal(body, &out); err != nil {
			t.Fatalf("decode %s: %v (%s)", path, err, body)
		}
	}
	return resp.StatusCode, out
}

// TestDeviceProjectionAnswersThroughTheDaemon boots the built daemon and
// asks for a device's projection over REST. The harness runs without the
// Matter bridge, so the Matter plane must say so instead of listing
// entries: the answer crosses the route, the composition root's projector
// and the plane the Matter wiring hands it.
func TestDeviceProjectionAnswersThroughTheDaemon(t *testing.T) {
	t.Parallel()
	h := harness.Start(t, harness.Options{AuthMode: harness.AuthSession})
	if err := h.REST().LoginSession(harness.AdminUser, harness.AdminPass); err != nil {
		t.Fatalf("login: %v", err)
	}
	devices := getJSONArray(t, h, "/api/v1/devices", "items")
	first, _ := devices[0].(map[string]any)
	addr, _ := first["address"].(string)
	if addr == "" {
		t.Fatalf("no device address in %v", devices[0])
	}

	status, out := getProjection(t, h, "/api/v1/devices/"+addr+"/projection")
	if status != http.StatusOK {
		t.Fatalf("status %d", status)
	}
	if out.Address != addr || out.Central == "" {
		t.Errorf("answer names %q on %q; want %q on its central", out.Address, out.Central, addr)
	}
	if len(out.Planes) != 1 || out.Planes[0].Plane != "matter" {
		t.Fatalf("planes = %+v; want the matter plane", out.Planes)
	}
	if p := out.Planes[0]; p.State != "inactive" || p.Reason != "plane_disabled" || len(p.Entries) != 0 {
		t.Errorf("matter plane = %+v; want inactive, plane_disabled, no entries", p)
	}

	if status, out := getProjection(t, h, "/api/v1/devices/"+addr+"/projection?plane=nope"); status != http.StatusOK || len(out.Planes) != 0 {
		t.Errorf("plane=nope: status %d, planes %+v; want 200 and none", status, out.Planes)
	}
	if status, _ := getProjection(t, h, "/api/v1/devices/NO-SUCH-DEVICE/projection"); status != http.StatusNotFound {
		t.Errorf("unknown device: status %d; want 404", status)
	}
}
