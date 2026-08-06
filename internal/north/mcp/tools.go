// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package mcp

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/reqctx"
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

type listIncidentsIn struct {
	CentralName string `json:"central_name,omitempty" jsonschema:"optional CCU name to scope the result; omit to span every central"`
	Limit       int    `json:"limit,omitempty" jsonschema:"maximum entries to return, newest first (default 50, max 1000)"`
}

type incidentSummary struct {
	ID        string `json:"id"`
	When      string `json:"when"`
	Component string `json:"component"`
	Severity  string `json:"severity"`
	Summary   string `json:"summary"`
	Detail    string `json:"detail,omitempty"`
}

type listIncidentsOut struct {
	Incidents []incidentSummary `json:"incidents"`
}

type readParamsetIn struct {
	Address string `json:"address" jsonschema:"the channel address, e.g. 0001D3C99C1234:1"`
	Key     string `json:"key" jsonschema:"the paramset key: MASTER (configuration) or VALUES (current state)"`
}

type readParamsetOut struct {
	Values map[string]any `json:"values"`
}

type getHealthIn struct{}

type healthComponentSummary struct {
	Name   string `json:"name"`
	Status string `json:"status"`
}

type getHealthOut struct {
	Overall    string                   `json:"overall"`
	Components []healthComponentSummary `json:"components"`
}

// parseParamsetKey accepts the two operator-facing paramset keys. LINK
// is intentionally excluded — it needs a peer address and a different
// tool shape.
// deviceAddressOf strips a channel suffix (`ADDR:n` → `ADDR`): write
// tools target channels while device ownership is tracked per device.
func deviceAddressOf(address string) string {
	if i := strings.IndexByte(address, ':'); i > 0 {
		return address[:i]
	}
	return address
}

func parseParamsetKey(s string) (hmenum.ParamsetKey, bool) {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "MASTER":
		return hmenum.ParamsetKeyMaster, true
	case "VALUES":
		return hmenum.ParamsetKeyValues, true
	default:
		return "", false
	}
}

// registerReadTools wires the always-available read surface. Each tool
// gates on its own dependency so a partial wiring never exposes a tool
// that cannot answer.
//
// Tool names follow one taxonomy across the whole MCP surface so the
// catalogue reads as a single design rather than a grab-bag:
//
//   - list_<plural>   — enumerate like entities (list_devices, list_programs)
//   - get_<singular>  — fetch one record or an overall view (get_device, get_health)
//   - read_<noun>     — read a keyed sub-structure (read_paramset)
//   - <verb>_<noun>   — write / action tools (set_datapoint, trigger_program)
//
// Names also use the project's compact domain vocabulary (central,
// datapoint, paramset, sysvar) rather than verbose spelled-out forms.
// Every central-spanning read tool takes an optional central_name.
func registerReadTools(s *mcpsdk.Server, d Deps) {
	registerListCentrals(s, d)
	registerListDevices(s, d)
	registerGetDevice(s, d)
	if d.Audit != nil {
		registerListAudit(s, d)
	}
	if d.Incidents != nil {
		registerListIncidents(s, d)
	}
	if d.Paramsets != nil {
		registerReadParamset(s, d)
	}
	if d.Health != nil {
		registerGetHealth(s, d)
	}
	// Device-topology read tools project the device model directly.
	if d.Devices != nil {
		registerListRooms(s, d)
		registerListFunctions(s, d)
		registerListChannels(s, d)
	}
	// Hub-aggregate read tools span the configured centrals via the
	// already-wired HubResolver + CentralLister seams.
	if d.Hubs != nil && d.Centrals != nil {
		registerListPrograms(s, d)
		registerListSysvars(s, d)
		registerListServiceMessages(s, d)
		registerListAlarmMessages(s, d)
		registerListInbox(s, d)
		registerGetSystemInfo(s, d)
	}
}

func registerListCentrals(s *mcpsdk.Server, d Deps) {
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
}

func registerListDevices(s *mcpsdk.Server, d Deps) {
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
}

func registerGetDevice(s *mcpsdk.Server, d Deps) {
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
}

