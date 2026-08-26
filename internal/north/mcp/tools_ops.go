// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package mcp

import (
	"context"
	"strings"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/SukramJ/openccu-loom/internal/addonupdate"
)

// This file holds the operational-domain read tools: the Matter bridge,
// stored CCU backups, and the add-on self-updater. REST has a full read
// (and, for two of the three, write) surface for all of them; MCP had
// none, so an assistant could not answer "is the bridge paired", "when
// did the last backup run" or "is there a pending update" even though
// every one of those is a read-only status question. Only the read
// half is projected here — see the package doc comment for why writes
// stay off the assistant surface for now.

// --- Matter bridge status ----------------------------------------------

type getMatterStatusIn struct{}

// getMatterStatusOut projects [handlers.MatterStatusResponse]. A local
// type rather than reusing the REST DTO directly, matching how every
// other tool in this package (deviceSummary, incidentSummary, …)
// projects its backing facade rather than exposing it verbatim.
type getMatterStatusOut struct {
	Enabled bool `json:"enabled"`
	// Listening reports whether the bridge's UDP listener is bound —
	// distinct from Enabled, which is only the config flag.
	Listening bool `json:"listening"`
	// FabricCount is the number of commissioned fabrics, i.e. the
	// number of controllers/ecosystems currently paired with the
	// bridge.
	FabricCount int `json:"fabric_count"`
	// EndpointCount is the number of bridged (non-root, non-Aggregator)
	// endpoints currently exposed.
	EndpointCount int `json:"endpoint_count"`
	// EnabledCount is how many exposure-allowlist entries are enabled.
	EnabledCount int  `json:"enabled_count"`
	Advertising  bool `json:"advertising"`
	// WindowOpen reports whether a commissioning window is currently
	// open, i.e. the bridge is accepting a new pairing right now.
	WindowOpen bool `json:"commissioning_window_open"`
	// WindowDurationSeconds is how long the open window stays open for;
	// zero when WindowOpen is false.
	WindowDurationSeconds uint16 `json:"commissioning_window_duration_seconds,omitempty"`
}

// registerGetMatterStatus implements `get_matter_status`, projecting
// the same [handlers.MatterStatusReader] seam GET /matter/status reads
// through. The bridge is one instance per daemon (not per CCU), so the
// tool takes no central_name.
func registerGetMatterStatus(s *mcpsdk.Server, d Deps) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name: "get_matter_status",
		Description: "Report the Matter bridge's runtime state: whether it is enabled and listening, " +
			"how many controllers (fabrics) are paired, how many bridged endpoints and enabled exposures " +
			"it currently carries, and whether a commissioning (pairing) window is open right now.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, _ getMatterStatusIn) (*mcpsdk.CallToolResult, getMatterStatusOut, error) {
		st := d.Matter.MatterStatus(ctx)
		return nil, getMatterStatusOut{
			Enabled:               st.Enabled,
			Listening:             st.Listening,
			FabricCount:           st.FabricCount,
			EndpointCount:         st.EndpointCount,
			EnabledCount:          st.EnabledCount,
			Advertising:           st.Advertising,
			WindowOpen:            st.WindowOpen,
			WindowDurationSeconds: st.WindowDuration,
		}, nil
	})
}

// --- backups -------------------------------------------------------------

type listBackupsIn struct {
	CentralName string `json:"central_name,omitempty" jsonschema:"optional CCU name to scope the list; omit to list every central's backups"`
}

type backupSummary struct {
	ID      string `json:"id"`
	Central string `json:"central"`
	Bytes   int64  `json:"bytes"`
	// CreatedAt is RFC3339 UTC.
	CreatedAt string `json:"created_at"`
	// Filename is the CCU-convention archive name
	// (`<hostname>-<firmware>-<YYYY-MM-DD-HHMM>.sbk`), what a download
	// is served as. Empty for an archive taken before this field
	// existed.
	Filename string `json:"filename,omitempty"`
}

