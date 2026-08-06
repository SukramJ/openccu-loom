// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hmevent

import (
	"strconv"
	"strings"

	"github.com/SukramJ/openccu-loom/pkg/hmenum"
	"github.com/SukramJ/openccu-loom/pkg/hmtypes"
)

// itoa converts an integer to its decimal string representation.
func itoa(n int) string { return strconv.Itoa(n) }

// joinStrings joins a slice of strings with ", " separator.
func joinStrings(ss []string) string { return strings.Join(ss, ", ") }

// Event tags. Keep the string form stable — metrics and the internal
// event-bus subscription key both reference it.
const (
	EventTypeCentralStateChanged       EventType = "central.state_changed"
	EventTypeClientStateChanged        EventType = "client.state_changed"
	EventTypeDataPointValueChanged     EventType = "datapoint.value_changed"
	EventTypeDataPointOptimisticRolled EventType = "datapoint.optimistic_rolled_back"
	// EventTypeDataPointSourceChanged fires when a wire data point's
	// lifecycle source token transitions (cache → live, live → stale,
	// stale → live). The value itself does not change with the
	// transition; consumers that care about freshness independent of
	// value diffs subscribe to this. See ADR 0019.
	EventTypeDataPointSourceChanged EventType = "datapoint.source_changed"
	// EventTypeCustomDataPointStateChanged labels the WebSocket push
	// that carries a Custom-DP's aggregated state. It is NOT published
	// on the event bus: the event bridge derives the aggregate from
	// the underlying wire-DP change and emits it directly on the
	// CDP-scoped WS topic, so a tile subscribes to one topic per CDP
	// instead of N per-DP topics.
	EventTypeCustomDataPointStateChanged EventType = "custom_data_point.state_changed"
	EventTypeDeviceCreated               EventType = "device.created"
	EventTypeDeviceRemoved               EventType = "device.removed"
	EventTypeDeviceTrigger               EventType = "device.trigger"
	EventTypeLinkPeerChanged             EventType = "link.peer_changed"
	EventTypeConnectionLost              EventType = "connection.lost"
	EventTypeCircuitBreakerTripped       EventType = "client.circuit_breaker_tripped"
	EventTypeCircuitBreakerStateChanged  EventType = "client.circuit_breaker_state_changed"
	EventTypeHeartbeatTimerFired         EventType = "client.heartbeat_timer_fired"
	EventTypePingPongMismatch            EventType = "client.pingpong_mismatch"
	EventTypeRequestCoalesced            EventType = "client.request_coalesced"
	EventTypeRecoveryStarted             EventType = "recovery.started"
	EventTypeRecoveryStageChanged        EventType = "recovery.stage_changed"
	EventTypeRecoveryCompleted           EventType = "recovery.completed"
	EventTypeRecoveryFailed              EventType = "recovery.failed"
	EventTypeProgramExecuted             EventType = "hub.program_executed"
	EventTypeProgramChanged              EventType = "hub.program_changed"
	EventTypeSysvarChanged               EventType = "hub.sysvar_changed"
	// EventTypeHubChannelsAssigned fires when the device-association pass
	// (assignHubChannels) changes which physical device one or more system
	// variables / programs are linked to. North-bound adapters re-publish the
	// affected hub-entity discovery so the linked entities move to the correct
	// device card.
	EventTypeHubChannelsAssigned EventType = "hub.channels_assigned"
	EventTypeInstallModeChanged  EventType = "hub.install_mode_changed"
	EventTypeSystemStatusChanged EventType = "system.status_changed"
	EventTypeAlarmMessage        EventType = "hub.alarm_message"
	EventTypeServiceMessage      EventType = "hub.service_message"
	EventTypeConnectivityChanged EventType = "connectivity.changed"
	EventTypeDriftCorrected      EventType = "reconciliation.drift_corrected"
	// EventTypeIncidentRecorded fires after a reliability incident
	// (circuit-breaker trip, ping/pong mismatch, retry-exhausted, …) has
	// been persisted. It mirrors the recorded [IncidentRecordedEvent] onto
	// the central event bus so north-bound consumers (the webhook bridge)
	// see incidents alongside datapoint and system-status events.
	EventTypeIncidentRecorded EventType = "incident.recorded"
	// EventTypeDataRefreshTriggered fires just before a background scheduler job
	// starts its refresh pass. Closes C-SCHED-2.
	EventTypeDataRefreshTriggered EventType = "scheduler.refresh_triggered"
	// EventTypeDataRefreshCompleted fires when a background scheduler job
	// finishes its refresh pass (success or failure). Closes C-SCHED-2.
	EventTypeDataRefreshCompleted EventType = "scheduler.refresh_completed"

	// EventTypeDataFetchCompleted fires when a single data-fetch phase
	// (e.g. VALUES paramset load for one interface) completes. Finer
	// grained than DataRefreshCompleted (which is scheduler-job scoped).
	EventTypeDataFetchCompleted EventType = "data.fetch_completed"

	// EventTypeRPCParameterReceived fires when a raw RPC parameter value
	// arrives via a CCU callback, before the value is written to the
	// data-point cache. A diagnostic wire trace with no subscriber
	// (declared in the subscriber-coverage guard).
	EventTypeRPCParameterReceived EventType = "rpc.parameter_received"

	// EventTypeDeviceLifecycle is the unified device lifecycle event
	// covering CREATED, DELAYED, UPDATED, REMOVED, and
	// AVAILABILITY_CHANGED sub-types. Python uses a single event with an
	// event_type discriminator; Go adds DELAYED and AVAILABILITY_CHANGED
	// which have no standalone equivalent.
	EventTypeDeviceLifecycle EventType = "device.lifecycle"

	// EventTypeDataPointValueReceived fires when a raw DP value arrives
	// from a CCU callback, before cache write.
	EventTypeDataPointValueReceived EventType = "datapoint.value_received"

	// EventTypeConnectionHealthChanged fires when a connection transitions
	// between healthy and unhealthy.
	EventTypeConnectionHealthChanged EventType = "connection.health_changed"

	// EventTypeCacheInvalidated fires when a cache entry or entire cache
	// is cleared.
	EventTypeCacheInvalidated EventType = "cache.invalidated"

	// EventTypeRecoveryAttempted fires after each per-interface recovery
	// attempt (success or failure).
	EventTypeRecoveryAttempted EventType = "recovery.attempted"

	// EventTypeWeekProfileChanged fires when a week-profile schedule is
	// saved or loaded through [weekprofile.Profile.publish]. The
	// north-bound schedule state travels through the profile's OnChange
	// callbacks; nothing consumes this bus event (declared in the
	// subscriber-coverage guard).
	EventTypeWeekProfileChanged EventType = "weekprofile.changed"

	// EventTypeCentralSouthboundReady fires once a central's southbound
	// bring-up (hub load → device/interface load → callbacks) has completed
	// against a ready CCU. North-bound adapters subscribe to it to publish
	// that central's device snapshot — the per-central counterpart to the
	// one-shot boot snapshot, needed because the bring-up is gated behind
	// CCU readiness and therefore completes asynchronously, per central.
	EventTypeCentralSouthboundReady EventType = "central.southbound_ready"

	// EventTypeCentralReadinessChanged fires each time a central advances
	// through its readiness-gated southbound bring-up (waiting_for_ccu →
	// loading_hub → loading_devices → ready). North-bound adapters subscribe
	// to reflect bring-up progress live, distinguishing "still initializing"
	// from "offline".
	EventTypeCentralReadinessChanged EventType = "central.readiness_changed"
)

