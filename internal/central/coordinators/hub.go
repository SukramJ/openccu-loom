// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package coordinators

import (
	"context"
	"errors"
	"sync"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/internal/model/naming"
	"github.com/SukramJ/openccu-loom/internal/observability"
	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmevent"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// SysvarSnapshot is the coordinator's record of a single CCU system
// variable.
type SysvarSnapshot struct {
	Name      string
	Value     hmtypes.ParamValue
	ValueType hmenum.HubValueType
}

// HubCoordinator owns the central's view of CCU "hub" entities:
// system variables, programs, alarm messages, service messages, and
// install-mode toggles. It re-emits changes on the internal bus.
type HubCoordinator struct {
	centralName string
	bus         *events.Bus
	recorder    observability.Recorder

	// hubModel is the domain model aggregating all hub entities.
	// Wired via SetHubModel; nil when not configured.
	hubModel *hub.Hub

	mu      sync.RWMutex
	sysvars map[string]SysvarSnapshot

	// Periodic-refresh hooks wired by the hub-side adapter once the
	// JSON-RPC session is up. The background scheduler calls the
	// matching Refresh* method which delegates here.
	programsRefresh        func(ctx context.Context) error
	sysvarsRefresh         func(ctx context.Context) error
	inboxRefresh           func(ctx context.Context) error
	serviceMessagesRefresh func(ctx context.Context) error
	alarmMessagesRefresh   func(ctx context.Context) error
	systemUpdateRefresh    func(ctx context.Context) error
	installModeRefresh     func(ctx context.Context) error
	metricsRefresh         func(ctx context.Context) error
	connectivityRefresh    func(ctx context.Context) error

	// Per-refresh-type mutexes serialise concurrent calls to each
	// Refresh* method. The scheduler and manual WS-triggered refreshes
	// may fire simultaneously; without serialisation both calls issue
	// duplicate JSON-RPCs to the CCU and race on the hub model's state.
	// One mutex per refresh type mirrors the per-fetch semaphore pattern
	// used in the Python reference (one asyncio.Semaphore per fetch kind).
	semaPrograms        sync.Mutex
	semaSysvars         sync.Mutex
	semaInbox           sync.Mutex
	semaServiceMessages sync.Mutex
	semaAlarmMessages   sync.Mutex
	semaSystemUpdate    sync.Mutex
	semaInstallMode     sync.Mutex
	semaMetrics         sync.Mutex
	semaConnectivity    sync.Mutex

	// programExecutor is the south-bound hook for ExecuteProgram.
	// Nil = no-op. Wired via SetProgramExecutor.
	programExecutor ProgramExecutor

	// serviceMessageSuppressor is called by SuppressServiceMessage.
	// Nil = no-op / return nil.
	serviceMessageSuppressor ServiceMessageSuppressor

	// programStateWriter is the south-bound hook for SetProgramState.
	// Nil = no-op. Wired via SetProgramStateWriter.
	programStateWriter ProgramStateWriter

	// sysvarValueWriter is the south-bound hook for SetSystemVariable.
	// Nil = no-op. Wired via SetSysvarValueWriter.
	sysvarValueWriter SysvarValueWriter

	// sysvarGetter is the south-bound hook for GetSystemVariable.
	// Nil = no-op. Wired via SetSysvarGetter.
	sysvarGetter SysvarGetter

	// serviceMessageReader is the south-bound hook for
	// GetSuppressedServiceMessages. Nil = no-op.
	serviceMessageReader ServiceMessageReader

	// sysvarCreator is the south-bound hook for CreateSysvar*.
	// Nil = return "not wired" error. Wired via SetSysvarCreator.
	sysvarCreator SysvarCreator
}

// NewHubCoordinator wires the coordinator.
func NewHubCoordinator(centralName string, bus *events.Bus) *HubCoordinator {
	return &HubCoordinator{
		centralName: centralName,
		bus:         bus,
		recorder:    observability.NoopRecorder{},
		sysvars:     make(map[string]SysvarSnapshot),
	}
}

// Clear drops all cached sysvar snapshots and resets the hub model's sysvar
// and program data points. Call on stop/reconnect to ensure the coordinator
// does not surface stale hub data to north-bound adapters. P2.
func (h *HubCoordinator) Clear() {
	h.mu.Lock()
	h.sysvars = make(map[string]SysvarSnapshot)
	m := h.hubModel
	h.mu.Unlock()

	// Reset hub model data points if a model is wired. Each AddSysvarDP
	// AddProgramDP call will re-populate after the next refresh cycle.
	if m != nil {
		m.ClearSysvars()
		m.ClearPrograms()
	}
}

// SetRecorder rewires the observability recorder. Nil falls back to
// [observability.NoopRecorder]. Returns the receiver for chaining.
func (h *HubCoordinator) SetRecorder(rec observability.Recorder) *HubCoordinator {
	if rec == nil {
		rec = observability.NoopRecorder{}
	}
	h.recorder = rec
	return h
}

// UpdateSysvar stores a new sysvar value and publishes a change event
// if the value actually changed.
func (h *HubCoordinator) UpdateSysvar(_ context.Context, snap SysvarSnapshot) {
	h.mu.Lock()
	prev, existed := h.sysvars[snap.Name]
	h.sysvars[snap.Name] = snap
	h.mu.Unlock()
	if existed && prev.Value.Equal(snap.Value) {
		return
	}
	var oldVal hmtypes.ParamValue
	if existed {
		oldVal = prev.Value
	}
	events.Publish(h.bus, hmevent.SysvarChangedEvent{
		Base:        hmevent.NewBase(),
		CentralName: h.centralName,
		Name:        snap.Name,
		OldValue:    oldVal,
		NewValue:    snap.Value,
		ValueType:   snap.ValueType,
	})
}

// Sysvars returns a snapshot of every known sysvar.
func (h *HubCoordinator) Sysvars() []SysvarSnapshot {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]SysvarSnapshot, 0, len(h.sysvars))
	for _, s := range h.sysvars {
		out = append(out, s)
	}
	return out
}