type listBackupsOut struct {
	Backups []backupSummary `json:"backups"`
}

// registerListBackups implements `list_backups`, projecting the same
// [BackupLister] seam GET /backups reads through. Backup storage is one
// instance per daemon, but each stored archive names the central it
// backs up, so central_name filters the result the same way
// list_devices does — and, per the read-tool convention, a named-but-
// unknown central is a client error, not a silently empty list.
func registerListBackups(s *mcpsdk.Server, d Deps) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "list_backups",
		Description: "List locally-stored CCU backup archives, optionally scoped to one central via central_name. Returns each archive's id, owning central, size, creation time and download filename.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in listBackupsIn) (*mcpsdk.CallToolResult, listBackupsOut, error) {
		out := listBackupsOut{Backups: []backupSummary{}}
		want := strings.TrimSpace(in.CentralName)
		if want != "" && !centralKnown(d, want) {
			return nil, listBackupsOut{}, errUnknownCentral(d, want)
		}
		entries, err := d.Backups.List(ctx)
		if err != nil {
			return nil, listBackupsOut{}, err
		}
		for _, e := range entries {
			if want != "" && e.Central != want {
				continue
			}
			out.Backups = append(out.Backups, backupSummary{
				ID:        e.ID,
				Central:   e.Central,
				Bytes:     e.Bytes,
				CreatedAt: rfc3339OrEmpty(e.CreatedAt),
				Filename:  e.Filename,
			})
		}
		return nil, out, nil
	})
}

// --- add-on self-update ---------------------------------------------------

type getAddonUpdateStatusIn struct{}

type getAddonUpdateStatusOut struct {
	// Supported is false on a deployment where the add-on self-updater
	// does not apply (not running as the HA add-on, or the platform
	// capability probe failed) — every other field is then meaningless.
	Supported       bool   `json:"supported"`
	CurrentVersion  string `json:"current_version,omitempty"`
	LatestVersion   string `json:"latest_version,omitempty"`
	UpdateAvailable bool   `json:"update_available"`
	// State is the updater's lifecycle state: idle, checking,
	// downloading, installing, or failed.
	State string `json:"state"`
	// InstallInProgress is true while State is downloading or
	// installing — the question "is an install running right now"
	// answered directly, without the caller having to know the state
	// vocabulary.
	InstallInProgress bool   `json:"install_in_progress"`
	LastCheck         string `json:"last_check,omitempty"`
	Error             string `json:"error,omitempty"`
}

// registerGetAddonUpdateStatus implements `get_addon_update_status`,
// projecting the same [AddonUpdateService] seam GET /system/addon-update
// reads through. Only Status() is called — Check and InstallAsync stay
// off the assistant surface: triggering an update check or install is a
// write with real side effects on the CCU's add-on, and belongs behind
// the write-tool gate (AllowWrites), not a read-only status query.
func registerGetAddonUpdateStatus(s *mcpsdk.Server, d Deps) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name: "get_addon_update_status",
		Description: "Report the CCU add-on self-updater's status: current and available version, " +
			"whether an update is available, and whether a download or install is currently running.",
	}, func(_ context.Context, _ *mcpsdk.CallToolRequest, _ getAddonUpdateStatusIn) (*mcpsdk.CallToolResult, getAddonUpdateStatusOut, error) {
		st := d.AddonUpdate.Status()
		out := getAddonUpdateStatusOut{
			Supported:         st.Supported,
			CurrentVersion:    st.CurrentVersion,
			LatestVersion:     st.LatestVersion,
			UpdateAvailable:   st.UpdateAvailable,
			State:             string(st.State),
			InstallInProgress: st.State == addonupdate.StateDownloading || st.State == addonupdate.StateInstalling,
			Error:             st.Error,
		}
		if !st.LastCheck.IsZero() {
			out.LastCheck = rfc3339OrEmpty(st.LastCheck)
		}
		return nil, out, nil
	})
}