// ---------- Central / clients ----------

// CentralStateChangedEvent fires when the central state machine
// transitions.
type CentralStateChangedEvent struct {
	Base
	CentralName string
	From        hmenum.CentralState
	To          hmenum.CentralState
	Reason      hmenum.FailureReason
}

// Type implements Event.
func (CentralStateChangedEvent) Type() EventType { return EventTypeCentralStateChanged }

// CentralReadinessChangedEvent fires when a central's readiness-gated
// southbound bring-up advances to a new phase (or its per-interface device
// load counts change). North-bound adapters use it to surface bring-up
// progress live.
type CentralReadinessChangedEvent struct {
	Base
	CentralName      string
	Phase            hmenum.ReadinessPhase
	InterfacesLoaded int
	InterfacesTotal  int
}

// Type implements Event.
func (CentralReadinessChangedEvent) Type() EventType { return EventTypeCentralReadinessChanged }

// CentralSouthboundReadyEvent fires once a central's southbound bring-up has
// completed against a ready CCU (names loaded with devices). North-bound
// adapters publish that central's snapshot in response.
type CentralSouthboundReadyEvent struct {
	Base
	CentralName string
}

// Type implements Event.
func (CentralSouthboundReadyEvent) Type() EventType { return EventTypeCentralSouthboundReady }