// NotifyProgramExecuted publishes a [hmevent.ProgramExecutedEvent].
func (h *HubCoordinator) NotifyProgramExecuted(_ context.Context, programID string, trigger hmenum.ProgramTrigger, success bool) {
	events.Publish(h.bus, hmevent.ProgramExecutedEvent{
		Base:        hmevent.NewBase(),
		CentralName: h.centralName,
		ProgramID:   programID,
		Trigger:     trigger,
		Success:     success,
	})
}

// SetRefreshHooks wires the periodic-refresh callbacks the background
// scheduler invokes. Each hook is optional; nil keeps the previously
// configured handler.
func (h *HubCoordinator) SetRefreshHooks(hooks RefreshHooks) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if hooks.Programs != nil {
		h.programsRefresh = hooks.Programs
	}
	if hooks.Sysvars != nil {
		h.sysvarsRefresh = hooks.Sysvars
	}
	if hooks.Inbox != nil {
		h.inboxRefresh = hooks.Inbox
	}
	if hooks.ServiceMessages != nil {
		h.serviceMessagesRefresh = hooks.ServiceMessages
	}
	if hooks.AlarmMessages != nil {
		h.alarmMessagesRefresh = hooks.AlarmMessages
	}
	if hooks.SystemUpdate != nil {
		h.systemUpdateRefresh = hooks.SystemUpdate
	}
	if hooks.InstallMode != nil {
		h.installModeRefresh = hooks.InstallMode
	}
	if hooks.Metrics != nil {
		h.metricsRefresh = hooks.Metrics
	}
	if hooks.Connectivity != nil {
		h.connectivityRefresh = hooks.Connectivity
	}
}

// RefreshHooks bundles the optional periodic-refresh callbacks.
type RefreshHooks struct {
	Programs        func(ctx context.Context) error
	Sysvars         func(ctx context.Context) error
	Inbox           func(ctx context.Context) error
	ServiceMessages func(ctx context.Context) error
	AlarmMessages   func(ctx context.Context) error
	SystemUpdate    func(ctx context.Context) error
	// InstallMode refreshes the remaining countdown on all registered
	// install-mode data points and publishes the updated values via
	// [HubCoordinator.PublishInstallModeRefreshed].
	InstallMode func(ctx context.Context) error
	// Metrics fetches CCU performance metrics and populates the metrics
	// data points on the hub model. Wired when the JSON-RPC session
	// supports the metrics endpoint.
	Metrics func(ctx context.Context) error
	// Connectivity refreshes the binary-sensor connectivity data points
	// for all registered interfaces. Mirrors hub.fetch_connectivity_data.
	Connectivity func(ctx context.Context) error
}

