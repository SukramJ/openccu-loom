// SPDX-License-Identifier: MIT
// Copyright (C) 2026 OpenCCU-Loom authors.

package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/SukramJ/openccu-loom/internal/audit"
	"github.com/SukramJ/openccu-loom/internal/central"
	"github.com/SukramJ/openccu-loom/internal/central/adapter"
	"github.com/SukramJ/openccu-loom/internal/central/cachereset"
	"github.com/SukramJ/openccu-loom/internal/config"
	"github.com/SukramJ/openccu-loom/internal/north/rest/handlers"
	"github.com/SukramJ/openccu-loom/internal/store/sqlite"
)

// cacheResetReset converts a possibly-nil [*cachereset.Service] into a genuine
// nil [handlers.CacheResetService] interface, so the REST router's
// `if d.CacheReset != nil` guard keeps the route unmounted rather than wrapping
// a typed-nil pointer (which would read as non-nil and serve a panicking route).
func cacheResetReset(svc *cachereset.Service) handlers.CacheResetService {
	if svc == nil {
		return nil
	}
	return svc
}

// configTopology adapts the loaded config to cachereset.Topology so a coarse
// scope can be expanded to (central, interface) units.
type configTopology struct{ cfg *config.Config }

func (t configTopology) Centrals() []string {
	out := make([]string, 0, len(t.cfg.Centrals))
	for i := range t.cfg.Centrals {
		out = append(out, t.cfg.Centrals[i].Name)
	}
	return out
}

func (t configTopology) Interfaces(centralName string) []string {
	for i := range t.cfg.Centrals {
		c := &t.cfg.Centrals[i]
		if c.Name != centralName {
			continue
		}
		out := make([]string, 0, len(c.Interfaces))
		for _, ifc := range c.Interfaces {
			out = append(out, ifc.Name)
		}
		return out
	}
	return nil
}

// buildCacheResetService wires the cache-reset service (ADR 0042) from the
// daemon's already-constructed collaborators. Device/paramset descriptions are
// refreshed by the re-pull (re-ingest overwrites the in-memory registries), so
// only the persisted VALUES and MASTER caches are cleared here; the nil-guarded
// store methods make a disabled cache a safe no-op. Returns nil only if there
// is no re-init manager (south-bound never wired) — callers guard on nil.
func buildCacheResetService(
	cfg *config.Config,
	reg *central.Registry,
	values *sqlite.ValuesCacheStore,
	master *sqlite.MasterValuesStore,
	mgr *adapter.BringUpManager,
	auditRec audit.Recorder,
	logger *slog.Logger,
) *cachereset.Service {
	if mgr == nil {
		return nil
	}
	deps := cachereset.Deps{
		Values:   values,
		Master:   master,
		Topology: configTopology{cfg: cfg},
		Reiniter: mgr,
		ClearValueCache: func(centralName string) {
			if u, ok := reg.Get(centralName); ok && u.Cache != nil {
				u.Cache.ClearAll()
			}
		},
		Logger: logger,
	}
	if auditRec != nil {
		deps.Audit = func(_ context.Context, scope cachereset.Scope, rep cachereset.Report) {
			auditRec.Record(audit.Entry{
				Timestamp: time.Now(),
				Action:    audit.Action("cache_clear"),
				Note: fmt.Sprintf(
					"scope=%s devices=%d paramsets=%d values=%d master=%d reinit=%v",
					scope.String(), rep.Devices, rep.Paramsets, rep.Values, rep.Master, rep.CentralsReinit,
				),
			})
		}
	}
	return cachereset.New(deps)
}
