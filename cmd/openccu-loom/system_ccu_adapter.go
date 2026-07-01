// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
)

// systemCCUAdapter walks the central registry to produce the
// per-central CCU metadata `GET /api/v1/system/ccu` returns. The
// configured-interface list is sourced from resolve, the live
// central-config lookup (see [adapter.CentralConfigResolver]), so a
// central adopted at runtime appears without a daemon restart; the
// registry only knows interfaces after the first connect round, and
// HA-side Repair-Flows want the configured set regardless of the
// runtime connectivity state.
type systemCCUAdapter struct {
	reg     *central.Registry
	resolve adapter.CentralConfigResolver
}

func newSystemCCUAdapter(reg *central.Registry, resolve adapter.CentralConfigResolver) *systemCCUAdapter {
	return &systemCCUAdapter{reg: reg, resolve: resolve}
}

// List returns one SystemCCUEntry per registered central, sorted by
// name (the order [central.Registry.List] already returns). A central
// whose config cannot be resolved (no store entry, disabled, or no
// resolver) still emits an entry — built from whatever the registry
// unit knows — with an empty Host and ConfiguredInterfaces rather than
// being dropped from the list.
func (a *systemCCUAdapter) List(ctx context.Context) []handlers.SystemCCUEntry {
	if a.reg == nil {
		return nil
	}
	units := a.reg.List()
	out := make([]handlers.SystemCCUEntry, 0, len(units))
	for _, c := range units {
		if c == nil {
			continue
		}
		entry := handlers.SystemCCUEntry{Name: c.Name()}
		if a.resolve != nil {
			if cc, ok := a.resolve(ctx, c.Name()); ok {
				entry.Host = cc.Host
				entry.ConfiguredInterfaces = interfaceNames(cc)
			}
		}
		si := c.SystemInformation()
		entry.Available = c.Available()
		entry.Model = si.Model
		entry.Version = si.Version
		entry.Hostname = si.Hostname
		entry.Serial = si.Serial
		entry.URL = si.URL
		entry.IsHaApp = si.IsHaApp
		out = append(out, entry)
	}
	return out
}

func interfaceNames(cc config.CentralConfig) []string {
	names := make([]string, 0, len(cc.Interfaces))
	for _, ifc := range cc.Interfaces {
		names = append(names, ifc.Name)
	}
	return names
}
