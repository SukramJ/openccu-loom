// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

// Package restapi holds REST-layer DI contracts whose definitions
// reference internal model types. These interfaces cannot live in
// pkg/interfaces because they depend on internal/model/* or
// internal/health types that are not exported from pkg/.
//
// Everything here is implemented by internal/central/adapter and
// consumed by internal/north/rest/handlers.
package restapi

import (
	"context"

	"github.com/SukramJ/openccu-loom/internal/health"
	"github.com/SukramJ/openccu-loom/internal/model/device"
	"github.com/SukramJ/openccu-loom/internal/model/hub"
	"github.com/SukramJ/openccu-loom/pkg/hmapi"
)

// NamedHub pairs a central name with its hub for multi-CCU aggregation.
type NamedHub struct {
	Central string
	Hub     *hub.Hub
}

// HubIndex is the facade hub-level endpoints depend on. Multi-CCU: the list
// endpoints aggregate over every central via [HubIndex.Hubs], tagging each
// item with its central; the mutating endpoints route to a specific central
// via [HubIndex.HubFor] (selected by the `central` query parameter).
type HubIndex interface {
	// Hub returns the first central's hub (back-compat for single-CCU paths
	// and tests). Prefer Hubs/HubFor for multi-CCU correctness.
	Hub() *hub.Hub
	// Hubs returns every registered central's hub, in stable name order.
	Hubs() []NamedHub
	// HubFor returns the named central's hub, or nil when unknown.
	HubFor(centralName string) *hub.Hub
}

// DeviceIndex is the narrow facade the device endpoints depend on.
// Implementations live in the central layer and translate address
// lookups into model.Device access.
type DeviceIndex interface {
	Devices() []*device.Device
	Device(address string) (*device.Device, bool)
	// CentralOf returns the name of the central unit owning the
	// device. Empty string when the device is unknown — handlers
	// surface that as an empty `central` field rather than an error.
	CentralOf(address string) string
}

// HealthReader is the narrow facade `GET /api/v1/health` needs.
type HealthReader interface {
	Overall() health.Status
	Snapshot() []health.Component
}

// InterfaceIndex is the facade `interfaces` endpoints use.
// InterfaceState itself is a leaf DTO in pkg/hmapi.
type InterfaceIndex interface {
	Interfaces() []hmapi.InterfaceState
	Interface(id string) (hmapi.InterfaceState, bool)
	Reconnect(ctx context.Context, id string) error
}
