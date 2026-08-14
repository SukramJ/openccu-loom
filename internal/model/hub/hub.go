// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hub

import (
	"context"
	"errors"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/SukramJ/openccu-loom/internal/model/naming"
	"github.com/SukramJ/openccu-loom/internal/payload"
)

// RoomMutator is the optional CCU-side write-path for room
// assignments. Implementations dispatch a Rega script.
type RoomMutator interface {
	SetDeviceRooms(ctx context.Context, deviceAddress string, rooms []string) error
}

// FunctionMutator is the optional CCU-side write-path for function
// (Gewerk) assignments. Implementations dispatch a Rega script.
type FunctionMutator interface {
	SetDeviceFunctions(ctx context.Context, deviceAddress string, functions []string) error
}

// RoomAdmin is the optional CCU-side write-path for room *entity*
// lifecycle (create / rename / delete), as opposed to per-device
// assignment. Implementations dispatch Rega scripts. The wired
// RoomMutator may also satisfy this; Hub type-asserts for it.
type RoomAdmin interface {
	// CreateRoom creates a room and returns its new CCU object ID.
	CreateRoom(ctx context.Context, name string) (int, error)
	RenameRoom(ctx context.Context, oldName, newName string) error
	DeleteRoom(ctx context.Context, name string) error
}

// FunctionAdmin is the subsection (Gewerk) counterpart of RoomAdmin.
type FunctionAdmin interface {
	CreateFunction(ctx context.Context, name string) (int, error)
	RenameFunction(ctx context.Context, oldName, newName string) error
	DeleteFunction(ctx context.Context, name string) error
}

// UserLevelReader reads a CCU user's permission level (UserLevel) via a
// privileged ReGa script. Used by the CCU authentication provider to map
// a CCU user to a Loom role. The wired RoomMutator may also satisfy it.
type UserLevelReader interface {
	// GetUserLevel returns the user's UserLevel (8/2/1/0) or -1 when the
	// user does not exist. username must be pre-sanitised by the caller.
	GetUserLevel(ctx context.Context, username string) (int, error)
}

// ErrNoUserLevelReader is returned by UserLevelRemote when no CCU-side
// reader is wired (e.g. the central has no JSON-RPC writer yet).
var ErrNoUserLevelReader = errors.New("hub: no user-level reader configured")

// ErrRoomExists / ErrFunctionExists signal a name collision on create.
var (
	ErrRoomExists     = errors.New("hub: room already exists")
	ErrFunctionExists = errors.New("hub: function already exists")
	// ErrRoomNotFound / ErrFunctionNotFound signal a missing target on
	// rename / delete.
	ErrRoomNotFound     = errors.New("hub: room not found")
	ErrFunctionNotFound = errors.New("hub: function not found")
	// ErrCentralAmbiguous / ErrCentralNotFound are raised by the
	// room/function admin path when a multi-CCU operation omits the
	// central name, or names an unknown one. They live here (not in the
	// adapter) so the REST handler can map them without importing the
	// adapter package (which would cycle through the ws bridge).
	ErrCentralAmbiguous = errors.New("hub: central name required (multiple CCUs)")
	ErrCentralNotFound  = errors.New("hub: central not found")
)

// SysvarCreateSpec carries every field `POST /sysvars` can set on a new
// CCU system variable. Empty string fields adopt the CCU default; the
// value-label fields (ValueName0/1) apply to binary (BOOL/ALARM)
// variables only and default to the CCU's own "false"/"true" text when
// left empty.
type SysvarCreateSpec struct {
	Name        string
	ValueType   string
	Unit        string
	Min         string
	Max         string
	Description string
	ValueList   []string
	ValueName0  string
	ValueName1  string
	// Channel optionally binds the new variable to a device channel
	// ("ADDR:idx", the CCU "Kanalzuordnung"). Empty leaves the variable
	// unassigned. The adapter resolves the address to the channel's ReGa
	// ise id before it reaches the CCU.
	Channel string
}

