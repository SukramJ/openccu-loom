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

// daemonTopology adapts the daemon's view of its CCUs to cachereset.Topology
// so a coarse scope can be expanded to (central, interface) units.
//
// It is the union of two sources on purpose. cfg.Centrals is materialised once
// when the config is loaded and the adopt path never appends to it, so a CCU
// the operator added at runtime is absent from it — expanding a scope from the
// config alone yields zero units for that CCU, and Clear then reports success
// having cleared nothing. The registry covers those, but only reports
// interfaces whose client is up, so a reset issued while a CCU is unreachable
// would clear nothing either. Together they cover both.
type daemonTopology struct {
	cfg *config.Config
	reg *central.Registry
}

func (t daemonTopology) Centrals() []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, len(t.cfg.Centrals))
	add := func(name string) {
		if name == "" {
			return
		}
		if _, dup := seen[name]; dup {
			return
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	for i := range t.cfg.Centrals {
		add(t.cfg.Centrals[i].Name)
	}
	if t.reg != nil {
		for _, name := range t.reg.Names() {
			add(name)
		}
	}
	return out
}

func (t daemonTopology) Interfaces(centralName string) []string {
	seen := make(map[string]struct{})
	out := make([]string, 0, 4)
	add := func(name string) {
		if name == "" {
			return
		}
		if _, dup := seen[name]; dup {
			return
		}
		seen[name] = struct{}{}
		out = append(out, name)
	}
	for i := range t.cfg.Centrals {
		c := &t.cfg.Centrals[i]
		if c.Name != centralName {
			continue
		}
		for _, ifc := range c.Interfaces {
			add(ifc.Name)
		}
	}
	if t.reg != nil {
		if u, ok := t.reg.Get(centralName); ok && u.Clients != nil {
			for _, e := range u.Clients.List() {
				add(string(e.Interface))
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// buildCacheResetService wires the cache-reset service (ADR 0042) from the
// daemon's already-constructed collaborators. All four persisted caches are
// cleared: VALUES, MASTER and the device-/paramset-description rows (whose
// registries the re-pull then repopulates — and, via the persistence sinks,
// re-persists). The nil-guarded store methods make a disabled cache a safe
// no-op. Returns nil only if there is no re-init manager (south-bound never
// wired) — callers guard on nil.
func buildCacheResetService(
	cfg *config.Config,
	reg *central.Registry,
	values *sqlite.ValuesCacheStore,
	master *sqlite.MasterValuesStore,
	descriptors adapter.DescriptorStores,
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
		Topology: daemonTopology{cfg: cfg, reg: reg},
		Reiniter: mgr,
		ClearValueCache: func(centralName string) {
			if u, ok := reg.Get(centralName); ok && u.Cache != nil {
				u.Cache.ClearAll()
			}
		},
		Logger: logger,
	}
	// Persisted descriptor rows participate in the ADR-0042 clear:
	// without this an operator "clear caches + re-pull" would leave
	// stale descriptions on disk for the next boot. Typed nil-checks —
	// cachereset.Deps carries interfaces, so assigning a nil *store
	// directly would produce a non-nil interface.
	if descriptors.Devices != nil {
		deps.Devices = descriptors.Devices
	}
	if descriptors.Paramsets != nil {
		deps.Paramsets = descriptors.Paramsets
	}
	if auditRec != nil {
		deps.Audit = func(_ context.Context, scope cachereset.Scope, rep cachereset.Report) {
			auditRec.Record(audit.Entry{
				Timestamp: time.Now(),
				Action:    audit.ActionCacheClear,
				Note: fmt.Sprintf(
					"scope=%s devices=%d paramsets=%d values=%d master=%d reinit=%v",
					scope.String(), rep.Devices, rep.Paramsets, rep.Values, rep.Master, rep.CentralsReinit,
				),
			})
		}
	}
	return cachereset.New(deps)
}
