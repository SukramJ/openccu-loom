// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package mcp exposes the OpenCCU-Loom domain to LLM agents over the
// Model Context Protocol, as a north-bound adapter (ADR 0025). It is a
// thin projection of the same domain the REST surface serves: every
// tool is scoped per central, reads are always available, and writes
// are registered only when the operator opts in twice (Enabled +
// AllowWrites). Authorization comes from the identity-resolve and
// role-gate middleware the composition root wraps the Streamable-HTTP
// handler in at its mount (daemon_rest_mount.go). It does not come from
// the REST listener as such — a mount that skips that wrapper is
// unauthenticated, which is why the wrapper and not the listener is
// named here. That mount gates the whole tool set at a single role, so
// the few tools whose REST twin is mounted With(admin) re-check the
// caller's role on the resolved identity themselves (callerHasRole).
package mcp

import (
	"context"
	"net/http"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
)

// CentralLister enumerates the configured CCUs — the scoping dimension
// every central-touching tool names explicitly (ADR 0002).
type CentralLister interface {
	Names() []string
}

// DeviceLister is the read surface over the device model. It mirrors
// the REST DeviceIndex contract so both adapters project the same data.
type DeviceLister interface {
	Devices() []*device.Device
	Device(address string) (*device.Device, bool)
	// CentralOf returns the owning CCU name, or "" when the device is
	// unknown.
	CentralOf(address string) string
}

// ValueWriter pushes a value to the CCU. Same contract as the REST
// DataPointWriter; only reached by the write-gated set_datapoint tool.
type ValueWriter interface {
	SetValue(
		ctx context.Context,
		address string,
		parameter hmenum.Parameter,
		value any,
		priority hmenum.CommandPriority,
	) error
}

// ParamsetService reads and writes device paramsets (MASTER / VALUES).
// Same contract as the REST ParamsetService; the read half backs
// read_paramset, the write half backs the gated write_paramset.
type ParamsetService interface {
	GetParamset(ctx context.Context, address string, key hmenum.ParamsetKey) (map[string]any, error)
	PutParamset(ctx context.Context, address string, key hmenum.ParamsetKey, values map[string]any) error
}

// HealthReader exposes the daemon's component-health view (CCU
// connectivity and subsystem status). Same contract as the REST
// HealthReader.
type HealthReader interface {
	Overall() health.Status
	Snapshot() []health.Component
}

// EditLockVerifier reports whether `token` currently holds the edit
// lock for a resource `key`. It mirrors the strict edit-lock gate the
// REST `PUT /devices/{addr}/paramsets/{key}` handler (enforceEditLock)
// and the WS `paramset.put` command enforce for MASTER/LINK writes, so
// a configuration write over MCP obeys the same lock a human editor's
// open session is protected by. *handlers.EditSessions satisfies it.
// A nil verifier disables enforcement — a test-only escape hatch; the
// production mount is expected to wire the shared registry.
type EditLockVerifier interface {
	Verify(key, token string) bool
}

// EditLockManager extends [EditLockVerifier] with the operations
// open_edit_session / close_edit_session need to mint and release a
// lock. Without it, write_paramset's MASTER/LINK gate (which only ever
// consults Verify) has no MCP-side way to obtain the token it demands
// — every REST/WS session-open path is closed to an MCP-only client, so
// a MASTER write is permanently unreachable over this transport even
// though the REST and WS siblings can perform it. *handlers.EditSessions
// (internal/north/rest/handlers/session.go), the same registry REST and
// WS share, satisfies it — the production composition root wires the
// identical value it already passes as EditLocks, so no extra wiring is
// needed to light this up. A nil manager disables both the gate and the
// two session tools (test-only escape hatch).
// loom:reachable:reason="the declared type of Deps.EditLocks, which the composition root fills at cmd/openccu-loom/daemon_rest_mount.go with the shared *handlers.EditSessions registry; an interface reached through its implementation, which the analyzer's type heuristic cannot follow"
type EditLockManager interface {
	EditLockVerifier
	// Open acquires the lock for key on behalf of subject. The second
	// return is false when another live session already holds it.
	Open(key, subject string) (handlers.EditLock, bool)
	// Close releases the lock for key when token matches its current
	// holder. Returns false when the key is unheld or token does not
	// match — same semantics as [handlers.EditSessions.Close].
	Close(key, token string) bool
}

// HubResolver resolves a central's hub model by name — the seam the
// gated trigger_program tool uses to find and run a CCU program.
// *central.Registry satisfies it.
type HubResolver interface {
	HubFor(centralName string) *hub.Hub
}

// IncidentsReader projects the cross-central reliability incident journal —
// the same enriched, source-tagged list the REST GET /incidents handler
// serves. *adapter.IncidentsStoreReader satisfies it, so MCP and REST
// expose byte-identical incident data.
type IncidentsReader interface {
	Incidents() []hmapi.Incident
}

// MatterStatusReader is an alias for the narrow facade GET
// /matter/status reads through (internal/north/rest/handlers). Reusing
// it rather than declaring a second copy keeps the two adapters
// consuming the same seam, so a daemon that wires REST's Matter status
// reader also lights up get_matter_status for free.
type MatterStatusReader = handlers.MatterStatusReader

