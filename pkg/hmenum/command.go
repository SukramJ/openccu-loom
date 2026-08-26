// SPDX-License-Identifier: MIT
// Copyright (C) 2026 SukramJ.

package hmenum

// CommandPriority orders queued southbound commands.
//
// IMPORTANT: CRITICAL is the zero value. Never use `if priority != 0`
// as the "set" check — always compare against CommandPriorityCritical.
type CommandPriority int

// CommandPriority values.
const (
	CommandPriorityCritical CommandPriority = 0
	CommandPriorityHigh     CommandPriority = 1
	CommandPriorityLow      CommandPriority = 2
)

// RollbackReason explains why an optimistic value update was reversed.
type RollbackReason string

// RollbackReason values.
const (
	RollbackReasonTimeout       RollbackReason = "timeout"
	RollbackReasonSendError     RollbackReason = "send_error"
	RollbackReasonValueMismatch RollbackReason = "mismatch"
)

// String returns the wire representation.
func (r RollbackReason) String() string { return string(r) }

// CallSource identifies what triggered a southbound call.
type CallSource string

// CallSource values.
const (
	CallSourceHAInit            CallSource = "ha_init"
	CallSourceHMInit            CallSource = "hm_init"
	CallSourceManualOrScheduled CallSource = "manual_or_scheduled"
)

// String returns the wire representation.
func (s CallSource) String() string { return string(s) }

// ServiceScope distinguishes methods that are exposed to external
// consumers from those used internally by the library.
type ServiceScope string

// ServiceScope values.
const (
	ServiceScopeExternal ServiceScope = "external"
	ServiceScopeInternal ServiceScope = "internal"
)

// String returns the wire representation.
func (s ServiceScope) String() string { return string(s) }

// SystemEventType names the high-level events the CCU callback server
// can emit about the daemon's lifecycle.
type SystemEventType string

// SystemEventType values.
const (
	SystemEventTypeDeleteDevices  SystemEventType = "deleteDevices"
	SystemEventTypeDevicesCreated SystemEventType = "devicesCreated"
	SystemEventTypeDevicesDelayed SystemEventType = "devicesDelayed"
	SystemEventTypeError          SystemEventType = "error"
	SystemEventTypeHubRefreshed   SystemEventType = "hubDataPointRefreshed"
	SystemEventTypeListDevices    SystemEventType = "listDevices"
	SystemEventTypeNewDevices     SystemEventType = "newDevices"
	SystemEventTypeReplaceDevice  SystemEventType = "replaceDevice"
	SystemEventTypeReAddedDevice  SystemEventType = "readdedDevice"
	SystemEventTypeUpdateDevice   SystemEventType = "updateDevice"
)

// String returns the wire representation.
func (e SystemEventType) String() string { return string(e) }
