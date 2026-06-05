// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// deviceSummary is the per-device projection shared by the device tools.
type deviceSummary struct {
	Address   string `json:"address"`
	Model     string `json:"model"`
	Name      string `json:"name"`
	Interface string `json:"interface"`
	Central   string `json:"central"`
}

func summarize(d *device.Device, central string) deviceSummary {
	return deviceSummary{
		Address:   d.Address,
		Model:     d.Model,
		Name:      d.Name,
		Interface: string(d.Interface),
		Central:   central,
	}
}

// --- read tools -------------------------------------------------------

type listCentralsIn struct{}

type listCentralsOut struct {
	Centrals []string `json:"centrals" jsonschema:"the configured CCU names; pass one as central_name to scope other tools"`
}

type listDevicesIn struct {
	CentralName string `json:"central_name,omitempty" jsonschema:"optional CCU name to scope the list; omit to list every central's devices"`
}

type listDevicesOut struct {
	Devices []deviceSummary `json:"devices"`
}

type getDeviceIn struct {
	Address string `json:"address" jsonschema:"the device address / serial, e.g. 0001D3C99C1234"`
}

type getDeviceOut struct {
	Found  bool          `json:"found"`
	Device deviceSummary `json:"device,omitempty"`
}

type listAuditIn struct {
	Limit int `json:"limit,omitempty" jsonschema:"maximum entries to return, newest first (default 50, max 1000)"`
}

type auditSummary struct {
	Timestamp     string `json:"timestamp"`
	User          string `json:"user,omitempty"`
	Action        string `json:"action"`
	DeviceAddress string `json:"device_address,omitempty"`
	Parameter     string `json:"parameter,omitempty"`
	Note          string `json:"note,omitempty"`
}

type listAuditOut struct {
	Entries []auditSummary `json:"entries"`
}

// registerReadTools wires the always-available read surface.
func registerReadTools(s *mcpsdk.Server, d Deps) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "list_centrals",
		Description: "List the configured Homematic CCUs (centrals). The returned names are the scoping dimension for every other tool.",
	}, func(_ context.Context, _ *mcpsdk.CallToolRequest, _ listCentralsIn) (*mcpsdk.CallToolResult, listCentralsOut, error) {
		var names []string
		if d.Centrals != nil {
			names = d.Centrals.Names()
		}
		return nil, listCentralsOut{Centrals: names}, nil
	})

	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "list_devices",
		Description: "List devices, optionally scoped to one central via central_name.",
	}, func(_ context.Context, _ *mcpsdk.CallToolRequest, in listDevicesIn) (*mcpsdk.CallToolResult, listDevicesOut, error) {
		out := listDevicesOut{Devices: []deviceSummary{}}
		if d.Devices == nil {
			return nil, out, nil
		}
		want := strings.TrimSpace(in.CentralName)
		for _, dev := range d.Devices.Devices() {
			central := d.Devices.CentralOf(dev.Address)
			if want != "" && central != want {
				continue
			}
			out.Devices = append(out.Devices, summarize(dev, central))
		}
		return nil, out, nil
	})

	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "get_device",
		Description: "Look up a single device by address, with its owning central.",
	}, func(_ context.Context, _ *mcpsdk.CallToolRequest, in getDeviceIn) (*mcpsdk.CallToolResult, getDeviceOut, error) {
		if d.Devices == nil {
			return nil, getDeviceOut{}, nil
		}
		dev, ok := d.Devices.Device(strings.TrimSpace(in.Address))
		if !ok {
			return nil, getDeviceOut{Found: false}, nil
		}
		return nil, getDeviceOut{Found: true, Device: summarize(dev, d.Devices.CentralOf(dev.Address))}, nil
	})

	if d.Audit != nil {
		mcpsdk.AddTool(s, &mcpsdk.Tool{
			Name:        "list_audit",
			Description: "Read the recent configuration change-log (who changed what, when). Newest first.",
		}, func(_ context.Context, _ *mcpsdk.CallToolRequest, in listAuditIn) (*mcpsdk.CallToolResult, listAuditOut, error) {
			limit := in.Limit
			if limit <= 0 {
				limit = 50
			}
			if limit > 1000 {
				limit = 1000
			}
			entries := d.Audit.List(limit)
			out := listAuditOut{Entries: make([]auditSummary, 0, len(entries))}
			for i := range entries {
				e := &entries[i]
				out.Entries = append(out.Entries, auditSummary{
					Timestamp:     e.Timestamp.UTC().Format(time.RFC3339),
					User:          e.User,
					Action:        string(e.Action),
					DeviceAddress: e.DeviceAddress,
					Parameter:     e.Parameter,
					Note:          e.Note,
				})
			}
			return nil, out, nil
		})
	}
}

// --- write tools (gated by AllowWrites) -------------------------------

type setDatapointIn struct {
	CentralName string `json:"central_name" jsonschema:"the CCU that owns the device (required; must match the device's central)"`
	Address     string `json:"address" jsonschema:"the channel address to write, e.g. 0001D3C99C1234:4"`
	Parameter   string `json:"parameter" jsonschema:"the parameter name, e.g. STATE or LEVEL"`
	Value       any    `json:"value" jsonschema:"the value to write (boolean, number, or string as the parameter expects)"`
}

type setDatapointOut struct {
	OK bool `json:"ok"`
}

// registerWriteTools wires the write surface. Only called when writes
// are enabled (AllowWrites + a non-nil Writer).
func registerWriteTools(s *mcpsdk.Server, d Deps) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "set_datapoint",
		Description: "Write a value to a device data point. Requires central_name; the named central must own the device.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in setDatapointIn) (*mcpsdk.CallToolResult, setDatapointOut, error) {
		central := strings.TrimSpace(in.CentralName)
		address := strings.TrimSpace(in.Address)
		parameter := strings.TrimSpace(in.Parameter)
		if central == "" || address == "" || parameter == "" {
			return nil, setDatapointOut{}, errors.New("central_name, address and parameter are required")
		}
		// Multi-CCU safety: refuse to write to a device the named
		// central does not own (ADR 0002 — central_name is explicit and
		// authoritative, never an implicit fallback).
		if owner := d.Devices.CentralOf(address); owner != central {
			return nil, setDatapointOut{}, fmt.Errorf("device %s belongs to central %q, not %q", address, owner, central)
		}
		// CommandPriorityHigh mirrors the REST default for user-initiated
		// writes — never the zero value (CommandPriorityCritical).
		if err := d.Writer.SetValue(ctx, address, hmenum.Parameter(parameter), in.Value, hmenum.CommandPriorityHigh); err != nil {
			return nil, setDatapointOut{}, fmt.Errorf("set value: %w", err)
		}
		if d.Audit != nil {
			d.Audit.Record(audit.Entry{
				Timestamp:     time.Now().UTC(),
				Action:        audit.ActionDataPointWrite,
				DeviceAddress: address,
				Parameter:     parameter,
				Note:          "via mcp",
			})
		}
		return nil, setDatapointOut{OK: true}, nil
	})
}
