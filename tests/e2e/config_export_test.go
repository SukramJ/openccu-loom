// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/SukramJ/openccu-loom/tests/e2e/harness"
)

// exportedChannelConfig mirrors the fields
// configui.ExportedConfiguration publishes that this test asserts on.
type exportedChannelConfig struct {
	Version        string         `json:"version"`
	CentralName    string         `json:"central_name"`
	DeviceAddress  string         `json:"device_address"`
	Model          string         `json:"model"`
	ChannelAddress string         `json:"channel_address"`
	ChannelType    string         `json:"channel_type"`
	ParamsetKey    string         `json:"paramset_key"`
	Values         map[string]any `json:"values"`
}

// TestE2EChannelConfigExport pins the channel configuration export /
// import endpoints through the real composition root.
//
// Both paths are published in assets/openapi.yaml and appear in the
// generated client types, but the daemon never assigned the backend they
// depend on: every production build answered 503 service_unready, with
// no configuration that could change the outcome. Only a black-box run
// against the built binary catches that — a router test handing the
// service in itself would have stayed green throughout.
func TestE2EChannelConfigExport(t *testing.T) {
	t.Parallel()

	h := harness.Start(t, harness.Options{AuthMode: harness.AuthSession})
	if err := h.REST().LoginSession(harness.AdminUser, harness.AdminPass); err != nil {
		t.Fatalf("login: %v", err)
	}

	addr := firstDeviceAddress(t, h)
	if addr == "" {
		t.Skip("no device in the daemon's device list — nothing to export")
	}

	path := "/api/v1/devices/" + addr + "/channels/1/config/export?paramset=MASTER"
	req, err := h.REST().NewRequest(http.MethodGet, path, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := h.REST().Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s = %d, want 200; body=%s", path, resp.StatusCode, body)
	}

	var cfg exportedChannelConfig
	if err := json.Unmarshal(body, &cfg); err != nil {
		t.Fatalf("export payload is not a configuration snapshot: %v; body=%s", err, body)
	}
	if cfg.Version == "" || cfg.ParamsetKey != "MASTER" {
		t.Fatalf("snapshot = %+v, want a versioned MASTER export", cfg)
	}
	if cfg.DeviceAddress != addr || cfg.ChannelAddress != addr+":1" {
		t.Fatalf("snapshot addresses = %q/%q, want %q/%q",
			cfg.DeviceAddress, cfg.ChannelAddress, addr, addr+":1")
	}
	// The metadata half comes from the DeviceIndex adapter. An empty
	// model or channel type means the export ran without it.
	if cfg.Model == "" || cfg.ChannelType == "" || cfg.CentralName == "" {
		t.Fatalf("snapshot metadata = model=%q channel_type=%q central=%q, want all populated",
			cfg.Model, cfg.ChannelType, cfg.CentralName)
	}

	// The exported snapshot must be applicable: every parameter it names
	// has to be one the write path accepts, or the export produces a file
	// its own import endpoint refuses.
	importPath := "/api/v1/devices/" + addr + "/channels/1/config/import"
	imp, err := h.REST().NewRequest(http.MethodPost, importPath, bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build import request: %v", err)
	}
	imp.Header.Set("Content-Type", "application/json")
	impResp, err := h.REST().Do(imp)
	if err != nil {
		t.Fatalf("POST %s: %v", importPath, err)
	}
	defer impResp.Body.Close()
	impBody, _ := io.ReadAll(impResp.Body)
	// The unwired backend is the failure this test exists for: it was the
	// only reachable outcome of both endpoints in every production build.
	var prob struct {
		Code   string `json:"code"`
		Detail string `json:"detail"`
	}
	_ = json.Unmarshal(impBody, &prob)
	if impResp.StatusCode == http.StatusServiceUnavailable || prob.Code == "service_unready" {
		t.Fatalf("POST %s = %d; the import backend is not wired: %s", importPath, impResp.StatusCode, impBody)
	}
	// A hidden parameter in the payload means the export handed out
	// something the write side rejects wholesale.
	if strings.Contains(prob.Detail, "hidden") {
		t.Fatalf("POST %s rejected the daemon's own export as unwritable: %s", importPath, impBody)
	}
}

// firstDeviceAddress returns any device address the daemon loaded, or "".
func firstDeviceAddress(t *testing.T, h *harness.Harness) string {
	t.Helper()
	req, err := h.REST().NewRequest(http.MethodGet, "/api/v1/devices", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := h.REST().Do(req)
	if err != nil {
		t.Fatalf("GET /api/v1/devices: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/v1/devices = %d", resp.StatusCode)
	}
	var page struct {
		Items []struct {
			Address string `json:"address"`
		} `json:"items"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
		t.Fatalf("decode device list: %v", err)
	}
	for _, d := range page.Items {
		if d.Address != "" {
			return d.Address
		}
	}
	return ""
}