func registerListAudit(s *mcpsdk.Server, d Deps) {
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

func registerListIncidents(s *mcpsdk.Server, d Deps) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "list_incidents",
		Description: "Read the recent reliability incident journal (circuit-breaker trips, ping/pong mismatches, retry exhaustion). Newest first. Optionally scope to one CCU via central_name.",
	}, func(_ context.Context, _ *mcpsdk.CallToolRequest, in listIncidentsIn) (*mcpsdk.CallToolResult, listIncidentsOut, error) {
		limit := in.Limit
		if limit <= 0 {
			limit = 50
		}
		if limit > 1000 {
			limit = 1000
		}
		incidents := d.Incidents.Incidents()
		// Optional central filter: Component is "<central>" or
		// "<central>/<interface>" (toAPIIncident), so match the central
		// segment exactly.
		if central := strings.TrimSpace(in.CentralName); central != "" {
			filtered := incidents[:0:0]
			for _, inc := range incidents {
				if inc.Component == central || strings.HasPrefix(inc.Component, central+"/") {
					filtered = append(filtered, inc)
				}
			}
			incidents = filtered
		}
		// Newest first; Incidents() ordering is not guaranteed, so sort
		// explicitly before clamping to the limit.
		sort.SliceStable(incidents, func(i, j int) bool {
			return incidents[i].When.After(incidents[j].When)
		})
		if len(incidents) > limit {
			incidents = incidents[:limit]
		}
		out := listIncidentsOut{Incidents: make([]incidentSummary, 0, len(incidents))}
		for i := range incidents {
			inc := &incidents[i]
			out.Incidents = append(out.Incidents, incidentSummary{
				ID:        inc.ID,
				When:      inc.When.UTC().Format(time.RFC3339),
				Component: inc.Component,
				Severity:  inc.Severity,
				Summary:   inc.Summary,
				Detail:    inc.Detail,
			})
		}
		return nil, out, nil
	})
}

func registerReadParamset(s *mcpsdk.Server, d Deps) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "read_paramset",
		Description: "Read a device paramset: MASTER (configuration) or VALUES (current state) for a channel address.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in readParamsetIn) (*mcpsdk.CallToolResult, readParamsetOut, error) {
		key, ok := parseParamsetKey(in.Key)
		if !ok {
			return nil, readParamsetOut{}, fmt.Errorf("key must be MASTER or VALUES, got %q", in.Key)
		}
		values, err := d.Paramsets.GetParamset(ctx, strings.TrimSpace(in.Address), key)
		if err != nil {
			return nil, readParamsetOut{}, fmt.Errorf("read paramset: %w", err)
		}
		return nil, readParamsetOut{Values: values}, nil
	})
}

func registerGetHealth(s *mcpsdk.Server, d Deps) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "get_health",
		Description: "Report the daemon's health: an overall status plus per-component status (CCU connectivity, subsystems).",
	}, func(_ context.Context, _ *mcpsdk.CallToolRequest, _ getHealthIn) (*mcpsdk.CallToolResult, getHealthOut, error) {
		out := getHealthOut{
			Overall:    string(d.Health.Overall()),
			Components: []healthComponentSummary{},
		}
		for _, c := range d.Health.Snapshot() {
			out.Components = append(out.Components, healthComponentSummary{
				Name:   c.Name,
				Status: string(c.Status),
			})
		}
		return nil, out, nil
	})
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

type writeParamsetIn struct {
	CentralName string         `json:"central_name" jsonschema:"the CCU that owns the device (required; must match the device's central)"`
	Address     string         `json:"address" jsonschema:"the channel address, e.g. 0001D3C99C1234:1"`
	Key         string         `json:"key" jsonschema:"the paramset key: MASTER or VALUES"`
	Values      map[string]any `json:"values" jsonschema:"the parameter→value map to write"`
}

type writeParamsetOut struct {
	OK bool `json:"ok"`
}

type triggerProgramIn struct {
	CentralName string `json:"central_name" jsonschema:"the CCU that owns the program (required)"`
	ProgramID   string `json:"program_id" jsonschema:"the CCU program ID (ISE object id)"`
}

