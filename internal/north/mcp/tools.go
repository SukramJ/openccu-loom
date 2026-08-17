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
	"github.com/SukramJ/openccu-loom/internal/auth"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	paramcoerce "github.com/SukramJ/openccu-loom/internal/parameter"
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

// resolveDataPoint resolves the VALUES data point for a channel address +
// parameter through the device model, returning nil when the model does
// not carry it (channel not hydrated, unknown parameter). It is the seam
// the write path uses to reach a parameter's descriptor so it can coerce
// the incoming value against it, exactly as the REST PUT /value handler
// does.
func resolveDataPoint(devices DeviceLister, channelAddress, parameterName string) device.ParameterDataPoint {
	if devices == nil {
		return nil
	}
	dev, ok := devices.Device(deviceAddressOf(channelAddress))
	if !ok || dev == nil {
		return nil
	}
	ch := dev.Channel(channelAddress)
	if ch == nil {
		return nil
	}
	return ch.Parameter(hmenum.Parameter(parameterName))
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
	if d.Alarm != nil {
		registerListAlarmZones(s, d)
		registerListTriggeredMotion(s, d)
	}
	if d.Security != nil {
		registerGetSecurityStatus(s, d)
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

// callerHasRole reports whether the identity the mount's resolve chain
// attached to the session context may act as want.
//
// The mount gates the whole tool set at a single role — viewer, or
// operator once writes are allowed — which is the right bar for the read
// surface as a whole but one step below the bar a few individual tools
// need. A tool whose REST twin is mounted With(admin) re-checks here so
// both surfaces draw the same boundary; everything else keeps trusting
// the mount.
func callerHasRole(ctx context.Context, want auth.Role) bool {
	id, ok := auth.IdentityFrom(ctx)
	return ok && id.HasRole(want)
}

func registerListAudit(s *mcpsdk.Server, d Deps) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "list_audit",
		Description: "Read the recent configuration change-log (who changed what, when). Newest first. Requires an admin identity.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in listAuditIn) (*mcpsdk.CallToolResult, listAuditOut, error) {
		// REST mounts GET /audit With(admin): the change-log names which
		// operator changed which credential-bearing section, which device
		// parameters were written and the notes attached to both. A viewer
		// identity must not read it here either.
		if !callerHasRole(ctx, auth.RoleAdmin) {
			return nil, listAuditOut{}, errors.New("the configuration change-log is admin-only")
		}
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
	// EditToken carries the edit-lock token a MASTER/LINK write must
	// present under strict enforcement, mirroring the WS paramset.put
	// `edit_token` field. Open an edit session first to obtain it.
	// Ignored for VALUES writes.
	EditToken string `json:"edit_token,omitempty" jsonschema:"edit-lock token for a MASTER write; obtain it by opening an edit session for the channel"`
}

type writeParamsetOut struct {
	OK bool `json:"ok"`
}

// editLockKey builds the same lock-key shape the REST enforceEditLock gate,
// the WS paramset.put handler and registerWriteParamset's own Verify call
// use, so a session opened here is recognised there. It must be built the
// same way in exactly one place; both callers below route through it.
func editLockKey(address string, key hmenum.ParamsetKey) string {
	return "channel:" + address + ":" + string(key)
}

type openEditSessionIn struct {
	Address string `json:"address" jsonschema:"the channel address to lock, e.g. 0001D3C99C1234:1"`
	Key     string `json:"key" jsonschema:"the paramset key to lock: MASTER (the only key write_paramset enforces the lock for)"`
}

type openEditSessionOut struct {
	EditToken string `json:"edit_token" jsonschema:"pass this as write_paramset's edit_token"`
	Expires   string `json:"expires" jsonschema:"RFC3339 timestamp the lock expires at unless closed first"`
}

type closeEditSessionIn struct {
	Address   string `json:"address" jsonschema:"the channel address passed to open_edit_session"`
	Key       string `json:"key" jsonschema:"the paramset key passed to open_edit_session"`
	EditToken string `json:"edit_token" jsonschema:"the token returned by open_edit_session"`
}

type closeEditSessionOut struct {
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
	if d.EditSessions != nil {
		// The only way an MCP client can ever obtain the edit_token
		// write_paramset's MASTER/LINK path requires: without these two
		// tools that gate is unconditionally unreachable over MCP (see
		// [EditSessionOpener]).
		registerOpenEditSession(s, d)
		registerCloseEditSession(s, d)
	}
	if d.Hubs != nil {
		registerTriggerProgram(s, d)
	}
	if d.AlarmControl != nil {
		registerArmAlarmZone(s, d)
		registerDisarmAlarmZone(s, d)
		registerResetMotion(s, d)
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
		// Coerce the JSON value against the parameter descriptor before it
		// reaches the wire — the same first step the REST PUT /value handler
		// takes. MCP JSON numbers arrive as float64, so an INTEGER given 21
		// or a BOOL given a number would otherwise reach the CCU mistyped,
		// and an ENUM given its option label would be sent verbatim instead
		// of the integer index the CCU expects. A parameter the model does
		// not carry (unhydrated channel, unknown parameter) falls through
		// with the raw value, preserving the prior best-effort behaviour.
		writeValue := in.Value
		if dp := resolveDataPoint(d.Devices, address, parameter); dp != nil {
			pv, cerr := paramcoerce.Coerce(dp.ParameterData(), in.Value)
			if cerr != nil {
				return nil, setDatapointOut{}, fmt.Errorf("coerce value: %w", cerr)
			}
			writeValue = pv.Unwrap()
		}
		// CommandPriorityHigh mirrors the REST default for user-initiated
		// writes — never the zero value (CommandPriorityCritical).
		if err := d.Writer.SetValue(ctx, address, hmenum.Parameter(parameter), writeValue, hmenum.CommandPriorityHigh); err != nil {
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
		// Strict edit-lock enforcement for configuration paramsets, mirroring
		// the REST enforceEditLock gate and the WS paramset.put handler:
		// MASTER (and LINK, though parseParamsetKey keeps LINK unreachable
		// here) are configuration writes that require holding the per-channel
		// edit lock, so a write another editor's open session is protected
		// against cannot arrive through this sibling instead. Verify returns
		// false when no session holds the lock and when the token does not
		// match, so the lock is required always — not only under contention.
		// A nil verifier disables enforcement (test-only escape hatch).
		if d.EditLocks != nil && (key == hmenum.ParamsetKeyMaster || key == hmenum.ParamsetKeyLink) {
			lockKey := editLockKey(address, key)
			if !d.EditLocks.Verify(lockKey, strings.TrimSpace(in.EditToken)) {
				return nil, writeParamsetOut{}, fmt.Errorf(
					"edit lock required for %s write on %s; open an edit session and pass edit_token", key, address)
			}
		}
		// The paramset domain this tool writes through records the change
		// itself, with the per-parameter before/after pairs. A row added
		// here would be a second entry for one write — the change history
		// would show the same write twice, once with values and once
		// without, so an operator counting or diffing changes sees one that
		// never happened.
		//
		// Provenance rides the request context instead, the same way the
		// program trigger below stamps itself: without it an assistant's
		// write and an operator's write are indistinguishable afterwards,
		// and "who changed this" is the first question asked about one.
		ctx = reqctx.WithOperation(ctx, "mcp:paramset-write")
		if err := d.Paramsets.PutParamset(ctx, address, key, in.Values); err != nil {
			return nil, writeParamsetOut{}, fmt.Errorf("write paramset: %w", err)
		}
		return nil, writeParamsetOut{OK: true}, nil
	})
}

// registerOpenEditSession lets an MCP caller acquire the same per-channel
// edit lock registerWriteParamset enforces for MASTER/LINK writes —
// mirroring REST's POST /sessions/edit and the WS config.session.open
// command, since MCP has no HTTP session of its own to piggyback a lock
// acquisition onto. Restricted to MASTER: LINK writes are unreachable
// through write_paramset today (parseParamsetKey has no LINK case), so
// there is nothing this tool's LINK path could ever unlock.
func registerOpenEditSession(s *mcpsdk.Server, d Deps) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "open_edit_session",
		Description: "Acquire the edit lock for a device's MASTER paramset, required before write_paramset can write it. Returns edit_token to pass on the write.",
	}, func(_ context.Context, _ *mcpsdk.CallToolRequest, in openEditSessionIn) (*mcpsdk.CallToolResult, openEditSessionOut, error) {
		address := strings.TrimSpace(in.Address)
		if address == "" {
			return nil, openEditSessionOut{}, errors.New("address is required")
		}
		if strings.ToUpper(strings.TrimSpace(in.Key)) != "MASTER" {
			return nil, openEditSessionOut{}, fmt.Errorf("key must be MASTER, got %q", in.Key)
		}
		lockKey := editLockKey(address, hmenum.ParamsetKeyMaster)
		token, expires, ok := d.EditSessions.Open(lockKey, "mcp")
		if !ok {
			return nil, openEditSessionOut{}, fmt.Errorf("edit lock for %s (MASTER) is already held by another session", address)
		}
		return nil, openEditSessionOut{EditToken: token, Expires: expires.UTC().Format(time.RFC3339)}, nil
	})
}

