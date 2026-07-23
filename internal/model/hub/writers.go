// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package hub

import (
	"context"
	"time"
)

// ProgramWriter is the contract for executing a CCU program.
type ProgramWriter interface {
	ExecuteProgram(ctx context.Context, id string) error
	SetProgramEnabled(ctx context.Context, id string, enabled bool) error
}

// ConditionalProgramWriter is the optional extension of [ProgramWriter]
// that evaluates the program's "if" condition and runs the program only
// when the condition is satisfied. The returned bool reports whether the
// program actually executed. Writers that do not implement this interface
// fall back to the unconditional [ProgramWriter.ExecuteProgram] path.
type ConditionalProgramWriter interface {
	ProgramWriter
	ExecuteProgramConditional(ctx context.Context, id string) (executed bool, err error)
}

// ProgramDeleter is the optional extension of [ProgramWriter] that removes
// a program from the CCU entirely (dom.DeleteObject). Writers that do not
// implement this interface make [Program.Delete] fail with
// [ErrProgramDeleteUnsupported]. The CCU has no JSON-RPC method that deletes
// a program by id, so implementations dispatch the delete_program ReGa script.
type ProgramDeleter interface {
	ProgramWriter
	DeleteProgram(ctx context.Context, id string) error
}

// SysvarWriter is the contract for writing a system variable.
type SysvarWriter interface {
	SetSysvar(ctx context.Context, name string, value any) error
}

// InstallModeWriter is the contract for toggling CCU install mode.
// duration is clamped by the CCU (typically 60 s min, 3600 s max); the
// writer does not clamp itself.
type InstallModeWriter interface {
	SetInstallMode(ctx context.Context, interfaceID string, enabled bool, duration time.Duration) error
}

// DeviceInstallModeWriter is the optional extension of [InstallModeWriter]
// that supports targeted single-device pairing. When a writer also
// implements this interface, [InstallMode.EnableForDevice] forwards the
// deviceAddress to the CCU so only the named device enters pairing mode.
// Writers that do not implement this interface fall back to the broadcast
// [InstallModeWriter.SetInstallMode] path.
type DeviceInstallModeWriter interface {
	InstallModeWriter
	SetInstallModeForDevice(ctx context.Context, interfaceID string, duration time.Duration, deviceAddress string) error
}

// LocalInstallModeWriter is the optional extension of [InstallModeWriter]
// for the keyserver-less HmIP LOCAL teach-in: pairing restricted to a
// single device identified by SGTIN + device key. Unlike
// [DeviceInstallModeWriter] there is deliberately no broadcast fallback
// for writers lacking this extension — silently opening an unrestricted
// pairing window would defeat the whitelist intent.
type LocalInstallModeWriter interface {
	InstallModeWriter
	SetInstallModeLocal(ctx context.Context, interfaceID string, duration time.Duration, sgtin, keyHex string) error
}

// MessageAcknowledger acknowledges a service- or alarm-message on the
// CCU. id is the CCU ISE ID of the message.
type MessageAcknowledger interface {
	AcknowledgeMessage(ctx context.Context, id string) error
}

// BulkMessageAcknowledger acknowledges every quittable message of a
// class on the CCU in a single pass, returning the number of messages
// that were acknowledged. The two methods back [ServiceMessages.AcknowledgeAll]
// and [AlarmMessages.AcknowledgeAll] respectively.
type BulkMessageAcknowledger interface {
	AcknowledgeAllServiceMessages(ctx context.Context) (int, error)
	AcknowledgeAllAlarmMessages(ctx context.Context) (int, error)
}

// ServiceMessageSuppressor permanently suppresses (or clears the
// suppression of) CCU service messages for a single channel parameter.
// Unlike an acknowledge — which dismisses a message once until the
// underlying condition re-triggers — suppression is durable: the CCU
// stops raising service messages for the given channel parameter until
// it is explicitly unsuppressed.
//
// interfaceID is the CCU interface the channel lives on (e.g.
// "HmIP-RF"); an empty value asks the implementation to resolve it from
// channelAddress. channelAddress is the channel address ("ADDR:chn").
// parameterID is the service parameter name (e.g. "LOWBAT"); an empty
// parameterID targets every service parameter of the channel. suppress
// toggles suppression (true) versus removal of the suppression (false).
//
// Backs the JSON-RPC Interface.suppressServiceMessages /
// Interface.getSuppressedServiceMessages calls; wire via
// [ServiceMessages.SetSuppressor].
type ServiceMessageSuppressor interface {
	SuppressServiceMessage(ctx context.Context, interfaceID, channelAddress, parameterID string, suppress bool) error
	GetSuppressedServiceMessages(ctx context.Context, interfaceID, channelAddress string) ([]string, error)
}