// runRefresh is the shared body for all Refresh* methods: pull the
// hook under the read lock, run it through observability.Instrument,
// and surface the (possibly wrapped) error.
func (h *HubCoordinator) runRefresh(ctx context.Context, op string, fn func(ctx context.Context) error) error {
	if fn == nil {
		return nil
	}
	return observability.Instrument(ctx, h.recorder, "hub_coordinator."+op, observability.ScopeCoordinator, fn)
}

// RefreshPrograms invokes the program-refresh hook (if any) and
// returns the call result. Used by the background scheduler.
// Concurrent callers are serialised so only one JSON-RPC fetch runs at
// a time for this refresh type.
func (h *HubCoordinator) RefreshPrograms(ctx context.Context) error {
	h.semaPrograms.Lock()
	defer h.semaPrograms.Unlock()
	h.mu.RLock()
	fn := h.programsRefresh
	h.mu.RUnlock()
	return h.runRefresh(ctx, "refresh_programs", fn)
}

// RefreshSysvars invokes the sysvar-refresh hook. Concurrent callers
// are serialised per fetch type.
func (h *HubCoordinator) RefreshSysvars(ctx context.Context) error {
	h.semaSysvars.Lock()
	defer h.semaSysvars.Unlock()
	h.mu.RLock()
	fn := h.sysvarsRefresh
	h.mu.RUnlock()
	return h.runRefresh(ctx, "refresh_sysvars", fn)
}

// RefreshInbox invokes the inbox-refresh hook. Concurrent callers are
// serialised per fetch type.
func (h *HubCoordinator) RefreshInbox(ctx context.Context) error {
	h.semaInbox.Lock()
	defer h.semaInbox.Unlock()
	h.mu.RLock()
	fn := h.inboxRefresh
	h.mu.RUnlock()
	return h.runRefresh(ctx, "refresh_inbox", fn)
}

// RefreshServiceMessages invokes the service-messages refresh hook.
// Concurrent callers are serialised per fetch type.
func (h *HubCoordinator) RefreshServiceMessages(ctx context.Context) error {
	h.semaServiceMessages.Lock()
	defer h.semaServiceMessages.Unlock()
	h.mu.RLock()
	fn := h.serviceMessagesRefresh
	h.mu.RUnlock()
	return h.runRefresh(ctx, "refresh_service_messages", fn)
}

// RefreshAlarmMessages invokes the alarm-messages refresh hook.
// Concurrent callers are serialised per fetch type.
func (h *HubCoordinator) RefreshAlarmMessages(ctx context.Context) error {
	h.semaAlarmMessages.Lock()
	defer h.semaAlarmMessages.Unlock()
	h.mu.RLock()
	fn := h.alarmMessagesRefresh
	h.mu.RUnlock()
	return h.runRefresh(ctx, "refresh_alarm_messages", fn)
}

// RefreshSystemUpdate invokes the system-update refresh hook.
// Concurrent callers are serialised per fetch type.
func (h *HubCoordinator) RefreshSystemUpdate(ctx context.Context) error {
	h.semaSystemUpdate.Lock()
	defer h.semaSystemUpdate.Unlock()
	h.mu.RLock()
	fn := h.systemUpdateRefresh
	h.mu.RUnlock()
	return h.runRefresh(ctx, "refresh_system_update", fn)
}

// RefreshInstallMode invokes the install-mode refresh hook (if any). The hook
// is expected to re-read the CCU's remaining install-mode countdown for each
// registered interface and call [PublishInstallModeRefreshed] when done.
// Concurrent callers are serialised per fetch type.
func (h *HubCoordinator) RefreshInstallMode(ctx context.Context) error {
	h.semaInstallMode.Lock()
	defer h.semaInstallMode.Unlock()
	h.mu.RLock()
	fn := h.installModeRefresh
	h.mu.RUnlock()
	return h.runRefresh(ctx, "refresh_install_mode", fn)
}

// RefreshConnectivity invokes the connectivity refresh hook (if any). The hook
// probes the CCU's interface-reachability state and updates the per-interface
// connectivity data points. Concurrent callers are serialised per fetch type.
func (h *HubCoordinator) RefreshConnectivity(ctx context.Context) error {
	h.semaConnectivity.Lock()
	defer h.semaConnectivity.Unlock()
	h.mu.RLock()
	fn := h.connectivityRefresh
	h.mu.RUnlock()
	return h.runRefresh(ctx, "refresh_connectivity", fn)
}