type triggerProgramOut struct {
	OK bool `json:"ok"`
}

// registerWriteTools wires the write surface. Called when AllowWrites is
// set; each tool still gates on its own dependency so a partial wiring
// never exposes a half-functional tool.
func registerWriteTools(s *mcpsdk.Server, d Deps) {
	if d.Writer != nil {
		registerSetDatapoint(s, d)
	}
	if d.Paramsets != nil {
		registerWriteParamset(s, d)
	}
	if d.Hubs != nil {
		registerTriggerProgram(s, d)
	}
}

func registerSetDatapoint(s *mcpsdk.Server, d Deps) {
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
		// authoritative, never an implicit fallback). Ownership is
		// tracked per device, so the channel suffix must be stripped
		// before the lookup — writes always target channels.
		if owner := d.Devices.CentralOf(deviceAddressOf(address)); owner != central {
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

func registerWriteParamset(s *mcpsdk.Server, d Deps) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "write_paramset",
		Description: "Write a device paramset (MASTER configuration or VALUES state). Requires central_name; the named central must own the device.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in writeParamsetIn) (*mcpsdk.CallToolResult, writeParamsetOut, error) {
		central := strings.TrimSpace(in.CentralName)
		address := strings.TrimSpace(in.Address)
		if central == "" || address == "" {
			return nil, writeParamsetOut{}, errors.New("central_name and address are required")
		}
		if len(in.Values) == 0 {
			return nil, writeParamsetOut{}, errors.New("values must not be empty")
		}
		key, ok := parseParamsetKey(in.Key)
		if !ok {
			return nil, writeParamsetOut{}, fmt.Errorf("key must be MASTER or VALUES, got %q", in.Key)
		}
		if owner := d.Devices.CentralOf(deviceAddressOf(address)); owner != central {
			return nil, writeParamsetOut{}, fmt.Errorf("device %s belongs to central %q, not %q", address, owner, central)
		}
		if err := d.Paramsets.PutParamset(ctx, address, key, in.Values); err != nil {
			return nil, writeParamsetOut{}, fmt.Errorf("write paramset: %w", err)
		}
		if d.Audit != nil {
			d.Audit.Record(audit.Entry{
				Timestamp:     time.Now().UTC(),
				Action:        audit.ActionParamsetWrite,
				DeviceAddress: address,
				Paramset:      string(key),
				Note:          "via mcp",
			})
		}
		return nil, writeParamsetOut{OK: true}, nil
	})
}

func registerTriggerProgram(s *mcpsdk.Server, d Deps) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "trigger_program",
		Description: "Run a CCU automation program by its ID on the named central.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in triggerProgramIn) (*mcpsdk.CallToolResult, triggerProgramOut, error) {
		central := strings.TrimSpace(in.CentralName)
		programID := strings.TrimSpace(in.ProgramID)
		if central == "" || programID == "" {
			return nil, triggerProgramOut{}, errors.New("central_name and program_id are required")
		}
		hubModel := d.Hubs.HubFor(central)
		if hubModel == nil {
			return nil, triggerProgramOut{}, fmt.Errorf("unknown central %q", central)
		}
		prog, ok := hubModel.Program(programID)
		if !ok {
			return nil, triggerProgramOut{}, fmt.Errorf("program %q not found on central %q", programID, central)
		}
		// Stamp the surface so the program-execute audit/log subscriber
		// can attribute the run to the MCP server.
		ctx = reqctx.WithOperation(ctx, "mcp:program-trigger")
		if err := prog.Execute(ctx); err != nil {
			return nil, triggerProgramOut{}, fmt.Errorf("execute program: %w", err)
		}
		if d.Audit != nil {
			d.Audit.Record(audit.Entry{
				Timestamp: time.Now().UTC(),
				Action:    audit.Action("program_execute"),
				Note:      "program=" + programID + " via mcp",
			})
		}
		return nil, triggerProgramOut{OK: true}, nil
	})
}
