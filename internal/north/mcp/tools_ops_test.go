// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mcp_test

import (
	"context"
	"testing"
	"time"

	"github.com/SukramJ/openccu-loom/internal/addonupdate"
	"github.com/SukramJ/openccu-loom/internal/north/mcp"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
)

// ─── fakes ────────────────────────────────────────────────────────────────

type fakeMatterStatus struct {
	resp handlers.MatterStatusResponse
}

func (f *fakeMatterStatus) MatterStatus(context.Context) handlers.MatterStatusResponse {
	return f.resp
}

type fakeBackupLister struct {
	entries []hmapi.BackupEntry
	err     error
}

func (f *fakeBackupLister) List(context.Context) ([]hmapi.BackupEntry, error) {
	return f.entries, f.err
}

type fakeAddonUpdate struct {
	status addonupdate.Status
}

func (f *fakeAddonUpdate) Status() addonupdate.Status { return f.status }

func (f *fakeAddonUpdate) Check(context.Context) error { return nil }

func (f *fakeAddonUpdate) InstallAsync(context.Context) error { return nil }

// ─── get_matter_status ──────────────────────────────────────────────────

func TestGetMatterStatus_ReportsBridgeState(t *testing.T) {
	matter := &fakeMatterStatus{resp: handlers.MatterStatusResponse{
		Enabled:        true,
		Listening:      true,
		EndpointCount:  3,
		FabricCount:    2,
		EnabledCount:   4,
		Advertising:    true,
		WindowOpen:     true,
		WindowDuration: 900,
	}}
	cs := connect(t, mcp.Deps{Matter: matter})
	defer cs.Close()

	res := callTool(t, cs, "get_matter_status", map[string]any{})
	if res.IsError {
		t.Fatalf("get_matter_status returned error: %v", res.Content)
	}

	var out struct {
		Enabled               bool `json:"enabled"`
		Listening             bool `json:"listening"`
		FabricCount           int  `json:"fabric_count"`
		EndpointCount         int  `json:"endpoint_count"`
		EnabledCount          int  `json:"enabled_count"`
		Advertising           bool `json:"advertising"`
		WindowOpen            bool `json:"commissioning_window_open"`
		WindowDurationSeconds int  `json:"commissioning_window_duration_seconds"`
	}
	unmarshalStructured(t, res, &out)

	if !out.Enabled || !out.Listening || !out.WindowOpen {
		t.Errorf("expected enabled/listening/window_open all true, got %+v", out)
	}
	if out.FabricCount != 2 {
		t.Errorf("fabric_count: want 2, got %d", out.FabricCount)
	}
	if out.EndpointCount != 3 {
		t.Errorf("endpoint_count: want 3, got %d", out.EndpointCount)
	}
	if out.WindowDurationSeconds != 900 {
		t.Errorf("commissioning_window_duration_seconds: want 900, got %d", out.WindowDurationSeconds)
	}
}

func TestGetMatterStatus_NotRegisteredWhenUnwired(t *testing.T) {
	cs := connect(t, mcp.Deps{})
	defer cs.Close()

	names := toolNames(t, cs)
	if names["get_matter_status"] {
		t.Error("get_matter_status must not be registered when Deps.Matter is nil")
	}
}

// ─── list_backups ────────────────────────────────────────────────────────

func TestListBackups_ReturnsStoredArchives(t *testing.T) {
	created := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)
	backups := &fakeBackupLister{entries: []hmapi.BackupEntry{
		{ID: "b1", Central: "alpha", Bytes: 1024, CreatedAt: created, Filename: "ccu-3.75.7-2026-08-20-1200.sbk"},
		{ID: "b2", Central: "beta", Bytes: 2048, CreatedAt: created},
	}}
	centrals := &fakeCentrals{names: []string{"alpha", "beta"}}
	cs := connect(t, mcp.Deps{Backups: backups, Centrals: centrals})
	defer cs.Close()

	res := callTool(t, cs, "list_backups", map[string]any{})
	if res.IsError {
		t.Fatalf("list_backups returned error: %v", res.Content)
	}

	var out struct {
		Backups []struct {
			ID        string `json:"id"`
			Central   string `json:"central"`
			Bytes     int64  `json:"bytes"`
			CreatedAt string `json:"created_at"`
			Filename  string `json:"filename,omitempty"`
		} `json:"backups"`
	}
	unmarshalStructured(t, res, &out)

	if len(out.Backups) != 2 {
		t.Fatalf("expected 2 backups, got %d", len(out.Backups))
	}
	if out.Backups[0].Filename != "ccu-3.75.7-2026-08-20-1200.sbk" {
		t.Errorf("filename not carried through: got %q", out.Backups[0].Filename)
	}
}