// ServiceMessageSuppressor is the south-bound contract for suppressing or
// unsuppressing a CCU service message. Wire via
// [HubCoordinator.SetServiceMessageSuppressor].
type ServiceMessageSuppressor interface {
	// SuppressServiceMessage acknowledges or unsuppresses the service
	// message on a channel. interfaceID identifies the CCU interface
	// (e.g. "HmIP-RF"), channelAddress is the channel address,
	// parameterID is the specific parameter ("" means all parameters),
	// and suppress toggles between suppression (true) and removal of the
	// suppression (false). Returns an error when the RPC call fails.
	SuppressServiceMessage(ctx context.Context, interfaceID, channelAddress, parameterID string, suppress bool) error
}

// SetServiceMessageSuppressor wires an optional south-bound suppressor.
// Nil disables suppression (SuppressServiceMessage returns nil). Returns
// the receiver for chaining.
func (h *HubCoordinator) SetServiceMessageSuppressor(s ServiceMessageSuppressor) *HubCoordinator {
	h.mu.Lock()
	h.serviceMessageSuppressor = s
	h.mu.Unlock()
	return h
}

// SuppressServiceMessage acknowledges or removes the suppression of the
// service message identified by interfaceID, channelAddress, and
// parameterID. Pass suppress=true to acknowledge, false to clear the
// suppression. Pass an empty parameterID to affect all parameters on the
// channel. Delegates to the wired [ServiceMessageSuppressor]; returns nil
// when no suppressor is wired.
func (h *HubCoordinator) SuppressServiceMessage(ctx context.Context, interfaceID, channelAddress, parameterID string, suppress bool) error {
	h.mu.RLock()
	sup := h.serviceMessageSuppressor
	h.mu.RUnlock()
	if sup == nil {
		return nil
	}
	return sup.SuppressServiceMessage(ctx, interfaceID, channelAddress, parameterID, suppress)
}

// --- Hub data-point accessors + lifecycle --------------------

// SetHubModel wires the [hub.Hub] domain model into the coordinator.
// Call once at daemon boot after constructing both objects. Returns
// the receiver for chaining. Nil detaches the model.
func (h *HubCoordinator) SetHubModel(m *hub.Hub) *HubCoordinator {
	h.mu.Lock()
	h.hubModel = m
	h.mu.Unlock()
	return h
}

// AlarmMessagesDP returns the coordinator's reference to the alarm-messages
// data-point aggregate, or nil when no hub model is wired.
func (h *HubCoordinator) AlarmMessagesDP() *hub.AlarmMessages {
	h.mu.RLock()
	m := h.hubModel
	h.mu.RUnlock()
	if m == nil {
		return nil
	}
	return m.Messages
}

// ServiceMessagesDP returns the service-messages aggregate or nil.
func (h *HubCoordinator) ServiceMessagesDP() *hub.ServiceMessages {
	h.mu.RLock()
	m := h.hubModel
	h.mu.RUnlock()
	if m == nil {
		return nil
	}
	return m.ServiceMessages
}

// MetricsDPs returns the hub metrics aggregate or nil.
func (h *HubCoordinator) MetricsDPs() *hub.Metrics {
	h.mu.RLock()
	m := h.hubModel
	h.mu.RUnlock()
	if m == nil {
		return nil
	}
	return m.Metrics
}

// InboxDP returns the hub inbox aggregate or nil.
func (h *HubCoordinator) InboxDP() *hub.Inbox {
	h.mu.RLock()
	m := h.hubModel
	h.mu.RUnlock()
	if m == nil {
		return nil
	}
	return m.Inbox
}

// UpdateDP returns the hub firmware-update aggregate or nil.
func (h *HubCoordinator) UpdateDP() *hub.Update {
	h.mu.RLock()
	m := h.hubModel
	h.mu.RUnlock()
	if m == nil {
		return nil
	}
	return m.Update
}

// InstallModeDPs returns all registered install-mode data points.
func (h *HubCoordinator) InstallModeDPs() []*hub.InstallMode {
	h.mu.RLock()
	m := h.hubModel
	h.mu.RUnlock()
	if m == nil {
		return nil
	}
	return m.InstallModeDPs()
}

