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
	EventTypeDataPointStatusChanged    EventType = "datapoint.status_changed"
	// EventTypeDataPointSourceChanged fires when a wire data point's
	// lifecycle source token transitions (cache → live, live → stale,
	// stale → live). The value itself does not change with the
	// transition; consumers that care about freshness independent of
	// value diffs subscribe to this. See ADR 0019.
	EventTypeDataPointSourceChanged EventType = "datapoint.source_changed"
	// EventTypeCustomDataPointStateChanged is emitted whenever a CCU
	// data-point change observable to a Custom-DP would alter the
	// CDP's aggregated state. SPA tiles subscribe to one CDP-scoped
	// topic per tile instead of N per-DP topics; HA-Discovery /
	// other adapters can use it as an aggregated state hook too.
	EventTypeCustomDataPointStateChanged EventType = "custom_data_point.state_changed"
	EventTypeDeviceCreated               EventType = "device.created"
	EventTypeDeviceRemoved               EventType = "device.removed"
	EventTypeDeviceTrigger               EventType = "device.trigger"
	EventTypeFirmwareStateChanged        EventType = "device.firmware_state_changed"
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
	EventTypeHealthRecorded              EventType = "health.recorded"
	EventTypeProgramExecuted             EventType = "hub.program_executed"
	EventTypeSysvarChanged               EventType = "hub.sysvar_changed"
	EventTypeInstallModeChanged          EventType = "hub.install_mode_changed"
	EventTypeSystemStatusChanged         EventType = "system.status_changed"
	EventTypeAlarmMessage                EventType = "hub.alarm_message"
	EventTypeServiceMessage              EventType = "hub.service_message"
	EventTypeConnectivityChanged         EventType = "connectivity.changed"
	EventTypeDriftCorrected              EventType = "reconciliation.drift_corrected"
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
	// data-point cache. Allows subscribers to inspect the raw wire value.
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

	// EventTypeConnectionStageChanged fires as reconnection stages advance.
	EventTypeConnectionStageChanged EventType = "connection.stage_changed"

	// EventTypeConnectionHealthChanged fires when a connection transitions
	// between healthy and unhealthy.
	EventTypeConnectionHealthChanged EventType = "connection.health_changed"

	// EventTypeCacheInvalidated fires when a cache entry or entire cache
	// is cleared.
	EventTypeCacheInvalidated EventType = "cache.invalidated"

	// EventTypeRecoveryAttempted fires after each per-interface recovery
	// attempt (success or failure).
	EventTypeRecoveryAttempted EventType = "recovery.attempted"

	// EventTypeDataPointsCreated fires when new data points are attached to
	// the domain model after device discovery.
	EventTypeDataPointsCreated EventType = "datapoint.batch_created"

	// EventTypeWeekProfileChanged fires when a week-profile schedule is
	// saved or loaded through [weekprofile.Profile.publish]. MQTT subscribers
	// can listen on this bus event instead of registering per-profile
	// OnChange callbacks.
	EventTypeWeekProfileChanged EventType = "weekprofile.changed"

	// EventTypeCentralSouthboundReady fires once a central's southbound
	// bring-up (hub load → device/interface load → callbacks) has completed
	// against a ready CCU. North-bound adapters subscribe to it to publish
	// that central's device snapshot — the per-central counterpart to the
	// one-shot boot snapshot, needed because the bring-up is gated behind
	// CCU readiness and therefore completes asynchronously, per central.
	EventTypeCentralSouthboundReady EventType = "central.southbound_ready"
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

// DataPointStatusChangedEvent fires when the paired *_STATUS parameter
// of a data point changes.
type DataPointStatusChangedEvent struct {
	Base
	Key  hmtypes.DataPointKey
	From hmenum.ParameterStatus
	To   hmenum.ParameterStatus
}

// Type implements Event.
func (DataPointStatusChangedEvent) Type() EventType { return EventTypeDataPointStatusChanged }

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