func TestListBackups_ScopesToNamedCentral(t *testing.T) {
	backups := &fakeBackupLister{entries: []hmapi.BackupEntry{
		{ID: "b1", Central: "alpha", Bytes: 1024},
		{ID: "b2", Central: "beta", Bytes: 2048},
	}}
	centrals := &fakeCentrals{names: []string{"alpha", "beta"}}
	cs := connect(t, mcp.Deps{Backups: backups, Centrals: centrals})
	defer cs.Close()

	res := callTool(t, cs, "list_backups", map[string]any{"central_name": "alpha"})
	if res.IsError {
		t.Fatalf("list_backups returned error: %v", res.Content)
	}

	var out struct {
		Backups []struct {
			ID string `json:"id"`
		} `json:"backups"`
	}
	unmarshalStructured(t, res, &out)

	if len(out.Backups) != 1 || out.Backups[0].ID != "b1" {
		t.Errorf("expected only alpha's backup b1, got %+v", out.Backups)
	}
}

// TestListBackups_UnknownCentralIsAnError is the negative control: a
// mistyped or hallucinated central_name must surface as an error, never
// as a silently empty list that reads as "this central genuinely has no
// backups".
func TestListBackups_UnknownCentralIsAnError(t *testing.T) {
	backups := &fakeBackupLister{entries: []hmapi.BackupEntry{
		{ID: "b1", Central: "alpha", Bytes: 1024},
	}}
	centrals := &fakeCentrals{names: []string{"alpha"}}
	cs := connect(t, mcp.Deps{Backups: backups, Centrals: centrals})
	defer cs.Close()

	res := callTool(t, cs, "list_backups", map[string]any{"central_name": "does-not-exist"})
	if !res.IsError {
		t.Fatalf("expected an error for an unknown central, got a result: %+v", res.StructuredContent)
	}
}

// ─── get_addon_update_status ────────────────────────────────────────────

func TestGetAddonUpdateStatus_ReportsCurrentAndAvailableVersion(t *testing.T) {
	updater := &fakeAddonUpdate{status: addonupdate.Status{
		Supported:       true,
		CurrentVersion:  "0.63.0",
		LatestVersion:   "0.64.0",
		UpdateAvailable: true,
		State:           addonupdate.StateIdle,
	}}
	cs := connect(t, mcp.Deps{AddonUpdate: updater})
	defer cs.Close()

	res := callTool(t, cs, "get_addon_update_status", map[string]any{})
	if res.IsError {
		t.Fatalf("get_addon_update_status returned error: %v", res.Content)
	}

	var out struct {
		Supported         bool   `json:"supported"`
		CurrentVersion    string `json:"current_version"`
		LatestVersion     string `json:"latest_version"`
		UpdateAvailable   bool   `json:"update_available"`
		State             string `json:"state"`
		InstallInProgress bool   `json:"install_in_progress"`
	}
	unmarshalStructured(t, res, &out)

	if out.CurrentVersion != "0.63.0" || out.LatestVersion != "0.64.0" || !out.UpdateAvailable {
		t.Errorf("version fields not carried through: %+v", out)
	}
	if out.InstallInProgress {
		t.Error("install_in_progress must be false while idle")
	}
}

func TestGetAddonUpdateStatus_InstallInProgressWhileInstalling(t *testing.T) {
	updater := &fakeAddonUpdate{status: addonupdate.Status{
		Supported: true,
		State:     addonupdate.StateInstalling,
	}}
	cs := connect(t, mcp.Deps{AddonUpdate: updater})
	defer cs.Close()

	res := callTool(t, cs, "get_addon_update_status", map[string]any{})
	if res.IsError {
		t.Fatalf("get_addon_update_status returned error: %v", res.Content)
	}

	var out struct {
		InstallInProgress bool `json:"install_in_progress"`
	}
	unmarshalStructured(t, res, &out)

	if !out.InstallInProgress {
		t.Error("install_in_progress must be true while State is StateInstalling")
	}
}