// ConnectivityDPs returns the per-interface connectivity aggregate from the
// wired hub model, or nil when no hub model is wired or no connectivity
// has been registered via [hub.Hub.SetConnectivity].
func (h *HubCoordinator) ConnectivityDPs() *hub.Connectivity {
	h.mu.RLock()
	m := h.hubModel
	h.mu.RUnlock()
	if m == nil {
		return nil
	}
	return m.ConnectivityDataPoints()
}

// ProgramDataPoints returns all programs registered with the hub model.
func (h *HubCoordinator) ProgramDataPoints() []*hub.Program {
	h.mu.RLock()
	m := h.hubModel
	h.mu.RUnlock()
	if m == nil {
		return nil
	}
	return m.Programs()
}

// SysvarDataPoints returns all sysvars registered with the hub model.
func (h *HubCoordinator) SysvarDataPoints() []*hub.Sysvar {
	h.mu.RLock()
	m := h.hubModel
	h.mu.RUnlock()
	if m == nil {
		return nil
	}
	return m.Sysvars()
}

// AddProgramDP registers a program data point with the hub model and wires
// the ExecuteNotifier so that every Execute call publishes a
// ProgramExecutedEvent on the bus. No-op when no hub model is wired.
func (h *HubCoordinator) AddProgramDP(p *hub.Program) {
	h.mu.RLock()
	m := h.hubModel
	h.mu.RUnlock()
	if m == nil || p == nil {
		return
	}
	// Wire the notifier so Execute emits a bus event.
	p.ExecuteNotifier = func(ctx context.Context, id string, trigger hmenum.ProgramTrigger, success bool) {
		h.NotifyProgramExecuted(ctx, id, trigger, success)
	}
	m.PutProgram(p)
}

// RemoveProgramDP removes a program data point from the hub model.
func (h *HubCoordinator) RemoveProgramDP(id string) bool {
	h.mu.RLock()
	m := h.hubModel
	h.mu.RUnlock()
	if m == nil {
		return false
	}
	return m.RemoveProgram(id)
}

// AddSysvarDP registers a sysvar data point with the hub model. No-op when no
// hub model is wired.
func (h *HubCoordinator) AddSysvarDP(s *hub.Sysvar) {
	h.mu.RLock()
	m := h.hubModel
	h.mu.RUnlock()
	if m == nil || s == nil {
		return
	}
	m.PutSysvar(s)
}

// RemoveSysvarDP removes a sysvar data point from the hub model.
func (h *HubCoordinator) RemoveSysvarDP(name string) bool {
	h.mu.RLock()
	m := h.hubModel
	h.mu.RUnlock()
	if m == nil {
		return false
	}
	return m.RemoveSysvar(name)
}

// AddInstallModeDP registers an install-mode data point with the hub
// model. No-op when no hub model is wired.
func (h *HubCoordinator) AddInstallModeDP(im *hub.InstallMode) {
	h.mu.RLock()
	m := h.hubModel
	h.mu.RUnlock()
	if m == nil || im == nil {
		return
	}
	m.PutInstallMode(im)
}

// --- SetProgramState -----------------------------------------------

// ProgramStateWriter is the south-bound contract for changing a CCU
// program's enabled/disabled state. Wire via
// [HubCoordinator.SetProgramStateWriter].
type ProgramStateWriter interface {
	// SetProgramActive enables (active=true) or disables a CCU program.
	SetProgramActive(ctx context.Context, programID string, active bool) error
}

// SetProgramStateWriter wires the CCU-side program state mutator.
// Nil disables remote state changes. Returns the receiver for chaining.
func (h *HubCoordinator) SetProgramStateWriter(w ProgramStateWriter) *HubCoordinator {
	h.mu.Lock()
	h.programStateWriter = w
	h.mu.Unlock()
	return h
}

// ProgramExecutor is the south-bound contract for triggering a CCU program
// run. Wire via [HubCoordinator.SetProgramExecutor].
type ProgramExecutor interface {
	// ExecuteProgram runs the program with the given ID on the CCU.
	ExecuteProgram(ctx context.Context, programID string) error
}

// SetProgramExecutor wires the CCU-side program execution hook.
// Nil disables remote program execution. Returns the receiver for chaining.
func (h *HubCoordinator) SetProgramExecutor(e ProgramExecutor) *HubCoordinator {
	h.mu.Lock()
	h.programExecutor = e
	h.mu.Unlock()
	return h
}

