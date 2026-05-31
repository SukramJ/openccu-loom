// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"

	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
)

// systemCCUAdapter walks the central registry to produce the
// per-central CCU metadata `GET /api/v1/system/ccu` returns. The
// configured-interface list is sourced from the static config (cfg)
// because the registry only knows interfaces after the first connect
// round; HA-side Repair-Flows want the configured set regardless of
// the runtime connectivity state.
type systemCCUAdapter struct {
	reg *central.Registry
	cfg *config.Config
}

func newSystemCCUAdapter(reg *central.Registry, cfg *config.Config) *systemCCUAdapter {
	return &systemCCUAdapter{reg: reg, cfg: cfg}
}

// List returns one SystemCCUEntry per registered central. Centrals
// present in cfg but not yet registered (e.g. during the bootstrap
// window) emit an entry with empty SystemInformation fields and
// Available=false — clients see the configured topology immediately.
func (a *systemCCUAdapter) List(_ context.Context) []handlers.SystemCCUEntry {
	if a.reg == nil || a.cfg == nil {
		return nil
	}
	out := make([]handlers.SystemCCUEntry, 0, len(a.cfg.Centrals))
	for i := range a.cfg.Centrals {
		entry := handlers.SystemCCUEntry{
			Name:                 a.cfg.Centrals[i].Name,
			Host:                 a.cfg.Centrals[i].Host,
			ConfiguredInterfaces: interfaceNames(a.cfg.Centrals[i]),
		}
		if c, ok := a.reg.Get(a.cfg.Centrals[i].Name); ok {
			si := c.SystemInformation()
			entry.Available = c.Available()
			entry.Model = si.Model
			entry.Version = si.Version
			entry.Hostname = si.Hostname
			entry.Serial = si.Serial
			entry.URL = si.URL
			entry.IsHaApp = si.IsHaApp
		}
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