// SysvarUpdateSpec carries every field `PATCH /sysvars/{name}` can change
// on an existing variable without altering its type. Empty string fields
// leave the corresponding CCU metadata untouched; a non-empty NewName
// renames the variable. Visible and Logged are tri-state: nil leaves the
// flag as-is, a non-nil pointer sets it.
type SysvarUpdateSpec struct {
	Name        string // current (target) sysvar name
	NewName     string
	Unit        string
	Min         string
	Max         string
	Description string
	ValueList   []string
	ValueName0  string
	ValueName1  string
	Visible     *bool
	Logged      *bool
	// Channel is the tri-state channel-assignment control ("Kanalzuordnung").
	// nil leaves the assignment untouched; a non-nil pointer sets it — an
	// empty string clears the assignment (ise id -1), a channel address
	// ("ADDR:idx") assigns it. The adapter resolves the address to the
	// channel's ReGa ise id before it reaches the CCU.
	Channel *string
}

// SysvarMutator is the optional CCU-side write-path for sysvars.
// Implementations dispatch ReGa scripts; nil leaves the hub in
// in-memory-only mode (Create/Delete return ErrNoSysvarMutator).
type SysvarMutator interface {
	CreateSysvar(ctx context.Context, spec SysvarCreateSpec) error
	UpdateSysvar(ctx context.Context, spec SysvarUpdateSpec) error
	DeleteSysvar(ctx context.Context, name string) error
}

// ErrNoSysvarMutator is returned by Hub.Create/DeleteSysvar when no
// CCU-side mutator is wired. The REST handler surfaces this as a
// 503 so the SPA can show "feature not configured" instead of a
// generic upstream error.
var ErrNoSysvarMutator = errors.New("hub: no sysvar mutator configured")

// SysvarUsage is one CCU program that references a system variable.
type SysvarUsage struct {
	ID     string
	Name   string
	Active bool
}

// SysvarUsageReader is the optional CCU-side reader for the programs that
// reference a sysvar (resolved from the CCU's program rules).
// It is intentionally NOT part of [SysvarMutator] so in-memory Mutator
// fakes keep compiling; SetMutator wires it opportunistically.
type SysvarUsageReader interface {
	SysvarUsagePrograms(ctx context.Context, name string) ([]SysvarUsage, error)
}

// ErrNoSysvarUsageReader is returned by Hub.SysvarUsageRemote when no
// CCU-side usage reader is wired. The REST handler surfaces this as 503.
var ErrNoSysvarUsageReader = errors.New("hub: no sysvar usage reader configured")

// ErrSysvarChannelUnknown is returned when a sysvar create/patch carries a
// channel address that the CCU cannot resolve to a ReGa ise id. The REST
// handler surfaces it as a 422 (bad request field) rather than a 502, since
// the fault is the caller's channel address, not the upstream CCU.
var ErrSysvarChannelUnknown = errors.New("hub: sysvar channel address not resolvable")

// ErrNoRoomMutator is the room-side analogue.
var ErrNoRoomMutator = errors.New("hub: no room mutator configured")

// ErrNoFunctionMutator is the function-side analogue.
var ErrNoFunctionMutator = errors.New("hub: no function mutator configured")

// ErrNoBackupTrigger is returned by TriggerBackupRemote / RestoreBackupRemote
// when the CCU-side bridge is missing.
var ErrNoBackupTrigger = errors.New("hub: no backup trigger configured")

// ErrNoFirmwareUpdater is returned when global firmware-update
// orchestration was not wired.
var ErrNoFirmwareUpdater = errors.New("hub: no firmware updater configured")

// ErrNoInboxAccepter is returned when the CCU-side inbox path was
// not wired.
var ErrNoInboxAccepter = errors.New("hub: no inbox accepter configured")

// ErrProgramNotFound is returned by [Hub.DeleteProgramRemote] when no
// program with the given ID exists in the hub cache, and by the writer
// when the CCU declines the delete (the id no longer resolves to a
// program object). The REST handler surfaces it as 404.
var ErrProgramNotFound = errors.New("hub: program not found")

// ErrProgramDeleteUnsupported is returned by [Program.Delete] when the
// wired [ProgramWriter] does not implement [ProgramDeleter] (execute-only
// mode). The REST handler surfaces it as 503.
var ErrProgramDeleteUnsupported = errors.New("hub: program deletion not supported by writer")

