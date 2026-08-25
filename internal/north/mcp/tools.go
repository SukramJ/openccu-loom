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
		Name:      d.Name(),
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
	// Gauges carries the daemon's numeric health readings, keyed by gauge
	// name. Omitted when none are registered. It is where the latency legs
	// live: `ws.heartbeat_rtt_ms` is the distance to the client asking,
	// `mqtt.publish_ack_ms` the distance to the broker, and the CCU's own
	// round-trip surfaces per central as `connection_latency_ms` on the hub
	// metrics rather than here.
	Gauges map[string]float64 `json:"gauges,omitempty"`
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
		registerGetDeviceSchedule(s, d)
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
	// Operational-domain read tools each project a single narrow REST
	// facade the daemon already wires for its own routes: the Matter
	// bridge, stored CCU backups, and the add-on self-updater. The
	// bridge and the self-updater are each one instance per daemon, not
	// one per CCU, so neither tool takes central_name; list_backups
	// does, since each stored archive names the central it backs up.
	if d.Matter != nil {
		registerGetMatterStatus(s, d)
	}
	if d.Backups != nil {
		registerListBackups(s, d)
	}
	if d.AddonUpdate != nil {
		registerGetAddonUpdateStatus(s, d)
	}
	// The MCP/REST parity backlog tools (tests/contract/mcp_rest_parity_test.go
	// restDomainsAwaitingMCPTools): groups, areas, interfaces, history,
	// visibility, energy, links, schedules. Each projects a single narrow
	// REST facade; a nil seam leaves its tool unregistered.
	if d.Groups != nil {
		registerListGroups(s, d)
	}
	if d.Areas != nil {
		registerListAreas(s, d)
	}
	if d.Interfaces != nil {
		registerListInterfaces(s, d)
	}
	if d.History != nil {
		registerGetMeasurements(s, d)
	}
	if d.Visibility != nil && d.Centrals != nil {
		registerListHiddenParameters(s, d)
	}
	if d.Energy != nil {
		registerGetEnergy(s, d)
	}
	if d.Links != nil {
		registerListLinks(s, d)
	}
	if d.Schedules != nil {
		registerListSchedules(s, d)
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
		if want != "" && !centralKnown(d, want) {
			return nil, listDevicesOut{}, errUnknownCentral(d, want)
		}
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
		Description: "Report the daemon's health: an overall status, per-component status (CCU connectivity, subsystems), and the numeric gauges — including the client, broker and controller latency legs.",
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
		if g := d.Health.Gauges(); len(g) > 0 {
			out.Gauges = g
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

type openEditSessionIn struct {
	Address string `json:"address" jsonschema:"the channel address to lock, e.g. 0001D3C99C1234:1"`
	Key     string `json:"key" jsonschema:"the paramset key to lock: MASTER"`
}

type openEditSessionOut struct {
	// Opened is false when another live session already holds the lock;
	// Token is then empty and the caller must retry once it is released
	// (Close, or the registry's own TTL) instead of writing.
	Opened  bool      `json:"opened"`
	Token   string    `json:"token,omitempty"`
	Key     string    `json:"key"`
	Expires time.Time `json:"expires,omitzero"`
}

type closeEditSessionIn struct {
	Address   string `json:"address" jsonschema:"the channel address the session was opened for"`
	Key       string `json:"key" jsonschema:"the paramset key the session was opened for: MASTER"`
	EditToken string `json:"edit_token" jsonschema:"the token open_edit_session returned"`
}

type closeEditSessionOut struct {
	// Closed is false when the key was already unheld or edit_token did
	// not match its current holder — the lock is not this caller's to
	// release either way, so nothing was changed.
	Closed bool `json:"closed"`
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
	if d.EditLocks != nil {
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
			lockKey := "channel:" + address + ":" + string(key)
			if !d.EditLocks.Verify(lockKey, strings.TrimSpace(in.EditToken)) {
				return nil, writeParamsetOut{}, fmt.Errorf(
					"edit lock required for %s write on %s; open an edit session and pass edit_token", key, address,
				)
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

// editLockSubject is the fixed identity open_edit_session records on
// the lock it opens. REST/WS sessions record the caller's authenticated
// subject; MCP write tools have no per-call human identity of their
// own (the mount authenticates the transport, not each tool call — see
// the package doc comment), so every MCP-opened lock is attributed to
// this constant rather than left blank or fabricated.
const editLockSubject = "mcp"

// registerOpenEditSession implements `open_edit_session`, the MCP-side
// counterpart of REST `POST /sessions/edit` and the seam write_paramset's
// MASTER/LINK gate needs: without a way to mint a token, that gate makes
// every MASTER write unreachable over MCP even though it never rejects a
// legitimate one over REST or WS.
func registerOpenEditSession(s *mcpsdk.Server, d Deps) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name: "open_edit_session",
		Description: "Acquire the per-channel edit lock a MASTER write_paramset call requires. " +
			"Returns a token to pass as write_paramset's edit_token, and the deadline the lock " +
			"expires at if not renewed. Call close_edit_session when done, or let it expire.",
	}, func(_ context.Context, _ *mcpsdk.CallToolRequest, in openEditSessionIn) (*mcpsdk.CallToolResult, openEditSessionOut, error) {
		address := strings.TrimSpace(in.Address)
		if address == "" {
			return nil, openEditSessionOut{}, errors.New("address is required")
		}
		key, ok := parseParamsetKey(in.Key)
		if !ok || key != hmenum.ParamsetKeyMaster {
			return nil, openEditSessionOut{}, fmt.Errorf("key must be MASTER, got %q", in.Key)
		}
		lockKey := "channel:" + address + ":" + string(key)
		lock, opened := d.EditLocks.Open(lockKey, editLockSubject)
		if !opened {
			return nil, openEditSessionOut{Key: string(key)},
				fmt.Errorf("edit lock for %s %s is already held by another session; retry once it is released or expires", address, key)
		}
		return nil, openEditSessionOut{Opened: true, Token: lock.Token, Key: string(key), Expires: lock.Expires}, nil
	})
}

// registerCloseEditSession implements `close_edit_session`, releasing a
// lock open_edit_session opened. Mirrors REST `DELETE /sessions/edit`.
func registerCloseEditSession(s *mcpsdk.Server, d Deps) {
	mcpsdk.AddTool(s, &mcpsdk.Tool{
		Name:        "close_edit_session",
		Description: "Release an edit lock opened by open_edit_session, e.g. once a write_paramset MASTER write completes.",
	}, func(_ context.Context, _ *mcpsdk.CallToolRequest, in closeEditSessionIn) (*mcpsdk.CallToolResult, closeEditSessionOut, error) {
		address := strings.TrimSpace(in.Address)
		if address == "" {
			return nil, closeEditSessionOut{}, errors.New("address is required")
		}
		key, ok := parseParamsetKey(in.Key)
		if !ok || key != hmenum.ParamsetKeyMaster {
			return nil, closeEditSessionOut{}, fmt.Errorf("key must be MASTER, got %q", in.Key)
		}
		token := strings.TrimSpace(in.EditToken)
		if token == "" {
			return nil, closeEditSessionOut{}, errors.New("edit_token is required")
		}
		lockKey := "channel:" + address + ":" + string(key)
		closed := d.EditLocks.Close(lockKey, token)
		return nil, closeEditSessionOut{Closed: closed}, nil
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