// ExecuteProgram triggers a CCU program run by ID. Delegates to the wired
// [ProgramExecutor]; returns nil when no executor is wired. This is
// semantically distinct from [SetProgramState]: ExecuteProgram fires a one-shot
// run of the program's action sequence, whereas SetProgramState enables or
// disables the program's scheduled execution.
func (h *HubCoordinator) ExecuteProgram(ctx context.Context, programID string) error {
	h.mu.RLock()
	e := h.programExecutor
	h.mu.RUnlock()
	if e == nil {
		return nil
	}
	return e.ExecuteProgram(ctx, programID)
}

// SetProgramState enables or disables a CCU program by ID. Delegates to the
// wired [ProgramStateWriter]; returns nil when no writer is wired.
func (h *HubCoordinator) SetProgramState(ctx context.Context, programID string, active bool) error {
	h.mu.RLock()
	w := h.programStateWriter
	h.mu.RUnlock()
	if w == nil {
		return nil
	}
	return w.SetProgramActive(ctx, programID, active)
}

// --- SetSystemVariable ---------------------------------------------

// SysvarValueWriter is the south-bound contract for writing a CCU
// system variable value. Wire via
// [HubCoordinator.SetSysvarValueWriter].
type SysvarValueWriter interface {
	// SetSysvar writes value to the named system variable via the CCU's
	// Rega/JSON-RPC path.
	SetSysvar(ctx context.Context, name string, value any) error
}

// SetSysvarValueWriter wires the CCU-side sysvar write path.
// Nil disables remote writes. Returns the receiver for chaining.
func (h *HubCoordinator) SetSysvarValueWriter(w SysvarValueWriter) *HubCoordinator {
	h.mu.Lock()
	h.sysvarValueWriter = w
	h.mu.Unlock()
	return h
}

// SetSystemVariable writes a system-variable value to the CCU via the wired
// [SysvarValueWriter]. Returns nil when no writer is wired.
func (h *HubCoordinator) SetSystemVariable(ctx context.Context, name string, value any) error {
	h.mu.RLock()
	w := h.sysvarValueWriter
	h.mu.RUnlock()
	if w == nil {
		return nil
	}
	return w.SetSysvar(ctx, name, value)
}

// --- Hub data-point lookups + install-mode publish -------

// SysvarGetter is the south-bound interface for reading a sysvar value
// from the CCU. Wired via [SetSysvarGetter].
type SysvarGetter interface {
	GetSysvar(ctx context.Context, name string) (any, error)
}

// ServiceMessageReader is the south-bound interface for listing the
// suppressed service message parameters on a channel. Wired via
// [SetServiceMessageReader].
type ServiceMessageReader interface {
	GetSuppressedServiceMessages(ctx context.Context, interfaceID, channelAddress string) ([]string, error)
}

// SetSysvarGetter wires the south-bound hook for [GetSystemVariable].
// Nil disables remote reads. Returns the receiver for chaining.
func (h *HubCoordinator) SetSysvarGetter(g SysvarGetter) *HubCoordinator {
	h.mu.Lock()
	h.sysvarGetter = g
	h.mu.Unlock()
	return h
}

// SetServiceMessageReader wires the south-bound hook for
// [GetSuppressedServiceMessages]. Nil disables reads. Returns the
// receiver for chaining.
func (h *HubCoordinator) SetServiceMessageReader(r ServiceMessageReader) *HubCoordinator {
	h.mu.Lock()
	h.serviceMessageReader = r
	h.mu.Unlock()
	return h
}

// GetHubDataPoints returns all hub data points (programs + sysvars). Pass an
// empty category to return all categories.
func (h *HubCoordinator) GetHubDataPoints() []any {
	progs := h.ProgramDataPoints()
	svars := h.SysvarDataPoints()
	out := make([]any, 0, len(progs)+len(svars))
	for _, p := range progs {
		out = append(out, p)
	}
	for _, s := range svars {
		out = append(out, s)
	}
	return out
}

// GetProgramDataPoint returns the [*hub.Program] whose ID matches pid, or nil
// when not found.
func (h *HubCoordinator) GetProgramDataPoint(pid string) *hub.Program {
	h.mu.RLock()
	m := h.hubModel
	h.mu.RUnlock()
	if m == nil {
		return nil
	}
	p, _ := m.Program(pid)
	return p
}