// ClientStateChangedEvent fires when a client's state machine transitions.
type ClientStateChangedEvent struct {
	Base
	CentralName string
	InterfaceID string
	Interface   hmenum.Interface
	From        hmenum.ClientState
	To          hmenum.ClientState
	Reason      hmenum.FailureReason
}

// Type implements Event.
func (ClientStateChangedEvent) Type() EventType { return EventTypeClientStateChanged }

// CircuitBreakerStateChangedEvent fires on breaker open/close transitions.
type CircuitBreakerStateChangedEvent struct {
	Base
	CentralName string
	InterfaceID string
	From        hmenum.CircuitState
	To          hmenum.CircuitState
}

// Type implements Event.
func (CircuitBreakerStateChangedEvent) Type() EventType { return EventTypeCircuitBreakerStateChanged }

// ConnectionLostEvent fires the moment a client detects a lost connection.
type ConnectionLostEvent struct {
	Base
	CentralName string
	InterfaceID string
	Reason      hmenum.FailureReason
}

// Type implements Event.
func (ConnectionLostEvent) Type() EventType { return EventTypeConnectionLost }

// CircuitBreakerTrippedEvent fires the moment a circuit breaker trips open
// (state changes from Closed to Open for the first time in a fault run).
type CircuitBreakerTrippedEvent struct {
	Base
	CentralName string
	InterfaceID string
	Reason      hmenum.FailureReason
}

// Type implements Event.
func (CircuitBreakerTrippedEvent) Type() EventType { return EventTypeCircuitBreakerTripped }

// HeartbeatTimerFiredEvent is published by the heartbeat scheduler job
// whenever the CCU heartbeat timer fires. The InterfaceIDs slice lists every
// interface that should be checked for liveness. Handlers (e.g.
// ConnectionRecoveryCoordinator) start a recovery run per listed interface.
type HeartbeatTimerFiredEvent struct {
	Base
	CentralName  string
	InterfaceIDs []string
}

// Type implements Event.
func (HeartbeatTimerFiredEvent) Type() EventType { return EventTypeHeartbeatTimerFired }

// RequestCoalescedEvent fires when a concurrent RPC request is folded into an
// already-in-flight call.
type RequestCoalescedEvent struct {
	Base
	CentralName string
	InterfaceID string
	// Key is the coalesce key (typically "method:arg-fingerprint").
	Key string
	// Waiters is the number of additional callers piggy-backing on the
	// in-flight request when the coalescer matched.
	Waiters int
}

// Type implements Event.
func (RequestCoalescedEvent) Type() EventType { return EventTypeRequestCoalesced }

// PingPongMismatchEvent fires when ping/pong reconciliation fails.
type PingPongMismatchEvent struct {
	Base
	CentralName  string
	InterfaceID  string
	MismatchType hmenum.PingPongMismatchType
	PendingCount int
	UnknownCount int
}

// Type implements Event.
func (PingPongMismatchEvent) Type() EventType { return EventTypePingPongMismatch }

// ---------- Data points ----------

// DataPointValueChangedEvent fires when a data-point value update lands
// in the cache.
type DataPointValueChangedEvent struct {
	Base
	Key      hmtypes.DataPointKey
	OldValue hmtypes.ParamValue
	NewValue hmtypes.ParamValue
}

// Type implements Event.
func (DataPointValueChangedEvent) Type() EventType { return EventTypeDataPointValueChanged }

// DataPointOptimisticRolledBackEvent fires when an optimistic update is
// reverted.
type DataPointOptimisticRolledBackEvent struct {
	Base
	Key     hmtypes.DataPointKey
	Reason  hmenum.RollbackReason
	Sent    hmtypes.ParamValue
	Present hmtypes.ParamValue
}

