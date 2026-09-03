// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package coordinators

import (
	"context"
	"errors"
	"sync"

	"github.com/SukramJ/openccu-loom/internal/central/events"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/internal/observability"
	"github.com/SukramJ/openccu-loom/internal/reqctx"
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

// BidcosInterfaceInfo is the cached radio-utilisation snapshot for one
// BidCos interface, populated by the periodic listBidcosInterfaces poll.
// DutyCycle and CarrierSense are percentages in the 0..100 range, or -1
// when the CCU did not report the value.
type BidcosInterfaceInfo struct {
	// Address is the primary gateway serial (e.g. "OEQ1234567").
	Address string
	// Type is the gateway type string.
	Type string
	// DutyCycle is the transmit duty cycle in percent (0..100), or -1
	// when unknown.
	DutyCycle int
	// CarrierSense is the receive carrier-sense load in percent (0..100),
	// or -1 when unknown.
	CarrierSense int
	// Connected reports whether the primary gateway is reachable.
	Connected bool
}

// DutyCycleWarningThreshold is the transmit duty cycle in percent at or
// above which a firmware update is flagged as risky. The CCU WebUI gates
// device updates on a high duty cycle (isDutyCycleOK4DevUpdate); we do not
// block — an OTA flash still queues — but the operator is warned so a
// stalled transfer over a saturated radio is expected, not surprising.
const DutyCycleWarningThreshold = 80

// FirmwareUpdateRisky reports whether a transmit duty cycle in percent is at
// or above [DutyCycleWarningThreshold]. A negative percentage means the CCU
// did not report the value, which is never risky.
func FirmwareUpdateRisky(dutyCyclePercent int) bool {
	return dutyCyclePercent >= DutyCycleWarningThreshold
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

	// unwireProgramNotifiers / unwireSysvarNotifiers detach the
	// OnProgramRegistered / OnSysvarRegistered hooks installed by
	// SetHubModel, so re-attaching a model does not leave the previous
	// subscriptions behind. Nil when no model is wired. Guarded by mu.
	unwireProgramNotifiers func()
	unwireSysvarNotifiers  func()

	mu      sync.RWMutex
	sysvars map[string]SysvarSnapshot

	// bidcos caches the per-interface BidCos radio-utilisation snapshot,
	// keyed by interface ID (e.g. "BidCos-RF"). Populated by the periodic
	// listBidcosInterfaces poll and read by the north-bound interface
	// index to surface duty-cycle / carrier-sense per radio interface.
	bidcos map[string]BidcosInterfaceInfo

	// refresh holds the ten per-type periodic-refresh slots. Each slot
	// owns its hook and its per-type serialisation semaphore; see
	// hub_refresh.go for the slot type and hubRefreshSet.
	refresh hubRefreshSet

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
// does not surface stale hub data to north-bound adapters.
func (h *HubCoordinator) Clear() {
	h.mu.Lock()
	h.sysvars = make(map[string]SysvarSnapshot)
	h.bidcos = nil
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
	h.NotifySysvarChanged(snap.Name, oldVal, snap.Value, snap.ValueType)
}

// NotifySysvarChanged publishes a [hmevent.SysvarChangedEvent].
func (h *HubCoordinator) NotifySysvarChanged(
	name string, old, next hmtypes.ParamValue, valueType hmenum.HubValueType,
) {
	events.Publish(h.bus, hmevent.SysvarChangedEvent{
		Base:        hmevent.NewBase(),
		CentralName: h.centralName,
		Name:        name,
		OldValue:    old,
		NewValue:    next,
		ValueType:   valueType,
	})
}

// wireSysvarNotifier connects one system variable's notifier hook to the bus.
// Idempotent, so the periodic hub scan can call it freely.
func (h *HubCoordinator) wireSysvarNotifier(sv *hub.Sysvar) {
	if sv == nil {
		return
	}
	sv.ValueNotifier = func(name string, old, next hmtypes.ParamValue) {
		// ValueType is rewritten in place by the hub scan through
		// Sysvar.ApplyMeta; read it through the guarded snapshot so this
		// notifier (which also fires from the callback-driven value push on a
		// different goroutine) cannot race that rewrite.
		h.NotifySysvarChanged(name, old, next, sv.Meta().ValueType)
	}
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

// NotifyProgramExecuted publishes a [hmevent.ProgramExecutedEvent]. The
// event's Source is lifted from the request context's Operation — every
// ingress that can run a program stamps one, so the audit trail and the
// daemon log can name the surface that asked instead of a generic "api".
func (h *HubCoordinator) NotifyProgramExecuted(ctx context.Context, programID string, trigger hmenum.ProgramTrigger, success bool) {
	source := ""
	if rc, ok := reqctx.FromContext(ctx); ok {
		source = rc.Operation
	}
	events.Publish(h.bus, hmevent.ProgramExecutedEvent{
		Base:        hmevent.NewBase(),
		CentralName: h.centralName,
		ProgramID:   programID,
		Trigger:     trigger,
		Success:     success,
		Source:      source,
	})
}

// NotifyProgramActiveChanged publishes a [hmevent.ProgramChangedEvent].
func (h *HubCoordinator) NotifyProgramActiveChanged(programID string, active bool) {
	events.Publish(h.bus, hmevent.ProgramChangedEvent{
		Base:        hmevent.NewBase(),
		CentralName: h.centralName,
		ProgramID:   programID,
		Active:      active,
	})
}

// wireProgramNotifiers connects one program's notifier hooks to the bus.
// Idempotent: re-wiring an already-wired program replaces the closures with
// equivalent ones, so the periodic hub scan can call it freely.
func (h *HubCoordinator) wireProgramNotifiers(p *hub.Program) {
	if p == nil {
		return
	}
	p.ExecuteNotifier = func(ctx context.Context, id string, trigger hmenum.ProgramTrigger, success bool) {
		h.NotifyProgramExecuted(ctx, id, trigger, success)
	}
	p.ActiveNotifier = func(id string, active bool) {
		h.NotifyProgramActiveChanged(id, active)
	}
}

// SetRefreshHooks wires the periodic-refresh callbacks the background
// scheduler invokes. Each hook is optional; nil keeps the previously
// configured handler.
func (h *HubCoordinator) SetRefreshHooks(hooks RefreshHooks) {
	h.refresh.programs.set(hooks.Programs)
	h.refresh.sysvars.set(hooks.Sysvars)
	h.refresh.inbox.set(hooks.Inbox)
	h.refresh.serviceMessages.set(hooks.ServiceMessages)
	h.refresh.alarmMessages.set(hooks.AlarmMessages)
	h.refresh.systemUpdate.set(hooks.SystemUpdate)
	h.refresh.installMode.set(hooks.InstallMode)
	h.refresh.metrics.set(hooks.Metrics)
	h.refresh.connectivity.set(hooks.Connectivity)
	h.refresh.bidcosInterfaces.set(hooks.BidcosInterfaces)
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
	// BidcosInterfaces polls the CCU's listBidcosInterfaces method for
	// every BidCos radio interface and refreshes the per-interface
	// duty-cycle / carrier-sense cache. Nil disables.
	BidcosInterfaces func(ctx context.Context) error
}

// RefreshPrograms invokes the program-refresh hook (if any) and
// returns the call result. Used by the background scheduler.
// Concurrent callers are serialised so only one JSON-RPC fetch runs at
// a time for this refresh type.
func (h *HubCoordinator) RefreshPrograms(ctx context.Context) error {
	return h.refresh.programs.run(ctx, h.recorder, "refresh_programs")
}

// RefreshSysvars invokes the sysvar-refresh hook. Concurrent callers
// are serialised per fetch type.
func (h *HubCoordinator) RefreshSysvars(ctx context.Context) error {
	return h.refresh.sysvars.run(ctx, h.recorder, "refresh_sysvars")
}

// RefreshInbox invokes the inbox-refresh hook. Concurrent callers are
// serialised per fetch type.
func (h *HubCoordinator) RefreshInbox(ctx context.Context) error {
	return h.refresh.inbox.run(ctx, h.recorder, "refresh_inbox")
}

// RefreshServiceMessages invokes the service-messages refresh hook.
// Concurrent callers are serialised per fetch type.
func (h *HubCoordinator) RefreshServiceMessages(ctx context.Context) error {
	return h.refresh.serviceMessages.run(ctx, h.recorder, "refresh_service_messages")
}

// RefreshAlarmMessages invokes the alarm-messages refresh hook.
// Concurrent callers are serialised per fetch type.
func (h *HubCoordinator) RefreshAlarmMessages(ctx context.Context) error {
	return h.refresh.alarmMessages.run(ctx, h.recorder, "refresh_alarm_messages")
}

// RefreshSystemUpdate invokes the system-update refresh hook.
// Concurrent callers are serialised per fetch type.
func (h *HubCoordinator) RefreshSystemUpdate(ctx context.Context) error {
	return h.refresh.systemUpdate.run(ctx, h.recorder, "refresh_system_update")
}

// RefreshInstallMode invokes the install-mode refresh hook (if any). The hook
// is expected to re-read the CCU's remaining install-mode countdown for each
// registered interface and call [PublishInstallModeRefreshed] when done.
// Concurrent callers are serialised per fetch type.
func (h *HubCoordinator) RefreshInstallMode(ctx context.Context) error {
	return h.refresh.installMode.run(ctx, h.recorder, "refresh_install_mode")
}

// RefreshConnectivity invokes the connectivity refresh hook (if any). The hook
// probes the CCU's interface-reachability state and updates the per-interface
// connectivity data points. Concurrent callers are serialised per fetch type.
func (h *HubCoordinator) RefreshConnectivity(ctx context.Context) error {
	return h.refresh.connectivity.run(ctx, h.recorder, "refresh_connectivity")
}

// RefreshBidcosInterfaces invokes the BidCos-interface refresh hook (if any).
// The hook polls the CCU's listBidcosInterfaces method for every BidCos radio
// interface and updates the per-interface duty-cycle / carrier-sense cache.
// Concurrent callers are serialised per fetch type.
func (h *HubCoordinator) RefreshBidcosInterfaces(ctx context.Context) error {
	return h.refresh.bidcosInterfaces.run(ctx, h.recorder, "refresh_bidcos_interfaces")
}

// SetBidcosInterfaces replaces the cached per-interface BidCos
// radio-utilisation snapshot. Passing an empty or nil map clears the cache.
// Safe for concurrent use.
func (h *HubCoordinator) SetBidcosInterfaces(m map[string]BidcosInterfaceInfo) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(m) == 0 {
		h.bidcos = nil
		return
	}
	next := make(map[string]BidcosInterfaceInfo, len(m))
	for k, v := range m {
		next[k] = v
	}
	h.bidcos = next
}

// BidcosInterface returns the cached radio-utilisation snapshot for the
// interface identified by id. The bool is false when no snapshot has been
// polled for that interface (e.g. HmIP interfaces, which carry no BidCos
// gateway). Safe for concurrent use.
func (h *HubCoordinator) BidcosInterface(id string) (BidcosInterfaceInfo, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	info, ok := h.bidcos[id]
	return info, ok
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
//
// Attaching the model also wires the notifier hooks of every program and
// system variable — the existing set plus everything the hub scan registers
// later. Without that the hooks stayed nil for every entity the scan created
// (it registers through [hub.Hub.PutProgram] / [hub.Hub.PutSysvar], not
// through [AddProgramDP] / [AddSysvarDP]), so neither a program's execution
// or activity nor a system variable's value ever reached the bus. The MQTT
// publisher subscribes to the model directly and was unaffected; every
// bus-driven consumer — the WebSocket plane above all — saw nothing.
func (h *HubCoordinator) SetHubModel(m *hub.Hub) *HubCoordinator {
	h.mu.Lock()
	if h.unwireProgramNotifiers != nil {
		h.unwireProgramNotifiers()
		h.unwireProgramNotifiers = nil
	}
	if h.unwireSysvarNotifiers != nil {
		h.unwireSysvarNotifiers()
		h.unwireSysvarNotifiers = nil
	}
	h.hubModel = m
	h.mu.Unlock()
	if m == nil {
		return h
	}
	// Subscribe before the snapshot walk so an entity registered in between is
	// not missed; re-wiring an already-wired one is harmless.
	unwirePrograms := m.OnProgramRegistered(h.wireProgramNotifiers)
	for _, p := range m.Programs() {
		h.wireProgramNotifiers(p)
	}
	unwireSysvars := m.OnSysvarRegistered(h.wireSysvarNotifier)
	for _, sv := range m.Sysvars() {
		h.wireSysvarNotifier(sv)
	}
	h.mu.Lock()
	h.unwireProgramNotifiers = unwirePrograms
	h.unwireSysvarNotifiers = unwireSysvars
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
	// Wire the notifiers so execution and activity emit bus events. The
	// OnProgramRegistered hook installed by SetHubModel does the same for
	// programs the hub scan registers directly.
	h.wireProgramNotifiers(p)
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

// ErrNoSysvarWriter reports that no south-bound sysvar write path is
// wired, so a value cannot reach the CCU.
//
// It exists because the previous behaviour was to return nil — success —
// which turned a missing wire into a silent no-op. The alarm sysvar
// mirror creates its variable through a separately wired creator and
// then writes the value through here; with the write path unwired the
// variable existed, never changed, and the caller's error branch never
// ran. A CCU program reading that variable waited forever for a trigger
// that could not arrive.
var ErrNoSysvarWriter = errors.New("hub: no sysvar value writer wired")

// SetSystemVariable writes a system-variable value to the CCU via the
// wired [SysvarValueWriter]. It returns [ErrNoSysvarWriter] when none is
// wired rather than reporting a write that did not happen as success.
func (h *HubCoordinator) SetSystemVariable(ctx context.Context, name string, value any) error {
	h.mu.RLock()
	w := h.sysvarValueWriter
	h.mu.RUnlock()
	if w == nil {
		return ErrNoSysvarWriter
	}
	return w.SetSysvar(ctx, name, value)
}

// HasSysvarValueWriter reports whether a south-bound sysvar write path is
// wired. The daemon checks it once at start-up so an operator learns
// about a missing wire from a log line rather than from a CCU program
// that never fires.
func (h *HubCoordinator) HasSysvarValueWriter() bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.sysvarValueWriter != nil
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
// initial load for the eight categories listed in the body.
//
// Two of the ten slots [hubRefreshSet] declares are deliberately absent here:
// systemUpdate is boot-loaded by the adapter instead
// (runInitialSystemUpdateLoad in internal/central/adapter/hub_wiring.go, a
// detached goroutine that outlives WireHub), and bidcosInterfaces has no boot
// load at all — its only production driver is the periodic
// "hub.bidcos_interfaces_refresh" scheduler job registered in
// internal/central/jobs.go, which carries no RunOnStart, so the category
// stays empty until the first tick.
//
// Ordering, measured against the single production call site: WireHub calls
// InitHub before it calls [HubCoordinator.SetRefreshHooks], so on a first
// bring-up every slot's hook is still nil and each run below returns without
// doing anything — only the Clear() half has an effect there. The loads
// matter on a repeat pass, where the previous pass's hooks are still set.
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
	_ = h.refresh.programs.run(ctx, h.recorder, "init_programs")
	_ = h.refresh.sysvars.run(ctx, h.recorder, "init_sysvars")
	_ = h.refresh.inbox.run(ctx, h.recorder, "init_inbox")
	_ = h.refresh.serviceMessages.run(ctx, h.recorder, "init_service_messages")
	_ = h.refresh.alarmMessages.run(ctx, h.recorder, "init_alarm_messages")
	_ = h.refresh.installMode.run(ctx, h.recorder, "init_install_mode")
	_ = h.refresh.metrics.run(ctx, h.recorder, "init_metrics")
	_ = h.refresh.connectivity.run(ctx, h.recorder, "init_connectivity")
}

// RefreshMetrics invokes the metrics-refresh hook (if any). Used by the
// background scheduler to keep CCU performance metrics up to date.
// Concurrent callers are serialised per fetch type.
func (h *HubCoordinator) RefreshMetrics(ctx context.Context) error {
	return h.refresh.metrics.run(ctx, h.recorder, "refresh_metrics")
}

// PublishInstallModeRefreshed fires an [hmevent.InstallModeChangedEvent] for
// each registered install-mode data point whose (enabled, remaining_s) pair
// changed since the last call, so north-bound adapters (MQTT, REST) pick up
// the refreshed countdown values without re-publishing an identical
// steady-state tuple on every poll — this job runs every 30s for the life
// of the daemon, and install mode is off far more often than it is on.
func (h *HubCoordinator) PublishInstallModeRefreshed() {
	dps := h.InstallModeDPs()
	for _, dp := range dps {
		if dp == nil {
			continue
		}
		enabled, remainingS, changed := dp.ConsumeChangeSincePublish()
		if !changed {
			continue
		}
		events.Publish(h.bus, hmevent.InstallModeChangedEvent{
			Base:        hmevent.NewBase(),
			CentralName: h.centralName,
			InterfaceID: dp.InterfaceID,
			Enabled:     enabled,
			RemainingS:  remainingS,
		})
	}
}