// GetSysvarDataPoint returns the [*hub.Sysvar] whose name (legacy_name)
// matches name, or nil when not found.
func (h *HubCoordinator) GetSysvarDataPoint(name string) *hub.Sysvar {
	h.mu.RLock()
	m := h.hubModel
	h.mu.RUnlock()
	if m == nil {
		return nil
	}
	s, _ := m.Sysvar(name)
	return s
}

// GetSystemVariable reads the current value of a system variable from the CCU
// via the wired [SysvarGetter]. Returns (nil, nil) when no getter is wired.
func (h *HubCoordinator) GetSystemVariable(ctx context.Context, name string) (any, error) {
	h.mu.RLock()
	g := h.sysvarGetter
	h.mu.RUnlock()
	if g == nil {
		return nil, nil
	}
	return g.GetSysvar(ctx, name)
}

// GetSuppressedServiceMessages returns the list of currently suppressed
// service message parameter IDs for a channel. Returns nil when no reader is
// wired.
func (h *HubCoordinator) GetSuppressedServiceMessages(ctx context.Context, interfaceID, channelAddress string) ([]string, error) {
	h.mu.RLock()
	r := h.serviceMessageReader
	h.mu.RUnlock()
	if r == nil {
		return nil, nil
	}
	return r.GetSuppressedServiceMessages(ctx, interfaceID, channelAddress)
}

// HubStatePaths returns the state-path strings for all registered sysvar and
// program data points. Used by [QueryFacade.GetStatePaths] to include hub
// entity paths in the full subscription path list.
func (h *HubCoordinator) HubStatePaths() []string {
	h.mu.RLock()
	m := h.hubModel
	h.mu.RUnlock()
	if m == nil {
		return nil
	}
	var out []string
	for _, s := range m.Sysvars() {
		if sp := s.PathData().StatePath; sp != "" {
			out = append(out, sp)
		}
	}
	for _, p := range m.Programs() {
		// Program state path mirrors
		// "program/status/<pid>" (model/naming/pathdata.go:113).
		if p.ID != "" {
			out = append(out, naming.NewProgramPathData(p.ID).StatePath)
		}
	}
	return out
}

// --- CreateSysvar* typed creators --------------------------------

// SysvarCreator is the south-bound contract for creating new CCU system
// variables. Wire via [HubCoordinator.SetSysvarCreator].
type SysvarCreator interface {
	// CreateSysvarBool creates a boolean system variable. Returns the
	// resulting variable metadata map, or nil when the backend does not
	// support variable creation.
	CreateSysvarBool(ctx context.Context, name string, initVal bool) (map[string]any, error)
	// CreateSysvarEnum creates an enum system variable with the given
	// string value list.
	CreateSysvarEnum(ctx context.Context, name string, valueList []string) (map[string]any, error)
	// CreateSysvarFloat creates a float system variable with bounds.
	CreateSysvarFloat(ctx context.Context, name string, minValue, maxValue float64) (map[string]any, error)
}

// SetSysvarCreator wires the south-bound sysvar creator. Nil disables
// remote creation (methods return an "not wired" error). Returns the
// receiver for chaining.
func (h *HubCoordinator) SetSysvarCreator(c SysvarCreator) *HubCoordinator {
	h.mu.Lock()
	h.sysvarCreator = c
	h.mu.Unlock()
	return h
}

// CreateSysvarBool creates a boolean system variable on the CCU via the wired
// [SysvarCreator]. Returns an error when no creator is wired or the primary
// client is unreachable.
func (h *HubCoordinator) CreateSysvarBool(ctx context.Context, name string, initVal bool) (map[string]any, error) {
	h.mu.RLock()
	c := h.sysvarCreator
	h.mu.RUnlock()
	if c == nil {
		return nil, errors.New("hub: CreateSysvarBool: sysvar creator not wired (primary client unreachable or not configured)")
	}
	return c.CreateSysvarBool(ctx, name, initVal)
}