// BackupTrigger initiates a CCU backup. Implementations dispatch
// `create_backup_start` / `create_backup_status` Rega scripts.
type BackupTrigger interface {
	TriggerBackup(ctx context.Context) error
	BackupStatus(ctx context.Context) (string, error)
}

// FirmwareUpdater is the CCU-global update trigger. Per-device
// firmware updates run through the device backend.
type FirmwareUpdater interface {
	TriggerFirmwareUpdate(ctx context.Context) error
}

// InboxAccepter promotes an inbox device into the registry.
type InboxAccepter interface {
	AcceptDeviceInInbox(ctx context.Context, deviceAddress string) error
}

// DataFetcher is the interface for fetching hub-level data from the
// CCU backend. Coordinators implement this; the Hub delegates fetch
// operations to it so the model layer stays backend-agnostic.
type DataFetcher interface {
	// FetchAlarmMessages retrieves the current alarm message list.
	FetchAlarmMessages(ctx context.Context) ([]AlarmMessage, error)
	// FetchInboxDevices retrieves devices waiting in the inbox.
	FetchInboxDevices(ctx context.Context) ([]InboxDevice, error)
}

// Hub aggregates all CCU-level entities for one central: programs,
// system variables, messages, metrics, inbox, install-mode state,
// connectivity sensors, and update entities. The north-bound
// adapters read from Hub to present a unified "gateway" view.
type Hub struct {
	// ServiceRegistry implements the write-half of [payload.Source].
	// Hub is read-only from the Source perspective; write operations
	// delegate to sub-aggregates (Programs, Sysvars, Messages, …) or
	// to the mutator interfaces wired on the Hub itself.
	payload.ServiceRegistry

	CentralName     string
	Messages        *AlarmMessages
	ServiceMessages *ServiceMessages
	Metrics         *Metrics
	Inbox           *Inbox
	// Update holds firmware-update state for the central.
	Update *Update
	// SysvarMutator wires the CCU-side create/delete path. Optional;
	// the daemon assigns it after constructing the hub.
	SysvarMutator SysvarMutator
	// RoomMutator dispatches CCU-side room assignment writes.
	RoomMutator RoomMutator
	// FunctionMutator dispatches CCU-side function (Gewerk)
	// assignment writes.
	FunctionMutator FunctionMutator
	// BackupTrigger initiates a CCU backup; nil → "feature unavailable".
	BackupTrigger BackupTrigger
	// FirmwareUpdater triggers global OpenCCU firmware update flows.
	FirmwareUpdater FirmwareUpdater
	// InboxAccepter promotes an inbox device.
	InboxAccepter InboxAccepter

	// sysvarUsageReader is an optional CCU-side reader for the programs
	// that reference a sysvar. Wired opportunistically by SetMutator when
	// the mutator also implements [SysvarUsageReader]; nil leaves
	// SysvarUsageRemote returning [ErrNoSysvarUsageReader].
	sysvarUsageReader SysvarUsageReader

	mu             sync.RWMutex
	programs       map[string]*Program
	sysvars        map[string]*Sysvar
	installModeDPs map[string]*InstallMode // keyed by InterfaceID
	// connectivity holds the per-interface reachability aggregate.
	// Populated via [Hub.SetConnectivity] once the adapter layer creates it.
	connectivity *Connectivity

	// includeInternalDefault is the per-central northbound default that
	// governs whether internal (Tmp_*, prgEnergyCounter_*) programs appear
	// in list responses that omit an explicit include_internal parameter.
	// The hub always holds the full program set (internal ones included);
	// this flag only steers the default delivery filter, mirroring the
	// CCU WebUI's footerBtnShowSystemPrograms default. Set during hub
	// wiring from the central's include_internal_programs config; read on
	// every programs-list request, so it is atomic for the lock-free read
	// path.
	includeInternalDefault atomic.Bool

	// Registration observers. The HubMQTTPublisher subscribes once at
	// daemon start and reacts to every later PutSysvar/PutProgram so
	// sysvars/programs loaded by the first ReGa refresh — which runs
	// AFTER the publisher's Start — still get discovery+state topics.
	// Slots are sparse: an unsubscribed entry is nil, the next
	// registration appends rather than reusing the slot.
	sysvarObservers  []func(*Sysvar)
	programObservers []func(*Program)
}