// FirmwareStateChangedEvent fires when a device's firmware state flips.
type FirmwareStateChangedEvent struct {
	Base
	CentralName string
	Address     string
	From        hmenum.DeviceFirmwareState
	To          hmenum.DeviceFirmwareState
}

// Type implements Event.
func (FirmwareStateChangedEvent) Type() EventType { return EventTypeFirmwareStateChanged }

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
// live / stale without (necessarily) a value change. MQTT, REST-WS
// and the SPA subscribe to this so consumers get a "freshness
// flipped" signal even when the value itself stayed the same. See
// ADR 0019.
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

// HealthRecordedEvent fires whenever the health tracker takes a sample.
type HealthRecordedEvent struct {
	Base
	CentralName string
	InterfaceID string
	Component   string
	Healthy     bool
	Note        string
}

// Type implements Event.
func (HealthRecordedEvent) Type() EventType { return EventTypeHealthRecorded }

// ProgramExecutedEvent fires when a CCU program runs.
type ProgramExecutedEvent struct {
	Base
	CentralName string
	ProgramID   string
	Trigger     hmenum.ProgramTrigger
	Success     bool
}

// Type implements Event.
func (ProgramExecutedEvent) Type() EventType { return EventTypeProgramExecuted }

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
// (via Load or Save on a [weekprofile.Profile]). MQTT subscribers listen to
// this event to push updated schedule state without coupling to per-profile
// OnChange callbacks.
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

// AlarmMessageEvent fires when the CCU emits a new alarm.
type AlarmMessageEvent struct {
	Base
	CentralName string
	ID          string
	Name        string
	Description string
	Severity    hmenum.IncidentSeverity
}

// Type implements Event.
func (AlarmMessageEvent) Type() EventType { return EventTypeAlarmMessage }

// ServiceMessageEvent fires when the CCU emits a new service message.
type ServiceMessageEvent struct {
	Base
	CentralName string
	ID          string
	Name        string
	Address     string
	MessageType hmenum.ServiceMessageType
	Quittable   bool
}

// Type implements Event.
func (ServiceMessageEvent) Type() EventType { return EventTypeServiceMessage }

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
// a CCU push callback, before it is written to the data-point cache.
// Subscribers that need the raw wire form (e.g. audit loggers) consume this
// event.
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
// begins its data-refresh pass. North-bound subscribers can use this to show
// a "refreshing" indicator.
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
// a CCU callback, before it is written to the cache. Subscribers that need
// the pre-cache wire value (audit loggers, raw-value dashboards) consume this
// event.
type DataPointValueReceivedEvent struct {
	Base
	CentralName    string
	InterfaceID    string
	ChannelAddress string
	Parameter      string
	// Value is the already-coerced Go value; callers that need the raw
	// string form should subscribe to [RPCParameterReceivedEvent] instead.
	Value any
}

// Type implements Event.
func (DataPointValueReceivedEvent) Type() EventType { return EventTypeDataPointValueReceived }

// ConnectionStageChangedEvent fires when a reconnection attempt advances from
// one connection stage to the next. Allows dashboards to show granular
// progress (TCP available → RPC available → warmup → established).
type ConnectionStageChangedEvent struct {
	Base
	CentralName               string
	InterfaceID               string
	Stage                     hmenum.ConnectionStage
	PreviousStage             hmenum.ConnectionStage
	DurationInPreviousStageMs float64
}

// Type implements Event.
func (ConnectionStageChangedEvent) Type() EventType { return EventTypeConnectionStageChanged }

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

// DataPointsCreatedEvent fires when a batch of data points has been created
// and attached to the domain model, typically after a device discovery pass
// or config reload. North-bound adapters use this to register new entities.
type DataPointsCreatedEvent struct {
	Base
	CentralName string
	InterfaceID string
	// Count is the number of newly created data points.
	Count int
}

// Type implements Event.
func (DataPointsCreatedEvent) Type() EventType { return EventTypeDataPointsCreated }

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
