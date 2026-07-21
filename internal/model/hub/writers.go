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