// NewHub constructs a Hub keyed by centralName. Aggregates are
// initialised empty; callers wire acknowledgers by assigning
// `Messages.Ack` / `ServiceMessages.Ack` after construction.
func NewHub(centralName string) *Hub {
	return &Hub{
		CentralName:     centralName,
		Messages:        NewAlarmMessages(nil),
		ServiceMessages: NewServiceMessages(nil),
		Metrics:         NewMetrics(),
		Inbox:           NewInbox(),
		Update:          NewUpdate(),
		programs:        make(map[string]*Program),
		sysvars:         make(map[string]*Sysvar),
		installModeDPs:  make(map[string]*InstallMode),
	}
}

// SetIncludeInternalProgramsDefault records the per-central northbound
// default for internal-program visibility. The hub keeps every program;
// this only governs list responses that omit an explicit override.
func (h *Hub) SetIncludeInternalProgramsDefault(v bool) {
	h.includeInternalDefault.Store(v)
}

// IncludeInternalProgramsDefault reports whether internal programs are
// exposed by default in list responses that omit an explicit
// include_internal parameter (default false, matching the CCU WebUI).
func (h *Hub) IncludeInternalProgramsDefault() bool {
	return h.includeInternalDefault.Load()
}

// --- Programs ---

// PutProgram registers (or replaces) a program under its ID. Fires
// every observer registered via [Hub.OnProgramRegistered] after the
// insert so late-bound consumers (MQTT publisher, UI cache) can wire
// per-program subscriptions.
func (h *Hub) PutProgram(p *Program) {
	if p == nil || p.ID == "" {
		return
	}
	h.mu.Lock()
	h.programs[p.ID] = p
	observers := append([]func(*Program){}, h.programObservers...)
	h.mu.Unlock()
	for _, cb := range observers {
		if cb != nil {
			cb(p)
		}
	}
}

// OnProgramRegistered subscribes cb to every [Hub.PutProgram] call.
// Returns an idempotent unsubscribe closure. The hook does NOT fire
// retroactively for programs already present — callers that need the
// existing set must read [Hub.Programs] themselves once after
// registering.
func (h *Hub) OnProgramRegistered(cb func(*Program)) func() {
	if cb == nil {
		return func() {}
	}
	h.mu.Lock()
	h.programObservers = append(h.programObservers, cb)
	idx := len(h.programObservers) - 1
	h.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			if idx < len(h.programObservers) {
				h.programObservers[idx] = nil
			}
		})
	}
}

// Program returns a program by ID.
func (h *Hub) Program(id string) (*Program, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	p, ok := h.programs[id]
	return p, ok
}