// Type implements Event.
func (DataPointOptimisticRolledBackEvent) Type() EventType {
	return EventTypeDataPointOptimisticRolled
}

// ---------- Devices ----------

// DeviceCreatedEvent fires when the registry observes a new device.
type DeviceCreatedEvent struct {
	Base
	CentralName string
	InterfaceID string
	Address     string
	Model       string
	Source      hmenum.SourceOfDeviceCreation
}

// Type implements Event.
func (DeviceCreatedEvent) Type() EventType { return EventTypeDeviceCreated }

// DeviceRemovedEvent fires when the CCU reports a device deletion.
type DeviceRemovedEvent struct {
	Base
	CentralName string
	InterfaceID string
	Address     string
}

// Type implements Event.
func (DeviceRemovedEvent) Type() EventType { return EventTypeDeviceRemoved }

// HubChannelsAssignedEvent fires when the device-association pass changes the
// device link of at least one system variable or program on a central (a
// variable whose name carries a device/channel identifier is associated with,
// or detached from, that device). It carries no per-entity detail: consumers
// re-derive the current links from the hub model. See assignHubChannels.
type HubChannelsAssignedEvent struct {
	Base
	CentralName string
}

// Type implements Event.
func (HubChannelsAssignedEvent) Type() EventType { return EventTypeHubChannelsAssigned }

// DeviceTriggerEvent fires for non-state device events (keypress,
// impulse, error).
type DeviceTriggerEvent struct {
	Base
	CentralName   string
	InterfaceID   string
	DeviceAddress string
	ChannelNo     int
	EventType_    hmenum.DeviceTriggerEventType //nolint:revive // field shadow avoided; trailing underscore is intentional
	Parameter     string
	Value         hmtypes.ParamValue
}

// Type implements Event.
func (DeviceTriggerEvent) Type() EventType { return EventTypeDeviceTrigger }

// LinkPeerChangedEvent fires when a device's link peers change.
type LinkPeerChangedEvent struct {
	Base
	CentralName string
	Address     string
	Peers       []string
}

// Type implements Event.
func (LinkPeerChangedEvent) Type() EventType { return EventTypeLinkPeerChanged }

// ---------- Recovery ----------

// RecoveryStartedEvent fires when the recovery coordinator picks up a
// failed connection.
type RecoveryStartedEvent struct {
	Base
	CentralName string
	InterfaceID string
}

// Type implements Event.
func (RecoveryStartedEvent) Type() EventType { return EventTypeRecoveryStarted }

// RecoveryStageChangedEvent fires on every stage transition during
// recovery.
type RecoveryStageChangedEvent struct {
	Base
	CentralName string
	InterfaceID string
	From        hmenum.RecoveryStage
	To          hmenum.RecoveryStage
	// DurationInOldStageMs is the wall-clock time spent in From before the
	// transition. Non-negative; 0 for the first transition from Idle.
	DurationInOldStageMs int64
}

// Type implements Event.
func (RecoveryStageChangedEvent) Type() EventType { return EventTypeRecoveryStageChanged }

// DataPointSourceChangedEvent fires when a wire data point's
// lifecycle source token transitions between unobserved / cache /
// live / stale without (necessarily) a value change. The event
// bridge subscribes and re-emits the current value on both the WS
// and the MQTT plane (envelope kind "refresh") so downstream
// consumers get a "freshness flipped" signal even when the value
// itself stayed the same. See ADR 0019.
type DataPointSourceChangedEvent struct {
	Base
	CentralName    string
	InterfaceID    string
	ChannelAddress string
	Parameter      string
	OldSource      hmenum.ValueSource
	NewSource      hmenum.ValueSource
	// Value is the current data-point value at the moment the
	// transition fired. Consumers that need to republish a topic
	// without a separate fetch read it from here.
	Value any
}

// Type implements Event.
func (DataPointSourceChangedEvent) Type() EventType { return EventTypeDataPointSourceChanged }

// RecoveryCompletedEvent fires once a recovery attempt finishes.
type RecoveryCompletedEvent struct {
	Base
	CentralName string
	InterfaceID string
	Result      hmenum.RecoveryResult
	Duration    int // milliseconds
}

// Type implements Event.
func (RecoveryCompletedEvent) Type() EventType { return EventTypeRecoveryCompleted }