// AddonUpdateService is an alias for the narrow facade the REST addon
// self-update routes read and act through (internal/north/rest/handlers).
// get_addon_update_status only ever calls Status(); the write half
// (Check/InstallAsync) is intentionally not reached from MCP.
type AddonUpdateService = handlers.AddonUpdateService

// GroupsReader is an alias for the narrow facade GET /api/v1/groups
// reads through (internal/north/rest/handlers). It is already scoped
// to one method, so MCP reuses it directly rather than declaring a
// second copy — a daemon that wires REST's groups reader lights up
// list_groups for free.
type GroupsReader = handlers.GroupsReader

// AreaLister is the read half of [handlers.AreaAdmin] — list_areas
// needs the area roster and its room assignments, nothing else, so it
// depends on this narrower seam rather than the full CRUD facade.
// *sqlite.AreaStore satisfies it directly.
//
// loom:reachable:reason="the declared type of Deps.Areas, filled at cmd/openccu-loom/daemon_rest_mount.go with the same area service REST receives; an interface reached only by assignment, which the analyzer's type heuristic cannot follow"
type AreaLister interface {
	GetAll(ctx context.Context) ([]sqlite.AreaRow, error)
	ListAssignments(ctx context.Context) ([]sqlite.RoomAreaRow, error)
}

// InterfaceLister is the read half of [restapi.InterfaceIndex] —
// list_interfaces only ever calls Interfaces(). Reconnect is
// deliberately not projected here: it actuates the radio link, the
// same argument that keeps install-mode off the MCP surface.
//
// loom:reachable:reason="the declared type of Deps.Interfaces, filled at cmd/openccu-loom/daemon_rest_mount.go with the same interface index REST receives; an interface reached only by assignment, which the analyzer's type heuristic cannot follow"
type InterfaceLister interface {
	Interfaces() []hmapi.InterfaceState
}

// HistoryReader is the read seam get_measurements calls through. It
// mirrors [handlers.HistoryService] field-for-field without importing
// it as an alias, since the handler's query/bucket types are reused
// directly as the tool's input/output shapes.
//
// loom:reachable:reason="the declared type of Deps.History, filled at cmd/openccu-loom/daemon_rest_mount.go with the history handler adapter; an interface reached only by assignment, which the analyzer's type heuristic cannot follow"
type HistoryReader interface {
	Query(ctx context.Context, q handlers.HistoryQuery) ([]handlers.HistoryBucket, string, error)
}

// VisibilityLister is the read half of the un-ignore visibility store —
// list_hidden_parameters needs only the per-central pattern listing,
// not the write/candidate machinery the REST facade also carries.
// *sqlite.VisibilityUnIgnoreStore satisfies it directly.
//
// loom:reachable:reason="the declared type of Deps.Visibility, filled at cmd/openccu-loom/daemon_rest_mount.go with the un-ignore store; an interface reached only by assignment, which the analyzer's type heuristic cannot follow"
type VisibilityLister interface {
	List(ctx context.Context, centralName string) ([]sqlite.UnIgnoreEntry, error)
}

// EnergyReader is an alias for the narrow facade GET /api/v1/energy
// reads through (internal/north/rest/handlers). Already scoped to one
// method, so get_energy reuses it directly.
type EnergyReader = handlers.EnergyService

// LinkLister is the read seam list_links calls through — the same
// method the REST global links overview (GET /api/v1/links) uses.
//
// loom:reachable:reason="the declared type of Deps.Links, filled at cmd/openccu-loom/daemon_rest_mount.go with the links domain; an interface reached only by assignment, which the analyzer's type heuristic cannot follow"
type LinkLister interface {
	ListAllLinks(ctx context.Context, centralName, locale string) ([]hmapi.Link, error)
}

// ScheduleLister is the read seam list_schedules calls through — the
// fleet-wide schedule overview, the same method GET /api/v1/schedules
// uses.
//
// loom:reachable:reason="the declared type of Deps.Schedules, filled at cmd/openccu-loom/daemon_rest_mount.go with the schedules domain; an interface reached only by assignment, which the analyzer's type heuristic cannot follow"
type ScheduleLister interface {
	ListScheduleDevices(ctx context.Context) ([]hmapi.ScheduleDeviceSummary, error)
}

// BackupLister is the read half of [interfaces.BackupService] —
// list_backups needs nothing else, so it depends on this narrower seam
// rather than the full read/write facade. Any concrete backup service
// the composition root already wires for REST satisfies it directly.
//
// loom:reachable:reason="the Deps.Backups seam the composition root satisfies with *adapter.BackupAdapter; an interface reached only by assignment, which the analyzer's type heuristic cannot see used"
type BackupLister interface {
	List(ctx context.Context) ([]hmapi.BackupEntry, error)
}

