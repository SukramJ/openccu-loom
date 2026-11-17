// SPDX-License-Identifier: Apache-2.0
// Copyright (C) 2026 SukramJ.

//go:build e2e

package e2e

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/tests/e2e/harness"
)

// TestEditorComponentIsServedByTheBuiltDaemon pins the hand-over an
// embedding integration relies on (ADR 0089): the manifest names a module
// the same daemon serves, without a session, whose bytes hash to the
// manifest's hash, together with the route list the relay enforces.
//
// Black-box against the built binary: whether the bundle is embedded and
// whether the manifest handler is handed it are properties of the build
// and of the composition root, which no handler test sees.
func TestEditorComponentIsServedByTheBuiltDaemon(t *testing.T) {
	t.Parallel()
	h := harness.Start(t, harness.Options{AuthMode: harness.AuthSession})
	rest := h.REST()
	if err := rest.LoginSession(harness.AdminUser, harness.AdminPass); err != nil {
		t.Fatalf("login: %v", err)
	}

	req, err := rest.NewRequest(http.MethodGet, "/api/v1/ui/editor-component", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := rest.Do(req)
	if err != nil {
		t.Fatalf("GET manifest: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("manifest status=%d body=%s", resp.StatusCode, body)
	}
	var m hmapi.EditorComponentManifest
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatalf("decode manifest: %v", err)
	}
	if !slices.Contains(m.Routes, "/devices") {
		t.Errorf("routes %v lack /devices, the editor's own resource", m.Routes)
	}
	if !strings.Contains(m.ModuleURL, m.Hash) {
		t.Errorf("module_url %q does not carry hash %q", m.ModuleURL, m.Hash)
	}

	// The module is public code: a client with no session fetches it.
	mod, err := http.Get(h.RESTBase() + m.ModuleURL) //nolint:gosec,noctx // the URL is the daemon under test
	if err != nil {
		t.Fatalf("GET module: %v", err)
	}
	defer mod.Body.Close()
	body, _ := io.ReadAll(mod.Body)
	if mod.StatusCode != http.StatusOK {
		t.Fatalf("module status=%d", mod.StatusCode)
	}
	if ct := mod.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/javascript") {
		t.Errorf("module content-type = %q", ct)
	}
	sum := sha256.Sum256(body)
	if got := hex.EncodeToString(sum[:]); got != m.Hash {
		t.Errorf("module hashes to %s, manifest says %s", got, m.Hash)
	}
	if !strings.Contains(string(body), "loom-device-editor") {
		t.Error("module does not define the loom-device-editor element")
	}
}
