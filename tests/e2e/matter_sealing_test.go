// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

//go:build e2e

package e2e

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/tests/e2e/harness"
)

// matterHealth reads the matter.bridge component of GET /api/v1/health.
func matterHealth(t *testing.T, h *harness.Harness) (status, note string) {
	t.Helper()
	req, err := h.REST().NewRequest(http.MethodGet, "/api/v1/health", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := h.REST().Do(req)
	if err != nil {
		t.Fatalf("GET /api/v1/health: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var body struct {
		Components []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
			Note   string `json:"note"`
		} `json:"components"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode health: %v", err)
	}
	for _, c := range body.Components {
		if c.Name == "matter.bridge" {
			return c.Status, c.Note
		}
	}
	return "", ""
}

// TestE2EMatterSealedStoreFailsClosedAndResetRecovers drives ADR 0092
// through the built daemon. The harness provisions no
// OPENCCU_LOOM_SECRET_KEY, so the daemon creates secret.key and seals the
// Matter store on its first start:
//
//   - with secret.key gone, the next start creates another key, which does
//     not open the store: the bridge stays off and /health says why;
//   - `matter reset` refuses while the daemon runs (the data directory's
//     lock);
//   - run while the daemon is down, it erases the store, and the bridge
//     comes back unpaired with the operator's selection.
func TestE2EMatterSealedStoreFailsClosedAndResetRecovers(t *testing.T) {
	h := harness.Start(t, harness.Options{EnableMatter: true, StartCCUNotReady: true, AuthMode: harness.AuthSession})
	login := func() {
		t.Helper()
		if err := h.REST().LoginSession(harness.AdminUser, harness.AdminPass); err != nil {
			t.Fatalf("login: %v", err)
		}
	}
	login()
	h.CCU().V().SetReady(true)
	awaitCentralReady(t, h, harness.CentralName)
	if n := h.EnableAllExposures(t); n == 0 {
		t.Fatal("the node offers no source to expose")
	}
	selected := len(h.NodeEndpoints(t))
	if selected == 0 {
		t.Fatal("the selection put no endpoint on the bridge")
	}

	h.RestartAfterWithoutBridge(t, func(dataDir, _, _ string) {
		if err := os.Remove(filepath.Join(dataDir, "secret.key")); err != nil {
			t.Fatalf("remove secret.key: %v", err)
		}
	})
	login()
	awaitCentralReady(t, h, harness.CentralName)
	if status, note := matterHealth(t, h); status == "healthy" || !strings.Contains(note, "does not open") {
		t.Fatalf("matter health with another key = %q %q, want unhealthy naming the key that does not open the store", status, note)
	}

	if out, err := h.Offline("matter", "reset", "--yes"); err == nil || !strings.Contains(out, "stop it first") {
		t.Fatalf("matter reset under the running daemon: err=%v output=%q, want a refusal", err, out)
	}

	h.RestartAfter(t, func(string, string, string) {
		if out, err := h.Offline("matter", "reset", "--yes"); err != nil {
			t.Fatalf("matter reset with the daemon down: %v\n%s", err, out)
		}
	})
	login()
	awaitCentralReady(t, h, harness.CentralName)
	if got := awaitEndpointCount(t, h, selected); got != selected {
		t.Fatalf("after the reset the bridge holds %d bridged endpoints, want the %d selected before", got, selected)
	}
	if st := h.NodeStatus(t); !st.WindowOpen {
		t.Fatal("after the reset the unpaired bridge has no window open")
	}
}