// Deps is the wiring surface. Writer may be nil (no writes available).
// Audit may be nil (no change-log surface); when present it serves both
// the read tool (List) and records MCP-origin writes (Record).
type Deps struct {
	Centrals  CentralLister
	Devices   DeviceLister
	Writer    ValueWriter
	Paramsets ParamsetService
	Health    HealthReader
	Hubs      HubResolver
	Audit     audit.Recorder
	Incidents IncidentsReader
	// EditLocks gates MASTER/LINK paramset writes through write_paramset
	// on holding the edit lock, exactly as the REST and WS siblings do,
	// and backs the open_edit_session / close_edit_session tools that
	// let an MCP-only client obtain that lock in the first place. Nil
	// disables enforcement and both session tools (test-only escape
	// hatch); the production composition root wires the shared
	// *handlers.EditSessions instance.
	EditLocks EditLockManager
	// Alarm / AlarmControl project the alarm system; Security projects
	// the Security & Safety domain. Each is optional: a nil seam leaves
	// its tools unregistered rather than advertising a tool that
	// cannot answer.
	Alarm        AlarmReader
	AlarmControl AlarmController
	Security     SecurityReader
	// Matter backs get_matter_status: the bridge's enabled/listening
	// state, commissioned-fabric and enabled-exposure counts, and
	// whether a commissioning window is currently open. Nil leaves the
	// tool unregistered rather than reporting a disabled bridge as if
	// it had been asked and answered "off".
	Matter MatterStatusReader
	// Backups backs list_backups: the locally-stored CCU archives, the
	// same read the REST GET /backups handler serves. Nil leaves the
	// tool unregistered.
	Backups BackupLister
	// AddonUpdate backs get_addon_update_status: current/available
	// version and install-progress state for the CCU add-on
	// self-updater (ADR 0057). Nil leaves the tool unregistered — never
	// registered as "unsupported", which would be indistinguishable
	// from a real answer.
	AddonUpdate AddonUpdateService
	// Groups backs list_groups: the heating-group roster, the same read
	// the REST GET /groups handler serves. Nil leaves the tool
	// unregistered.
	Groups GroupsReader
	// Areas backs list_areas: operator-defined room groupings with
	// their assigned rooms. Nil leaves the tool unregistered.
	Areas AreaLister
	// Interfaces backs list_interfaces: per-interface connectivity and
	// radio-quality state (read-only; reconnect is not projected). Nil
	// leaves the tool unregistered.
	Interfaces InterfaceLister
	// History backs get_measurements: server-bucketed data-point
	// history, the same aggregation GET /history serves. Nil leaves
	// the tool unregistered.
	History HistoryReader
	// Visibility backs list_hidden_parameters: the persisted un-ignore
	// patterns that promote otherwise-hidden parameters into the
	// visible surface. Nil leaves the tool unregistered.
	Visibility VisibilityLister
	// Energy backs get_energy: per-device power/energy aggregation,
	// the same read GET /energy serves. Nil leaves the tool
	// unregistered.
	Energy EnergyReader
	// Links backs list_links: the global direct-link overview across
	// every configured central. Nil leaves the tool unregistered.
	Links LinkLister
	// Schedules backs list_schedules: the fleet-wide week-schedule
	// overview, the same read GET /schedules serves. Nil leaves the
	// tool unregistered.
	Schedules   ScheduleLister
	AllowWrites bool
	Version     string
}

// NewServer builds the MCP server and registers the tool set. Read
// tools are always registered (each gated on its own dependency being
// wired); write tools only when AllowWrites is set, and each still gates
// on its own dependency so a partial wiring never exposes a half-tool.
func NewServer(d Deps) *mcpsdk.Server {
	version := d.Version
	if version == "" {
		version = "0.0.0"
	}
	s := mcpsdk.NewServer(&mcpsdk.Implementation{
		Name:    "openccu-loom",
		Version: version,
	}, nil)
	registerReadTools(s, d)
	if d.AllowWrites {
		registerWriteTools(s, d)
	}
	return s
}

// Handler returns the Streamable-HTTP handler to mount on the REST
// listener (e.g. at /mcp). The server is built once and shared by every
// request.
//
// The transport runs stateless: each POST is served by its own short-lived
// session that is closed when the request completes. Two properties follow,
// and this adapter depends on both.
//
// Authorization is per request. The mount wraps this handler in the daemon's
// identity-resolve and role-gate middleware, so a tool handler's context is
// the calling request's context and the tools that re-check a role
// (callerHasRole) judge the caller. A retained session would instead hand
// every later call the context captured when the session was opened: an
// identity that has since been demoted, or that belongs to whoever first used
// the session id, would keep its old privileges for the session's lifetime.
//
// Nothing accumulates. Retained sessions are only released by an explicit
// DELETE, which a client that simply restarts never sends, so every
// reconnect would leave a session and its reader goroutine behind for the
// daemon's lifetime.
//
// The cost is that server-initiated traffic (GET event streams, sampling,
// elicitation) is unavailable — this adapter exposes plain
// request/response tools, so nothing here needs it.
func Handler(d Deps) http.Handler {
	srv := NewServer(d)
	return mcpsdk.NewStreamableHTTPHandler(
		func(*http.Request) *mcpsdk.Server { return srv },
		&mcpsdk.StreamableHTTPOptions{Stateless: true},
	)
}