// Programs returns every registered program sorted by ID.
func (h *Hub) Programs() []*Program {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]*Program, 0, len(h.programs))
	for _, p := range h.programs {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ClearPrograms drops all registered programs atomically. Used by
// [HubCoordinator.Clear] on stop / reconnect to reset hub state.
func (h *Hub) ClearPrograms() {
	h.mu.Lock()
	h.programs = make(map[string]*Program)
	h.mu.Unlock()
}

// RemoveProgram drops the program and reports whether one existed. Fires the
// program's [Program.NotifyRemoved] hooks before the entry is deleted so
// subscribers (MQTT discovery, UI state) can clean up.
func (h *Hub) RemoveProgram(id string) bool {
	h.mu.Lock()
	prog, ok := h.programs[id]
	if !ok {
		h.mu.Unlock()
		return false
	}
	delete(h.programs, id)
	h.mu.Unlock()
	if prog != nil {
		prog.NotifyRemoved()
	}
	return true
}

// DeleteProgramRemote removes a program on the CCU and drops it from the
// in-memory cache once the call succeeded. Returns [ErrProgramNotFound]
// when no program with the given ID is registered. The cache entry is
// removed (firing [Program.NotifyRemoved]) only after the CCU round-trip
// succeeds, so a failed delete leaves the mirror intact. Mirrors
// [Hub.DeleteSysvarRemote].
func (h *Hub) DeleteProgramRemote(ctx context.Context, id string) error {
	p, ok := h.Program(id)
	if !ok {
		return ErrProgramNotFound
	}
	if err := p.Delete(ctx); err != nil {
		return err
	}
	h.RemoveProgram(id)
	return nil
}

// --- System variables ---

// PutSysvar registers (or replaces) a sysvar under its Name. Fires
// every observer registered via [Hub.OnSysvarRegistered] after the
// insert so late-bound consumers (MQTT publisher, UI cache) can wire
// per-sysvar subscriptions.
func (h *Hub) PutSysvar(s *Sysvar) {
	if s == nil || s.Name == "" {
		return
	}
	h.mu.Lock()
	h.sysvars[s.Name] = s
	observers := append([]func(*Sysvar){}, h.sysvarObservers...)
	h.mu.Unlock()
	for _, cb := range observers {
		if cb != nil {
			cb(s)
		}
	}
}

// OnSysvarRegistered subscribes cb to every [Hub.PutSysvar] call.
// Returns an idempotent unsubscribe closure. The hook does NOT fire
// retroactively for sysvars already present — callers that need the
// existing set must read [Hub.Sysvars] themselves once after
// registering.
func (h *Hub) OnSysvarRegistered(cb func(*Sysvar)) func() {
	if cb == nil {
		return func() {}
	}
	h.mu.Lock()
	h.sysvarObservers = append(h.sysvarObservers, cb)
	idx := len(h.sysvarObservers) - 1
	h.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			h.mu.Lock()
			defer h.mu.Unlock()
			if idx < len(h.sysvarObservers) {
				h.sysvarObservers[idx] = nil
			}
		})
	}
}

// Sysvar returns a sysvar by Name.
func (h *Hub) Sysvar(name string) (*Sysvar, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	s, ok := h.sysvars[name]
	return s, ok
}