// RecoveryFailedEvent fires when recovery exhausts retries.
type RecoveryFailedEvent struct {
	Base
	CentralName string
	InterfaceID string
	Reason      hmenum.FailureReason
	Attempts    int
	// TotalDurationMs is the wall-clock time spent in the failed
	// recovery attempt, mirroring Python's `total_duration_ms`.
	TotalDurationMs int64
	// LastStageReached is the recovery pipeline stage in flight when
	// the failure was recorded; matches Python's `last_stage_reached`.
	LastStageReached hmenum.RecoveryStage
	// RequiresManualIntervention is true when recovery is exhausted
	// and the operator must intervene (Reason == FailureReasonExhausted).
	RequiresManualIntervention bool
}

// Type implements Event.
func (RecoveryFailedEvent) Type() EventType { return EventTypeRecoveryFailed }

// ---------- Health / hub ----------

// ProgramExecutedEvent fires when a CCU program runs.
type ProgramExecutedEvent struct {
	Base
	CentralName string
	ProgramID   string
	Trigger     hmenum.ProgramTrigger
	Success     bool
	// Source names the surface that asked for the run — the
	// request-context operation stamped by the ingress (e.g.
	// "mqtt:program-trigger", "rest:program-execute"). Empty when the
	// execution path carried no request context. Without it every
	// daemon route reports the same generic trigger tag and a
	// program-ran-twice report cannot be attributed to a surface.
	Source string
}

// Type implements Event.
func (ProgramExecutedEvent) Type() EventType { return EventTypeProgramExecuted }

// ProgramChangedEvent fires when a CCU program's activity flag changes —
// the operator toggled it in the CCU WebUI, or a north-bound client wrote
// it. A CCU program is two controls: the activity flag decides whether it
// reacts at all, and the execution runs it once. A deactivated program
// refuses the execution, so a consumer offering "run now" has to learn
// about the transition to render that control unavailable.
//
// Distinct from [ProgramExecutedEvent], which reports a run.
type ProgramChangedEvent struct {
	Base
	CentralName string
	ProgramID   string
	Active      bool
}

// Type implements Event.
func (ProgramChangedEvent) Type() EventType { return EventTypeProgramChanged }

// SysvarChangedEvent fires when a sysvar's value changes.
type SysvarChangedEvent struct {
	Base
	CentralName string
	Name        string
	OldValue    hmtypes.ParamValue
	NewValue    hmtypes.ParamValue
	ValueType   hmenum.HubValueType
}

// Type implements Event.
func (SysvarChangedEvent) Type() EventType { return EventTypeSysvarChanged }

// InstallModeChangedEvent fires when CCU install mode toggles. The
// install-mode data points are per-interface (HmIP-RF, BidCos-RF), so
// InterfaceID identifies which interface's countdown changed.
type InstallModeChangedEvent struct {
	Base
	CentralName string
	InterfaceID string
	Enabled     bool
	RemainingS  int
}

// Type implements Event.
func (InstallModeChangedEvent) Type() EventType { return EventTypeInstallModeChanged }

// WeekProfileChangedEvent fires when a week-profile schedule is published
// (via Load or Save on a [weekprofile.Profile]). The north-bound schedule
// state travels through the profile's OnChange callbacks; nothing consumes
// this bus event (declared in the subscriber-coverage guard).
type WeekProfileChangedEvent struct {
	Base
	// CentralName scopes the event to the originating central.
	CentralName string
	// ChannelAddress is the channel the profile is attached to.
	ChannelAddress string
	// ProfileKey is the active profile key ("P1".."P6", or "" for non-climate).
	ProfileKey string
}

// Type implements Event.
func (WeekProfileChangedEvent) Type() EventType { return EventTypeWeekProfileChanged }

