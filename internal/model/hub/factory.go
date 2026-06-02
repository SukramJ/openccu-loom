// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package hub provides factory wrappers that allow test code to instantiate
// Hub objects without accessing Coordinator internals.
package hub

import "github.com/SukramJ/openccu-loom/pkg/hmenum"

// NewConnectivityFactory ist ein dünner Wrapper über [NewConnectivity].
// Ermöglicht Test-Stubability: Test-Code ruft die Factory auf, ohne
// Coordinator-Innereien zu kennen.
func NewConnectivityFactory() *Connectivity {
	return NewConnectivity()
}

// NewMetricsFactory ist ein dünner Wrapper über [NewMetrics].
func NewMetricsFactory() *Metrics {
	return NewMetrics()
}

// NewProgramFactory ist ein dünner Wrapper über [NewProgram].
// Parameter entsprechen [NewProgram] 1:1.
func NewProgramFactory(centralName, id, name, description string, isInternal bool, writer ProgramWriter) *Program {
	return NewProgram(centralName, id, name, description, isInternal, writer)
}

// NewSysvarFactory ist ein dünner Wrapper über [NewSysvar].
func NewSysvarFactory(centralName, name, description string, valueType hmenum.HubValueType, writer SysvarWriter) *Sysvar {
	return NewSysvar(centralName, name, description, valueType, writer)
}

// NewInboxFactory ist ein dünner Wrapper über [NewInboxWithCentral].
// Multi-CCU-safe: der central-Parameter wird an [NewInboxWithCentral]
// weitergereicht.
func NewInboxFactory(centralName string) *Inbox {
	return NewInboxWithCentral(centralName)
}

// NewServiceMessagesFactory ist ein dünner Wrapper über [NewServiceMessagesWithCentral].
func NewServiceMessagesFactory(centralName string, ack MessageAcknowledger) *ServiceMessages {
	return NewServiceMessagesWithCentral(centralName, ack)
}

// NewInstallModeFactory ist ein dünner Wrapper über [NewInstallMode].
func NewInstallModeFactory(interfaceID string, w InstallModeWriter) *InstallMode {
	return NewInstallMode(interfaceID, w)
}