// SysvarByTopicSegment resolves the `<name>` segment of a sysvar MQTT
// topic back to the sysvar it was built from.
//
// The topic segment is [naming.TopicSafe]d, so a CCU sysvar named
// `Außen Temperatur` is declared — and therefore written to — as
// `Außen_Temperatur`. An exact map lookup on that segment misses, and
// every command published to the topic the discovery payload itself
// advertised was dropped as "unknown sysvar".
//
// The exact name is tried first (the common case, and the shape every
// non-MQTT caller passes), then the unique sysvar whose escaped name
// equals the segment. Two names that collapse onto the same segment
// resolve to nothing: refusing an ambiguous write is safer than
// picking one of the two at random.
func (h *Hub) SysvarByTopicSegment(seg string) (*Sysvar, bool) {
	if seg == "" {
		return nil, false
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	if s, ok := h.sysvars[seg]; ok {
		return s, true
	}
	var (
		found   *Sysvar
		matches int
	)
	for name, s := range h.sysvars {
		if naming.TopicSafe(name) == seg {
			found = s
			matches++
		}
	}
	if matches != 1 {
		return nil, false
	}
	return found, true
}

// Sysvars returns every registered sysvar sorted by Name.
func (h *Hub) Sysvars() []*Sysvar {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]*Sysvar, 0, len(h.sysvars))
	for _, s := range h.sysvars {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// ClearSysvars drops all registered sysvars atomically. Used by
// [HubCoordinator.Clear] on stop / reconnect to reset hub state.
func (h *Hub) ClearSysvars() {
	h.mu.Lock()
	h.sysvars = make(map[string]*Sysvar)
	h.mu.Unlock()
}

// RemoveSysvar drops a sysvar from the in-memory cache and reports
// whether one existed. Use DeleteSysvarRemote for full CCU-side
// removal — this method is the local-only path used during
// re-snapshots.
func (h *Hub) RemoveSysvar(name string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.sysvars[name]; !ok {
		return false
	}
	delete(h.sysvars, name)
	return true
}

// RenameSysvar re-keys a cached sysvar from oldName to newName and
// updates the entry's Name field, preserving the same pointer so
// subscribers wired via OnSysvarRegistered stay valid. It reports
// whether an entry existed under oldName. Local-only: the CCU-side
// rename runs through UpdateSysvarRemote. A no-op when the names match,
// oldName is unknown, or newName is already taken (the periodic refresh
// reconciles any residual state).
func (h *Hub) RenameSysvar(oldName, newName string) bool {
	if oldName == newName || newName == "" {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	s, ok := h.sysvars[oldName]
	if !ok {
		return false
	}
	if _, taken := h.sysvars[newName]; taken {
		return false
	}
	delete(h.sysvars, oldName)
	s.Name = newName
	h.sysvars[newName] = s
	return true
}

// Mutator bundles every CCU-side write interface the hub exposes. The
// hub-wiring adapter wires a single object (the JSON-RPC writer) that
// implements all of them via [Hub.SetMutator].
type Mutator interface {
	SysvarMutator
	RoomMutator
	FunctionMutator
	BackupTrigger
	FirmwareUpdater
	InboxAccepter
}

// SetMutator wires (or re-wires) every CCU-side mutator under the hub mutex.
// Use this instead of assigning the exported fields directly whenever the
// wiring can run concurrently with a reader — specifically the background
// WireHub recovery, which re-applies the mutators after a transient boot-time
// hub failure while the daemon may already be servicing a hub write. The
// guarded reader methods below (and SetMutator) serialise through h.mu, so a
// recovery write never races a concurrent remote operation.
func (h *Hub) SetMutator(m Mutator) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.SysvarMutator = m
	h.RoomMutator = m
	h.FunctionMutator = m
	h.BackupTrigger = m
	h.FirmwareUpdater = m
	h.InboxAccepter = m
	// Opportunistic: the JSON-RPC writer also reads sysvar usage. An
	// in-memory fake that doesn't implement it leaves the reader nil.
	if r, ok := m.(SysvarUsageReader); ok {
		h.sysvarUsageReader = r
	}
}

func (h *Hub) sysvarMut() SysvarMutator { h.mu.RLock(); defer h.mu.RUnlock(); return h.SysvarMutator }

func (h *Hub) roomMut() RoomMutator { h.mu.RLock(); defer h.mu.RUnlock(); return h.RoomMutator }

func (h *Hub) funcMut() FunctionMutator { h.mu.RLock(); defer h.mu.RUnlock(); return h.FunctionMutator }

func (h *Hub) backupMut() BackupTrigger { h.mu.RLock(); defer h.mu.RUnlock(); return h.BackupTrigger }

func (h *Hub) firmwareMut() FirmwareUpdater {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.FirmwareUpdater
}

func (h *Hub) inboxMut() InboxAccepter { h.mu.RLock(); defer h.mu.RUnlock(); return h.InboxAccepter }

func (h *Hub) sysvarUsageRdr() SysvarUsageReader {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.sysvarUsageReader
}

// SysvarUsageRemote lists the CCU programs that reference the named
// system variable. Returns [ErrNoSysvarUsageReader] when no CCU-side
// reader is wired (the REST handler maps that to 503).
func (h *Hub) SysvarUsageRemote(ctx context.Context, name string) ([]SysvarUsage, error) {
	r := h.sysvarUsageRdr()
	if r == nil {
		return nil, ErrNoSysvarUsageReader
	}
	return r.SysvarUsagePrograms(ctx, name)
}

// CreateSysvarRemote provisions a sysvar on the CCU. The hub mirror
// is updated lazily by the periodic sysvar refresh; the REST handler
// returns 202 once the call lands.
func (h *Hub) CreateSysvarRemote(ctx context.Context, spec SysvarCreateSpec) error {
	m := h.sysvarMut()
	if m == nil {
		return ErrNoSysvarMutator
	}
	return m.CreateSysvar(ctx, spec)
}

// DeleteSysvarRemote removes a sysvar on the CCU and drops it from
// the in-memory cache once the call succeeded.
func (h *Hub) DeleteSysvarRemote(ctx context.Context, name string) error {
	m := h.sysvarMut()
	if m == nil {
		return ErrNoSysvarMutator
	}
	if err := m.DeleteSysvar(ctx, name); err != nil {
		return err
	}
	h.RemoveSysvar(name)
	return nil
}

// UpdateSysvarRemote patches a sysvar's metadata (name, unit, bounds,
// value list, description, value labels, visibility and archive flags)
// without changing its type. A non-empty NewName that differs from Name
// renames the variable; the local cache is re-keyed once the CCU call
// lands so the new name is visible before the next periodic refresh
// reconciles it. Type changes are unsafe at the CCU level — callers
// wanting that must delete + recreate.
func (h *Hub) UpdateSysvarRemote(ctx context.Context, spec SysvarUpdateSpec) error {
	m := h.sysvarMut()
	if m == nil {
		return ErrNoSysvarMutator
	}
	if err := m.UpdateSysvar(ctx, spec); err != nil {
		return err
	}
	if spec.NewName != "" && spec.NewName != spec.Name {
		h.RenameSysvar(spec.Name, spec.NewName)
	}
	return nil
}

// SetDeviceRoomsRemote replaces the device's room assignments via
// the wired RoomMutator. The hub mirror picks up the new state on
// the next device-list refresh.
func (h *Hub) SetDeviceRoomsRemote(
	ctx context.Context, deviceAddress string, rooms []string,
) error {
	m := h.roomMut()
	if m == nil {
		return ErrNoRoomMutator
	}
	return m.SetDeviceRooms(ctx, deviceAddress, rooms)
}

// SetDeviceFunctionsRemote replaces the device's function
// assignments via the wired FunctionMutator.
func (h *Hub) SetDeviceFunctionsRemote(
	ctx context.Context, deviceAddress string, functions []string,
) error {
	m := h.funcMut()
	if m == nil {
		return ErrNoFunctionMutator
	}
	return m.SetDeviceFunctions(ctx, deviceAddress, functions)
}

// CreateRoomRemote creates a room entity on the CCU and returns its new
// object ID. Requires the wired RoomMutator to also satisfy RoomAdmin.
func (h *Hub) CreateRoomRemote(ctx context.Context, name string) (int, error) {
	a, ok := h.roomMut().(RoomAdmin)
	if !ok {
		return 0, ErrNoRoomMutator
	}
	return a.CreateRoom(ctx, name)
}

// RenameRoomRemote renames a room entity on the CCU.
func (h *Hub) RenameRoomRemote(ctx context.Context, oldName, newName string) error {
	a, ok := h.roomMut().(RoomAdmin)
	if !ok {
		return ErrNoRoomMutator
	}
	return a.RenameRoom(ctx, oldName, newName)
}

// DeleteRoomRemote deletes a room entity on the CCU.
func (h *Hub) DeleteRoomRemote(ctx context.Context, name string) error {
	a, ok := h.roomMut().(RoomAdmin)
	if !ok {
		return ErrNoRoomMutator
	}
	return a.DeleteRoom(ctx, name)
}

// CreateFunctionRemote creates a function (Gewerk) entity on the CCU.
func (h *Hub) CreateFunctionRemote(ctx context.Context, name string) (int, error) {
	a, ok := h.funcMut().(FunctionAdmin)
	if !ok {
		return 0, ErrNoFunctionMutator
	}
	return a.CreateFunction(ctx, name)
}

// RenameFunctionRemote renames a function entity on the CCU.
func (h *Hub) RenameFunctionRemote(ctx context.Context, oldName, newName string) error {
	a, ok := h.funcMut().(FunctionAdmin)
	if !ok {
		return ErrNoFunctionMutator
	}
	return a.RenameFunction(ctx, oldName, newName)
}

// DeleteFunctionRemote deletes a function entity on the CCU.
func (h *Hub) DeleteFunctionRemote(ctx context.Context, name string) error {
	a, ok := h.funcMut().(FunctionAdmin)
	if !ok {
		return ErrNoFunctionMutator
	}
	return a.DeleteFunction(ctx, name)
}

// UserLevelRemote reads a CCU user's permission level via the wired
// RoomMutator when it also satisfies UserLevelReader. Requires the
// privileged service session (the mutator's JSON-RPC client).
func (h *Hub) UserLevelRemote(ctx context.Context, username string) (int, error) {
	r, ok := h.roomMut().(UserLevelReader)
	if !ok {
		return -1, ErrNoUserLevelReader
	}
	return r.GetUserLevel(ctx, username)
}

// TriggerBackupRemote runs the CCU backup script.
func (h *Hub) TriggerBackupRemote(ctx context.Context) error {
	m := h.backupMut()
	if m == nil {
		return ErrNoBackupTrigger
	}
	return m.TriggerBackup(ctx)
}

// BackupStatusRemote polls the CCU backup script status.
func (h *Hub) BackupStatusRemote(ctx context.Context) (string, error) {
	m := h.backupMut()
	if m == nil {
		return "", ErrNoBackupTrigger
	}
	return m.BackupStatus(ctx)
}

// TriggerFirmwareUpdateRemote kicks off the global CCU firmware
// update flow.
func (h *Hub) TriggerFirmwareUpdateRemote(ctx context.Context) error {
	m := h.firmwareMut()
	if m == nil {
		return ErrNoFirmwareUpdater
	}
	return m.TriggerFirmwareUpdate(ctx)
}

// AcceptInboxDeviceRemote flips the device's ReadyConfig flag.
func (h *Hub) AcceptInboxDeviceRemote(
	ctx context.Context, deviceAddress string,
) error {
	m := h.inboxMut()
	if m == nil {
		return ErrNoInboxAccepter
	}
	return m.AcceptDeviceInInbox(ctx, deviceAddress)
}

// --- InstallMode data points ---

// PutInstallMode registers (or replaces) an InstallMode keyed by its
// InterfaceID.
func (h *Hub) PutInstallMode(m *InstallMode) {
	if m == nil || m.InterfaceID == "" {
		return
	}
	h.mu.Lock()
	h.installModeDPs[m.InterfaceID] = m
	h.mu.Unlock()
}

// InstallModeDP returns the InstallMode for a given interfaceID.
func (h *Hub) InstallModeDP(interfaceID string) (*InstallMode, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	m, ok := h.installModeDPs[interfaceID]
	return m, ok
}

// InstallModeDPs returns all registered install-mode data points. Returns a
// snapshot slice; the caller must not modify the elements.
func (h *Hub) InstallModeDPs() []*InstallMode {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]*InstallMode, 0, len(h.installModeDPs))
	for _, m := range h.installModeDPs {
		out = append(out, m)
	}
	return out
}