// SystemStatusChangedEvent carries a coarse-grained system status flip (e.g.
// DEGRADED→RUNNING via central, or metrics flapping).
//
// North-bound adapters (REST /status, WS system.status, MQTT system topic)
// consume the extended payload to render per-interface health.
type SystemStatusChangedEvent struct {
	Base
	CentralName string
	Component   string
	Healthy     bool
	Reason      string

	// InterfaceID is the CCU interface that triggered the status
	// change, when the event is interface-scoped (RPC error, ping-pong
	// mismatch, callback timeout). Empty for central-wide events.
	InterfaceID string

	// ErrorCode carries the CCU-side error class when the event
	// originates from an `error()` RPC callback. Mirrors the
	// `error_code` field
	// `central/rpc_server.py:265`.
	ErrorCode int

	// FailureReason is the categorised failure when the central
	// reports a non-healthy state. Empty when [Healthy] is true.
	FailureReason hmenum.FailureReason

	// CentralState is the post-transition central-state when the event is
	// central-state-scoped.
	CentralState hmenum.CentralState

	// ConnectionState is the post-transition connection-state when
	// the event reflects a transport health change. Free-form
	// string ("up", "down", "degraded") to avoid premature enum
	// commitment.
	ConnectionState string

	// ClientState is the post-transition client-state when an
	// interface client transitioned (one of the
	// [hmenum.ClientState] values).
	ClientState hmenum.ClientState

	// CallbackState reflects the inbound-callback-channel health
	// ("alive", "stale", "missing"). Free-form string.
	CallbackState string

	// DegradedInterfaces lists the interface IDs currently degraded.
	// Empty when no interfaces are in a degraded state.
	DegradedInterfaces []string

	// DegradedInterfaceReasons maps each degraded interface ID to the
	// failure reason that caused the degradation. It is a superset of
	// [DegradedInterfaces]: every key in this map is also present in
	// [DegradedInterfaces]. Populated by [Unit.EvaluateCentralState]
	// when the state machine carries per-interface failure information.
	DegradedInterfaceReasons map[string]hmenum.FailureReason

	// Issues carries structured diagnostic markers (ping-pong,
	// init failures, paramset-fetch errors). Each entry is an
	// opaque code; consumers translate to user-facing strings via
	// the i18n catalogue.
	Issues []string
}

// Type implements Event.
func (SystemStatusChangedEvent) Type() EventType { return EventTypeSystemStatusChanged }

// IncidentRecordedEvent fires after a reliability incident has been
// persisted into the incident store. It carries the same fields as the
// reliability-layer incident record so north-bound consumers can render or
// forward the incident without reaching back into the store. CentralName is
// the multi-CCU scoping dimension and is always set.
type IncidentRecordedEvent struct {
	Base
	CentralName string
	InterfaceID string
	// IncidentType is the incident classification. It is deliberately not
	// named Type to avoid colliding with the Type() Event-interface method.
	IncidentType hmenum.IncidentType
	Severity     hmenum.IncidentSeverity
	Message      string
	Details      string
}

// Type implements Event.
func (IncidentRecordedEvent) Type() EventType { return EventTypeIncidentRecorded }

// ConnectivityChangedEvent fires whenever per-interface reachability
// flips. The reconciliation job emits one for every interface whose
// state has drifted from the cached value; the regular push pipeline
// emits one whenever a CCU callback signals a change.
type ConnectivityChangedEvent struct {
	Base
	CentralName string
	InterfaceID string
	Reachable   bool
	// LatencyMs is the round-trip latency observed during the probe that
	// triggered this event, in milliseconds. Zero when the probe did not
	// measure latency (e.g. push-driven path).
	LatencyMs float64
}

// Type implements Event.
func (ConnectivityChangedEvent) Type() EventType { return EventTypeConnectivityChanged }

// DriftCorrectedEvent fires when the reconciliation job observes that
// a cached state diverged from the CCU's reported state and applied
// a correction. Useful for diagnosing stuck push callbacks.
type DriftCorrectedEvent struct {
	Base
	CentralName string
	Component   string // "connectivity" | "system_health" | …
	Detail      string
}

// Type implements Event.
func (DriftCorrectedEvent) Type() EventType { return EventTypeDriftCorrected }

// DataFetchCompletedEvent fires when a single data-fetch phase for one
// interface completes (success or failure). Finer-grained than
// DataRefreshCompletedEvent which is scoped to the entire scheduler job.
type DataFetchCompletedEvent struct {
	Base
	CentralName string
	// InterfaceID is the CCU interface whose data was fetched.
	InterfaceID string
	// Operation names the fetch step (e.g. "values", "paramset_description").
	Operation string
	// Count is the number of data points successfully fetched.
	Count int
	// Success is false when the operation returned an error.
	Success bool
}