// CreateSysvarEnum creates an enum system variable on the CCU via the
// wired [SysvarCreator]. Returns an error when no creator is wired.
func (h *HubCoordinator) CreateSysvarEnum(ctx context.Context, name string, valueList []string) (map[string]any, error) {
	h.mu.RLock()
	c := h.sysvarCreator
	h.mu.RUnlock()
	if c == nil {
		return nil, errors.New("hub: CreateSysvarEnum: sysvar creator not wired (primary client unreachable or not configured)")
	}
	return c.CreateSysvarEnum(ctx, name, valueList)
}

// CreateSysvarFloat creates a float system variable on the CCU via the
// wired [SysvarCreator]. Returns an error when no creator is wired.
func (h *HubCoordinator) CreateSysvarFloat(ctx context.Context, name string, minValue, maxValue float64) (map[string]any, error) {
	h.mu.RLock()
	c := h.sysvarCreator
	h.mu.RUnlock()
	if c == nil {
		return nil, errors.New("hub: CreateSysvarFloat: sysvar creator not wired (primary client unreachable or not configured)")
	}
	return c.CreateSysvarFloat(ctx, name, minValue, maxValue)
}

// InitHub performs the hub-coordinator initialization sequence. It clears
// any stale state from a previous run (sysvars, programs) and triggers an
// initial load for every hub data category so the hub model is populated
// as soon as the CCU connection comes up.
//
// The initial loads run best-effort: individual hook errors are ignored so a
// partial CCU response does not block the rest of the init sequence.
// Call once at daemon startup or after a CCU reconnect before wiring new
// refresh hooks.
func (h *HubCoordinator) InitHub() {
	h.Clear()
	ctx := context.Background()
	// Trigger initial loads for all hub data categories. Errors are
	// intentionally ignored — a failed first load does not prevent the
	// coordinator from entering operational state; the background scheduler
	// will retry on the next tick.
	_ = h.runRefresh(ctx, "init_programs", h.getPrograms())
	_ = h.runRefresh(ctx, "init_sysvars", h.getSysvars())
	_ = h.runRefresh(ctx, "init_inbox", h.getInbox())
	_ = h.runRefresh(ctx, "init_service_messages", h.getServiceMessages())
	_ = h.runRefresh(ctx, "init_alarm_messages", h.getAlarmMessages())
	_ = h.runRefresh(ctx, "init_install_mode", h.getInstallMode())
	_ = h.runRefresh(ctx, "init_metrics", h.getMetrics())
	_ = h.runRefresh(ctx, "init_connectivity", h.getConnectivity())
}

func (h *HubCoordinator) getPrograms() func(ctx context.Context) error {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.programsRefresh
}

func (h *HubCoordinator) getSysvars() func(ctx context.Context) error {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.sysvarsRefresh
}

func (h *HubCoordinator) getInbox() func(ctx context.Context) error {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.inboxRefresh
}

func (h *HubCoordinator) getServiceMessages() func(ctx context.Context) error {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.serviceMessagesRefresh
}

func (h *HubCoordinator) getAlarmMessages() func(ctx context.Context) error {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.alarmMessagesRefresh
}

func (h *HubCoordinator) getInstallMode() func(ctx context.Context) error {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.installModeRefresh
}

func (h *HubCoordinator) getMetrics() func(ctx context.Context) error {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.metricsRefresh
}

func (h *HubCoordinator) getConnectivity() func(ctx context.Context) error {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.connectivityRefresh
}

// RefreshMetrics invokes the metrics-refresh hook (if any). Used by the
// background scheduler to keep CCU performance metrics up to date.
// Concurrent callers are serialised per fetch type.
func (h *HubCoordinator) RefreshMetrics(ctx context.Context) error {
	h.semaMetrics.Lock()
	defer h.semaMetrics.Unlock()
	return h.runRefresh(ctx, "refresh_metrics", h.getMetrics())
}

// PublishInstallModeRefreshed fires an [hmevent.InstallModeChangedEvent] for
// each registered install-mode data point so north-bound adapters (MQTT,
// REST) pick up the refreshed countdown values.
func (h *HubCoordinator) PublishInstallModeRefreshed() {
	dps := h.InstallModeDPs()
	for _, dp := range dps {
		if dp == nil {
			continue
		}
		events.Publish(h.bus, hmevent.InstallModeChangedEvent{
			Base:        hmevent.NewBase(),
			CentralName: h.centralName,
			InterfaceID: dp.InterfaceID,
			Enabled:     dp.IsActive(),
			RemainingS:  int(dp.Remaining().Seconds()),
		})
	}
}