// registerCloseEditSession releases a lock [registerOpenEditSession]
// acquired, mirroring REST's DELETE /sessions/edit. Not calling it is not
// a correctness bug — the lock's own TTL (handlers.EditSessionTTL) reclaims
// it — but leaves the channel locked to every other editor for up to that
// TTL after an assistant's session ends.
func registerCloseEditSession(s *mcpsdk.Server, d Deps) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "close_edit_session",
		Description: "Release an edit lock acquired by open_edit_session.",
	}, func(_ context.Context, _ *mcpsdk.CallToolRequest, in closeEditSessionIn) (*mcpsdk.CallToolResult, closeEditSessionOut, error) {
		address := strings.TrimSpace(in.Address)
		if address == "" {
			return nil, closeEditSessionOut{}, errors.New("address is required")
		}
		if strings.ToUpper(strings.TrimSpace(in.Key)) != "MASTER" {
			return nil, closeEditSessionOut{}, fmt.Errorf("key must be MASTER, got %q", in.Key)
		}
		lockKey := editLockKey(address, hmenum.ParamsetKeyMaster)
		ok := d.EditSessions.Close(lockKey, strings.TrimSpace(in.EditToken))
		return nil, closeEditSessionOut{OK: ok}, nil
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
				Action:    audit.ActionProgramExecute,
				Note:      "program=" + programID + " via mcp",
			})
		}
		return nil, triggerProgramOut{OK: true}, nil
	})
}