// Type implements Event.
func (DataFetchCompletedEvent) Type() EventType { return EventTypeDataFetchCompleted }

// RPCParameterReceivedEvent fires when a raw RPC parameter value arrives via
// a CCU push callback, before it is written to the data-point cache. It is a
// diagnostic wire trace with no subscriber (declared in the
// subscriber-coverage guard); the coerced value change travels as
// [DataPointValueChangedEvent].
type RPCParameterReceivedEvent struct {
	Base
	CentralName    string
	InterfaceID    string
	ChannelAddress string
	Parameter      string
	// RawValue is the raw string representation of the wire value
	// before type conversion. Using string avoids carrying the
	// XML-RPC sum type into the pkg layer.
	RawValue string
}

// Type implements Event.
func (RPCParameterReceivedEvent) Type() EventType { return EventTypeRPCParameterReceived }

// DeviceLifecycleEvent is the unified device lifecycle event covering
// CREATED, DELAYED, UPDATED, REMOVED, and AVAILABILITY_CHANGED
// sub-types. Python uses a single event with an event_type
// discriminator; this type gives Go callers one place to subscribe to
// all device lifecycle changes.
type DeviceLifecycleEvent struct {
	Base
	CentralName string
	InterfaceID string
	Address     string
	// Subtype names the kind of lifecycle transition.
	Subtype hmenum.DeviceLifecycleSubtype
	// Available is populated for AVAILABILITY_CHANGED events.
	Available bool
	// Model is populated for CREATED and UPDATED events.
	Model string
	// Source is populated for CREATED events.
	Source hmenum.SourceOfDeviceCreation
}

// Type implements Event.
func (DeviceLifecycleEvent) Type() EventType { return EventTypeDeviceLifecycle }

// DataRefreshTriggeredEvent fires just before a background scheduler job
// begins its data-refresh pass. It is a diagnostic trace with no subscriber
// (declared in the subscriber-coverage guard); the completion event carries
// the result.
type DataRefreshTriggeredEvent struct {
	Base
	CentralName string
	// JobName is the registered [scheduler.Job.Name]; serves as
	// openccu-loom's `refresh_type` discriminator (Python uses an enum).
	JobName string
	// InterfaceID is the interface scope for the refresh; empty for
	// hub-level (cross-interface) refresh jobs. Matches Python's
	// `interface_id` field which is the event key.
	InterfaceID string
	// Scheduled distinguishes scheduler-driven refreshes (true) from
	// manual force-refresh calls (false). Always true for jobs.go +
	// scheduler_events.go publishers; false-paths land when the
	// north-bound API exposes manual refresh.
	Scheduled bool
}

// Type implements Event.
func (DataRefreshTriggeredEvent) Type() EventType { return EventTypeDataRefreshTriggered }

// DataRefreshCompletedEvent fires when a background scheduler job finishes a
// data-refresh pass.
type DataRefreshCompletedEvent struct {
	Base
	CentralName string
	// JobName is the registered [scheduler.Job.Name]; openccu-loom's
	// `refresh_type` discriminator.
	JobName string
	// InterfaceID is the interface scope for the refresh; empty for
	// hub-level refreshes.
	InterfaceID string
	// Duration is the wall-clock time the job took, in milliseconds
	// (matches Python's `duration_ms`).
	Duration int64
	// Success is false when the job returned an error.
	Success bool
	// ItemsRefreshed counts the items the job processed (e.g.,
	// device-descriptions fetched, paramsets persisted). Zero when the
	// publisher does not yet thread the count through; future
	// publishers should fill this for richer dashboards.
	ItemsRefreshed int
	// ErrorMessage carries the failure description on Success=false.
	// Empty on success.
	ErrorMessage string
}

// Type implements Event.
func (DataRefreshCompletedEvent) Type() EventType { return EventTypeDataRefreshCompleted }

// DataPointValueReceivedEvent fires when a raw data-point value arrives from
// a CCU callback, before it is written to the cache. The health wiring
// subscribes to it to record per-interface "last event received" activity
// for the UI.
type DataPointValueReceivedEvent struct {
	Base
	CentralName    string
	InterfaceID    string
	ChannelAddress string
	Parameter      string
	// Value is the already-coerced Go value; the raw string form
	// travels as [RPCParameterReceivedEvent].
	Value any
}