// --- Connectivity ---

// SetConnectivity wires the per-interface reachability tracker to the Hub.
// Called by the adapter layer once the connectivity aggregate is initialised.
// Nil detaches. Returns the Hub for chaining.
func (h *Hub) SetConnectivity(c *Connectivity) *Hub {
	h.mu.Lock()
	h.connectivity = c
	h.mu.Unlock()
	return h
}

// ConnectivityDataPoints returns the connectivity aggregate or nil when it
// has not been wired via [Hub.SetConnectivity].
func (h *Hub) ConnectivityDataPoints() *Connectivity {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.connectivity
}

// --- Hub-level data fetch delegates ---

// FetchAlarmMessagesData retrieves alarm messages from the backend via the
// supplied [DataFetcher] and updates the hub's [Messages] aggregate. Returns
// an error only when the fetcher call fails.
func (h *Hub) FetchAlarmMessagesData(ctx context.Context, fetcher DataFetcher) error {
	if fetcher == nil {
		return errors.New("hub: nil data fetcher")
	}
	msgs, err := fetcher.FetchAlarmMessages(ctx)
	if err != nil {
		return err
	}
	h.Messages.Replace(msgs)
	return nil
}

// FetchInboxData retrieves inbox devices from the backend via the supplied
// [DataFetcher] and updates the hub's [Inbox] aggregate. Returns an error
// only when the fetcher call fails.
func (h *Hub) FetchInboxData(ctx context.Context, fetcher DataFetcher) error {
	if fetcher == nil {
		return errors.New("hub: nil data fetcher")
	}
	devices, err := fetcher.FetchInboxDevices(ctx)
	if err != nil {
		return err
	}
	h.Inbox.Replace(devices)
	return nil
}
