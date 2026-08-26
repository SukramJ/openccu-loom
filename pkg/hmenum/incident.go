// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hmenum

// IncidentType classifies entries in the incident store.
type IncidentType string

// IncidentType values.
const (
	IncidentTypeAuthFailure        IncidentType = "auth_failure"
	IncidentTypeConnectionLost     IncidentType = "connection_lost"
	IncidentTypeCircuitBreakerOpen IncidentType = "circuit_breaker_open"
	IncidentTypePingPongMismatch   IncidentType = "pingpong_mismatch"
	IncidentTypeRPCFault           IncidentType = "rpc_fault"
	IncidentTypeRecoveryFailed     IncidentType = "recovery_failed"
	IncidentTypeParamsetPatch      IncidentType = "paramset_patch"
	IncidentTypeDeviceProfileMiss  IncidentType = "device_profile_miss"
	IncidentTypeConfigError        IncidentType = "config_error"

	// RPC_ERROR — a generic RPC-level error (e.g. transport error,
	// malformed response).
	IncidentTypeRPCError IncidentType = "rpc_error"

	// CALLBACK_TIMEOUT — the CCU did not deliver an event callback within the
	// expected window.
	IncidentTypeCallbackTimeout IncidentType = "callback_timeout"

	// CIRCUIT_BREAKER_TRIPPED — the circuit breaker opened due to excessive
	// failures.
	IncidentTypeCircuitBreakerTripped IncidentType = "circuit_breaker_tripped"

	// CIRCUIT_BREAKER_RECOVERED — the circuit breaker transitioned from
	// open/half-open back to closed.
	IncidentTypeCircuitBreakerRecovered IncidentType = "circuit_breaker_recovered"

	// PARAMSET_INCONSISTENCY — a paramset read from the CCU contains
	// structurally inconsistent data (e.g. duplicate keys, wrong type).
	IncidentTypeParamsetInconsistency IncidentType = "paramset_inconsistency"

	// IncidentTypePingPongMismatchHigh is recorded when the pending PONG count
	// exceeds the configured threshold.
	IncidentTypePingPongMismatchHigh IncidentType = "pingpong_mismatch_high"

	// IncidentTypePingPongUnknownHigh is recorded when the unknown PONG count
	// exceeds the configured threshold.
	IncidentTypePingPongUnknownHigh IncidentType = "pingpong_unknown_high"

	// IncidentTypeRetryExhausted is recorded when a Retrier consumes all
	// configured attempts without success. The incident carries the last
	// error as the message detail.
	IncidentTypeRetryExhausted IncidentType = "retry_exhausted"
)

// String returns the wire representation.
func (t IncidentType) String() string { return string(t) }

// IncidentSeverity orders incidents for filtering and alerting.
type IncidentSeverity string

// IncidentSeverity values.
const (
	IncidentSeverityInfo     IncidentSeverity = "info"
	IncidentSeverityWarning  IncidentSeverity = "warning"
	IncidentSeverityError    IncidentSeverity = "error"
	IncidentSeverityCritical IncidentSeverity = "critical"
)

// String returns the wire representation.
func (s IncidentSeverity) String() string { return string(s) }

// IntegrationIssueSeverity grades problems the daemon surfaces to
// external integrations (Home Assistant repair-issue flow, for instance).
type IntegrationIssueSeverity string

// IntegrationIssueSeverity values.
const (
	IntegrationIssueSeverityError   IntegrationIssueSeverity = "error"
	IntegrationIssueSeverityWarning IntegrationIssueSeverity = "warning"
)

// String returns the wire representation.
func (s IntegrationIssueSeverity) String() string { return string(s) }

// IntegrationIssueType names the categories of integration issues.
type IntegrationIssueType string

// IntegrationIssueType values.
const (
	IntegrationIssueTypePingPongMismatch     IntegrationIssueType = "ping_pong_mismatch"
	IntegrationIssueTypeFetchDataFailed      IntegrationIssueType = "fetch_data_failed"
	IntegrationIssueTypeIncompleteDeviceData IntegrationIssueType = "incomplete_device_data"
	IntegrationIssueTypeParamsetInconsistent IntegrationIssueType = "paramset_inconsistency"
)

// String returns the wire representation.
func (t IntegrationIssueType) String() string { return string(t) }

// DeviceTriggerEventType names the domain event types the daemon
// surfaces for non-state device events.
type DeviceTriggerEventType string

// DeviceTriggerEventType values.
const (
	DeviceTriggerEventTypeDeviceError DeviceTriggerEventType = "homematic.device_error"
	DeviceTriggerEventTypeImpulse     DeviceTriggerEventType = "homematic.impulse"
	DeviceTriggerEventTypeKeypress    DeviceTriggerEventType = "homematic.keypress"
)

// String returns the wire representation.
func (t DeviceTriggerEventType) String() string { return string(t) }

// Short returns the last dotted component of the event type, useful as
// a short slug in logs and MQTT topics.
func (t DeviceTriggerEventType) Short() string {
	s := string(t)
	for i := len(s) - 1; i >= 0; i-- {
		if s[i] == '.' {
			return s[i+1:]
		}
	}
	return s
}

// ServiceMessageType mirrors the CCU's AlType numeric classification.
type ServiceMessageType int

// ServiceMessageType values.
const (
	ServiceMessageTypeGeneric       ServiceMessageType = 0
	ServiceMessageTypeSticky        ServiceMessageType = 1
	ServiceMessageTypeConfigPending ServiceMessageType = 2
	ServiceMessageTypeAlarm         ServiceMessageType = 3
	ServiceMessageTypeUpdatePending ServiceMessageType = 4
	ServiceMessageTypeCommunication ServiceMessageType = 5
)

// String returns the lowercase identifier used by the REST API.
func (t ServiceMessageType) String() string {
	switch t {
	case ServiceMessageTypeGeneric:
		return "generic"
	case ServiceMessageTypeSticky:
		return "sticky"
	case ServiceMessageTypeConfigPending:
		return "config_pending"
	case ServiceMessageTypeAlarm:
		return "alarm"
	case ServiceMessageTypeUpdatePending:
		return "update_pending"
	case ServiceMessageTypeCommunication:
		return "communication"
	}
	return "unknown"
}