// Type implements Event.
func (DataPointValueReceivedEvent) Type() EventType { return EventTypeDataPointValueReceived }

// ConnectionHealthChangedEvent fires when the health status of a client
// connection changes (goes healthy or becomes unhealthy).
type ConnectionHealthChangedEvent struct {
	Base
	CentralName         string
	InterfaceID         string
	IsHealthy           bool
	FailureReason       hmenum.FailureReason
	ConsecutiveFailures int
}

// Type implements Event.
func (ConnectionHealthChangedEvent) Type() EventType { return EventTypeConnectionHealthChanged }

// CacheInvalidatedEvent fires when a cache is cleared or invalidated, either
// for a single device, a whole interface, or the entire daemon.
type CacheInvalidatedEvent struct {
	Base
	CentralName string
	CacheType   hmenum.CacheType
	Reason      hmenum.CacheInvalidationReason
	// Scope is the device address, interface ID, or empty string for a
	// full-cache invalidation.
	Scope           string
	EntriesAffected int
}

// Type implements Event.
func (CacheInvalidatedEvent) Type() EventType { return EventTypeCacheInvalidated }

// RecoveryAttemptedEvent fires after each connection-recovery attempt,
// whether it succeeded or failed, providing per-attempt diagnostics.
type RecoveryAttemptedEvent struct {
	Base
	CentralName   string
	InterfaceID   string
	AttemptNumber int
	MaxAttempts   int
	StageReached  hmenum.RecoveryStage
	Success       bool
	ErrorMessage  string
}

// Type implements Event.
func (RecoveryAttemptedEvent) Type() EventType { return EventTypeRecoveryAttempted }

// IntegrationIssue represents a structured diagnostic problem that requires
// operator attention (ping-pong mismatch, fetch failure, incomplete device
// data, paramset inconsistency). Carried in the Issues field of
// [SystemStatusChangedEvent].
type IntegrationIssue struct {
	// IssueType is the category of problem.
	IssueType hmenum.IntegrationIssueType
	// Severity indicates whether this is a warning or error.
	Severity hmenum.IntegrationIssueSeverity
	// InterfaceID is the CCU interface where the issue occurred.
	InterfaceID string
	// DeviceAddresses lists affected devices (non-empty for
	// IncompleteDeviceData and ParamsetInconsistency issues).
	DeviceAddresses []string
	// MissingParameters lists missing parameter names (non-empty for
	// ParamsetInconsistency issues).
	MissingParameters []string
	// MismatchCount is the ping-pong mismatch counter (PingPongMismatch only).
	MismatchCount int
}

// IssueID returns the canonical identifier that uniquely names this
// issue instance.
func (i IntegrationIssue) IssueID() string {
	return string(i.IssueType) + "_" + i.InterfaceID
}

// TranslationKey returns the i18n catalogue key for this issue type.
func (i IntegrationIssue) TranslationKey() string { return string(i.IssueType) }

// TranslationPlaceholders returns all key/value pairs that should be
// interpolated into the translated string for this issue. The map always
// contains "interface_id"; additional keys depend on the issue type: -
// "mismatch_count" for PingPongMismatch issues (when MismatchCount > 0). -
// "device_count" and "device_addresses" for IncompleteDeviceData and
// ParamsetInconsistency issues (when DeviceAddresses is non-empty). -
// "parameter_count" and "missing_parameters" for ParamsetInconsistency issues
// (when MissingParameters is non-empty).
func (i IntegrationIssue) TranslationPlaceholders() map[string]string {
	result := map[string]string{"interface_id": i.InterfaceID}
	if i.MismatchCount > 0 {
		result["mismatch_count"] = itoa(i.MismatchCount)
	}
	if len(i.DeviceAddresses) > 0 {
		result["device_count"] = itoa(len(i.DeviceAddresses))
		result["device_addresses"] = joinStrings(i.DeviceAddresses)
	}
	if len(i.MissingParameters) > 0 {
		result["parameter_count"] = itoa(len(i.MissingParameters))
		result["missing_parameters"] = joinStrings(i.MissingParameters)
	}
	return result
}
