// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package hub provides factory wrappers that allow test code to instantiate
// Hub objects without accessing Coordinator internals.
package hub

import "github.com/SukramJ/openccu-loom/pkg/hmenum"

// NewConnectivityFactory is a thin wrapper around [NewConnectivity]. It
// exists so callers can construct the object through a function value and
// substitute their own, without depending on coordinator internals.
func NewConnectivityFactory() *Connectivity {
	return NewConnectivity()
}

// NewMetricsFactory is a thin wrapper around [NewMetrics].
func NewMetricsFactory() *Metrics {
	return NewMetrics()
}

// NewProgramFactory is a thin wrapper around [NewProgram]; the parameters
// map onto it one to one.
func NewProgramFactory(centralName, id, name, description string, isInternal bool, writer ProgramWriter) *Program {
	return NewProgram(centralName, id, name, description, isInternal, writer)
}

// NewSysvarFactory is a thin wrapper around [NewSysvar].
func NewSysvarFactory(centralName, name, description string, valueType hmenum.HubValueType, writer SysvarWriter) *Sysvar {
	return NewSysvar(centralName, name, description, valueType, writer)
}

// NewInboxFactory is a thin wrapper around [NewInboxWithCentral]. It is
// multi-CCU safe: centralName is passed straight through, so the inbox is
// scoped to the CCU it belongs to.
func NewInboxFactory(centralName string) *Inbox {
	return NewInboxWithCentral(centralName)
}

// NewServiceMessagesFactory is a thin wrapper around [NewServiceMessagesWithCentral].
func NewServiceMessagesFactory(centralName string, ack MessageAcknowledger) *ServiceMessages {
	return NewServiceMessagesWithCentral(centralName, ack)
}

// NewInstallModeFactory is a thin wrapper around [NewInstallMode].
func NewInstallModeFactory(interfaceID string, w InstallModeWriter) *InstallMode {
	return NewInstallMode(interfaceID, w)
}
